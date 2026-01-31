package engine

import (
	"cursortab/text"
	"cursortab/types"
)

// doPartialAcceptCompletion is the action for EventPartialAccept from stateHasCompletion
func (e *Engine) doPartialAcceptCompletion(event Event) {
	e.partialAcceptCompletion()
}

// doPartialAcceptStreaming is the action for EventPartialAccept from stateStreamingCompletion
func (e *Engine) doPartialAcceptStreaming(event Event) {
	// Only allow if first stage was rendered during streaming
	if e.streamingState != nil && e.streamingState.FirstStageRendered {
		e.cancelLineStreamingKeepPartial()
		e.partialAcceptCompletion()
	}
}

// partialAcceptCompletion handles partial accept logic
func (e *Engine) partialAcceptCompletion() {
	if len(e.completions) == 0 {
		return
	}

	e.syncBuffer()

	// Get current stage groups
	groups := e.getCurrentGroups()
	if len(groups) == 0 {
		return
	}

	firstGroup := groups[0]

	if firstGroup.RenderHint == "append_chars" {
		e.partialAcceptAppendChars(firstGroup)
	} else {
		e.partialAcceptNextLine()
	}
}

// getCurrentGroups returns the groups for the current completion/stage
func (e *Engine) getCurrentGroups() []*text.Group {
	if e.stagedCompletion != nil {
		stage := e.getStage(e.stagedCompletion.CurrentIdx)
		if stage != nil {
			return stage.Groups
		}
	}
	// For non-staged completions, use stored groups
	return e.currentGroups
}

// partialAcceptAppendChars handles partial accept for append_chars hint
func (e *Engine) partialAcceptAppendChars(group *text.Group) {
	bufferLines := e.buffer.Lines()
	lineIdx := group.BufferLine - 1
	if lineIdx < 0 || lineIdx >= len(bufferLines) {
		return
	}
	currentLine := bufferLines[lineIdx]

	if len(e.completions) == 0 || len(e.completions[0].Lines) == 0 {
		return
	}
	targetLine := e.completions[0].Lines[0]

	// Calculate remaining ghost text
	if len(currentLine) >= len(targetLine) {
		e.finishPartialAccept()
		return
	}
	remainingGhost := targetLine[len(currentLine):]

	// Find next word boundary
	acceptLen := text.FindNextWordBoundary(remainingGhost)
	textToAccept := remainingGhost[:acceptLen]

	// Insert text at end of current line
	if err := e.buffer.InsertText(lineIdx+1, len(currentLine), textToAccept); err != nil {
		return
	}

	// Check if fully accepted
	newLineLen := len(currentLine) + acceptLen
	if newLineLen >= len(targetLine) {
		e.finishPartialAccept()
	} else {
		// Update UI with remaining ghost text
		e.rerenderAfterPartialAccept()
	}
}

// partialAcceptNextLine handles partial accept for non-append_chars (line-by-line)
func (e *Engine) partialAcceptNextLine() {
	if len(e.completions) == 0 || len(e.completions[0].Lines) == 0 {
		return
	}

	completion := e.completions[0]
	firstLine := completion.Lines[0]

	// Replace first line
	if err := e.buffer.ReplaceLine(completion.StartLine, firstLine); err != nil {
		return
	}

	if len(completion.Lines) == 1 {
		e.finishPartialAccept()
	} else {
		// Update completion to remaining lines
		e.completions[0].Lines = completion.Lines[1:]
		e.completions[0].StartLine++
		e.completions[0].EndLineInc = e.completions[0].StartLine + len(e.completions[0].Lines) - 1

		// Re-render remaining completion
		e.rerenderAfterPartialAccept()
	}
}

