package text

import (
	"cursortab/logger"
	"cursortab/types"
)

// IncrementalDiffBuilder builds diff results incrementally as lines stream in.
// It computes changes line-by-line using similarity matching against old lines.
type IncrementalDiffBuilder struct {
	OldLines    []string           // Original content lines
	NewLines    []string           // Accumulated new content lines
	Changes     map[int]LineChange // Changes keyed by new line number (1-indexed)
	LineMapping *LineMapping       // Coordinate mapping between old and new

	// Tracking state
	oldLineIdx          int          // Current position in old lines (0-indexed)
	usedOldLines        map[int]bool // Old line indices that have been matched
	similarityThreshold float64      // Threshold for considering lines as matches
}

// NewIncrementalDiffBuilder creates a new incremental diff builder
func NewIncrementalDiffBuilder(oldLines []string) *IncrementalDiffBuilder {
	return &IncrementalDiffBuilder{
		OldLines:            oldLines,
		NewLines:            []string{},
		Changes:             make(map[int]LineChange),
		LineMapping:         &LineMapping{NewToOld: []int{}, OldToNew: make([]int, len(oldLines))},
		oldLineIdx:          0,
		usedOldLines:        make(map[int]bool),
		similarityThreshold: 0.3, // Lower threshold to catch modifications like "1" -> "1 mod"
	}
}

// AddLine processes a new line and returns the change if any.
// Returns nil if the line matches exactly (no change).
func (b *IncrementalDiffBuilder) AddLine(line string) *LineChange {
	newLineNum := len(b.NewLines) + 1 // 1-indexed
	b.NewLines = append(b.NewLines, line)

	// Find matching old line
	oldLineNum := b.findMatchingOldLine(line, newLineNum)
	b.LineMapping.NewToOld = append(b.LineMapping.NewToOld, oldLineNum)

	if oldLineNum <= 0 {
		// Pure addition - no matching old line
		// Use the current old line position as anchor if available
		anchorOld := -1
		if b.oldLineIdx > 0 && b.oldLineIdx <= len(b.OldLines) {
			anchorOld = b.oldLineIdx
		}

		change := LineChange{
			Type:       ChangeAddition,
			OldLineNum: anchorOld,
			NewLineNum: newLineNum,
			Content:    line,
		}
		b.Changes[newLineNum] = change
		return &change
	}

	// Mark old line as used
	b.usedOldLines[oldLineNum] = true
	if oldLineNum-1 < len(b.LineMapping.OldToNew) {
		b.LineMapping.OldToNew[oldLineNum-1] = newLineNum
	}

	// Check for exact match
	oldContent := b.OldLines[oldLineNum-1]
	if oldContent == line {
		// Advance oldLineIdx past matched lines
		if oldLineNum > b.oldLineIdx {
			b.oldLineIdx = oldLineNum
		}
		return nil // No change
	}

	// Modification - categorize the change
	changeType, colStart, colEnd := categorizeLineChangeWithColumns(oldContent, line)
	change := LineChange{
		Type:       changeType,
		OldLineNum: oldLineNum,
		NewLineNum: newLineNum,
		OldContent: oldContent,
		Content:    line,
		ColStart:   colStart,
		ColEnd:     colEnd,
	}
	b.Changes[newLineNum] = change

	// Advance oldLineIdx past matched lines
	if oldLineNum > b.oldLineIdx {
		b.oldLineIdx = oldLineNum
	}

	return &change
}

// findMatchingOldLine searches for the best matching old line for the given new line.
// Returns the 1-indexed old line number, or 0 if no match found.
func (b *IncrementalDiffBuilder) findMatchingOldLine(newLine string, _ int) int {
	if len(b.OldLines) == 0 {
		return 0
	}

	// First, check for exact match at expected position
	expectedPos := b.oldLineIdx
	if expectedPos < len(b.OldLines) && !b.usedOldLines[expectedPos+1] {
		if b.OldLines[expectedPos] == newLine {
			return expectedPos + 1 // 1-indexed
		}
	}

	// Search in a window around expected position
	searchStart := max(0, expectedPos-2)
	searchEnd := min(len(b.OldLines), expectedPos+10)

	bestIdx := -1
	bestSimilarity := b.similarityThreshold

	for i := searchStart; i < searchEnd; i++ {
		if b.usedOldLines[i+1] {
			continue // Already matched
		}

		// Check exact match first
		if b.OldLines[i] == newLine {
			return i + 1
		}

		// Check similarity
		similarity := LineSimilarity(newLine, b.OldLines[i])
		if similarity > bestSimilarity {
			bestSimilarity = similarity
			bestIdx = i
		}
	}

	if bestIdx >= 0 {
		return bestIdx + 1
	}

	return 0
}

