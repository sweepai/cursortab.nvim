package engine

import (
	"context"
	"errors"

	"cursortab/logger"
	"cursortab/text"
	"cursortab/types"
)

// prefetchState represents the state of prefetch operations
type prefetchState int

const (
	prefetchNone prefetchState = iota
	prefetchInFlight
	prefetchWaitingForTab
	prefetchWaitingForCursorPrediction
	prefetchReady
)

// requestPrefetch requests a completion for a specific cursor position without changing the engine state
func (e *Engine) requestPrefetch(source types.CompletionSource, overrideRow int, overrideCol int) {
	if e.stopped {
		return
	}

	// Cancel existing prefetch if any
	if e.prefetchCancel != nil {
		e.prefetchCancel()
		e.prefetchCancel = nil
		e.prefetchState = prefetchNone
	}

	// Sync buffer to ensure latest context
	e.syncBuffer()

	ctx, cancel := context.WithTimeout(e.mainCtx, e.config.CompletionTimeout)
	e.prefetchCancel = cancel
	e.prefetchState = prefetchInFlight

	// Snapshot required values to avoid races with buffer mutation
	lines := append([]string{}, e.buffer.Lines()...)
	previousLines := append([]string{}, e.buffer.PreviousLines()...)
	version := e.buffer.Version()
	filePath := e.buffer.Path()
	linterErrors := e.buffer.LinterErrors()
	viewportHeight := e.getViewportHeightConstraint()

	go func() {
		defer cancel()

		result, err := e.provider.GetCompletion(ctx, &types.CompletionRequest{
			Source:            source,
			WorkspacePath:     e.WorkspacePath,
			WorkspaceID:       e.WorkspaceID,
			FilePath:          filePath,
			Lines:             lines,
			Version:           version,
			PreviousLines:     previousLines,
			FileDiffHistories: e.getAllFileDiffHistories(),
			CursorRow:         overrideRow,
			CursorCol:         overrideCol,
			ViewportHeight:    viewportHeight,
			LinterErrors:      linterErrors,
		})

		if err != nil {
			select {
			case e.eventChan <- Event{Type: EventPrefetchError, Data: err}:
			case <-e.mainCtx.Done():
			}
			return
		}

		select {
		case e.eventChan <- Event{Type: EventPrefetchReady, Data: result}:
		case <-e.mainCtx.Done():
		}
	}()
}

// handlePrefetchReady processes a successful prefetch response
func (e *Engine) handlePrefetchReady(resp *types.CompletionResponse) {
	e.prefetchedCompletions = resp.Completions
	e.prefetchedCursorTarget = resp.CursorTarget
	previousPrefetchState := e.prefetchState
	e.prefetchState = prefetchReady

	// If we were waiting for prefetch due to tab press, continue with cursor target logic
	if previousPrefetchState == prefetchWaitingForTab {
		e.handleDeferredCursorTarget()
		return
	}

	// If we were waiting for prefetch to show cursor prediction,
	// but only if we're not currently showing a completion to the user
	if previousPrefetchState == prefetchWaitingForCursorPrediction {
		// Don't interrupt an active completion - let user accept/reject it first
		if e.state == stateHasCompletion || e.state == stateStreamingCompletion {
			return
		}
		e.handlePrefetchCursorPrediction()
	}
}

// handlePrefetchCursorPrediction checks if prefetch should be shown immediately or as cursor target
func (e *Engine) handlePrefetchCursorPrediction() {
	if len(e.prefetchedCompletions) == 0 {
		return
	}

	comp := e.prefetchedCompletions[0]

	// Extract old lines for diff analysis
	bufferLines := e.buffer.Lines()
	var oldLines []string
	for i := comp.StartLine; i <= comp.EndLineInc && i-1 < len(bufferLines); i++ {
		oldLines = append(oldLines, bufferLines[i-1])
	}

	// Find first changed line
	targetLine := text.FindFirstChangedLine(oldLines, comp.Lines, comp.StartLine-1)
	if targetLine <= 0 {
		return
	}

	// Check distance to determine if show completion or cursor prediction
	distance := abs(targetLine - e.buffer.Row())
	if distance <= e.config.CursorPrediction.ProximityThreshold {
		e.tryShowPrefetchedCompletion()
	} else {
		// Show cursor prediction to the target line
		e.cursorTarget = &types.CursorPredictionTarget{
			RelativePath:    e.buffer.Path(),
			LineNumber:      int32(targetLine),
			ShouldRetrigger: false,
		}
		e.state = stateHasCursorTarget
		e.buffer.ShowCursorTarget(targetLine)
	}
}

// tryShowPrefetchedCompletion attempts to show prefetched completion immediately.
// Returns true if completion was shown, false otherwise.
func (e *Engine) tryShowPrefetchedCompletion() bool {
	if len(e.prefetchedCompletions) == 0 {
		return false
	}

	// Sync buffer to get current cursor position
	e.syncBuffer()

	comp := e.prefetchedCompletions[0]

	// Clear prefetch state before processing
	e.prefetchedCompletions = nil
	e.prefetchedCursorTarget = nil
	e.prefetchState = prefetchNone

	return e.processCompletion(comp)
}

// handlePrefetchError processes a prefetch error
func (e *Engine) handlePrefetchError(err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("prefetch error: %v", err)
	}

	previousPrefetchState := e.prefetchState
	e.prefetchState = prefetchNone

	// If we were waiting for prefetch due to tab press, fall back to original logic
	if previousPrefetchState == prefetchWaitingForTab {
		e.handleDeferredCursorTarget()
	}
}

// handleDeferredCursorTarget handles cursor target logic that was deferred due to prefetch in progress
func (e *Engine) handleDeferredCursorTarget() {
	if e.cursorTarget == nil {
		return
	}

	// Check if we now have prefetched completions
	if len(e.prefetchedCompletions) > 0 {
		// Sync buffer to get updated cursor position
		e.syncBuffer()

		comp := e.prefetchedCompletions[0]

		// Clear prefetch state before processing
		e.prefetchedCompletions = nil
		e.prefetchedCursorTarget = nil
		e.prefetchState = prefetchNone

		if e.processCompletion(comp) {
			return
		}

		// No changes
		e.handleCursorTarget()
		return
	}

	// Fall back to original behavior - trigger new completion if needed
	if e.cursorTarget.ShouldRetrigger {
		e.requestCompletion(types.CompletionSourceTyping)
		e.state = stateIdle
		e.cursorTarget = nil
		return
	}

	e.state = stateIdle
	e.cursorTarget = nil
}
