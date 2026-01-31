package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"cursortab/buffer"
	"cursortab/logger"
	"cursortab/text"
	"cursortab/types"
)

type Engine struct {
	WorkspacePath string
	WorkspaceID   string

	provider        Provider
	buffer          Buffer
	clock           Clock
	state           state
	ctx             context.Context
	currentCancel   context.CancelFunc
	prefetchCancel  context.CancelFunc
	idleTimer       Timer
	textChangeTimer Timer
	mu              sync.RWMutex
	eventChan       chan Event

	// Main context and cancel for the engine lifecycle
	mainCtx    context.Context
	mainCancel context.CancelFunc
	stopped    bool
	stopOnce   sync.Once

	// Completion state
	completions  []*types.Completion
	applyBatch   buffer.Batch
	cursorTarget *types.CursorPredictionTarget

	// Staged completion state (for multi-stage completions)
	stagedCompletion *types.StagedCompletion

	// Original buffer lines when completion was shown (for partial typing optimization)
	completionOriginalLines []string

	// Current groups for partial accept (stored when showing completion)
	currentGroups []*text.Group

	// Prefetch state
	prefetchedCompletions  []*types.Completion
	prefetchedCursorTarget *types.CursorPredictionTarget
	prefetchState          prefetchState

	// Streaming state (line-by-line)
	streamingState          *StreamingState
	streamingCancel         context.CancelFunc
	streamLinesChan         <-chan string // Lines channel (nil when not streaming)
	streamLineNum           int           // Line counter for current stream
	acceptedDuringStreaming bool          // True if user accepted partial during streaming

	// Token streaming state (token-by-token for inline)
	tokenStreamingState *TokenStreamingState
	tokenStreamChan     <-chan string // Token stream channel (nil when not streaming)

	// Config options
	config EngineConfig

	// Per-file state that persists across file switches (for context restoration)
	fileStateStore map[string]*FileState
}

func NewEngine(provider Provider, buf Buffer, config EngineConfig, clock Clock) (*Engine, error) {
	workspacePath, err := os.Getwd()
	if err != nil {
		logger.Warn("error getting current directory, using home: %v", err)
		workspacePath = "~"
	}
	workspaceID := fmt.Sprintf("%s-%d", workspacePath, os.Getpid())

	return &Engine{
		WorkspacePath:          workspacePath,
		WorkspaceID:            workspaceID,
		provider:               provider,
		buffer:                 buf,
		clock:                  clock,
		state:                  stateIdle,
		ctx:                    nil,
		eventChan:              make(chan Event, 100),
		config:                 config,
		idleTimer:              nil,
		textChangeTimer:        nil,
		mu:                     sync.RWMutex{},
		completions:            nil,
		cursorTarget:           nil,
		prefetchedCompletions:  nil,
		prefetchedCursorTarget: nil,
		prefetchState:          prefetchNone,
		stopped:                false,
		fileStateStore:         make(map[string]*FileState),
	}, nil
}

func (e *Engine) Start(ctx context.Context) {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return
	}

	// Create main context for engine lifecycle
	e.mainCtx, e.mainCancel = context.WithCancel(ctx)
	e.mu.Unlock()

	go e.eventLoop(e.mainCtx)
	logger.Info("engine started")
}

// Stop gracefully shuts down the engine and cleans up all resources
func (e *Engine) Stop() {
	e.stopOnce.Do(func() {
		e.mu.Lock()
		defer e.mu.Unlock()

		logger.Info("stopping engine...")

		// Mark as stopped to prevent new operations
		e.stopped = true
		// Cancel main context to stop event loop
		if e.mainCancel != nil {
			e.mainCancel()
		}
		// Cancel any pending operations
		if e.currentCancel != nil {
			e.currentCancel()
			e.currentCancel = nil
		}
		if e.prefetchCancel != nil {
			e.prefetchCancel()
			e.prefetchCancel = nil
		}
		// Stop idle timer
		e.stopIdleTimer()
		// Stop text change timer
		e.stopTextChangeTimer()
		// Clear any pending completions/predictions (without calling OnReject since we're stopping)
		e.state = stateIdle
		e.cursorTarget = nil
		e.completions = nil
		e.applyBatch = nil
		e.stagedCompletion = nil
		e.prefetchedCompletions = nil
		e.prefetchedCursorTarget = nil
		e.prefetchState = prefetchNone
		e.completionOriginalLines = nil
		// Close event channel (this will cause eventLoop to exit if it hasn't already)
		close(e.eventChan)

		logger.Info("engine stopped")
	})
}

