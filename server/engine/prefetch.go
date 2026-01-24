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

// String returns a human-readable name for the prefetch state
func (s prefetchState) String() string {
	switch s {
	case prefetchNone:
		return "None"
	case prefetchInFlight:
		return "InFlight"
	case prefetchWaitingForTab:
		return "WaitingForTab"
	case prefetchWaitingForCursorPrediction:
		return "WaitingForCursorPrediction"
	case prefetchReady:
		return "Ready"
	default:
		return "Unknown"
	}
}

// resolveCursorTarget returns the appropriate cursor target for a completion.
// Uses prefetchedCursorTarget if available, otherwise creates one with auto_advance if enabled.
func (e *Engine) resolveCursorTarget(endLineInc int) *types.CursorPredictionTarget {
	if e.prefetchedCursorTarget != nil {
		return e.prefetchedCursorTarget
	}
	if e.config.CursorPrediction.AutoAdvance && e.config.CursorPrediction.Enabled {
		return &types.CursorPredictionTarget{
			RelativePath:    e.buffer.Path,
			LineNumber:      int32(endLineInc),
			ShouldRetrigger: true,
		}
	}
	return nil
}

// requestPrefetch requests a completion for a specific cursor position without changing the engine state
func (e *Engine) requestPrefetch(source types.CompletionSource, overrideRow int, overrideCol int) {
	if e.stopped || e.n == nil {
		return
	}

	// Cancel existing prefetch if any
	if e.prefetchCancel != nil {
		e.prefetchCancel()
		e.prefetchCancel = nil
		e.prefetchState = prefetchNone
	}

	// Sync buffer to ensure latest context
	e.buffer.SyncIn(e.n, e.WorkspacePath)

	ctx, cancel := context.WithTimeout(e.mainCtx, e.config.CompletionTimeout)
	e.prefetchCancel = cancel
	e.prefetchState = prefetchInFlight

	// Snapshot required values to avoid races with buffer mutation
	lines := append([]string{}, e.buffer.Lines...)
	previousLines := append([]string{}, e.buffer.PreviousLines...)
	version := e.buffer.Version
	filePath := e.buffer.Path
	linterErrors := e.buffer.GetProviderLinterErrors(e.n)

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
	}

	// If we were waiting for prefetch to show cursor prediction (last stage case),
	// check if first change is close enough to show completion, otherwise show cursor prediction
	if previousPrefetchState == prefetchWaitingForCursorPrediction {
		if len(e.prefetchedCompletions) > 0 && e.n != nil {
			comp := e.prefetchedCompletions[0]
			// Extract old lines from buffer for the completion range
			var oldLines []string
			for i := comp.StartLine; i <= comp.EndLineInc && i-1 < len(e.buffer.Lines); i++ {
				oldLines = append(oldLines, e.buffer.Lines[i-1])
			}
			// Find the first line that actually differs
			targetLine := text.FindFirstChangedLine(oldLines, comp.Lines, comp.StartLine-1)

			if targetLine > 0 {
				distance := abs(targetLine - e.buffer.Row)
				if distance <= e.config.CursorPrediction.DistThreshold {
					// Close enough - show completion immediately
					e.tryShowPrefetchedCompletion()
				} else {
					// Far away - show cursor prediction to that line
					e.cursorTarget = &types.CursorPredictionTarget{
						RelativePath:    e.buffer.Path,
						LineNumber:      int32(targetLine),
						ShouldRetrigger: false, // Will use prefetched data
					}
					e.state = stateHasCursorTarget
					e.buffer.OnCursorPredictionReady(e.n, targetLine)
				}
			}
		}
	}
}

// tryShowPrefetchedCompletion attempts to show prefetched completion immediately.
// Returns true if completion was shown, false otherwise.
func (e *Engine) tryShowPrefetchedCompletion() bool {
	if len(e.prefetchedCompletions) == 0 || e.n == nil {
		return false
	}

	comp := e.prefetchedCompletions[0]

	// Check if there are actual changes before transitioning to HasCompletion state
	if !e.buffer.HasChanges(comp.StartLine, comp.EndLineInc, comp.Lines) {
		logger.Debug("no changes to completion (prefetched tryShow)")
		e.prefetchedCompletions = nil
		e.prefetchedCursorTarget = nil
		e.prefetchState = prefetchNone
		return false
	}

	e.state = stateHasCompletion
	e.completions = e.prefetchedCompletions
	e.cursorTarget = e.resolveCursorTarget(comp.EndLineInc)
	e.prefetchedCompletions = nil
	e.prefetchedCursorTarget = nil
	e.prefetchState = prefetchNone
	e.applyBatch = e.buffer.OnCompletionReady(e.n, comp.StartLine, comp.EndLineInc, comp.Lines)
	return true
}

// handlePrefetchError processes a prefetch error
func (e *Engine) handlePrefetchError(err error) {
	if err != nil && errors.Is(err, context.Canceled) {
		logger.Debug("prefetch canceled: %v", err)
	} else if err != nil {
		logger.Error("prefetch error: %v", err)
	} else {
		logger.Debug("prefetch error: nil")
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
	if e.n == nil || e.cursorTarget == nil {
		return
	}

	// Check if we now have prefetched completions
	if len(e.prefetchedCompletions) > 0 {
		// Sync buffer to get updated cursor position
		e.buffer.SyncIn(e.n, e.WorkspacePath)

		e.state = stateHasCompletion
		e.completions = e.prefetchedCompletions
		e.cursorTarget = e.resolveCursorTarget(e.completions[0].EndLineInc)
		e.prefetchedCompletions = nil
		e.prefetchedCursorTarget = nil
		e.prefetchState = prefetchNone

		if e.buffer.HasChanges(e.completions[0].StartLine, e.completions[0].EndLineInc, e.completions[0].Lines) {
			e.applyBatch = e.buffer.OnCompletionReady(e.n, e.completions[0].StartLine, e.completions[0].EndLineInc, e.completions[0].Lines)
		} else {
			logger.Debug("no changes to completion (deferred prefetched)")
			e.handleCursorTarget()
		}

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

// usePrefetchedCompletion attempts to use prefetched data when accepting a cursor target.
// Returns true if prefetched data was used, false if caller should handle normally.
func (e *Engine) usePrefetchedCompletion() bool {
	if len(e.prefetchedCompletions) == 0 {
		return false
	}

	// Sync buffer to get updated cursor position after move
	e.buffer.SyncIn(e.n, e.WorkspacePath)

	e.state = stateHasCompletion
	e.completions = e.prefetchedCompletions
	e.cursorTarget = e.resolveCursorTarget(e.completions[0].EndLineInc)
	e.prefetchedCompletions = nil
	e.prefetchedCursorTarget = nil
	e.prefetchState = prefetchNone

	if e.buffer.HasChanges(e.completions[0].StartLine, e.completions[0].EndLineInc, e.completions[0].Lines) {
		e.applyBatch = e.buffer.OnCompletionReady(e.n, e.completions[0].StartLine, e.completions[0].EndLineInc, e.completions[0].Lines)
	} else {
		logger.Debug("no changes to completion (prefetched)")
		e.handleCursorTarget()
	}

	return true
}
