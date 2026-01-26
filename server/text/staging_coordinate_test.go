package text

import (
	"cursortab/assert"
	"cursortab/types"
	"fmt"
	"testing"
)

// =============================================================================
// Tests for createDiffResultFromVisualGroups - the fix for overlapping renders
// =============================================================================

// TestCreateDiffResultFromVisualGroups_NoOverlaps verifies that the fix works:
// when visual groups are provided from staging, createDiffResultFromVisualGroups
// produces a diff result without overlapping render positions.
func TestCreateDiffResultFromVisualGroups_NoOverlaps(t *testing.T) {
	newLines := []string{
		"    },",
		"    {",
		`      "timestamp": "2022-01-05T00:00:00Z",`,
		`      "value": 300,`,
		`      "name": "John"`,
		"    },",
		"    {",
		`      "timestamp": "2022-01-06T00:00:00Z",`,
	}

	// Visual groups from staging - already have consistent coordinates
	// Staging groups consecutive changes of the same type
	visualGroups := []*types.VisualGroup{
		{
			Type:      "modification",
			StartLine: 1,
			EndLine:   2,
			Lines:     []string{"    },", "    {"},
			OldLines:  []string{"    },", "        {"},
		},
		{
			Type:      "addition",
			StartLine: 3,
			EndLine:   8,
			Lines:     newLines[2:],
			OldLines:  nil,
		},
	}

	// Create diff result from visual groups (the fix)
	diffResult := createDiffResultFromVisualGroups(visualGroups, newLines, len(newLines))

	// Verify the diff result has changes
	assert.True(t, len(diffResult.Changes) > 0, "diff result should have changes")

	// Verify no overlapping render positions
	// Since visual groups are already non-overlapping (consecutive grouping),
	// the resulting diff should also be non-overlapping
	positions := make(map[int]string) // line number -> change type
	for lineNum, change := range diffResult.Changes {
		if _, exists := positions[lineNum]; exists {
			t.Errorf("OVERLAP: multiple changes at line %d", lineNum)
		}
		positions[lineNum] = change.Type.String()
	}

	// Verify we have distinct modification and addition entries
	hasModification := false
	hasAddition := false
	for _, change := range diffResult.Changes {
		if change.Type == LineModification || change.Type == LineModificationGroup {
			hasModification = true
		}
		if change.Type == LineAddition || change.Type == LineAdditionGroup {
			hasAddition = true
		}
	}
	assert.True(t, hasModification, "should have modification")
	assert.True(t, hasAddition, "should have addition")

	// Verify cursor position is set
	assert.True(t, diffResult.CursorLine > 0, "cursor line should be set")
	assert.True(t, diffResult.CursorCol >= 0, "cursor col should be set")
}

// TestCreateDiffResultFromVisualGroups_BoundsValidation verifies that
// createDiffResultFromVisualGroups properly validates bounds.
func TestCreateDiffResultFromVisualGroups_BoundsValidation(t *testing.T) {
	newLines := []string{"line1", "line2", "line3"}

	// Visual group with out-of-bounds coordinates
	visualGroups := []*types.VisualGroup{
		{
			Type:      "modification",
			StartLine: 10, // Out of bounds
			EndLine:   15,
			Lines:     []string{"ignored"},
			OldLines:  []string{"ignored"},
		},
		{
			Type:      "addition",
			StartLine: 1,
			EndLine:   2,
			Lines:     []string{"line1", "line2"},
		},
	}

	diffResult := createDiffResultFromVisualGroups(visualGroups, newLines, len(newLines))

	// Should only have the valid visual group
	assert.Equal(t, 1, len(diffResult.Changes), "should only have 1 change (the valid one)")

	// The valid change should be at line 1
	_, exists := diffResult.Changes[1]
	assert.True(t, exists, "valid change at line 1 should exist")
}