// finishPartialAccept handles completion of a partial accept when nothing remains
func (e *Engine) finishPartialAccept() {
	e.buffer.CommitPending()
	e.saveCurrentFileState()
	e.clearKeepPrefetch()

	// Handle staged completion advancement (similar to acceptCompletion logic)
	if e.stagedCompletion != nil {
		// Track cumulative offset for unequal line count stages
		currentStage := e.getStage(e.stagedCompletion.CurrentIdx)
		if currentStage != nil {
			oldLineCount := currentStage.BufferEnd - currentStage.BufferStart + 1
			newLineCount := len(currentStage.Lines)
			e.stagedCompletion.CumulativeOffset += newLineCount - oldLineCount
		}

		e.stagedCompletion.CurrentIdx++
		if e.stagedCompletion.CurrentIdx < len(e.stagedCompletion.Stages) {
			e.syncBuffer()

			// Apply cumulative offset to remaining stages
			if e.stagedCompletion.CumulativeOffset != 0 {
				for i := e.stagedCompletion.CurrentIdx; i < len(e.stagedCompletion.Stages); i++ {
					stage := e.getStage(i)
					if stage != nil {
						stage.BufferStart += e.stagedCompletion.CumulativeOffset
						stage.BufferEnd += e.stagedCompletion.CumulativeOffset
					}
					if stage != nil && stage.CursorTarget != nil {
						stage.CursorTarget.LineNumber += int32(e.stagedCompletion.CumulativeOffset)
					}
				}
				e.stagedCompletion.CumulativeOffset = 0
			}

			// At n-1 stage (one stage remaining), trigger prefetch early so it has
			// fresh context and time to complete before the last stage is accepted.
			if e.stagedCompletion.CurrentIdx == len(e.stagedCompletion.Stages)-1 {
				lastStage := e.getStage(len(e.stagedCompletion.Stages) - 1)
				if lastStage != nil && lastStage.CursorTarget != nil && lastStage.CursorTarget.ShouldRetrigger {
					overrideRow := max(1, lastStage.BufferStart)
					e.requestPrefetch(types.CompletionSourceTyping, overrideRow, 0)
				}
			}

			// Set cursorTarget from next stage before calling handleCursorTarget
			nextStage := e.getStage(e.stagedCompletion.CurrentIdx)
			if nextStage != nil && nextStage.CursorTarget != nil {
				e.cursorTarget = nextStage.CursorTarget
			}

			e.handleCursorTarget()
			return
		}
		e.stagedCompletion = nil
	}

	// Sync buffer to get the updated state after applying completion
	e.syncBuffer()

	// Prefetch next completion if cursor target requests retrigger (after applying current completion)
	// Skip if prefetch is already in flight (e.g., triggered at n-1 stage)
	if e.cursorTarget != nil && e.cursorTarget.ShouldRetrigger && e.prefetchState == prefetchNone {
		overrideRow := max(1, int(e.cursorTarget.LineNumber))
		e.requestPrefetch(types.CompletionSourceTyping, overrideRow, 0)
	}

	e.handleCursorTarget()
}

// rerenderAfterPartialAccept re-renders the remaining ghost text after partial accept
func (e *Engine) rerenderAfterPartialAccept() {
	e.syncBuffer()

	if len(e.completions) == 0 {
		return
	}

	completion := e.completions[0]

	// Get current buffer lines for the completion range
	bufferLines := e.buffer.Lines()
	var originalLines []string
	for i := completion.StartLine; i <= completion.EndLineInc && i-1 < len(bufferLines); i++ {
		originalLines = append(originalLines, bufferLines[i-1])
	}

	// Re-analyze diff for updated groups
	diffResult := text.ComputeDiff(
		text.JoinLines(originalLines),
		text.JoinLines(completion.Lines),
	)

	// Group the changes
	groups := text.GroupChanges(diffResult.Changes)

	// Set BufferLine for each group (absolute buffer position)
	for _, g := range groups {
		g.BufferLine = completion.StartLine + g.StartLine - 1
	}

	// Re-render with updated groups
	e.applyBatch = e.buffer.PrepareCompletion(
		completion.StartLine,
		completion.EndLineInc,
		completion.Lines,
		groups,
	)

	// Store groups for next partial accept
	e.currentGroups = groups

	// Update original lines snapshot
	e.completionOriginalLines = originalLines
}