// ClearOptions configures what to clear in clearState
type ClearOptions struct {
	CancelCurrent     bool
	CancelPrefetch    bool
	ClearStaged       bool // Clear staged completion state (set false when advancing stages)
	ClearCursorTarget bool
	CallOnReject      bool
}

// clearState consolidates all state clearing into one method with configurable options
func (e *Engine) clearState(opts ClearOptions) {
	if opts.CancelCurrent && e.currentCancel != nil {
		e.currentCancel()
		e.currentCancel = nil
	}
	if opts.CancelPrefetch && e.prefetchCancel != nil {
		e.prefetchCancel()
		e.prefetchCancel = nil
		e.prefetchState = prefetchNone
		e.prefetchedCompletions = nil
		e.prefetchedCursorTarget = nil
	}
	if opts.ClearCursorTarget {
		e.cursorTarget = nil
	}
	if opts.CallOnReject {
		e.buffer.ClearUI()
	}
	e.completions = nil
	e.applyBatch = nil
	if opts.ClearStaged {
		e.stagedCompletion = nil
	}
	e.completionOriginalLines = nil
	e.currentGroups = nil
}

// clearAll clears everything including prefetch and staged completions
func (e *Engine) clearAll() {
	e.clearState(ClearOptions{CancelCurrent: true, CancelPrefetch: true, ClearStaged: true, ClearCursorTarget: true, CallOnReject: true})
}

// eventLoopRestarts tracks the number of event loop restarts for panic recovery
var eventLoopRestarts atomic.Int32

const maxEventLoopRestarts = 3

func (e *Engine) eventLoop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			restarts := eventLoopRestarts.Add(1)
			logger.Error("event loop panic [%d/%d]: %v\n%s",
				restarts, maxEventLoopRestarts, r, debug.Stack())

			if int(restarts) < maxEventLoopRestarts {
				e.eventLoop(e.mainCtx) // Restart the event loop
			} else {
				logger.Error("max event loop restarts reached, stopping engine")
				go e.Stop() // async to avoid deadlock
			}
		}
	}()

	for {
		// Get current stream channels (nil when not streaming)
		e.mu.RLock()
		linesChan := e.streamLinesChan
		tokenChan := e.tokenStreamChan
		e.mu.RUnlock()

		select {
		case <-ctx.Done():
			// Clean shutdown when context is cancelled
			return

		case line, ok := <-linesChan:
			// Direct stream line handling - no intermediate buffer
			// When linesChan is nil, this case is never selected
			e.mu.Lock()
			if e.stopped {
				e.mu.Unlock()
				return
			}
			if e.streamLinesChan != linesChan {
				// Stream changed while we were waiting, ignore stale data
				e.mu.Unlock()
				continue
			}
			if !ok {
				// Channel closed - stream complete
				e.handleStreamCompleteSimple()
				e.mu.Unlock()
				continue
			}
			e.streamLineNum++
			e.handleStreamLine(line)
			e.mu.Unlock()

		case text, ok := <-tokenChan:
			// Token stream handling (cumulative text for inline completion)
			// When tokenChan is nil, this case is never selected
			e.mu.Lock()
			if e.stopped {
				e.mu.Unlock()
				return
			}
			if e.tokenStreamChan != tokenChan {
				// Stream changed while we were waiting, ignore stale data
				e.mu.Unlock()
				continue
			}
			if !ok {
				// Channel closed - token stream complete
				e.handleTokenStreamComplete()
				e.mu.Unlock()
				continue
			}
			e.handleTokenChunk(text)
			e.mu.Unlock()

		case event, ok := <-e.eventChan:
			if !ok {
				// Channel closed, exit gracefully
				return
			}

			// Check if we're stopped before processing
			e.mu.RLock()
			stopped := e.stopped
			e.mu.RUnlock()

			if stopped {
				return
			}

			// Wrap event handling in its own recovery
			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("event handler panic recovered for event %v: %v", event.Type, r)
					}
				}()
				e.handleEvent(event)
			}()
		}
	}
}

func (e *Engine) handleEvent(event Event) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Double-check we're not stopped while holding the lock
	if e.stopped {
		return
	}

	logger.Debug("handle event: %v (state=%s)", event.Type, e.state)
	defer func() {
		logger.Debug("after event: %v (state=%s)", event.Type, e.state)
	}()

	// Layer 1: Background/async results
	if e.handleBackgroundEvent(event) {
		return
	}

	// Layer 2: Dispatch table for user/timer events
	e.dispatch(event)
}