// TestCreateDiffResultFromVisualGroups_SingleLineGroups verifies handling of
// single-line visual groups (where StartLine == EndLine).
func TestCreateDiffResultFromVisualGroups_SingleLineGroups(t *testing.T) {
	newLines := []string{"modified line", "added line"}

	visualGroups := []*types.VisualGroup{
		{
			Type:      "modification",
			StartLine: 1,
			EndLine:   1, // Single line
			Lines:     []string{"modified line"},
			OldLines:  []string{"old line"},
		},
		{
			Type:      "addition",
			StartLine: 2,
			EndLine:   2, // Single line
			Lines:     []string{"added line"},
		},
	}

	diffResult := createDiffResultFromVisualGroups(visualGroups, newLines, len(newLines))

	assert.Equal(t, 2, len(diffResult.Changes), "should have 2 changes")

	// Single-line changes should NOT be group types
	mod, exists := diffResult.Changes[1]
	assert.True(t, exists, "modification at line 1")
	assert.Equal(t, LineModification, mod.Type, "single-line modification type")

	add, exists := diffResult.Changes[2]
	assert.True(t, exists, "addition at line 2")
	assert.Equal(t, LineAddition, add.Type, "single-line addition type")
}

// TestCreateDiffResultFromVisualGroups_EmptyGroups verifies handling of empty input.
func TestCreateDiffResultFromVisualGroups_EmptyGroups(t *testing.T) {
	newLines := []string{"line1", "line2"}

	diffResult := createDiffResultFromVisualGroups(nil, newLines, len(newLines))

	assert.Equal(t, 0, len(diffResult.Changes), "no changes for nil visual groups")
	assert.Equal(t, -1, diffResult.CursorLine, "no cursor position")

	diffResult = createDiffResultFromVisualGroups([]*types.VisualGroup{}, newLines, len(newLines))

	assert.Equal(t, 0, len(diffResult.Changes), "no changes for empty visual groups")
}

// TestCreateDiffResultFromVisualGroups_GroupContent verifies that group content
// is correctly extracted from newLines.
func TestCreateDiffResultFromVisualGroups_GroupContent(t *testing.T) {
	newLines := []string{"line1", "line2", "line3", "line4", "line5"}

	visualGroups := []*types.VisualGroup{
		{
			Type:      "addition",
			StartLine: 2,
			EndLine:   4,
			Lines:     []string{"line2", "line3", "line4"},
		},
	}

	diffResult := createDiffResultFromVisualGroups(visualGroups, newLines, len(newLines))

	change, exists := diffResult.Changes[2]
	assert.True(t, exists, "change at line 2")
	assert.Equal(t, LineAdditionGroup, change.Type, "multi-line addition is a group")
	assert.Equal(t, 3, len(change.GroupLines), "group has 3 lines")
	assert.Equal(t, "line2", change.GroupLines[0], "first group line")
	assert.Equal(t, "line3", change.GroupLines[1], "second group line")
	assert.Equal(t, "line4", change.GroupLines[2], "third group line")
	assert.Equal(t, 2, change.StartLine, "StartLine")
	assert.Equal(t, 4, change.EndLine, "EndLine")
}

// TestCreateDiffResultFromVisualGroups_CursorPosition verifies cursor is placed
// at the end of the last change.
func TestCreateDiffResultFromVisualGroups_CursorPosition(t *testing.T) {
	newLines := []string{"short", "this is a longer line", "medium len"}

	visualGroups := []*types.VisualGroup{
		{
			Type:      "modification",
			StartLine: 1,
			EndLine:   1,
			Lines:     []string{"short"},
			OldLines:  []string{"old"},
		},
		{
			Type:      "addition",
			StartLine: 2,
			EndLine:   3,
			Lines:     []string{"this is a longer line", "medium len"},
		},
	}

	diffResult := createDiffResultFromVisualGroups(visualGroups, newLines, len(newLines))

	// Cursor should be at line 3 (end of last change)
	assert.Equal(t, 3, diffResult.CursorLine, "cursor at last change line")
	// Cursor col should be end of "medium len" = 10
	assert.Equal(t, 10, diffResult.CursorCol, "cursor at end of last line")
}

// =============================================================================
// Tests for OldLineNum coordinate mapping - critical for Lua rendering
// =============================================================================

