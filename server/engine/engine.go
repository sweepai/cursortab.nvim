package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"cursortab/logger"
	"cursortab/text"
	"cursortab/types"
	"cursortab/utils"

	"github.com/neovim/go-client/nvim"
)

type state int

const (
	stateIdle state = iota
	statePendingCompletion
	stateHasCompletion
	stateHasCursorTarget
)

type CursorPredictionConfig struct {
	Enabled       bool // Show jump indicators (default: true)
	AutoAdvance   bool // On no-op, jump to last line + retrigger (default: true)
	DistThreshold int  // Lines apart to trigger staging (default: 3)
}

type EngineConfig struct {
	NsID                int
	CompletionTimeout   time.Duration
	IdleCompletionDelay time.Duration
	TextChangeDebounce  time.Duration
	CursorPrediction    CursorPredictionConfig
	MaxDiffTokens       int // Maximum tokens for diff history per file (0 = no limit)
}

type Engine struct {
	WorkspacePath string
	WorkspaceID   string

	provider        types.Provider
	n               *nvim.Nvim
	buffer          *text.Buffer
	state           state
	ctx             context.Context
	currentCancel   context.CancelFunc
	prefetchCancel  context.CancelFunc
	idleTimer       *time.Timer
	textChangeTimer *time.Timer
	mu              sync.RWMutex
	eventChan       chan Event

	// Main context and cancel for the engine lifecycle
	mainCtx    context.Context
	mainCancel context.CancelFunc
	stopped    bool
	stopOnce   sync.Once

	// Completion state
	completions  []*types.Completion
	applyBatch   *nvim.Batch
	cursorTarget *types.CursorPredictionTarget

	// Staged completion state (for multi-stage completions)
	stagedCompletion *types.StagedCompletion

	// Original buffer lines when completion was shown (for partial typing optimization)
	completionOriginalLines []string

	// Prefetch state
	prefetchedCompletions  []*types.Completion
	prefetchedCursorTarget *types.CursorPredictionTarget
	prefetchState          prefetchState

	// Config options
	config EngineConfig

	// Per-file cumulative diff histories within the current workspace
	fileDiffStore map[string][]*types.DiffEntry
}