// IncrementalStageBuilder builds stages incrementally as lines stream in.
// It finalizes stages when gaps or viewport boundaries are detected.
type IncrementalStageBuilder struct {
	OldLines           []string
	BaseLineOffset     int // Where the diff range starts in the buffer (1-indexed)
	ProximityThreshold int
	ViewportTop        int
	ViewportBottom     int
	CursorRow          int
	FilePath           string

	// State
	diffBuilder            *IncrementalDiffBuilder
	currentStage           *Stage
	currentStageInViewport bool
	finalizedStages        []*Stage
	lastChangeBufferLine   int // Track last BUFFER line with a change (not new line number)
}

// NewIncrementalStageBuilder creates a new incremental stage builder
func NewIncrementalStageBuilder(
	oldLines []string,
	baseLineOffset int,
	proximityThreshold int,
	viewportTop, viewportBottom int,
	cursorRow int,
	filePath string,
) *IncrementalStageBuilder {
	return &IncrementalStageBuilder{
		OldLines:             oldLines,
		BaseLineOffset:       baseLineOffset,
		ProximityThreshold:   proximityThreshold,
		ViewportTop:          viewportTop,
		ViewportBottom:       viewportBottom,
		CursorRow:            cursorRow,
		FilePath:             filePath,
		diffBuilder:          NewIncrementalDiffBuilder(oldLines),
		finalizedStages:      []*Stage{},
		lastChangeBufferLine: 0,
	}
}

// AddLine processes a new line and returns a newly finalized stage if any.
// Returns nil if no stage was finalized on this line.
func (b *IncrementalStageBuilder) AddLine(line string) *Stage {
	change := b.diffBuilder.AddLine(line)
	lineNum := len(b.diffBuilder.NewLines) // 1-indexed

	if change == nil {
		// No change on this line - but check if we should finalize based on
		// buffer line gap (where this line maps in the original file).
		if b.currentStage != nil && b.lastChangeBufferLine > 0 {
			// Compute where this unchanged line maps in the buffer
			currentBufferLine := b.computeCurrentBufferLine(lineNum)
			if currentBufferLine > 0 {
				bufferGap := currentBufferLine - b.lastChangeBufferLine
				if bufferGap > b.ProximityThreshold {
					return b.finalizeCurrentStage()
				}
			}
		}
		return nil
	}

	// We have a change - compute its buffer line
	bufferLine := GetBufferLineForChange(*change, lineNum, b.BaseLineOffset, b.diffBuilder.LineMapping)

	// Determine if this change is in viewport
	isInViewport := b.ViewportTop == 0 && b.ViewportBottom == 0 ||
		(bufferLine >= b.ViewportTop && bufferLine <= b.ViewportBottom)

	// Check if this starts a new stage (buffer line gap or viewport boundary)
	if b.shouldStartNewStage(bufferLine, isInViewport) {
		finalized := b.finalizeCurrentStage()
		b.startNewStage(lineNum, bufferLine, *change, isInViewport)
		return finalized
	}

	// Extend current stage or start first one
	if b.currentStage == nil {
		b.startNewStage(lineNum, bufferLine, *change, isInViewport)
	} else {
		b.extendCurrentStage(lineNum, bufferLine, *change)
	}

	return nil
}