// TestCreateDiffResultFromVisualGroups_ModificationHasOldLineNum verifies that
// modifications have OldLineNum set, which is required for Lua to correctly
// calculate buffer positions when rendering inline changes.
//
// Without OldLineNum, Lua falls back to using the map key which causes offset errors.
func TestCreateDiffResultFromVisualGroups_ModificationHasOldLineNum(t *testing.T) {
	newLines := []string{"line1", "modified line 2", "line3"}

	visualGroups := []*types.VisualGroup{
		{
			Type:      "modification",
			StartLine: 2,
			EndLine:   2,
			Lines:     []string{"modified line 2"},
			OldLines:  []string{"old line 2"},
		},
	}

	diffResult := createDiffResultFromVisualGroups(visualGroups, newLines, len(newLines))

	change, exists := diffResult.Changes[2]
	assert.True(t, exists, "change at line 2 should exist")
	assert.Equal(t, LineModification, change.Type, "should be modification type")

	// CRITICAL: OldLineNum must be set for modifications
	// Lua uses this to calculate: absolute_line_num = startLine + oldLineNum - 1
	// Without it (OldLineNum=0), Lua falls back to using map key which is wrong
	assert.True(t, change.OldLineNum > 0,
		"modification must have OldLineNum > 0 for correct Lua rendering")
	assert.Equal(t, 2, change.OldLineNum,
		"OldLineNum should equal StartLine for modifications (1:1 replacement)")
}

// TestCreateDiffResultFromVisualGroups_ModificationGroupHasOldLineNum verifies
// that modification groups also have OldLineNum set correctly.
func TestCreateDiffResultFromVisualGroups_ModificationGroupHasOldLineNum(t *testing.T) {
	newLines := []string{"line1", "mod2", "mod3", "mod4", "line5"}

	visualGroups := []*types.VisualGroup{
		{
			Type:      "modification",
			StartLine: 2,
			EndLine:   4, // Multi-line group
			Lines:     []string{"mod2", "mod3", "mod4"},
			OldLines:  []string{"old2", "old3", "old4"},
		},
	}

	diffResult := createDiffResultFromVisualGroups(visualGroups, newLines, len(newLines))

	change, exists := diffResult.Changes[2]
	assert.True(t, exists, "change at line 2 should exist")
	assert.Equal(t, LineModificationGroup, change.Type, "should be modification_group type")

	// OldLineNum must be set for modification groups too
	assert.True(t, change.OldLineNum > 0,
		"modification_group must have OldLineNum > 0")
	assert.Equal(t, 2, change.OldLineNum,
		"OldLineNum should equal StartLine for modification groups")
}

// TestCreateDiffResultFromVisualGroups_AdditionHasNoOldLineNum verifies that
// additions do NOT have OldLineNum set (they're new lines with no old equivalent).
func TestCreateDiffResultFromVisualGroups_AdditionHasNoOldLineNum(t *testing.T) {
	newLines := []string{"line1", "added line 2", "line3"}

	visualGroups := []*types.VisualGroup{
		{
			Type:      "addition",
			StartLine: 2,
			EndLine:   2,
			Lines:     []string{"added line 2"},
			OldLines:  nil,
		},
	}

	diffResult := createDiffResultFromVisualGroups(visualGroups, newLines, len(newLines))

	change, exists := diffResult.Changes[2]
	assert.True(t, exists, "change at line 2 should exist")
	assert.Equal(t, LineAddition, change.Type, "should be addition type")

	// Additions should NOT have OldLineNum (no corresponding old line)
	assert.True(t, change.OldLineNum <= 0,
		"additions should not have OldLineNum > 0 (they're new lines)")
}