// handleBackgroundEvent handles async completion and prefetch results.
// Returns true if the event was handled, false if it should be dispatched.
func (e *Engine) handleBackgroundEvent(event Event) bool {
	switch event.Type {
	case EventCompletionReady:
		if e.state != statePendingCompletion {
			return true
		}
		e.handleCompletionReadyImpl(event.Data.(*types.CompletionResponse))
		return true

	case EventCompletionError:
		if err, ok := event.Data.(error); !ok || !errors.Is(err, context.Canceled) {
			logger.Error("completion error: %v", event.Data)
		}
		return true

	case EventPrefetchReady:
		// Guard: only process if we're expecting prefetch results
		if e.prefetchState != prefetchInFlight &&
			e.prefetchState != prefetchWaitingForTab &&
			e.prefetchState != prefetchWaitingForCursorPrediction {
			return true
		}
		e.handlePrefetchReady(event.Data.(*types.CompletionResponse))
		return true

	case EventPrefetchError:
		// Guard: only process if we're expecting prefetch results
		if e.prefetchState != prefetchInFlight &&
			e.prefetchState != prefetchWaitingForTab &&
			e.prefetchState != prefetchWaitingForCursorPrediction {
			return true
		}
		// Nil-safe error handling
		if err, ok := event.Data.(error); ok {
			e.handlePrefetchError(err)
		} else {
			e.handlePrefetchError(nil)
		}
		return true

		// Note: EventStreamLine, EventStreamComplete, EventStreamError are now handled
		// directly in the event loop via channel selection, not through eventChan
	}
	return false
}

func (e *Engine) reject() {
	e.clearState(ClearOptions{
		CancelCurrent:     true,
		CancelPrefetch:    true,
		ClearStaged:       true,
		ClearCursorTarget: true,
		CallOnReject:      true,
	})
	e.state = stateIdle
}

// syncBuffer syncs the buffer state and handles file switching.
// This should be called instead of buffer.Sync directly to ensure
// file context is properly saved/restored when switching files.
func (e *Engine) syncBuffer() {
	result, err := e.buffer.Sync(e.WorkspacePath)
	if err != nil {
		logger.Debug("sync error: %v", err)
		return
	}

	if result != nil && result.BufferChanged {
		e.handleFileSwitch(result.OldPath, result.NewPath, e.buffer.Lines())
	}
}

func (e *Engine) requestCompletion(source types.CompletionSource) {
	// Check if stopped before making request
	if e.stopped {
		return
	}

	e.syncBuffer()

	req := &types.CompletionRequest{
		Source:            source,
		WorkspacePath:     e.WorkspacePath,
		WorkspaceID:       e.WorkspaceID,
		FilePath:          e.buffer.Path(),
		Lines:             e.buffer.Lines(),
		Version:           e.buffer.Version(),
		PreviousLines:     e.buffer.PreviousLines(),
		FileDiffHistories: e.getAllFileDiffHistories(),
		CursorRow:         e.buffer.Row(),
		CursorCol:         e.buffer.Col(),
		ViewportHeight:    e.getViewportHeightConstraint(),
		LinterErrors:      e.buffer.LinterErrors(),
	}

	// Check if provider supports streaming
	if streamProvider, ok := e.provider.(LineStreamProvider); ok {
		switch streamProvider.GetStreamingType() {
		case StreamingTypeLines:
			e.requestStreamingCompletion(streamProvider, req)
			return
		case StreamingTypeTokens:
			if tokenProvider, ok := e.provider.(TokenStreamProvider); ok {
				e.requestTokenStreamingCompletion(tokenProvider, req)
				return
			}
		}
	}

	// Fallback to batch mode
	e.state = statePendingCompletion

	ctx, cancel := context.WithTimeout(e.mainCtx, e.config.CompletionTimeout)
	e.currentCancel = cancel

	go func() {
		defer cancel()

		result, err := e.provider.GetCompletion(ctx, req)

		if err != nil {
			select {
			case e.eventChan <- Event{Type: EventCompletionError, Data: err}:
			case <-e.mainCtx.Done():
			}
			return
		}

		select {
		case e.eventChan <- Event{Type: EventCompletionReady, Data: result}:
		case <-e.mainCtx.Done():
		}
	}()
}