// shouldStartNewStage determines if we need to start a new stage based on
// BUFFER LINE gaps (not new line numbers). This ensures stages split when
// changes map to far-apart positions in the original file.
func (b *IncrementalStageBuilder) shouldStartNewStage(bufferLine int, isInViewport bool) bool {
	if b.currentStage == nil {
		return false // First change will be handled by starting new stage
	}

	// Check buffer line gap (not new line gap!)
	if b.lastChangeBufferLine > 0 {
		bufferGap := bufferLine - b.lastChangeBufferLine
		if bufferGap < 0 {
			bufferGap = -bufferGap // Handle out-of-order matches
		}
		if bufferGap > b.ProximityThreshold {
			return true
		}
	}

	// Check viewport boundary crossing
	if b.currentStageInViewport != isInViewport {
		return true
	}

	return false
}

// startNewStage initializes a new stage with the given change
func (b *IncrementalStageBuilder) startNewStage(lineNum int, bufferLine int, change LineChange, isInViewport bool) {
	b.currentStage = &Stage{
		startLine:  lineNum,
		endLine:    lineNum,
		rawChanges: make(map[int]LineChange),
	}
	b.currentStage.rawChanges[lineNum] = change
	b.currentStageInViewport = isInViewport
	b.lastChangeBufferLine = bufferLine

	// Compute initial buffer range
	b.currentStage.BufferStart, b.currentStage.BufferEnd = b.computeStageBufferRange(b.currentStage)
}

// extendCurrentStage adds a change to the current stage
func (b *IncrementalStageBuilder) extendCurrentStage(lineNum int, bufferLine int, change LineChange) {
	b.currentStage.rawChanges[lineNum] = change
	if lineNum > b.currentStage.endLine {
		b.currentStage.endLine = lineNum
	}
	b.lastChangeBufferLine = bufferLine

	// Update buffer range
	b.currentStage.BufferStart, b.currentStage.BufferEnd = b.computeStageBufferRange(b.currentStage)
}

// finalizeCurrentStage finalizes and returns the current stage
func (b *IncrementalStageBuilder) finalizeCurrentStage() *Stage {
	if b.currentStage == nil || len(b.currentStage.rawChanges) == 0 {
		return nil
	}

	stage := b.currentStage

	// Extract content for the stage
	newStartLine, newEndLine := getStageNewLineRange(stage)
	var stageLines []string
	for j := newStartLine; j <= newEndLine && j-1 < len(b.diffBuilder.NewLines); j++ {
		if j > 0 {
			stageLines = append(stageLines, b.diffBuilder.NewLines[j-1])
		}
	}

	// Remap changes to relative line numbers and extract old content
	stageOldLines := make([]string, len(stageLines))
	remappedChanges := make(map[int]LineChange)
	relativeToBufferLine := make(map[int]int)

	// Compute buffer lines for this stage
	lineNumToBufferLine := make(map[int]int)
	for lineNum, change := range stage.rawChanges {
		bufferLine := GetBufferLineForChange(change, lineNum, b.BaseLineOffset, b.diffBuilder.LineMapping)
		lineNumToBufferLine[lineNum] = bufferLine
	}

	for lineNum, change := range stage.rawChanges {
		newLineNum := lineNum
		if change.NewLineNum > 0 {
			newLineNum = change.NewLineNum
		}
		relativeLine := newLineNum - newStartLine + 1
		relativeIdx := newLineNum - newStartLine

		if relativeIdx >= 0 && relativeIdx < len(stageOldLines) {
			stageOldLines[relativeIdx] = change.OldContent
		}

		if relativeLine > 0 && relativeLine <= len(stageLines) {
			relativeToBufferLine[relativeLine] = lineNumToBufferLine[lineNum]

			remappedChange := change
			remappedChange.NewLineNum = relativeLine
			remappedChanges[relativeLine] = remappedChange
		}
	}

	// Compute groups
	groups := GroupChanges(remappedChanges)

	// Find the last modification's relative line for addition positioning
	lastModificationLine := 0
	modificationBufferLine := stage.BufferStart
	for relativeLine, change := range remappedChanges {
		if change.Type == ChangeModification || change.Type == ChangeAppendChars ||
			change.Type == ChangeDeleteChars || change.Type == ChangeReplaceChars {
			if relativeLine > lastModificationLine {
				lastModificationLine = relativeLine
				if bufLine, ok := relativeToBufferLine[relativeLine]; ok {
					modificationBufferLine = bufLine
				}
			}
		}
	}

	// Set buffer line for each group
	for _, g := range groups {
		if g.Type == "addition" && lastModificationLine > 0 && g.StartLine > lastModificationLine {
			g.BufferLine = modificationBufferLine + 1
		} else if bufLine, ok := relativeToBufferLine[g.StartLine]; ok {
			g.BufferLine = bufLine
		} else {
			g.BufferLine = stage.BufferStart + g.StartLine - 1
		}
	}

	cursorLine, cursorCol := CalculateCursorPosition(remappedChanges, stageLines)

	// Populate stage fields
	stage.Lines = stageLines
	stage.Changes = remappedChanges
	stage.Groups = groups
	stage.CursorLine = cursorLine
	stage.CursorCol = cursorCol

	b.finalizedStages = append(b.finalizedStages, stage)
	b.currentStage = nil

	return stage
}