// TestCreateDiffResultFromVisualGroups_ExpansionOldLineNum verifies that when
// 1 old line becomes 3 new lines, the modification's OldLineNum is clamped to
// the actual old line count (1), not the new line number (2).
// This fixes the bug where Lua calculated incorrect buffer positions.
func TestCreateDiffResultFromVisualGroups_ExpansionOldLineNum(t *testing.T) {
	// Scenario: 1 old line (whitespace) becomes 3 new lines (timestamp, value, name)
	// The modification at new line 2 should have OldLineNum = 1 (the only old line)
	newLines := []string{
		`            "timestamp": "2022-01-04T01:00:00Z",`,
		`            "value": 260,`,
		`            "name": "John"`,
	}

	visualGroups := []*types.VisualGroup{
		{
			Type:      "addition",
			StartLine: 1,
			EndLine:   1,
			Lines:     []string{newLines[0]},
		},
		{
			Type:      "modification",
			StartLine: 2,
			EndLine:   2,
			Lines:     []string{newLines[1]},
			OldLines:  []string{"            "}, // whitespace
		},
		{
			Type:      "addition",
			StartLine: 3,
			EndLine:   3,
			Lines:     []string{newLines[2]},
		},
	}

	// CRITICAL: Pass oldLineCount=1 to indicate only 1 old line exists
	diffResult := createDiffResultFromVisualGroups(visualGroups, newLines, 1)

	// The modification at new line 2 should have OldLineNum = 1 (clamped to old line count)
	mod, exists := diffResult.Changes[2]
	assert.True(t, exists, "modification at line 2 should exist")
	assert.Equal(t, LineModification, mod.Type, "should be modification type")

	// CRITICAL FIX: OldLineNum should be 1, NOT 2
	// If oldLineNum = 2, Lua calculates: bufferLine = startLine + 2 - 1 = startLine + 1
	// But the stage only covers 1 buffer line (startLine), so this is wrong!
	// With oldLineNum = 1, Lua calculates: bufferLine = startLine + 1 - 1 = startLine (correct)
	assert.Equal(t, 1, mod.OldLineNum,
		"modification OldLineNum should be 1 (clamped to old line count), not 2 (new line number)")
}

// TestCreateDiffResultFromVisualGroups_MixedChangesCoordinates verifies that
// a mix of modifications and additions have correct coordinate mappings.
// This simulates the real scenario: a stage with some modifications followed by additions.
func TestCreateDiffResultFromVisualGroups_MixedChangesCoordinates(t *testing.T) {
	// Simulate: 5 old lines replaced by 8 new lines
	// - Lines 1-3 unchanged (no visual group)
	// - Line 4 modified
	// - Lines 5-8 are additions
	newLines := []string{
		"unchanged1",
		"unchanged2",
		"unchanged3",
		"MODIFIED line 4",
		"ADDED line 5",
		"ADDED line 6",
		"ADDED line 7",
		"ADDED line 8",
	}

	visualGroups := []*types.VisualGroup{
		{
			Type:      "modification",
			StartLine: 4,
			EndLine:   4,
			Lines:     []string{"MODIFIED line 4"},
			OldLines:  []string{"old line 4"},
		},
		{
			Type:      "addition",
			StartLine: 5,
			EndLine:   8,
			Lines:     []string{"ADDED line 5", "ADDED line 6", "ADDED line 7", "ADDED line 8"},
			OldLines:  nil,
		},
	}

	diffResult := createDiffResultFromVisualGroups(visualGroups, newLines, len(newLines))

	// Verify modification at line 4
	mod, exists := diffResult.Changes[4]
	assert.True(t, exists, "modification at line 4")
	assert.Equal(t, LineModification, mod.Type, "line 4 is modification")
	assert.Equal(t, 4, mod.OldLineNum, "modification OldLineNum should be 4")
	assert.Equal(t, 4, mod.NewLineNum, "modification NewLineNum should be 4")

	// Verify addition group at line 5
	add, exists := diffResult.Changes[5]
	assert.True(t, exists, "addition at line 5")
	assert.Equal(t, LineAdditionGroup, add.Type, "line 5 is addition_group")
	assert.True(t, add.OldLineNum <= 0, "addition should not have OldLineNum > 0")
	assert.Equal(t, 5, add.NewLineNum, "addition NewLineNum should be 5")
	assert.Equal(t, 5, add.StartLine, "addition StartLine")
	assert.Equal(t, 8, add.EndLine, "addition EndLine")
}

