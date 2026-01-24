package text

import (
	"fmt"
	"sort"
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
	LineNumber int      // one-indexed
	Content    string   // new content
	OldContent string   // For modifications to compare changes
	ColStart   int      // Start column (0-based) for character-level changes
	ColEnd     int      // End column (0-based) for character-level changes
	GroupLines []string // For group types: array of content lines in the group
	StartLine  int      // For group types: starting line number of the group (1-indexed)
	EndLine    int      // For group types: ending line number of the group (1-indexed)
	MaxOffset  int      // For modification groups: maximum left offset for positioning
}

// DiffResult contains all categorized diff operations mapped by line number
type DiffResult struct {
	Changes              map[int]LineDiff // Map of line number (1-indexed) to diff operation
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
//   - Line number must be positive
//   - Collision handling: deletion + addition at same line = modification
//
// Returns true if the change was added, false if rejected.
func (r *DiffResult) addChange(lineNum int, oldContent, newContent string, changeType DiffType, colStart, colEnd int) bool {
	// INVARIANT 1: Identical content is not a change
	if oldContent == newContent && changeType != LineDeletion {
		return false
	}

	// INVARIANT 2: Line number must be positive
	if lineNum <= 0 {
		return false
	}

	// INVARIANT 3: Handle collisions
	if existing, exists := r.Changes[lineNum]; exists {
		// Deletion + Addition at same line = Modification
		// Note: For deletions, the deleted content is stored in Content field
		if existing.Type == LineDeletion && (changeType == LineAddition || changeType == LineAdditionGroup) {
			deletedContent := existing.Content // Deletion stores content in Content field
			changeType, colStart, colEnd = categorizeLineChangeWithColumns(deletedContent, newContent)
			oldContent = deletedContent
		} else {
			// Other collisions: keep the existing change
			return false
		}
	}

	r.Changes[lineNum] = LineDiff{
		Type:       changeType,
		LineNumber: lineNum,
		Content:    newContent,
		OldContent: oldContent,
		ColStart:   colStart,
		ColEnd:     colEnd,
	}
	return true
}

// addDeletion adds a deletion change for the given line
// Note: For deletions, the deleted content is stored in the Content field (not OldContent)
func (r *DiffResult) addDeletion(lineNum int, content string) bool {
	if lineNum <= 0 {
		return false
	}
	// For deletions, we bypass addChange to store content correctly
	// Deletions don't need identical-content check (deleting empty line is valid)
	if _, exists := r.Changes[lineNum]; exists {
		return false // Don't overwrite existing change
	}
	r.Changes[lineNum] = LineDiff{
		Type:       LineDeletion,
		LineNumber: lineNum,
		Content:    content, // Deleted content goes in Content field
	}
	return true
}

// addAddition adds an addition change for the given line
func (r *DiffResult) addAddition(lineNum int, content string) bool {
	return r.addChange(lineNum, "", content, LineAddition, 0, 0)
}

// addModification adds a modification change, auto-categorizing the change type
// based on the difference between oldContent and newContent
func (r *DiffResult) addModification(lineNum int, oldContent, newContent string) bool {
	changeType, colStart, colEnd := categorizeLineChangeWithColumns(oldContent, newContent)
	return r.addChange(lineNum, oldContent, newContent, changeType, colStart, colEnd)
}

// analyzeDiff computes and categorizes line-level diffs between two texts
func analyzeDiff(text1, text2 string) *DiffResult {
	result := &DiffResult{
		Changes: make(map[int]LineDiff),
	}

	// Use line-level diff to get the basic diff operations
	dmp := diffmatchpatch.New()
	chars1, chars2, lineArray := dmp.DiffLinesToChars(text1, text2)
	diffs := dmp.DiffMain(chars1, chars2, false)
	lineDiffs := dmp.DiffCharsToLines(diffs, lineArray)

	// Process the line diffs and intelligently merge delete+insert into modifications
	processLineDiffs(lineDiffs, result)

	// Apply grouping logic to consecutive modifications and additions
	applyGrouping(result, text1)

	// Process changes and calculate summary properties (after grouping)
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

// applyGrouping identifies consecutive modifications and additions and groups them
func applyGrouping(result *DiffResult, oldText string) {
	oldLines := strings.Split(oldText, "\n")

	// Get sorted line numbers for processing
	var lineNumbers []int
	for lineNum := range result.Changes {
		lineNumbers = append(lineNumbers, lineNum)
	}
	sort.Ints(lineNumbers)

	if len(lineNumbers) == 0 {
		return
	}

	// Find consecutive groups
	groupedChanges := make(map[int]LineDiff)
	i := 0

	for i < len(lineNumbers) {
		lineNum := lineNumbers[i]
		change := result.Changes[lineNum]

		// Check if this change should be grouped
		if change.Type == LineModification || change.Type == LineAddition {
			// Look for consecutive changes of the same type
			groupStart := i
			groupEnd := i
			groupType := change.Type

			// Find end of consecutive group
			for j := i + 1; j < len(lineNumbers); j++ {
				nextLineNum := lineNumbers[j]
				nextChange := result.Changes[nextLineNum]

				// Check if consecutive and same type
				if nextLineNum == lineNumbers[j-1]+1 && nextChange.Type == groupType {
					groupEnd = j
				} else {
					break
				}
			}

			// If we have multiple consecutive changes, create a group
			if groupEnd > groupStart {
				createGroup(result, lineNumbers[groupStart:groupEnd+1], groupType, oldLines, groupedChanges)
				i = groupEnd + 1
			} else {
				// Single change, keep as is
				groupedChanges[lineNum] = change
				i++
			}
		} else {
			// Not groupable, keep as is
			groupedChanges[lineNum] = change
			i++
		}
	}

	// Replace the changes with grouped changes
	result.Changes = groupedChanges
}

// createGroup creates a group change from consecutive individual changes
func createGroup(result *DiffResult, lineNumbers []int, groupType DiffType, oldLines []string, groupedChanges map[int]LineDiff) {
	if len(lineNumbers) == 0 {
		return
	}

	startLine := lineNumbers[0]
	endLine := lineNumbers[len(lineNumbers)-1]

	// Collect group content
	var groupLines []string
	var maxOffset int

	for _, lineNum := range lineNumbers {
		change := result.Changes[lineNum]
		groupLines = append(groupLines, change.Content)

		// For modification groups, calculate max offset based on old content
		if groupType == LineModification {
			if lineNum-1 < len(oldLines) {
				lineWidth := len(oldLines[lineNum-1])
				if lineWidth > maxOffset {
					maxOffset = lineWidth
				}
			}
		}
	}

	// Determine group type
	var finalGroupType DiffType
	if groupType == LineModification {
		finalGroupType = LineModificationGroup
	} else {
		finalGroupType = LineAdditionGroup
	}

	// Create the group change
	groupChange := LineDiff{
		Type:       finalGroupType,
		LineNumber: startLine,                      // Use first line as primary line number
		Content:    strings.Join(groupLines, "\n"), // Join all content
		GroupLines: groupLines,
		StartLine:  startLine,
		EndLine:    endLine,
		MaxOffset:  maxOffset,
	}

	// Add the group change (using the first line number as key)
	groupedChanges[startLine] = groupChange
}

// processLineDiffs processes line-level diffs and intelligently categorizes them
func processLineDiffs(lineDiffs []diffmatchpatch.Diff, result *DiffResult) {
	oldLineNum := 0
	newLineNum := 0
	i := 0

	for i < len(lineDiffs) {
		diff := lineDiffs[i]
		lines := splitLines(diff.Text)

		switch diff.Type {
		case diffmatchpatch.DiffEqual:
			// Equal lines, just advance counters
			oldLineNum += len(lines)
			newLineNum += len(lines)
			i++

		case diffmatchpatch.DiffDelete:
			// Check if this is followed by an insert - potential modification
			if i+1 < len(lineDiffs) && lineDiffs[i+1].Type == diffmatchpatch.DiffInsert {
				// This is a delete followed by insert - treat as modification(s)
				insertLines := splitLines(lineDiffs[i+1].Text)

				// Handle the modification(s)
				handleModifications(lines, insertLines, oldLineNum, newLineNum, result)

				oldLineNum += len(lines)
				newLineNum += len(insertLines)
				i += 2 // Skip both delete and insert
			} else {
				// Pure deletion
				for j, line := range lines {
					lineNum := oldLineNum + j + 1
					result.addDeletion(lineNum, line)
				}
				oldLineNum += len(lines)
				i++
			}

		case diffmatchpatch.DiffInsert:
			// Pure addition (not preceded by delete)
			for j, line := range lines {
				lineNum := newLineNum + j + 1
				result.addAddition(lineNum, line)
			}
			newLineNum += len(lines)
			i++
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

// handleModifications processes delete+insert pairs as modifications
// Uses DiffResult helper methods which enforce all invariants automatically:
//   - Identical lines are silently skipped
//   - Collision handling (deletion + addition = modification) is automatic
func handleModifications(deletedLines, insertedLines []string, oldLineStart, newLineStart int, result *DiffResult) {
	// If we have equal number of lines, treat each pair as a modification
	if len(deletedLines) == len(insertedLines) {
		for j := range len(deletedLines) {
			lineNum := oldLineStart + j + 1

			if deletedLines[j] != "" && insertedLines[j] != "" {
				// Both non-empty: modification (addModification handles identical check)
				result.addModification(lineNum, deletedLines[j], insertedLines[j])
			} else if deletedLines[j] != "" {
				// Only old has content: deletion
				result.addDeletion(lineNum, deletedLines[j])
			} else if insertedLines[j] != "" {
				// Only new has content: addition (use new line number)
				result.addAddition(newLineStart+j+1, insertedLines[j])
			}
			// Both empty and identical: addModification would reject anyway
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

	// Second pass: Process matched pairs as modifications
	for delIdx, insIdx := range matches {
		lineNum := oldLineStart + delIdx + 1
		// addModification handles identical line check automatically
		result.addModification(lineNum, deletedLines[delIdx], insertedLines[insIdx])
	}

	// Third pass: Handle unmatched deletions
	for i, deletedLine := range deletedLines {
		if usedDeletes[i] {
			continue
		}
		lineNum := oldLineStart + i + 1
		// addDeletion handles collision check automatically
		result.addDeletion(lineNum, deletedLine)
	}

	// Fourth pass: Handle unmatched additions
	for i, insertedLine := range insertedLines {
		if usedInserts[i] {
			continue
		}
		lineNum := newLineStart + i + 1
		// addAddition handles collision check automatically (deletion + addition = modification)
		result.addAddition(lineNum, insertedLine)
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

	for i := 0; i < minLen; i++ {
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