func NewEngine(provider types.Provider, config EngineConfig) (*Engine, error) {
	workspacePath, err := os.Getwd()
	if err != nil {
		logger.Warn("error getting current directory, using home: %v", err)
		workspacePath = "~"
	}
	workspaceID := fmt.Sprintf("%s-%d", workspacePath, os.Getpid())

	buffer, err := text.NewBuffer(text.BufferConfig{
		NsID: config.NsID,
	})
	if err != nil {
		return nil, err
	}

	return &Engine{
		WorkspacePath:           workspacePath,
		WorkspaceID:             workspaceID,
		provider:                provider,
		n:                       nil, // Will be set later via SetNvim
		buffer:                  buffer,
		state:                   stateIdle,
		ctx:                     nil,
		eventChan:               make(chan Event, 100),
		config:                  config,
		idleTimer:               nil,
		textChangeTimer:         nil,
		mu:                      sync.RWMutex{},
		completions:             nil,
		cursorTarget:            nil,
		prefetchedCompletions:  nil,
		prefetchedCursorTarget: nil,
		prefetchState:          prefetchNone,
		stopped:                false,
		fileDiffStore:           make(map[string][]*types.DiffEntry),
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
	if opts.CallOnReject && e.n != nil {
		e.buffer.OnReject(e.n)
	}
	e.completions = nil
	e.applyBatch = nil
	if opts.ClearStaged {
		e.stagedCompletion = nil
	}
	e.completionOriginalLines = nil
}

// clearAll clears everything including prefetch and staged completions
func (e *Engine) clearAll() {
	e.clearState(ClearOptions{CancelCurrent: true, CancelPrefetch: true, ClearStaged: true, ClearCursorTarget: true, CallOnReject: true})
}

// clearKeepPrefetch clears current completion but keeps prefetch data, staged completion state, and cursor target
func (e *Engine) clearKeepPrefetch() {
	e.clearState(ClearOptions{CancelCurrent: true, CancelPrefetch: false, ClearStaged: false, ClearCursorTarget: false, CallOnReject: true})
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
		select {
		case <-ctx.Done():
			// Clean shutdown when context is cancelled
			return
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
			logger.Debug("ignoring stale completion: state=%v", e.state)
			return true
		}
		e.handleCompletionReadyImpl(event.Data.(*types.CompletionResponse))
		return true

	case EventCompletionError:
		if err, ok := event.Data.(error); ok && errors.Is(err, context.Canceled) {
			logger.Debug("completion canceled: %v", err)
		} else {
			logger.Error("completion error: %v", event.Data)
		}
		return true

	case EventPrefetchReady:
		// Guard: only process if we're expecting prefetch results
		if e.prefetchState != prefetchInFlight &&
			e.prefetchState != prefetchWaitingForTab &&
			e.prefetchState != prefetchWaitingForCursorPrediction {
			logger.Debug("ignoring stale prefetch: prefetchState=%v", e.prefetchState)
			return true
		}
		e.handlePrefetchReady(event.Data.(*types.CompletionResponse))
		return true

	case EventPrefetchError:
		// Guard: only process if we're expecting prefetch results
		if e.prefetchState != prefetchInFlight &&
			e.prefetchState != prefetchWaitingForTab &&
			e.prefetchState != prefetchWaitingForCursorPrediction {
			logger.Debug("ignoring stale prefetch error: prefetchState=%v", e.prefetchState)
			return true
		}
		// Nil-safe error handling
		if err, ok := event.Data.(error); ok {
			e.handlePrefetchError(err)
		} else {
			e.handlePrefetchError(nil)
		}
		return true
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

func (e *Engine) requestCompletion(source types.CompletionSource) {
	// Check if stopped before making request
	if e.stopped || e.n == nil {
		return
	}

	e.state = statePendingCompletion
	e.buffer.SyncIn(e.n, e.WorkspacePath)

	ctx, cancel := context.WithTimeout(e.mainCtx, e.config.CompletionTimeout)
	e.currentCancel = cancel

	go func() {
		defer cancel()

		result, err := e.provider.GetCompletion(ctx, &types.CompletionRequest{
			Source:            source,
			WorkspacePath:     e.WorkspacePath,
			WorkspaceID:       e.WorkspaceID,
			FilePath:          e.buffer.Path,
			Lines:             e.buffer.Lines,
			Version:           e.buffer.Version,
			PreviousLines:     e.buffer.PreviousLines,
			FileDiffHistories: e.getAllFileDiffHistories(),
			CursorRow:         e.buffer.Row,
			CursorCol:         e.buffer.Col,
			LinterErrors:      e.buffer.GetProviderLinterErrors(e.n),
		})

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

	distance := abs(int(e.cursorTarget.LineNumber) - e.buffer.Row)
	if distance <= e.config.CursorPrediction.DistThreshold {
		// Close enough - don't show cursor prediction
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
	e.buffer.OnCursorPredictionReady(e.n, int(e.cursorTarget.LineNumber))
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

func (e *Engine) acceptCompletion() {
	if e.applyBatch != nil {
		if err := e.applyBatch.Execute(); err != nil {
			logger.Error("error applying completion: %v", err)
		}
	}

	// Commit pending file changes only after successful apply
	e.buffer.CommitPendingEdit()

	// After commit, update per-file diff store for the current file
	if e.buffer.Path != "" && len(e.buffer.DiffHistories) > 0 {
		// Copy slice to avoid aliasing
		diffs := make([]*types.DiffEntry, len(e.buffer.DiffHistories))
		copy(diffs, e.buffer.DiffHistories)
		e.fileDiffStore[e.buffer.Path] = diffs

		// Keep at most two files in file histories
		e.trimFileDiffStoreToMaxFiles(2)
	}

	e.clearKeepPrefetch()

	// Handle staged completions: if there are more stages, show cursor target to next stage
	if e.stagedCompletion != nil {
		e.stagedCompletion.CurrentIdx++
		if e.stagedCompletion.CurrentIdx < len(e.stagedCompletion.Stages) {
			// More stages remaining - sync buffer and show cursor target to next stage
			e.buffer.SyncIn(e.n, e.WorkspacePath)

			// At n-1 stage (one stage remaining), trigger prefetch early so it has
			// fresh context and time to complete before the last stage is accepted.
			// Use stage n's StartLine as context position (where the user will be looking).
			if e.stagedCompletion.CurrentIdx == len(e.stagedCompletion.Stages)-1 {
				lastStage := e.stagedCompletion.Stages[len(e.stagedCompletion.Stages)-1]
				if lastStage.CursorTarget != nil && lastStage.CursorTarget.ShouldRetrigger && lastStage.Completion != nil {
					overrideRow := max(1, lastStage.Completion.StartLine)
					e.requestPrefetch(types.CompletionSourceTyping, overrideRow, 0)
				}
			}

			e.handleCursorTarget() // Shows jump indicator to next stage
			return
		}
		// All stages complete - clear staged completion
		// (cursorTarget already has ShouldRetrigger from last stage if applicable)
		e.stagedCompletion = nil
	}

	// Sync buffer to get the updated state after applying completion
	e.buffer.SyncIn(e.n, e.WorkspacePath)

	// Prefetch next completion if cursor target requests retrigger (after applying current completion)
	// Skip if prefetch is already in flight (e.g., triggered at n-1 stage)
	if e.cursorTarget != nil && e.cursorTarget.ShouldRetrigger && e.prefetchState == prefetchNone {
		// Prefetch targeting the predicted cursor line
		overrideRow := max(1, int(e.cursorTarget.LineNumber))
		e.requestPrefetch(types.CompletionSourceTyping, overrideRow, 0)
	}

	e.handleCursorTarget()
}

// trimFileDiffStoreToMaxFiles keeps only the most recent maxFiles files in the diff store
func (e *Engine) trimFileDiffStoreToMaxFiles(maxFiles int) {
	if len(e.fileDiffStore) <= maxFiles {
		return
	}

	// Convert to slice for sorting by some criteria (e.g., file name for deterministic behavior)
	type fileEntry struct {
		fileName string
		diffs    []*types.DiffEntry
	}

	var entries []fileEntry
	for fileName, diffs := range e.fileDiffStore {
		entries = append(entries, fileEntry{fileName, diffs})
	}

	// Sort by file name to ensure deterministic behavior
	// In a real implementation, you might want to sort by last access time
	for i := 0; i < len(entries)-1; i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[i].fileName > entries[j].fileName {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	// Keep only the first maxFiles entries
	entriesToKeep := entries
	if len(entries) > maxFiles {
		entriesToKeep = entries[:maxFiles]
	}

	// Rebuild the map with only the kept entries
	newFileDiffStore := make(map[string][]*types.DiffEntry)
	for _, entry := range entriesToKeep {
		newFileDiffStore[entry.fileName] = entry.diffs
	}

	e.fileDiffStore = newFileDiffStore
}

// getAllFileDiffHistories returns all known file diff histories in provider format
func (e *Engine) getAllFileDiffHistories() []*types.FileDiffHistory {
	if len(e.fileDiffStore) == 0 {
		return nil
	}
	histories := make([]*types.FileDiffHistory, 0, len(e.fileDiffStore))
	for fileName, diffs := range e.fileDiffStore {
		if len(diffs) == 0 {
			continue
		}
		// Copy to ensure immutability
		copyDiffs := make([]*types.DiffEntry, len(diffs))
		copy(copyDiffs, diffs)

		// Apply token limiting if configured
		if e.config.MaxDiffTokens > 0 {
			copyDiffs = utils.TrimDiffEntries(copyDiffs, e.config.MaxDiffTokens)
		}

		if len(copyDiffs) == 0 {
			continue
		}

		histories = append(histories, &types.FileDiffHistory{
			FileName:    fileName,
			DiffHistory: copyDiffs,
		})
	}
	if len(histories) == 0 {
		return nil
	}
	return histories
}

func (e *Engine) acceptCursorTarget() {
	if e.n == nil || e.cursorTarget == nil {
		return
	}

	err := e.buffer.MoveCursorToStartOfLine(e.n, int(e.cursorTarget.LineNumber), true, true)
	if err != nil {
		logger.Error("error moving cursor: %v", err)
	}

	if e.n != nil {
		e.buffer.OnReject(e.n)
	}

	// Handle staged completions: if there are more stages, show the next stage
	if e.stagedCompletion != nil && e.stagedCompletion.CurrentIdx < len(e.stagedCompletion.Stages) {
		e.buffer.SyncIn(e.n, e.WorkspacePath)
		e.showCurrentStage()
		return
	}

	// Try to use prefetched completion
	if e.usePrefetchedCompletion() {
		return
	}

	// If prefetch is in progress, wait for it to complete instead of triggering new request
	if e.prefetchState == prefetchInFlight {
		e.prefetchState = prefetchWaitingForTab
		return
	}

	if e.cursorTarget.ShouldRetrigger {
		e.requestCompletion(types.CompletionSourceTyping)
		e.state = stateIdle
		e.cursorTarget = nil
		return
	}

	e.state = stateIdle
	e.cursorTarget = nil
}

// showCurrentStage displays the current stage of a multi-stage completion
func (e *Engine) showCurrentStage() {
	if e.stagedCompletion == nil || e.stagedCompletion.CurrentIdx >= len(e.stagedCompletion.Stages) {
		return
	}

	stage := e.stagedCompletion.Stages[e.stagedCompletion.CurrentIdx]

	e.completions = []*types.Completion{stage.Completion}
	e.cursorTarget = stage.CursorTarget
	e.state = stateHasCompletion

	e.applyBatch = e.buffer.OnCompletionReady(
		e.n,
		stage.Completion.StartLine,
		stage.Completion.EndLineInc,
		stage.Completion.Lines,
	)

	// Store original buffer lines for partial typing optimization
	e.completionOriginalLines = nil
	for i := stage.Completion.StartLine; i <= stage.Completion.EndLineInc && i-1 < len(e.buffer.Lines); i++ {
		e.completionOriginalLines = append(e.completionOriginalLines, e.buffer.Lines[i-1])
	}
}

// SetNvim sets a new nvim instance for the engine (used for socket connections)
func (e *Engine) SetNvim(n *nvim.Nvim) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Don't change if stopped
	if e.stopped {
		return
	}

	e.n = n

	// Re-register the event handler for the new connection
	if err := e.n.RegisterHandler("cursortab_event", func(n *nvim.Nvim, event string) {
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