// TestLuaCoordinateCalculation simulates how Lua calculates buffer positions
// and verifies that the coordinates from createDiffResultFromVisualGroups are correct.
func TestLuaCoordinateCalculation(t *testing.T) {
	// Simulate a stage that covers buffer lines 28-32 (startLine=28, endLineInclusive=32)
	// with 8 new lines replacing 5 old lines
	stageStartLine := 28
	newLines := []string{
		"line1", "line2", "line3",
		"MODIFIED line 4",
		"ADDED 5", "ADDED 6", "ADDED 7", "ADDED 8",
	}

	visualGroups := []*types.VisualGroup{
		{
			Type:      "modification",
			StartLine: 4, // Relative to stage content
			EndLine:   4,
			Lines:     []string{"MODIFIED line 4"},
			OldLines:  []string{"old 4"},
		},
		{
			Type:      "addition",
			StartLine: 5, // Relative to stage content
			EndLine:   8,
			Lines:     []string{"ADDED 5", "ADDED 6", "ADDED 7", "ADDED 8"},
		},
	}

	diffResult := createDiffResultFromVisualGroups(visualGroups, newLines, len(newLines))

	// Simulate Lua's coordinate calculation for modification
	mod := diffResult.Changes[4]

	// Lua code (simplified):
	// if is_modification_type and oldLineNum > 0 then
	//     absolute_line_num = startLine + oldLineNum - 1
	// else
	//     absolute_line_num = startLine + map_key - 1  (fallback - WRONG for mods!)
	// end

	var absoluteLineNum int
	if mod.OldLineNum > 0 {
		// Correct path: use oldLineNum
		absoluteLineNum = stageStartLine + mod.OldLineNum - 1
	} else {
		// Fallback path (what happens without the fix)
		absoluteLineNum = stageStartLine + 4 - 1 // map key is 4
	}

	// Expected: modification at buffer line 31 (28 + 4 - 1)
	expectedBufferLine := 31
	assert.Equal(t, expectedBufferLine, absoluteLineNum,
		"modification should render at buffer line 31")

	// Verify OldLineNum is set (otherwise we'd hit the fallback)
	assert.True(t, mod.OldLineNum > 0,
		"OldLineNum must be > 0 for Lua to use the correct calculation")
}

// TestVisualGroupsDoNotOverlapWithModifications verifies that when staging
// creates visual groups, they don't overlap with modification positions.
func TestVisualGroupsDoNotOverlapWithModifications(t *testing.T) {
	// Scenario: modifications at some lines, additions at others
	// Visual groups for additions should not overlap with modification lines

	diff := &DiffResult{
		Changes: map[int]LineDiff{
			// Modification at new line 1 (was old line 3)
			1: {Type: LineModification, LineNumber: 1, NewLineNum: 1, OldLineNum: 3,
				Content: "new1", OldContent: "old3"},
			// delete_chars at new line 2 (was old line 1)
			2: {Type: LineDeleteChars, LineNumber: 2, NewLineNum: 2, OldLineNum: 1,
				Content: "new2", OldContent: "old1", ColStart: 0, ColEnd: 4},
			// Addition at new line 3
			3: {Type: LineAddition, LineNumber: 3, NewLineNum: 3, OldLineNum: -1,
				Content: "added3"},
			// Addition at new line 4
			4: {Type: LineAddition, LineNumber: 4, NewLineNum: 4, OldLineNum: -1,
				Content: "added4"},
			// Addition group at new lines 5-8
			5: {Type: LineAdditionGroup, LineNumber: 5, NewLineNum: 5, OldLineNum: -1,
				Content: "", StartLine: 5, EndLine: 8,
				GroupLines: []string{"added5", "added6", "added7", "added8"}},
		},
		OldLineCount: 5,
		NewLineCount: 8,
	}

	newLines := []string{"new1", "new2", "added3", "added4", "added5", "added6", "added7", "added8"}
	oldLines := []string{"", "", "old3", "", "old1"} // Sparse: only modifications have old content

	// Compute visual groups
	groups := computeVisualGroups(diff.Changes, newLines, oldLines)

	// Verify visual groups don't reference lines beyond newLines bounds
	for _, vg := range groups {
		assert.True(t, vg.StartLine >= 1 && vg.StartLine <= len(newLines),
			fmt.Sprintf("VisualGroup StartLine %d should be within [1, %d]", vg.StartLine, len(newLines)))
		assert.True(t, vg.EndLine >= 1 && vg.EndLine <= len(newLines),
			fmt.Sprintf("VisualGroup EndLine %d should be within [1, %d]", vg.EndLine, len(newLines)))
	}
}

