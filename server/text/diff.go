package text

import (
	"fmt"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// splitLines splits text by newline and removes trailing empty element if present
func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// DiffType represents the type of diff operation
type DiffType int

const (
	LineDeletion DiffType = iota
	LineAddition
	LineModification
	LineAppendChars
	LineDeleteChars
	LineReplaceChars
	LineModificationGroup
	LineAdditionGroup
)

// String returns the string representation of DiffType for Lua integration
func (dt DiffType) String() string {
	switch dt {
	case LineDeletion:
		return "deletion"
	case LineAddition:
		return "addition"
	case LineModification:
		return "modification"
	case LineAppendChars:
		return "append_chars"
	case LineDeleteChars:
		return "delete_chars"
	case LineReplaceChars:
		return "replace_chars"
	case LineModificationGroup:
		return "modification_group"
	case LineAdditionGroup:
		return "addition_group"
	default:
		return "unknown"
	}
}

// LineDiff represents a line-level diff operation
type LineDiff struct {
	Type       DiffType
	LineNumber int    // one-indexed (kept for backward compatibility, equals NewLineNum when set)
	OldLineNum int    // Position in old text (1-indexed), -1 if pure insertion
	NewLineNum int    // Position in new text (1-indexed), -1 if pure deletion
	Content    string // new content
	OldContent string // For modifications to compare changes
	ColStart   int    // Start column (0-based) for character-level changes
	ColEnd     int    // End column (0-based) for character-level changes
	GroupLines []string // For group types: array of content lines in the group
	StartLine  int      // For group types: starting line number of the group (1-indexed)
	EndLine    int      // For group types: ending line number of the group (1-indexed)
	MaxOffset  int      // For modification groups: maximum left offset for positioning
}

// LineMapping tracks correspondence between new and old line coordinates.
// This enables staging to work correctly when line counts differ (insertions/deletions).
type LineMapping struct {
	NewToOld []int // NewToOld[i] = old line num for new line i+1, or -1 if pure insertion
	OldToNew []int // OldToNew[i] = new line num for old line i+1, or -1 if deleted
}

// DiffResult contains all categorized diff operations mapped by line number
type DiffResult struct {
	Changes              map[int]LineDiff // Map of line number (1-indexed) to diff operation
	LineMapping          *LineMapping     // Coordinate mapping between old and new line numbers
	OldLineCount         int              // Number of lines in original text
	NewLineCount         int              // Number of lines in new text
	IsOnlyLineDeletion   bool             // True if the diff contains only deletions
	LastDeletion         int              // The line number (1-indexed) of the last deletion, -1 if no deletion
	LastAddition         int              // The line number (1-indexed) of the last addition, -1 if no addition
	LastLineModification int              // The line number (1-indexed) of the last line modification, -1 if no line modification
	LastAppendChars      int              // The line number (1-indexed) of the last append chars, -1 if no append chars
	LastDeleteChars      int              // The line number (1-indexed) of the last delete chars, -1 if no delete chars
	LastReplaceChars     int              // The line number (1-indexed) of the last replace chars, -1 if no replace chars
	CursorLine           int              // The optimal line (1-indexed) to position cursor, -1 if no positioning needed
	CursorCol            int              // The optimal column (0-indexed) to position cursor, -1 if no positioning needed
}

// =============================================================================
// DiffResult helper methods - SINGLE POINT for adding changes
// All invariants are enforced here, making it impossible to add invalid changes.
// =============================================================================

// addChange is the SINGLE ENTRY POINT for adding changes to the result.
// It enforces all invariants:
//   - Identical content is not a change (silently rejected)
//   - At least one line number must be valid
//   - Collision handling: deletion + addition at same line = modification
//
// oldLineNum: position in old text (1-indexed), -1 if pure insertion
// newLineNum: position in new text (1-indexed), -1 if pure deletion
// Returns true if the change was added, false if rejected.
func (r *DiffResult) addChange(oldLineNum, newLineNum int, oldContent, newContent string, changeType DiffType, colStart, colEnd int) bool {
	// INVARIANT 1: Identical content is not a change
	if oldContent == newContent && changeType != LineDeletion {
		return false
	}

	// INVARIANT 2: At least one line number must be valid
	if oldLineNum <= 0 && newLineNum <= 0 {
		return false
	}

	// Use newLineNum as the map key (primary coordinate for content extraction)
	// For deletions, use oldLineNum as the key since there's no new line
	mapKey := newLineNum
	if newLineNum <= 0 {
		mapKey = oldLineNum
	}

	// INVARIANT 3: Handle collisions
	if existing, exists := r.Changes[mapKey]; exists {
		// Deletion + Addition at same line = Modification
		// Note: For deletions, the deleted content is stored in Content field
		if existing.Type == LineDeletion && (changeType == LineAddition || changeType == LineAdditionGroup) {
			deletedContent := existing.Content // Deletion stores content in Content field
			changeType, colStart, colEnd = categorizeLineChangeWithColumns(deletedContent, newContent)
			oldContent = deletedContent
			// Merge coordinates: take oldLineNum from existing deletion
			oldLineNum = existing.OldLineNum
		} else {
			// Other collisions: keep the existing change
			return false
		}
	}

	r.Changes[mapKey] = LineDiff{
		Type:       changeType,
		LineNumber: mapKey, // Backward compatibility
		OldLineNum: oldLineNum,
		NewLineNum: newLineNum,
		Content:    newContent,
		OldContent: oldContent,
		ColStart:   colStart,
		ColEnd:     colEnd,
	}
	return true
}

// addDeletion adds a deletion change with explicit coordinates.
// Note: For deletions, the deleted content is stored in the Content field (not OldContent).
// oldLineNum: position in old text (1-indexed, required)
// newLineNum: anchor point in new text (1-indexed), or -1 if no anchor
func (r *DiffResult) addDeletion(oldLineNum, newLineNum int, content string) bool {
	if oldLineNum <= 0 {
		return false
	}
	// Use oldLineNum as map key for deletions (no new line exists)
	mapKey := oldLineNum
	// For deletions, we bypass addChange to store content correctly
	// Deletions don't need identical-content check (deleting empty line is valid)
	if _, exists := r.Changes[mapKey]; exists {
		return false // Don't overwrite existing change
	}
	r.Changes[mapKey] = LineDiff{
		Type:       LineDeletion,
		LineNumber: mapKey,
		OldLineNum: oldLineNum,
		NewLineNum: newLineNum, // -1 or anchor point
		Content:    content,    // Deleted content goes in Content field
	}
	return true
}

// addAddition adds an addition change with explicit coordinates.
// oldLineNum: anchor point in old text (1-indexed), or -1 if no anchor
// newLineNum: position in new text (1-indexed, required)
func (r *DiffResult) addAddition(oldLineNum, newLineNum int, content string) bool {
	return r.addChange(oldLineNum, newLineNum, "", content, LineAddition, 0, 0)
}

// addModification adds a modification change with explicit coordinates,
// auto-categorizing the change type based on content differences.
// oldLineNum: position in old text (1-indexed)
// newLineNum: position in new text (1-indexed)
func (r *DiffResult) addModification(oldLineNum, newLineNum int, oldContent, newContent string) bool {
	changeType, colStart, colEnd := categorizeLineChangeWithColumns(oldContent, newContent)
	return r.addChange(oldLineNum, newLineNum, oldContent, newContent, changeType, colStart, colEnd)
}

// analyzeDiff computes and categorizes line-level diffs between two texts
func analyzeDiff(text1, text2 string) *DiffResult {
	return analyzeDiffWithViewport(text1, text2, 0, 0, 0)
}

// analyzeDiffWithViewport computes line-level diffs
func analyzeDiffWithViewport(text1, text2 string, _, _, _ int) *DiffResult {
	// Count lines in both texts
	oldLines := splitLines(text1)
	newLines := splitLines(text2)
	oldLineCount := len(oldLines)
	newLineCount := len(newLines)

	result := &DiffResult{
		Changes:      make(map[int]LineDiff),
		OldLineCount: oldLineCount,
		NewLineCount: newLineCount,
	}

	dmp := diffmatchpatch.New()
	chars1, chars2, lineArray := dmp.DiffLinesToChars(text1, text2)
	diffs := dmp.DiffMain(chars1, chars2, false)
	lineDiffs := dmp.DiffCharsToLines(diffs, lineArray)

	// Build line mapping and process diffs
	result.LineMapping = processLineDiffsWithMapping(lineDiffs, result, oldLineCount, newLineCount)
	processChangesSummary(result)

	// Calculate optimal cursor position based on diff results
	calculateCursorPosition(result, text2)

	return result
}

// processChangesSummary processes all changes and calculates summary properties
func processChangesSummary(result *DiffResult) {
	// Initialize all properties to -1
	result.LastDeletion = -1
	result.LastAddition = -1
	result.LastLineModification = -1
	result.LastDeleteChars = -1
	result.LastReplaceChars = -1
	result.LastAppendChars = -1
	result.CursorLine = -1
	result.CursorCol = -1

	onlyDeletions := len(result.Changes) > 0
	for lineNum, change := range result.Changes {
		// IsOnlyDeletion
		if change.Type != LineDeletion {
			onlyDeletions = false
		}
		// LastDeletion - find the maximum line number
		if change.Type == LineDeletion && lineNum > result.LastDeletion {
			result.LastDeletion = lineNum
		}
		// LastAddition - find the maximum line number (for groups, use the first line)
		if (change.Type == LineAddition || change.Type == LineAdditionGroup) && lineNum > result.LastAddition {
			result.LastAddition = lineNum
		}
		// LastLineModification - find the maximum line number (for groups, use the first line)
		if (change.Type == LineModification || change.Type == LineModificationGroup) && lineNum > result.LastLineModification {
			result.LastLineModification = lineNum
		}
		// LastDeleteChars - find the maximum line number
		if change.Type == LineDeleteChars && lineNum > result.LastDeleteChars {
			result.LastDeleteChars = lineNum
		}
		// LastReplaceChars - find the maximum line number
		if change.Type == LineReplaceChars && lineNum > result.LastReplaceChars {
			result.LastReplaceChars = lineNum
		}
		// LastAppendChars - find the maximum line number
		if change.Type == LineAppendChars && lineNum > result.LastAppendChars {
			result.LastAppendChars = lineNum
		}
	}
	result.IsOnlyLineDeletion = onlyDeletions
}

// calculateCursorPosition determines optimal cursor positioning based on diff results
func calculateCursorPosition(result *DiffResult, newText string) {
	// Don't position cursor for pure deletions
	if result.IsOnlyLineDeletion {
		return
	}

	// Don't position cursor when there are no changes at all
	if len(result.Changes) == 0 {
		return
	}

	newLines := strings.Split(newText, "\n")

	// Priority order: modifications > additions > other changes
	var targetLine int = -1

	if result.LastLineModification != -1 {
		targetLine = result.LastLineModification
	} else if result.LastAddition != -1 {
		targetLine = result.LastAddition
	} else if result.LastAppendChars != -1 {
		targetLine = result.LastAppendChars
	} else if result.LastReplaceChars != -1 {
		targetLine = result.LastReplaceChars
	} else if result.LastDeleteChars != -1 {
		targetLine = result.LastDeleteChars
	} else if result.LastDeletion != -1 {
		targetLine = result.LastDeletion
	} else if len(newLines) > 0 {
		// Default to end of completion when there are changes
		targetLine = len(newLines)
	}

	// For group types, adjust target line to be the end of the group
	if targetLine > 0 {
		if change, exists := result.Changes[targetLine]; exists {
			if change.Type == LineModificationGroup || change.Type == LineAdditionGroup {
				targetLine = change.EndLine
			}
		}
	}

	// Set cursor position (clamp to valid range if targetLine exceeds newLines length)
	if targetLine > 0 {
		if targetLine > len(newLines) {
			targetLine = len(newLines)
		}
		if targetLine > 0 {
			result.CursorLine = targetLine
			// Position at end of the target line
			result.CursorCol = len(newLines[targetLine-1])
		}
	}
}

// LineSimilarity computes a similarity score between two lines (0.0 to 1.0)
// using Levenshtein ratio: 1 - (levenshtein_distance / max_length)
// Higher score means more similar. Empty lines have 0 similarity with non-empty lines.
func LineSimilarity(line1, line2 string) float64 {
	// Empty lines
	if line1 == "" && line2 == "" {
		return 1.0
	}
	if line1 == "" || line2 == "" {
		return 0.0
	}

	// Use Levenshtein ratio for intuitive similarity scoring
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(line1, line2, false)
	levenshteinDist := dmp.DiffLevenshtein(diffs)

	maxLen := max(len(line1), len(line2))
	if maxLen == 0 {
		return 0.0
	}

	return 1.0 - float64(levenshteinDist)/float64(maxLen)
}

// findBestMatch finds the best matching line in insertedLines for the given deletedLine
// Returns the index of the best match and its similarity score
func findBestMatch(deletedLine string, insertedLines []string, usedInserts map[int]bool) (int, float64) {
	bestIdx := -1
	bestSimilarity := 0.0

	for i, insertedLine := range insertedLines {
		if usedInserts[i] {
			continue
		}

		similarity := LineSimilarity(deletedLine, insertedLine)
		if similarity > bestSimilarity {
			bestSimilarity = similarity
			bestIdx = i
		}
	}

	return bestIdx, bestSimilarity
}

// processLineDiffsWithMapping processes line-level diffs and builds the coordinate mapping.
// Returns the LineMapping that tracks correspondence between old and new line numbers.
func processLineDiffsWithMapping(lineDiffs []diffmatchpatch.Diff, result *DiffResult, oldLineCount, newLineCount int) *LineMapping {
	// Initialize mapping arrays with -1 (unmapped)
	newToOld := make([]int, newLineCount)
	oldToNew := make([]int, oldLineCount)
	for i := range newToOld {
		newToOld[i] = -1
	}
	for i := range oldToNew {
		oldToNew[i] = -1
	}

	oldLineNum := 0 // 0-indexed counter
	newLineNum := 0 // 0-indexed counter
	i := 0

	for i < len(lineDiffs) {
		diff := lineDiffs[i]
		lines := splitLines(diff.Text)

		switch diff.Type {
		case diffmatchpatch.DiffEqual:
			// Equal lines map 1:1
			for j := range len(lines) {
				if newLineNum+j < newLineCount {
					newToOld[newLineNum+j] = oldLineNum + j + 1 // 1-indexed
				}
				if oldLineNum+j < oldLineCount {
					oldToNew[oldLineNum+j] = newLineNum + j + 1 // 1-indexed
				}
			}
			oldLineNum += len(lines)
			newLineNum += len(lines)
			i++

		case diffmatchpatch.DiffDelete:
			// Check if this is followed by an insert - potential modification
			if i+1 < len(lineDiffs) && lineDiffs[i+1].Type == diffmatchpatch.DiffInsert {
				// This is a delete followed by insert - treat as modification(s)
				insertLines := splitLines(lineDiffs[i+1].Text)

				// Build mapping for the modification region
				handleModificationsWithMapping(lines, insertLines, oldLineNum, newLineNum,
					oldLineCount, newLineCount, newToOld, oldToNew, result)

				oldLineNum += len(lines)
				newLineNum += len(insertLines)
				i += 2 // Skip both delete and insert
			} else {
				// Pure deletion - deleted lines have no new correspondence
				// oldToNew already -1, add deletion changes
				for j, line := range lines {
					oldIdx := oldLineNum + j
					// Find the anchor point in new text (the line before deletion, or -1)
					anchorNew := -1
					if newLineNum > 0 {
						anchorNew = newLineNum // Point to line before (will be used as insertion anchor)
					}
					result.addDeletion(oldIdx+1, anchorNew, line)
				}
				oldLineNum += len(lines)
				i++
			}

		case diffmatchpatch.DiffInsert:
			// Pure addition (not preceded by delete)
			// newToOld already -1, add addition changes
			for j, line := range lines {
				newIdx := newLineNum + j
				// Find the anchor point in old text (the line before insertion)
				anchorOld := -1
				if oldLineNum > 0 {
					anchorOld = oldLineNum // Point to line before (for buffer coordinate calculation)
				}
				result.addAddition(anchorOld, newIdx+1, line)
			}
			newLineNum += len(lines)
			i++
		}
	}

	return &LineMapping{
		NewToOld: newToOld,
		OldToNew: oldToNew,
	}
}

// handleModificationsWithMapping processes delete+insert pairs as modifications
// and updates the coordinate mapping accordingly.
func handleModificationsWithMapping(deletedLines, insertedLines []string,
	oldLineStart, newLineStart int,
	oldLineCount, newLineCount int,
	newToOld, oldToNew []int,
	result *DiffResult) {

	// If we have equal number of lines, treat each pair as a modification with 1:1 mapping
	if len(deletedLines) == len(insertedLines) {
		for j := range len(deletedLines) {
			oldIdx := oldLineStart + j
			newIdx := newLineStart + j

			// Update mapping - these lines correspond
			if newIdx < newLineCount {
				newToOld[newIdx] = oldIdx + 1 // 1-indexed
			}
			if oldIdx < oldLineCount {
				oldToNew[oldIdx] = newIdx + 1 // 1-indexed
			}

			if deletedLines[j] != "" && insertedLines[j] != "" {
				// Both non-empty: modification
				result.addModification(oldIdx+1, newIdx+1, deletedLines[j], insertedLines[j])
			} else if deletedLines[j] != "" {
				// Only old has content: deletion
				result.addDeletion(oldIdx+1, newIdx+1, deletedLines[j])
			} else if insertedLines[j] != "" {
				// Only new has content: addition
				result.addAddition(oldIdx+1, newIdx+1, insertedLines[j])
			}
		}
		return
	}

	// Unequal number of lines - use similarity-based matching
	usedInserts := make(map[int]bool)
	usedDeletes := make(map[int]bool)

	// First pass: Match similar non-empty lines with similarity threshold
	const similarityThreshold = 0.3
	matches := make(map[int]int) // maps deleted index to inserted index

	for i, deletedLine := range deletedLines {
		if deletedLine == "" {
			continue
		}
		bestIdx, bestSimilarity := findBestMatch(deletedLine, insertedLines, usedInserts)
		if bestIdx != -1 && bestSimilarity >= similarityThreshold {
			matches[i] = bestIdx
			usedInserts[bestIdx] = true
			usedDeletes[i] = true
		}
	}

	// Second pass: Process matched pairs as modifications and update mapping
	for delIdx, insIdx := range matches {
		oldIdx := oldLineStart + delIdx
		newIdx := newLineStart + insIdx

		// Update mapping for matched pairs
		if newIdx < newLineCount {
			newToOld[newIdx] = oldIdx + 1
		}
		if oldIdx < oldLineCount {
			oldToNew[oldIdx] = newIdx + 1
		}

		result.addModification(oldIdx+1, newIdx+1, deletedLines[delIdx], insertedLines[insIdx])
	}

	// Third pass: Handle unmatched deletions (oldToNew stays -1)
	for i, deletedLine := range deletedLines {
		if usedDeletes[i] {
			continue
		}
		oldIdx := oldLineStart + i
		// Anchor to nearest mapped new line
		anchorNew := -1
		if newLineStart > 0 {
			anchorNew = newLineStart
		}
		result.addDeletion(oldIdx+1, anchorNew, deletedLine)
	}

	// Fourth pass: Handle unmatched additions (newToOld stays -1)
	for i, insertedLine := range insertedLines {
		if usedInserts[i] {
			continue
		}
		newIdx := newLineStart + i
		// Anchor to the first deleted line position (1-indexed) so that additions
		// have the same buffer line as their related modifications during viewport
		// partitioning. This ensures delete+insert blocks stay grouped together.
		anchorOld := -1
		if oldLineStart >= 0 {
			anchorOld = oldLineStart + 1 // Use 1-indexed position of first deleted line
		}
		result.addAddition(anchorOld, newIdx+1, insertedLine)
	}
}

// categorizeLineChangeWithColumns determines the type of change between two lines and returns column range
func categorizeLineChangeWithColumns(oldLine, newLine string) (DiffType, int, int) {
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(oldLine, newLine, false)
	diffs = dmp.DiffCleanupSemantic(diffs)

	// Count diff operations and extract texts
	var insertions, deletions int
	var hasEqual bool
	var deletedText, insertedText string

	for _, diff := range diffs {
		switch diff.Type {
		case diffmatchpatch.DiffInsert:
			insertions++
			insertedText = diff.Text
		case diffmatchpatch.DiffDelete:
			deletions++
			deletedText = diff.Text
		case diffmatchpatch.DiffEqual:
			hasEqual = true
		}
	}

	// Handle pure insertions (no deletions)
	if deletions == 0 && insertions > 0 && hasEqual {
		return categorizePureInsertion(oldLine, newLine, diffs, insertions)
	}

	// Handle pure deletions (no insertions)
	if insertions == 0 && deletions > 0 && hasEqual {
		return categorizePureDeletion(diffs)
	}

	// Handle single insertion + single deletion (potential simple replacement)
	if insertions == 1 && deletions == 1 && hasEqual {
		return categorizeSingleReplacement(diffs, deletedText, insertedText)
	}

	// Default to general modification
	return LineModification, 0, 0
}

// categorizePureInsertion handles cases with only insertions (no deletions)
func categorizePureInsertion(oldLine, newLine string, diffs []diffmatchpatch.Diff, insertions int) (DiffType, int, int) {
	// Check if it's an append at the end
	if strings.HasPrefix(newLine, oldLine) {
		return LineAppendChars, len(oldLine), len(newLine)
	}

	// Single insertion - find position and treat as replacement
	if insertions == 1 {
		pos := 0
		for _, diff := range diffs {
			if diff.Type == diffmatchpatch.DiffInsert {
				return LineReplaceChars, pos, pos + len(diff.Text)
			}
			if diff.Type == diffmatchpatch.DiffEqual {
				pos += len(diff.Text)
			}
		}
	}

	// Multiple insertions - general modification
	return LineModification, 0, 0
}

// categorizePureDeletion handles cases with only deletions (no insertions)
func categorizePureDeletion(diffs []diffmatchpatch.Diff) (DiffType, int, int) {
	pos := 0
	for _, diff := range diffs {
		if diff.Type == diffmatchpatch.DiffDelete {
			return LineDeleteChars, pos, pos + len(diff.Text)
		}
		if diff.Type == diffmatchpatch.DiffEqual {
			pos += len(diff.Text)
		}
	}
	return LineModification, 0, 0
}

// categorizeSingleReplacement handles cases with exactly one deletion and one insertion
func categorizeSingleReplacement(diffs []diffmatchpatch.Diff, deletedText, insertedText string) (DiffType, int, int) {
	// Check if this is a complex modification based on word count heuristics
	if isComplexModification(deletedText, insertedText) {
		return LineModification, 0, 0
	}

	// Simple replacement - find the insertion position
	pos := 0
	for _, diff := range diffs {
		if diff.Type == diffmatchpatch.DiffInsert {
			return LineReplaceChars, pos, pos + len(diff.Text)
		}
		if diff.Type == diffmatchpatch.DiffEqual {
			pos += len(diff.Text)
		}
	}

	return LineModification, 0, 0
}

// isComplexModification determines if a deletion+insertion pair is too complex for simple replacement
func isComplexModification(deletedText, insertedText string) bool {
	deletedWords := len(strings.Fields(deletedText))
	insertedWords := len(strings.Fields(insertedText))

	// Too many words = complex modification
	if deletedWords > 2 || insertedWords > 2 {
		return true
	}

	// Large word count difference = complex modification
	if abs(deletedWords-insertedWords) > 1 {
		return true
	}

	// Check length ratio (avoid division by zero)
	deletedLen := len(deletedText)
	insertedLen := len(insertedText)

	if deletedLen == 0 {
		// Deletion is empty but insertion exists - treat as modification if insertion is substantial
		return insertedLen > 10
	}

	lengthRatio := float64(insertedLen) / float64(deletedLen)

	// For single-word changes, be lenient (allow up to 3x difference)
	if deletedWords == 1 && insertedWords == 1 {
		return lengthRatio > 3.0 || lengthRatio < 0.33
	}

	// For other cases, be stricter (allow up to 2x difference)
	return lengthRatio > 2.0 || lengthRatio < 0.5
}

// abs returns the absolute value of an integer
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ToLuaFormat converts a LineDiff to a Lua-friendly map format
func (ld LineDiff) ToLuaFormat() map[string]any {
	luaFormat := map[string]any{
		"type":       ld.Type.String(),
		"lineNumber": ld.LineNumber,
		"oldLineNum": ld.OldLineNum, // Original buffer position (for modifications)
		"newLineNum": ld.NewLineNum, // New content position (for additions)
		"content":    ld.Content,
		"oldContent": ld.OldContent,
		"colStart":   ld.ColStart,
		"colEnd":     ld.ColEnd,
	}

	// Add group-specific fields if they exist
	if ld.Type == LineModificationGroup || ld.Type == LineAdditionGroup {
		luaFormat["startLine"] = ld.StartLine
		luaFormat["endLine"] = ld.EndLine
		luaFormat["maxOffset"] = ld.MaxOffset
		luaFormat["groupLines"] = ld.GroupLines
	}

	return luaFormat
}

// FindFirstChangedLine compares old lines with new lines and returns the first line number (1-indexed)
// where they differ. Returns 0 if no differences found.
// The baseLineOffset is added to the result to convert from relative to absolute line numbers.
func FindFirstChangedLine(oldLines, newLines []string, baseLineOffset int) int {
	// Quick path: find first differing line by direct comparison
	minLen := min(len(oldLines), len(newLines))

	for i := range minLen {
		if oldLines[i] != newLines[i] {
			return i + 1 + baseLineOffset // 1-indexed + offset
		}
	}

	// If lengths differ, the first "extra" line is a change
	if len(oldLines) != len(newLines) {
		return minLen + 1 + baseLineOffset
	}

	// No differences found
	return 0
}

// ToLuaFormat converts a DiffResult to a Lua-friendly map format
// Additional fields can be passed as key-value pairs: ToLuaFormat("startLine", 10, "endLineInclusive", 15)
func (dr *DiffResult) ToLuaFormat(additionalFields ...any) map[string]any {
	// Convert LineDiff changes to Lua-friendly format with string keys
	luaChanges := make(map[string]map[string]any)
	for lineNum, lineDiff := range dr.Changes {
		lineKey := fmt.Sprintf("%d", lineNum)
		luaChanges[lineKey] = lineDiff.ToLuaFormat()
	}

	luaFormat := map[string]any{
		"changes":              luaChanges,
		"isOnlyLineDeletion":   dr.IsOnlyLineDeletion,
		"lastDeletion":         dr.LastDeletion,
		"lastAddition":         dr.LastAddition,
		"lastLineModification": dr.LastLineModification,
		"lastAppendChars":      dr.LastAppendChars,
		"lastDeleteChars":      dr.LastDeleteChars,
		"lastReplaceChars":     dr.LastReplaceChars,
		"cursorLine":           dr.CursorLine,
		"cursorCol":            dr.CursorCol,
	}

	// Add additional fields from variadic parameters (key, value pairs)
	for i := 0; i < len(additionalFields)-1; i += 2 {
		if key, ok := additionalFields[i].(string); ok {
			luaFormat[key] = additionalFields[i+1]
		}
	}

	return luaFormat
}