func (e *Engine) handleCursorTarget() {
	if !e.config.CursorPrediction.Enabled {
		e.clearCompletionUIOnly()
		return
	}

	if e.cursorTarget == nil || e.cursorTarget.LineNumber < 1 {
		e.clearCompletionUIOnly()
		return
	}

	distance := abs(int(e.cursorTarget.LineNumber) - e.buffer.Row())
	if distance <= e.config.CursorPrediction.ProximityThreshold {
		// Close enough - don't show cursor prediction

		// If we have remaining staged completions, check if next stage is still close
		if e.stagedCompletion != nil && e.stagedCompletion.CurrentIdx < len(e.stagedCompletion.Stages) {
			nextStage := e.getStage(e.stagedCompletion.CurrentIdx)
			if nextStage == nil {
				return
			}
			stageStart := nextStage.BufferStart
			stageEnd := nextStage.BufferEnd

			// Calculate distance from cursor to next stage
			var stageDistance int
			if e.buffer.Row() < stageStart {
				stageDistance = stageStart - e.buffer.Row()
			} else if e.buffer.Row() > stageEnd {
				stageDistance = e.buffer.Row() - stageEnd
			} else {
				stageDistance = 0 // Cursor is within the stage range
			}

			if stageDistance <= e.config.CursorPrediction.ProximityThreshold {
				// Stage is close enough - show it directly
				e.showCurrentStage()
				return
			}
			// Stage is far - show cursor prediction to it instead
			e.cursorTarget = &types.CursorPredictionTarget{
				RelativePath:    e.buffer.Path(),
				LineNumber:      int32(stageStart),
				ShouldRetrigger: false,
			}
			e.state = stateHasCursorTarget
			e.buffer.ShowCursorTarget(stageStart)
			return
		}

		// If prefetch is ready, show completion immediately
		if e.prefetchState == prefetchReady && e.tryShowPrefetchedCompletion() {
			return
		}
		// If prefetch is in flight, wait for it
		if e.prefetchState == prefetchInFlight {
			e.prefetchState = prefetchWaitingForCursorPrediction
		}
		// Go idle (no cursor prediction needed when close)
		e.clearCompletionUIOnly()
		return
	}

	// Far away - show cursor prediction to the target line
	e.state = stateHasCursorTarget
	e.buffer.ShowCursorTarget(int(e.cursorTarget.LineNumber))
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// clearCompletionUIOnly clears completion state but preserves prefetch.
// This is used when transitioning out of cursor target state without
// wanting to cancel an in-flight prefetch.
// NOTE: This does NOT call OnReject because it's expected that
// clearKeepPrefetch() was already called before this, which handles the UI clearing.
func (e *Engine) clearCompletionUIOnly() {
	e.clearState(ClearOptions{CancelCurrent: true, CancelPrefetch: false, ClearStaged: true, CallOnReject: false})
	e.state = stateIdle
	e.cursorTarget = nil
}

// showCurrentStage displays the current stage of a multi-stage completion
func (e *Engine) showCurrentStage() {
	if e.stagedCompletion == nil || e.stagedCompletion.CurrentIdx >= len(e.stagedCompletion.Stages) {
		return
	}

	stage := e.getStage(e.stagedCompletion.CurrentIdx)
	if stage == nil {
		return
	}

	e.completions = []*types.Completion{{
		StartLine:  stage.BufferStart,
		EndLineInc: stage.BufferEnd,
		Lines:      stage.Lines,
	}}
	e.cursorTarget = stage.CursorTarget
	e.state = stateHasCompletion

	// Use PrepareCompletion with pre-computed groups from stage
	e.applyBatch = e.buffer.PrepareCompletion(
		stage.BufferStart,
		stage.BufferEnd,
		stage.Lines,
		stage.Groups,
	)

	// Store original buffer lines for partial typing optimization
	bufferLines := e.buffer.Lines()
	e.completionOriginalLines = nil
	for i := stage.BufferStart; i <= stage.BufferEnd && i-1 < len(bufferLines); i++ {
		e.completionOriginalLines = append(e.completionOriginalLines, bufferLines[i-1])
	}

	// Store groups for partial accept
	e.currentGroups = stage.Groups
}

// getStage returns the stage at the given index with type assertion
func (e *Engine) getStage(idx int) *text.Stage {
	if e.stagedCompletion == nil || idx < 0 || idx >= len(e.stagedCompletion.Stages) {
		return nil
	}
	stage, ok := e.stagedCompletion.Stages[idx].(*text.Stage)
	if !ok {
		return nil
	}
	return stage
}

// processCompletion is the SINGLE ENTRY POINT for processing all completions.
// It handles diff analysis, staging decisions, and showing completions.
// Called from both fresh completion responses and prefetch paths.
// Returns true if completion was processed successfully, false if no changes.
func (e *Engine) processCompletion(completion *types.Completion) bool {
	defer logger.Trace("engine.processCompletion")()
	if completion == nil {
		return false
	}

	// Check for actual changes
	if !e.buffer.HasChanges(completion.StartLine, completion.EndLineInc, completion.Lines) {
		return false
	}

	// Extract original lines from buffer
	bufferLines := e.buffer.Lines()
	var originalLines []string
	for i := completion.StartLine; i <= completion.EndLineInc && i-1 < len(bufferLines); i++ {
		originalLines = append(originalLines, bufferLines[i-1])
	}

	// Analyze diff with viewport awareness
	viewportTop, viewportBottom := e.buffer.ViewportBounds()
	originalText := text.JoinLines(originalLines)
	newText := text.JoinLines(completion.Lines)
	diffResult := text.AnalyzeDiffForStagingWithViewport(
		originalText, newText,
		viewportTop, viewportBottom,
		completion.StartLine,
	)

	// Create stages - CreateStages handles all viewport/distance logic and returns:
	// - nil: no staging needed (single visible+close cluster or no changes)
	// - StagingResult with FirstNeedsNavigation: whether to show cursor prediction UI
	stagingResult := text.CreateStages(
		diffResult,
		e.buffer.Row(),
		viewportTop, viewportBottom,
		completion.StartLine,
		e.config.CursorPrediction.ProximityThreshold,
		e.buffer.Path(),
		completion.Lines,
		originalLines, // oldLines parameter
	)

	if stagingResult != nil && len(stagingResult.Stages) > 0 {
		// Convert stages to any slice for storage
		stagesAny := make([]any, len(stagingResult.Stages))
		for i, s := range stagingResult.Stages {
			stagesAny[i] = s
		}
		e.stagedCompletion = &types.StagedCompletion{
			Stages:     stagesAny,
			CurrentIdx: 0,
			SourcePath: e.buffer.Path(),
		}

		if stagingResult.FirstNeedsNavigation {
			// First stage is outside viewport or far from cursor - show cursor prediction
			firstStage := stagingResult.Stages[0]
			e.cursorTarget = &types.CursorPredictionTarget{
				RelativePath:    e.buffer.Path(),
				LineNumber:      int32(firstStage.BufferStart),
				ShouldRetrigger: false,
			}
			e.state = stateHasCursorTarget
			e.buffer.ShowCursorTarget(firstStage.BufferStart)
			return true
		}

		// Show first stage directly
		e.showCurrentStage()
		return true
	}

	// No staging means no changes to show
	return false
}

// RegisterEventHandler registers the event handler for nvim RPC callbacks.
// This should be called after buffer.SetClient has been called with a valid nvim connection.
func (e *Engine) RegisterEventHandler() {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Don't change if stopped
	if e.stopped {
		return
	}

	// Register the event handler for the new connection
	if err := e.buffer.RegisterEventHandler(func(event string) {
		e.mu.RLock()
		stopped := e.stopped
		e.mu.RUnlock()

		if stopped {
			return
		}

		eventType := EventTypeFromString(event)
		if eventType != "" {
			select {
			case e.eventChan <- Event{Type: eventType, Data: nil}:
			case <-e.mainCtx.Done():
				return
			}
		}
	}); err != nil {
		logger.Error("error registering event handler for new connection: %v", err)
	}
}

// getViewportHeightConstraint returns the viewport height constraint for completion requests.
// When staging is enabled, returns 0 (no limit) to allow multi-line completions.
// When staging is disabled, limits to distance from cursor to viewport bottom to prevent overflow.
func (e *Engine) getViewportHeightConstraint() int {
	if e.config.CursorPrediction.Enabled {
		return 0
	}
	// Distance from cursor to end of viewport
	_, viewportBottom := e.buffer.ViewportBounds()
	if viewportBottom > 0 && e.buffer.Row() > 0 {
		if constraint := viewportBottom - e.buffer.Row(); constraint > 0 {
			return constraint
		}
	}
	return 0
}