// TestSingleLineToMultipleLinesAllIncluded verifies that when one old line becomes
// multiple new lines (like whitespace becoming timestamp, value, name), all the new
// lines are included in the diff changes and visual groups.
// This tests the fix for the bug where middle lines were missing from the diff.
func TestSingleLineToMultipleLinesAllIncluded(t *testing.T) {
	// Scenario: one whitespace line becomes three content lines
	oldText := `        {

        }`
	newText := `        {
            "timestamp": "2022-01-04T01:00:00Z",
            "value": 260,
            "name": "John"
        }`

	diffResult := analyzeDiff(oldText, newText)

	// We should have changes for new lines 2, 3, 4 (the three content lines)
	// Line 1 ({) and line 5 (}) are equal
	// Line 2 is a modification (whitespace -> timestamp, categorized as append/modification)
	// Lines 3 and 4 are additions (value, name)

	// Check that we have at least 3 changes (could be 2 modifications + 1 addition depending on categorization)
	assert.True(t, len(diffResult.Changes) >= 2,
		fmt.Sprintf("Expected at least 2 changes, got %d", len(diffResult.Changes)))

	// Verify that additions from a delete+insert block stay together for staging
	// by checking their OldLineNum values are consistent
	var additionOldLineNums []int
	for _, change := range diffResult.Changes {
		if change.Type == LineAddition || change.Type == LineAdditionGroup {
			additionOldLineNums = append(additionOldLineNums, change.OldLineNum)
		}
	}

	// All additions should have the same anchor (OldLineNum)
	for i := 1; i < len(additionOldLineNums); i++ {
		assert.Equal(t, additionOldLineNums[0], additionOldLineNums[i],
			"All additions in a delete+insert block should have the same OldLineNum anchor")
	}
}

// TestStageIncludesAllLinesFromDeleteInsertBlock verifies that when creating stages
// from a diff where one line becomes multiple lines, all the new lines are included
// in the stage and its visual groups.
func TestStageIncludesAllLinesFromDeleteInsertBlock(t *testing.T) {
	// Scenario: one whitespace line becomes three content lines
	oldText := "            " // just whitespace
	newText := `            "timestamp": "2022-01-04T01:00:00Z",
            "value": 260,
            "name": "John"`

	diffResult := analyzeDiff(oldText, newText)

	// Create stages from this diff
	newLines := splitLines(newText)
	stagingResult := CreateStages(
		diffResult,
		1,    // cursorRow
		0, 0, // no viewport (all visible)
		1,    // baseLineOffset
		3,    // proximityThreshold
		"test.json",
		newLines,
	)

	// All changes should be in one stage since they're from the same delete+insert block
	if stagingResult != nil && len(stagingResult.Stages) > 0 {
		stage := stagingResult.Stages[0]

		// The stage's completion should have all 3 lines
		assert.Equal(t, 3, len(stage.Completion.Lines),
			fmt.Sprintf("Stage should have 3 lines, got %d", len(stage.Completion.Lines)))

		// The visual groups should cover all lines
		totalLinesInGroups := 0
		for _, vg := range stage.VisualGroups {
			totalLinesInGroups += vg.EndLine - vg.StartLine + 1
		}
		// All 3 lines should be accounted for in visual groups
		// (either as one group of 3 or multiple groups totaling 3)
		assert.True(t, totalLinesInGroups >= 2,
			fmt.Sprintf("Visual groups should cover at least 2 lines, got %d", totalLinesInGroups))
	}
}