// computeStageBufferRange computes the buffer range for a stage
func (b *IncrementalStageBuilder) computeStageBufferRange(stage *Stage) (int, int) {
	// Create a temporary DiffResult for compatibility with getStageBufferRange
	diffResult := &DiffResult{
		Changes:      b.diffBuilder.Changes,
		LineMapping:  b.diffBuilder.LineMapping,
		OldLineCount: len(b.OldLines),
		NewLineCount: len(b.diffBuilder.NewLines),
	}

	return getStageBufferRange(stage, b.BaseLineOffset, diffResult, nil)
}

// computeCurrentBufferLine computes the buffer line for the current position
// (used for gap detection on unchanged lines)
func (b *IncrementalStageBuilder) computeCurrentBufferLine(lineNum int) int {
	// Use the line mapping to find where this new line maps in the old file
	if b.diffBuilder.LineMapping != nil && lineNum > 0 && lineNum <= len(b.diffBuilder.LineMapping.NewToOld) {
		oldLine := b.diffBuilder.LineMapping.NewToOld[lineNum-1]
		if oldLine > 0 {
			return oldLine + b.BaseLineOffset - 1
		}
	}
	// Fallback: estimate based on position
	return lineNum + b.BaseLineOffset - 1
}

// Finalize completes the build and returns all stages sorted by cursor distance.
// Call this when the stream completes.
func (b *IncrementalStageBuilder) Finalize() *StagingResult {
	defer logger.Trace("IncrementalStageBuilder.Finalize")()

	// Finalize any remaining stage
	if b.currentStage != nil && len(b.currentStage.rawChanges) > 0 {
		b.finalizeCurrentStage()
	}

	if len(b.finalizedStages) == 0 {
		return nil
	}

	// Sort stages by cursor distance
	stages := b.finalizedStages
	for i := 0; i < len(stages)-1; i++ {
		for j := i + 1; j < len(stages); j++ {
			distI := stageDistanceFromCursor(stages[i], b.CursorRow)
			distJ := stageDistanceFromCursor(stages[j], b.CursorRow)
			if distJ < distI || (distJ == distI && stages[j].startLine < stages[i].startLine) {
				stages[i], stages[j] = stages[j], stages[i]
			}
		}
	}

	// Set cursor targets and IsLastStage
	for i, stage := range stages {
		isLastStage := i == len(stages)-1

		if isLastStage {
			stage.CursorTarget = &types.CursorPredictionTarget{
				RelativePath:    b.FilePath,
				LineNumber:      int32(stage.BufferEnd),
				ShouldRetrigger: true,
			}
		} else {
			nextStage := stages[i+1]
			stage.CursorTarget = &types.CursorPredictionTarget{
				RelativePath:    b.FilePath,
				LineNumber:      int32(nextStage.BufferStart),
				ShouldRetrigger: false,
			}
		}
		stage.IsLastStage = isLastStage

		// Clear rawChanges
		stage.rawChanges = nil
	}

	// Check if first stage needs navigation UI
	firstNeedsNav := StageNeedsNavigation(
		stages[0], b.CursorRow, b.ViewportTop, b.ViewportBottom, b.ProximityThreshold,
	)

	return &StagingResult{
		Stages:               stages,
		FirstNeedsNavigation: firstNeedsNav,
	}
}

