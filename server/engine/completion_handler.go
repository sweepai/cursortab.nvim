package engine

import (
	"cursortab/types"
)

// handleCompletionReadyImpl processes a successful completion response.
func (e *Engine) handleCompletionReadyImpl(response *types.CompletionResponse) {
	e.syncBuffer()

	if len(response.Completions) == 0 {
		e.handleCursorTarget()
		return
	}

	completion := response.Completions[0]

	if e.processCompletion(completion) {
		return
	}

	// No changes - set up cursor target if auto-advance enabled
	e.handleCompletionNoChanges(completion)
}

// handleCompletionNoChanges handles the case where completion has no changes.
func (e *Engine) handleCompletionNoChanges(completion *types.Completion) {
	if e.config.CursorPrediction.AutoAdvance && e.config.CursorPrediction.Enabled {
		e.cursorTarget = &types.CursorPredictionTarget{
			LineNumber:      int32(completion.EndLineInc),
			ShouldRetrigger: true,
		}
	}
	e.handleCursorTarget()
}

// handleTextChangeImpl handles text change when we have an active completion.
// It checks if the user typed content that matches the prediction.
func (e *Engine) handleTextChangeImpl() {
	if len(e.completions) == 0 {
		e.rejectAndRearmTimer()
		return
	}

	e.syncBuffer()

	matches, hasRemaining := e.checkTypingMatchesPrediction()
	if matches {
		if hasRemaining {
			// Typing matches - Lua already updated the visual, just keep completion state
			return
		}
		// User typed everything - completion fully typed
		e.clearAll()
		e.state = stateIdle
		e.startTextChangeTimer()
		return
	}

	// Typing does not match prediction
	e.rejectAndRearmTimer()
}

// rejectAndRearmTimer rejects the current completion and restarts the text change timer.
func (e *Engine) rejectAndRearmTimer() {
	e.reject()
	e.startTextChangeTimer()
}

// checkTypingMatchesPrediction checks if the current buffer state (after user typed)
// matches the prediction, meaning the user typed content consistent with the completion.
// Returns (matches, hasRemaining) where:
// - matches: true if the current buffer is a valid prefix of the target
// - hasRemaining: true if there's still content left to predict
func (e *Engine) checkTypingMatchesPrediction() (bool, bool) {
	if len(e.completions) == 0 || len(e.completionOriginalLines) == 0 {
		return false, false
	}

	completion := e.completions[0]
	targetLines := completion.Lines
	originalLines := e.completionOriginalLines
	bufferLines := e.buffer.Lines()

	if len(targetLines) == 0 {
		return false, false
	}

	startIdx := completion.StartLine - 1 // Convert to 0-indexed
	if startIdx < 0 || startIdx >= len(bufferLines) {
		return false, false
	}

	// For simplicity, only optimize when completion doesn't delete lines
	if len(targetLines) < len(originalLines) {
		return false, false
	}

	// Get current buffer lines in the target range
	targetEndIdx := startIdx + len(targetLines) - 1
	var currentLines []string
	for i := startIdx; i <= targetEndIdx && i < len(bufferLines); i++ {
		currentLines = append(currentLines, bufferLines[i])
	}

	if len(currentLines) == 0 {
		return false, false
	}

	// Check each current line is a prefix of the corresponding target line
	// and track if user made progress
	madeProgress := false
	for i, currentLine := range currentLines {
		if i >= len(targetLines) {
			return false, false
		}

		targetLine := targetLines[i]

		// Current line must be a prefix of target line
		if len(currentLine) > len(targetLine) {
			return false, false
		}
		if currentLine != targetLine[:len(currentLine)] {
			return false, false
		}

		// Check if user made progress compared to original
		if i < len(originalLines) {
			if currentLine != originalLines[i] && len(currentLine) > len(originalLines[i]) {
				madeProgress = true
			}
		} else if currentLine != "" {
			madeProgress = true
		}
	}

	if !madeProgress {
		return false, false
	}

	// Check if there's remaining content to predict
	hasRemaining := len(currentLines) < len(targetLines)
	if !hasRemaining {
		for i := range currentLines {
			if i < len(targetLines) && len(currentLines[i]) < len(targetLines[i]) {
				hasRemaining = true
				break
			}
		}
	}

	return true, hasRemaining
}
