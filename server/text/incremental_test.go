package text

import (
	"cursortab/assert"
	"testing"
)

func TestIncrementalDiffBuilder_BasicModification(t *testing.T) {
	oldLines := []string{"hello world", "foo bar", "baz qux"}
	builder := NewIncrementalDiffBuilder(oldLines)

	// Add lines that match/modify the old lines
	change1 := builder.AddLine("hello world") // exact match
	assert.Nil(t, change1, "expected no change for exact match")

	change2 := builder.AddLine("foo baz") // modification
	assert.NotNil(t, change2, "expected change for modification")
	assert.True(t, change2.Type == ChangeReplaceChars || change2.Type == ChangeModification, "expected modification type")

	change3 := builder.AddLine("baz qux") // exact match
	assert.Nil(t, change3, "expected no change for exact match")

	// Verify final state
	assert.Equal(t, 1, len(builder.Changes), "change count")
}

func TestIncrementalDiffBuilder_Addition(t *testing.T) {
	oldLines := []string{"line 1", "line 2"}
	builder := NewIncrementalDiffBuilder(oldLines)

	builder.AddLine("line 1")   // match
	builder.AddLine("new line") // addition
	builder.AddLine("line 2")   // match

	assert.Equal(t, 1, len(builder.Changes), "change count")

	// Find the addition
	found := false
	for _, change := range builder.Changes {
		if change.Type == ChangeAddition {
			assert.Equal(t, "new line", change.Content, "content")
			found = true
			break
		}
	}
	assert.True(t, found, "did not find addition change")
}

func TestIncrementalDiffBuilder_MultipleAdditions(t *testing.T) {
	oldLines := []string{"a", "b"}
	builder := NewIncrementalDiffBuilder(oldLines)

	builder.AddLine("a") // match
	builder.AddLine("x") // addition
	builder.AddLine("y") // addition
	builder.AddLine("b") // match

	assert.Equal(t, 2, len(builder.Changes), "change count")
}

func TestIncrementalStageBuilder_SingleStage(t *testing.T) {
	oldLines := []string{"line 1", "line 2", "line 3"}
	builder := NewIncrementalStageBuilder(
		oldLines,
		1, // baseLineOffset
		3, // proximityThreshold
		0, // viewportTop (disabled)
		0, // viewportBottom (disabled)
		1, // cursorRow
		"test.go",
	)

	// Add modified lines that should all be in the same stage
	builder.AddLine("line 1 modified") // modification
	builder.AddLine("line 2 modified") // modification
	builder.AddLine("line 3")          // match

	result := builder.Finalize()
	assert.NotNil(t, result, "staging result")

	assert.Equal(t, 1, len(result.Stages), "stage count")

	stage := result.Stages[0]
	assert.Equal(t, 2, len(stage.Changes), "changes in stage")
}

func TestIncrementalStageBuilder_MultipleStages(t *testing.T) {
	oldLines := []string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
		"line 5",
		"line 6",
		"line 7",
		"line 8",
		"line 9",
		"line 10",
	}
	builder := NewIncrementalStageBuilder(
		oldLines,
		1, // baseLineOffset
		2, // proximityThreshold (small to force multiple stages)
		0, // viewportTop
		0, // viewportBottom
		1, // cursorRow
		"test.go",
	)

	// Add lines with gaps > proximityThreshold to create multiple stages
	builder.AddLine("line 1 modified") // modification at line 1
	builder.AddLine("line 2")          // match
	builder.AddLine("line 3")          // match
	builder.AddLine("line 4")          // match
	builder.AddLine("line 5")          // match
	builder.AddLine("line 6 modified") // modification at line 6 (gap > 2)
	builder.AddLine("line 7")          // match
	builder.AddLine("line 8")          // match
	builder.AddLine("line 9")          // match
	builder.AddLine("line 10")         // match

	result := builder.Finalize()
	assert.NotNil(t, result, "staging result")

	assert.Equal(t, 2, len(result.Stages), "stage count")
}

func TestIncrementalStageBuilder_StageFinalizationOnGap(t *testing.T) {
	oldLines := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	builder := NewIncrementalStageBuilder(
		oldLines,
		1,    // baseLineOffset
		2,    // proximityThreshold
		0, 0, // viewport disabled
		1, // cursorRow
		"test.go",
	)

	// Line 1: modification, starts a stage
	finalized := builder.AddLine("a modified")
	assert.Nil(t, finalized, "should not finalize on first change")

	// Lines 2-3: matches (gap building up but not exceeding threshold)
	finalized = builder.AddLine("b") // gap = 1, threshold = 2
	assert.Nil(t, finalized, "should not finalize when gap <= threshold")
	finalized = builder.AddLine("c") // gap = 2, still not > threshold
	assert.Nil(t, finalized, "should not finalize when gap == threshold")

	// Line 4: match, but gap now exceeds threshold (gap = 3 > 2)
	// Stage should finalize even without a new change
	finalized = builder.AddLine("d")
	assert.NotNil(t, finalized, "should finalize stage when gap exceeds threshold")
	assert.Equal(t, 1, len(finalized.Changes), "finalized stage should have 1 change")

	// Line 5: modification starts a new stage
	finalized = builder.AddLine("e modified")
	assert.Nil(t, finalized, "should not finalize on first change of new stage")
}

func TestIncrementalDiffBuilder_SimilarityMatching(t *testing.T) {
	oldLines := []string{
		"func hello() {",
		"    return world",
		"}",
	}
	builder := NewIncrementalDiffBuilder(oldLines)

	// Modified version with similar structure
	change1 := builder.AddLine("func hello() {") // exact match
	assert.Nil(t, change1, "expected exact match")

	change2 := builder.AddLine("    return world + 1") // modification
	assert.NotNil(t, change2, "expected modification")
	assert.Equal(t, 2, change2.OldLineNum, "old line number")

	change3 := builder.AddLine("}") // exact match
	assert.Nil(t, change3, "expected exact match for closing brace")
}

func TestIncrementalStageBuilder_ViewportBoundary(t *testing.T) {
	// Use more distinct line content to avoid similarity matching issues
	oldLines := []string{
		"line one",
		"line two",
		"line three",
		"line four",
		"line five",
		"line six",
		"line seven",
		"line eight",
		"line nine",
		"line ten",
	}
	builder := NewIncrementalStageBuilder(
		oldLines,
		1,  // baseLineOffset
		10, // proximityThreshold (high to prevent gap-based splits)
		1,  // viewportTop
		5,  // viewportBottom (first 5 lines visible)
		3,  // cursorRow
		"test.go",
	)

	// Modifications in viewport (lines 1-5)
	builder.AddLine("line one modified") // in viewport, buffer line = 1
	builder.AddLine("line two")
	builder.AddLine("line three")
	builder.AddLine("line four")
	builder.AddLine("line five modified") // still in viewport, buffer line = 5

	// Add remaining lines to complete the sequence
	builder.AddLine("line six modified") // outside viewport, buffer line = 6
	builder.AddLine("line seven")
	builder.AddLine("line eight")
	builder.AddLine("line nine")
	builder.AddLine("line ten")

	// Finalize and check we got multiple stages
	result := builder.Finalize()
	assert.NotNil(t, result, "expected staging result")

	// Should have 2 stages: one for viewport changes (1, 5), one for outside (6)
	if len(result.Stages) < 2 {
		for i, stage := range result.Stages {
			t.Logf("Stage %d: BufferStart=%d, BufferEnd=%d, changes=%d",
				i, stage.BufferStart, stage.BufferEnd, len(stage.Changes))
		}
	}
	assert.GreaterOrEqual(t, len(result.Stages), 2, "expected at least 2 stages due to viewport boundary")
}

func TestIncrementalDiffBuilder_EmptyOldLines(t *testing.T) {
	builder := NewIncrementalDiffBuilder([]string{})

	change := builder.AddLine("new content")
	assert.NotNil(t, change, "expected addition change")
	assert.Equal(t, ChangeAddition, change.Type, "change type")
}

// TestIncrementalStageBuilder_BaseLineOffset verifies that BufferStart/BufferEnd
// are correctly offset when the provider trims content (baseLineOffset > 1).
// This simulates when the model only sees a window of the file, not the full file.
func TestIncrementalStageBuilder_BaseLineOffset(t *testing.T) {
	// Simulate a trimmed window: model sees lines 20-25 of original file
	// oldLines here represents the TRIMMED content (what model sees)
	oldLines := []string{
		"  if (article.tags === null) {",  // buffer line 20
		"    article.tags = tag;",         // buffer line 21
		"  } else {",                      // buffer line 22
		"    article.tags = concat(tag);", // buffer line 23
		"  }",                             // buffer line 24
	}

	baseLineOffset := 20 // Window starts at buffer line 20

	builder := NewIncrementalStageBuilder(
		oldLines,
		baseLineOffset,
		3,      // proximityThreshold
		15, 30, // viewport (lines 15-30 visible)
		22, // cursorRow (in middle of window)
		"test.ts",
	)

	// Model outputs modified content
	builder.AddLine("  if (article.tags === null) {") // match
	builder.AddLine("    article.tags = [tag];")      // modification
	builder.AddLine("  } else {")                     // match
	builder.AddLine("    article.tags.push(tag);")    // modification
	builder.AddLine("  }")                            // match

	result := builder.Finalize()
	assert.NotNil(t, result, "expected staging result")

	assert.Equal(t, 1, len(result.Stages), "stage count")

	stage := result.Stages[0]

	// BufferStart should be 21 (baseLineOffset + 1 for the second line where change is)
	// BufferEnd should be 24 (baseLineOffset + 3 for line 4 where last change is)
	// The key test: these should NOT be 1-5, they should be offset by baseLineOffset
	assert.GreaterOrEqual(t, stage.BufferStart, baseLineOffset, "BufferStart >= baseLineOffset")
	assert.GreaterOrEqual(t, stage.BufferEnd, baseLineOffset, "BufferEnd >= baseLineOffset")

	// More specific check: changes are on lines 2 and 4 of input (1-indexed)
	// So BufferStart should be baseLineOffset + 1 = 21
	// And BufferEnd should be baseLineOffset + 3 = 23 (line 4 of 5)
	expectedStart := 21 // Line 2 of trimmed = buffer line 21
	expectedEnd := 23   // Line 4 of trimmed = buffer line 23

	assert.Equal(t, expectedStart, stage.BufferStart, "BufferStart")
	assert.Equal(t, expectedEnd, stage.BufferEnd, "BufferEnd")

	// Verify changes exist
	assert.Equal(t, 2, len(stage.Changes), "change count")
}

// TestIncrementalStageBuilder_BaseLineOffsetWithGap tests that gap detection
// works correctly when baseLineOffset > 1 and stages finalize mid-stream.
func TestIncrementalStageBuilder_BaseLineOffsetWithGap(t *testing.T) {
	// Simulate window starting at line 50
	oldLines := []string{
		"line A", // buffer line 50
		"line B", // buffer line 51
		"line C", // buffer line 52
		"line D", // buffer line 53
		"line E", // buffer line 54
		"line F", // buffer line 55
		"line G", // buffer line 56
		"line H", // buffer line 57
	}

	baseLineOffset := 50

	builder := NewIncrementalStageBuilder(
		oldLines,
		baseLineOffset,
		2,      // proximityThreshold
		40, 60, // viewport
		52, // cursorRow
		"test.go",
	)

	// Change on line 1 (buffer line 50)
	finalized := builder.AddLine("line A modified")
	assert.Nil(t, finalized, "should not finalize on first change")

	// Lines 2-4: no changes, building gap
	builder.AddLine("line B") // gap = 1
	builder.AddLine("line C") // gap = 2

	// Line 4: gap > threshold, should finalize
	finalized = builder.AddLine("line D") // gap = 3 > 2
	assert.NotNil(t, finalized, "expected stage to finalize on gap")

	// Check the finalized stage has correct buffer positions
	assert.Equal(t, 50, finalized.BufferStart, "finalized stage BufferStart")
	assert.Equal(t, 50, finalized.BufferEnd, "finalized stage BufferEnd")
}

// TestIncrementalStageBuilder_GapDetectionWithSimilarityMatching verifies that when
// similarity matching maps model output to scattered buffer positions, gap detection
// still groups changes appropriately based on buffer line proximity.
func TestIncrementalStageBuilder_GapDetectionWithSimilarityMatching(t *testing.T) {
	// Simulate a scenario where similarity matching maps new lines to scattered old lines.
	// Old lines represent different functions in a file.
	oldLines := []string{
		"function foo() {", // line 1 (buffer 52)
		"  const x = 1;",   // line 2 (buffer 53)
		"  return x;",      // line 3 (buffer 54)
		"}",                // line 4 (buffer 55)
		"",                 // line 5 (buffer 56)
		"function bar() {", // line 6 (buffer 57)
		"  const y = 2;",   // line 7 (buffer 58)
		"  return y;",      // line 8 (buffer 59)
		"}",                // line 9 (buffer 60)
		"",                 // line 10 (buffer 61)
		"function baz() {", // line 11 (buffer 62)
		"  const z = 3;",   // line 12 (buffer 63)
		"  return z;",      // line 13 (buffer 64)
		"}",                // line 14 (buffer 65)
	}

	baseLineOffset := 52

	builder := NewIncrementalStageBuilder(
		oldLines,
		baseLineOffset,
		3,      // proximityThreshold - gaps > 3 should split stages
		40, 80, // viewport
		58, // cursorRow (in middle)
		"test.ts",
	)

	// Model outputs content where:
	// - Line 1 matches old line 1 (exact)
	// - Line 2 is a MODIFICATION of old line 2 -> buffer 53
	// - Line 3 matches old line 3 (exact)
	// - Line 4 matches old line 4 (exact)
	// - Line 5 matches old line 5 (exact)
	// - Line 6 matches old line 6 (exact)
	// - Line 7 is a MODIFICATION of old line 7 -> buffer 58 (buffer gap = 5!)
	// - Line 8 matches old line 8 (exact)
	// - etc.

	builder.AddLine("function foo() {") // match line 1
	builder.AddLine("  const x = 100;") // MODIFY line 2 -> buffer 53
	builder.AddLine("  return x;")      // match line 3
	builder.AddLine("}")                // match line 4
	builder.AddLine("")                 // match line 5
	builder.AddLine("function bar() {") // match line 6
	builder.AddLine("  const y = 200;") // MODIFY line 7 -> buffer 58 (buffer gap = 5!)
	builder.AddLine("  return y;")      // match line 8
	builder.AddLine("}")                // match line 9
	builder.AddLine("")                 // match line 10
	builder.AddLine("function baz() {") // match line 11
	builder.AddLine("  const z = 300;") // MODIFY line 12 -> buffer 63 (buffer gap = 5!)
	builder.AddLine("  return z;")      // match line 13
	builder.AddLine("}")                // match line 14

	result := builder.Finalize()
	assert.NotNil(t, result, "expected staging result")

	// With proximityThreshold=3 and buffer gaps of 5 between functions,
	// changes should be split into separate stages
	assert.True(t, len(result.Stages) >= 3, "expected at least 3 stages")

	// Verify each stage has changes
	for _, stage := range result.Stages {
		assert.True(t, len(stage.Changes) > 0, "stage should have changes")
	}
}

// TestIncrementalStageBuilder_SimilarityMatchingToSimilarLines verifies behavior when
// model output contains lines similar to multiple locations in the original file.
func TestIncrementalStageBuilder_SimilarityMatchingToSimilarLines(t *testing.T) {
	// Simulate model outputting content from wrong function.
	// Old lines have similar patterns in different locations.
	oldLines := []string{
		"  article.title = title;",   // line 1 (buffer 20) - in setTitle()
		"  article.author = author;", // line 2 (buffer 21)
		"  return true;",             // line 3 (buffer 22)
		"}",                          // line 4 (buffer 23)
		"",                           // line 5 (buffer 24)
		"function updateTags() {",    // line 6 (buffer 25)
		"  article.tags = tags;",     // line 7 (buffer 26) - similar to line 1!
		"  article.count = count;",   // line 8 (buffer 27) - similar to line 2!
		"  return true;",             // line 9 (buffer 28)
		"}",                          // line 10 (buffer 29)
	}

	baseLineOffset := 20

	builder := NewIncrementalStageBuilder(
		oldLines,
		baseLineOffset,
		3,      // proximityThreshold
		15, 35, // viewport
		23, // cursorRow
		"test.ts",
	)

	// Model outputs lines that SHOULD modify lines 1-3 (setTitle function)
	// but similarity matching might find matches at lines 7-9 (updateTags function)
	// if the content is similar enough.

	// Intentionally use content that's similar to BOTH locations
	builder.AddLine("  article.name = name;")   // Similar to line 1 AND line 7
	builder.AddLine("  article.value = value;") // Similar to line 2 AND line 8
	builder.AddLine("  return false;")          // Similar to line 3 AND line 9

	result := builder.Finalize()
	assert.NotNil(t, result, "expected staging result")

	// Check that changes are coherent (all in one location, not scattered)
	if len(result.Stages) == 1 {
		stage := result.Stages[0]
		bufferRange := stage.BufferEnd - stage.BufferStart
		// If all changes are coherent, range should be small (2-3 lines)
		// If scattered, range could be large (6+ lines spanning two functions)
		assert.True(t, bufferRange <= 5, "changes should be coherent")
	}
}

// TestIncrementalStageBuilder_GapDetectionBehavior tests gap detection behavior
// when changes map to non-consecutive buffer positions.
func TestIncrementalStageBuilder_GapDetectionBehavior(t *testing.T) {
	// Create old lines where we can control exactly which lines match
	// Use similar prefixes to ensure modifications are detected
	oldLines := []string{
		"  func alpha() {", // line 1 (buffer 10)
		"    return 1",     // line 2 (buffer 11)
		"  }",              // line 3 (buffer 12)
		"",                 // line 4 (buffer 13)
		"  func beta() {",  // line 5 (buffer 14)
		"    return 2",     // line 6 (buffer 15)
	}

	baseLineOffset := 10

	builder := NewIncrementalStageBuilder(
		oldLines,
		baseLineOffset,
		2,     // proximityThreshold = 2 (gap > 2 should split)
		5, 20, // viewport
		12, // cursorRow
		"test.go",
	)

	// Output lines where:
	// - New line 1: modification of old line 1 (buffer 10)
	// - New line 2: exact match of old line 2
	// - New line 3: exact match of old line 3
	// - New line 4: exact match of old line 4
	// - New line 5: modification of old line 6 (buffer 15!) - skipping old line 5
	//
	// New line gap between changes: 5 - 1 = 4 > threshold (should split)
	// Buffer line gap between changes: 15 - 10 = 5 > threshold (should split)

	builder.AddLine("  func alpha(x) {") // Modify old line 1 -> buffer 10
	builder.AddLine("    return 1")      // Match old line 2
	builder.AddLine("  }")               // Match old line 3
	builder.AddLine("")                  // Match old line 4
	builder.AddLine("    return 200")    // Modify - similar to old line 6 -> buffer 15

	result := builder.Finalize()
	assert.NotNil(t, result, "expected staging result")

	// Since BOTH new-line gap (4) and buffer-line gap (5) exceed threshold (2),
	// we expect 2 stages regardless of which gap metric is used.
	assert.Equal(t, 2, len(result.Stages), "stage count")
}

// TestIncrementalStageBuilder_ConsecutiveOutputMapsToScatteredLines tests when
// consecutive model output lines map to non-consecutive buffer positions via similarity.
func TestIncrementalStageBuilder_ConsecutiveOutputMapsToScatteredLines(t *testing.T) {
	// Old lines with distinct content. We'll output SIMILAR lines to ensure
	// they match as modifications (not additions).
	oldLines := []string{
		"  article.title = title;",   // line 1 (buffer 50) - will modify
		"  const x = 1;",             // line 2 (buffer 51) - will skip
		"  article.author = author;", // line 3 (buffer 52) - will modify
		"  const y = 2;",             // line 4 (buffer 53) - will skip
		"  article.tags = tags;",     // line 5 (buffer 54) - will modify
	}

	baseLineOffset := 50

	builder := NewIncrementalStageBuilder(
		oldLines,
		baseLineOffset,
		1,      // proximityThreshold = 1 (very strict - gap > 1 should split)
		45, 60, // viewport
		52, // cursorRow
		"test.go",
	)

	// Model outputs CONSECUTIVE new lines (no gaps in new-line numbers)
	// These are similar enough to match the old lines at positions 1, 3, 5:
	// - New line 1 -> old line 1 (buffer 50) - similar "article.title"
	// - New line 2 -> old line 3 (buffer 52) - similar "article.author" (buffer gap = 2)
	// - New line 3 -> old line 5 (buffer 54) - similar "article.tags" (buffer gap = 2)
	//
	// New line gaps: 1, 1 (NOT > threshold)
	// Buffer line gaps: 2, 2 (> threshold)

	finalized1 := builder.AddLine("  article.title = newTitle;")   // Modify -> buffer 50
	finalized2 := builder.AddLine("  article.author = newAuthor;") // Modify -> buffer 52
	finalized3 := builder.AddLine("  article.tags = newTags;")     // Modify -> buffer 54

	// Track stages finalized during streaming
	streamFinalizedCount := 0
	if finalized1 != nil {
		streamFinalizedCount++
	}
	if finalized2 != nil {
		streamFinalizedCount++
	}
	if finalized3 != nil {
		streamFinalizedCount++
	}

	result := builder.Finalize()
	assert.NotNil(t, result, "expected staging result")

	// Verify changes exist in stages
	totalChanges := 0
	for _, stage := range result.Stages {
		totalChanges += len(stage.Changes)
	}
	assert.Greater(t, totalChanges, 0, "expected changes in stages")

	// When buffer gaps exceed threshold, expect separate stages
	assert.GreaterOrEqual(t, len(result.Stages), 3, "expected separate stages due to buffer gaps")
}

// TestIncrementalDiffBuilder_SearchWindowConstraint verifies that matching is
// constrained to the search window and doesn't match far-away lines.
func TestIncrementalDiffBuilder_SearchWindowConstraint(t *testing.T) {
	// Simulate the scenario from the logs:
	// - 98 old lines (full file)
	// - Model outputs something, and first line matches to old line 41
	//
	// Old line 0: "import { Hono } from \"hono\";"
	// Old line 40: "export { WorkflowRuntimeEntrypoint... }"
	// Old line 41: empty or comment
	//
	// If model line 1 is "import apiKeyRoutes...", it should match within [0, 10),
	// NOT to line 41.

	oldLines := make([]string, 98)
	// Fill with realistic content
	oldLines[0] = "import { Hono } from \"hono\";"
	oldLines[1] = ""
	oldLines[2] = "import auth from \"./auth\";"
	oldLines[3] = "import { ApiContext } from \"./context\";"
	for i := 4; i < 40; i++ {
		oldLines[i] = "import something from \"./something\";"
	}
	oldLines[40] = "// Export comment"
	oldLines[41] = "export { WorkflowRuntimeEntrypoint as Runtime } from \"./runtime\";"
	oldLines[42] = ""
	oldLines[43] = "// Initialize app"
	oldLines[44] = "const application = new Hono<ApiContext>();"
	for i := 45; i < 98; i++ {
		oldLines[i] = "app.route(\"/path\", handler);"
	}

	builder := NewIncrementalDiffBuilder(oldLines)

	// Model outputs "import apiKeyRoutes..." as first line
	// This should match within [0, 10), NOT to line 41
	modelLine1 := "import apiKeyRoutes from \"./routes/api-keys\";"
	change1 := builder.AddLine(modelLine1)

	// Check where it matched
	assert.True(t, len(builder.LineMapping.NewToOld) > 0, "line mapping should be populated")

	matchedOldLine := builder.LineMapping.NewToOld[0] // 1-indexed old line number

	// The search window for first line is [0, 10)
	// Matches should be constrained to this window
	assert.True(t, matchedOldLine <= 10, "first model line should match within search window")

	// Check the recorded change
	if change1 != nil {
		assert.True(t, change1.OldLineNum <= 10, "change OldLineNum should be within search window")
	}
}

// TestIncrementalDiffBuilder_SearchWindowRespected verifies the search window bounds
func TestIncrementalDiffBuilder_SearchWindowRespected(t *testing.T) {
	// Create old lines where exact match exists ONLY outside the search window
	oldLines := make([]string, 50)
	for i := 0; i < 50; i++ {
		oldLines[i] = "generic line"
	}
	// Put a unique line at position 30 (outside initial search window [0, 10))
	oldLines[30] = "unique content at line 31"

	builder := NewIncrementalDiffBuilder(oldLines)

	// Try to match the unique line as FIRST model line
	// It should NOT match because it's outside [0, 10)
	builder.AddLine("unique content at line 31")

	matchedOldLine := builder.LineMapping.NewToOld[0]

	// Should either:
	// 1. Match to something in [0, 10) via similarity
	// 2. Or be recorded as addition (matchedOldLine == 0)
	// Should NOT match to line 31 (outside search window)
	assert.NotEqual(t, 31, matchedOldLine, "should not match to line 31")
}

// TestIncrementalDiffBuilder_OldLineIdxProgression verifies oldLineIdx advances correctly
func TestIncrementalDiffBuilder_OldLineIdxProgression(t *testing.T) {
	// Use very distinct lines to avoid similarity matching
	oldLines := []string{
		"function alpha() {",   // 1
		"function beta() {",    // 2
		"function gamma() {",   // 3
		"function delta() {",   // 4
		"function epsilon() {", // 5
	}

	builder := NewIncrementalDiffBuilder(oldLines)

	// Match line 1
	builder.AddLine("function alpha() {")
	assert.Equal(t, 1, builder.oldLineIdx, "oldLineIdx after matching line 1")

	// Match line 2
	builder.AddLine("function beta() {")
	assert.Equal(t, 2, builder.oldLineIdx, "oldLineIdx after matching line 2")

	// Add something completely different (should be addition)
	builder.AddLine("ZZZZZ COMPLETELY DIFFERENT ZZZZZ")
	// With 0.3 similarity threshold, this might still match something

	// Match line 3 (should still work because search window extends forward)
	builder.AddLine("function gamma() {}")
	assert.GreaterOrEqual(t, builder.oldLineIdx, 3, "oldLineIdx should be at least 3")
}

// TestIncrementalDiffBuilder_OutOfOrderOutput verifies behavior when model output
// contains lines in a different order than the original file.
func TestIncrementalDiffBuilder_OutOfOrderOutput(t *testing.T) {
	oldLines := []string{
		"func first() {}",
		"func second() {}",
		"func third() {}",
		"func fourth() {}",
		"func fifth() {}",
	}

	builder := NewIncrementalDiffBuilder(oldLines)

	// Output line 4 first (out of order)
	builder.AddLine("func fourth() {}")
	firstMatch := builder.LineMapping.NewToOld[0]

	// Should match to line 4 within search window [0, 10)
	assert.Equal(t, 4, firstMatch, "first match should be to line 4")

	// Output duplicate of line 4
	builder.AddLine("func fourth() {}")
	secondMatch := builder.LineMapping.NewToOld[1]

	// usedOldLines should prevent duplicate matching
	assert.True(t, secondMatch != firstMatch || firstMatch == 0, "duplicate line should not match same old line twice")
}

// TestIncrementalDiffBuilder_SearchWindowBounds tests that matching is constrained
// to a sliding search window.
func TestIncrementalDiffBuilder_SearchWindowBounds(t *testing.T) {
	tests := []struct {
		name           string
		oldLineCount   int
		startPosition  int
		uniquePosition int
		expectMatch    bool
	}{
		{
			name:           "unique line within window",
			oldLineCount:   50,
			startPosition:  0,
			uniquePosition: 5,
			expectMatch:    true,
		},
		{
			name:           "unique line outside window",
			oldLineCount:   50,
			startPosition:  0,
			uniquePosition: 30,
			expectMatch:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldLines := make([]string, tt.oldLineCount)
			for i := range oldLines {
				oldLines[i] = "generic content"
			}
			// Put unique content at specific position
			oldLines[tt.uniquePosition] = "unique_content_here"

			builder := NewIncrementalDiffBuilder(oldLines)
			builder.oldLineIdx = tt.startPosition

			builder.AddLine("unique_content_here")
			matched := builder.LineMapping.NewToOld[0]

			if tt.expectMatch {
				expectedLine := tt.uniquePosition + 1 // 1-indexed
				assert.Equal(t, expectedLine, matched, "should match at expected line")
			} else {
				// Should not match to the unique position (outside window)
				assert.NotEqual(t, tt.uniquePosition+1, matched, "should not match outside search window")
			}
		})
	}
}

// TestIncrementalDiffBuilder_LongFileWithManyExactMatches tests incremental
// diff building on a large file where most lines match exactly.
func TestIncrementalDiffBuilder_LongFileWithManyExactMatches(t *testing.T) {
	// Large file (50 lines) where first 40 lines match exactly,
	// then changes start occurring
	oldLines := make([]string, 50)
	for i := 0; i < 40; i++ {
		oldLines[i] = "func line" + string(rune('A'+i%26)) + "() {}"
	}
	oldLines[40] = "// Comment"
	oldLines[41] = ""
	for i := 42; i < 50; i++ {
		oldLines[i] = "func other" + string(rune('A'+i%26)) + "() {}"
	}

	builder := NewIncrementalDiffBuilder(oldLines)

	// Model outputs exact same content for first 40 lines
	for i := 0; i < 40; i++ {
		change := builder.AddLine(oldLines[i])
		assert.Nil(t, change, "line should be exact match")
	}

	// oldLineIdx should have advanced to 40
	assert.Equal(t, 40, builder.oldLineIdx, "oldLineIdx")

	// Now model outputs something different
	change := builder.AddLine("func completely_new() {}")
	assert.NotNil(t, change, "expected change for non-matching line")
}

// TestIncrementalDiffBuilder_MatchingWhenModelSkipsLines tests what happens when
// model output is out of order or skips lines.
func TestIncrementalDiffBuilder_MatchingWhenModelSkipsLines(t *testing.T) {
	oldLines := []string{
		"func first() {}",
		"func second() {}",
		"func third() {}",
		"func fourth() {}",
		"func fifth() {}",
	}

	builder := NewIncrementalDiffBuilder(oldLines)

	// Output line 4 first (skipping lines 1-3)
	builder.AddLine("func fourth() {}")
	matched1 := builder.LineMapping.NewToOld[0]

	assert.Equal(t, 4, matched1, "first match should be to line 4")

	// Output line 1 - but search window has moved past it
	builder.AddLine("func first() {}")
	matched2 := builder.LineMapping.NewToOld[1]

	// Line 1 is outside the new search window after matching line 4
	// Should not match to line 1
	assert.NotEqual(t, 1, matched2, "should not match to line 1")
}

// TestIncrementalStageBuilder_WhenModelOutputStartsMidFile tests when model
// output starts from middle of file instead of beginning.
func TestIncrementalStageBuilder_WhenModelOutputStartsMidFile(t *testing.T) {
	oldLines := []string{
		"func first() {}",
		"func second() {}",
		"func third() {}",
		"",
		"// Section 2",
		"func fourth() {}",
		"",
		"func fifth() {}",
		"func sixth() {}",
	}

	builder := NewIncrementalStageBuilder(
		oldLines,
		1,    // baseLineOffset
		3,    // proximityThreshold
		0, 0, // viewport disabled
		5, // cursorRow
		"test.go",
	)

	// Model starts outputting from line 5 (skipping lines 1-4)
	modelOutput := []string{
		"// Section 2",              // matches line 5
		"func fourth_modified() {}", // modified
		"",                          // matches line 7
		"func fifth_modified() {}",  // modified
	}

	for _, line := range modelOutput {
		builder.AddLine(line)
	}

	result := builder.Finalize()
	assert.NotNil(t, result, "expected staging result")

	// Should have at least one stage
	assert.True(t, len(result.Stages) > 0, "expected at least one stage")

	// Buffer positions should be within file bounds
	for _, stage := range result.Stages {
		assert.True(t, stage.BufferStart <= len(oldLines)+1, "stage BufferStart within bounds")
		assert.True(t, stage.BufferEnd <= len(oldLines)+10, "stage BufferEnd within bounds")
	}
}

// TestLineSimilarity verifies similarity calculation for various line comparisons.
func TestLineSimilarity(t *testing.T) {
	tests := []struct {
		name   string
		line1  string
		line2  string
		minSim float64
		maxSim float64
	}{
		{
			name:   "identical lines",
			line1:  "const x = 1;",
			line2:  "const x = 1;",
			minSim: 1.0,
			maxSim: 1.0,
		},
		{
			name:   "small modification",
			line1:  "const x = 1;",
			line2:  "const x = 2;",
			minSim: 0.8,
			maxSim: 1.0,
		},
		{
			name:   "completely different",
			line1:  "function foo() {",
			line2:  "// comment here",
			minSim: 0.0,
			maxSim: 0.3,
		},
		{
			name:   "empty vs content",
			line1:  "",
			line2:  "some content",
			minSim: 0.0,
			maxSim: 0.1,
		},
		{
			name:   "variable rename",
			line1:  "const app = new Server();",
			line2:  "const server = new Server();",
			minSim: 0.6,
			maxSim: 0.95,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			similarity := LineSimilarity(tt.line1, tt.line2)
			assert.True(t, similarity >= tt.minSim && similarity <= tt.maxSim, "similarity in expected range")
		})
	}
}

// TestIncrementalDiffBuilder_LargeFile tests incremental diff building with a large file.
func TestIncrementalDiffBuilder_LargeFile(t *testing.T) {
	// Create a large file with distinct sections
	oldLines := make([]string, 50)
	for i := 0; i < 20; i++ {
		oldLines[i] = "func section1_line" + string(rune('A'+i)) + "() {}"
	}
	oldLines[20] = ""
	for i := 21; i < 40; i++ {
		oldLines[i] = "func section2_line" + string(rune('A'+i-21)) + "() {}"
	}
	oldLines[40] = ""
	for i := 41; i < 50; i++ {
		oldLines[i] = "func section3_line" + string(rune('A'+i-41)) + "() {}"
	}

	builder := NewIncrementalDiffBuilder(oldLines)

	// Process exact matches for first 20 lines
	for i := 0; i < 20; i++ {
		change := builder.AddLine(oldLines[i])
		assert.Nil(t, change, "line should be exact match")
	}

	// oldLineIdx should have advanced
	assert.Equal(t, 20, builder.oldLineIdx, "oldLineIdx")

	// Add a new line that doesn't exist
	change := builder.AddLine("func new_function() {}")
	assert.NotNil(t, change, "expected change for new line")

	// Should be recorded as either addition or modification (depending on similarity)
	assert.True(t, change.Type == ChangeAddition || change.Type == ChangeModification || change.Type == ChangeReplaceChars, "expected addition or modification type")
}

// TestIncrementalDiffBuilder_DuplicateLinesPrevented verifies that the same old line
// cannot be matched twice (usedOldLines tracking).
func TestIncrementalDiffBuilder_DuplicateLinesPrevented(t *testing.T) {
	oldLines := []string{
		"func alpha() {}",
		"func beta() {}",
		"func gamma() {}",
	}

	builder := NewIncrementalDiffBuilder(oldLines)

	// Match line 1
	builder.AddLine("func alpha() {}")
	firstMatch := builder.LineMapping.NewToOld[0]
	assert.Equal(t, 1, firstMatch, "first line should match old line 1")

	// Try to match line 1 again (duplicate in model output)
	builder.AddLine("func alpha() {}")
	secondMatch := builder.LineMapping.NewToOld[1]

	// Should NOT match to line 1 again
	assert.NotEqual(t, 1, secondMatch, "duplicate model line should not match same old line twice")
}

// TestIncrementalStageBuilder_DuplicateOutputHandling verifies stage building
// when model outputs duplicate lines.
func TestIncrementalStageBuilder_DuplicateOutputHandling(t *testing.T) {
	oldLines := []string{
		"func setup() {",
		"    init()",
		"}",
		"",
		"func run() {",
		"    execute()",
		"}",
	}

	builder := NewIncrementalStageBuilder(
		oldLines,
		1,    // baseLineOffset
		3,    // proximityThreshold
		0, 0, // viewport disabled
		1, // cursorRow
		"test.go",
	)

	// Exact matches for first 3 lines
	for i := 0; i < 3; i++ {
		builder.AddLine(oldLines[i])
	}

	// Now output duplicates
	builder.AddLine("func setup() {}") // duplicate
	builder.AddLine("func setup() {}") // duplicate again

	result := builder.Finalize()
	assert.NotNil(t, result, "expected staging result")

	// Should have at least one stage with the duplicate changes
	assert.True(t, len(result.Stages) > 0, "expected at least one stage")

	// Each stage should have valid buffer coordinates
	for _, stage := range result.Stages {
		assert.GreaterOrEqual(t, stage.BufferStart, 1, "stage BufferStart valid")
		assert.GreaterOrEqual(t, stage.BufferEnd, stage.BufferStart, "stage BufferEnd >= BufferStart")
	}
}

func TestIncrementalStageBuilder_ConsistencyWithComputeDiff(t *testing.T) {
	oldLines := []string{
		"func main() {",
		"    fmt.Println(\"hello\")",
		"    return",
		"}",
	}
	newLines := []string{
		"func main() {",
		"    fmt.Println(\"hello world\")",
		"    return nil",
		"}",
	}

	// Use batch ComputeDiff
	oldText := JoinLines(oldLines)
	newText := JoinLines(newLines)
	batchResult := ComputeDiff(oldText, newText)

	// Use incremental builder
	builder := NewIncrementalDiffBuilder(oldLines)
	for _, line := range newLines {
		builder.AddLine(line)
	}

	// Compare change counts
	assert.Equal(t, len(batchResult.Changes), len(builder.Changes), "change count")

	// Both should identify modifications on lines 2 and 3
	for lineNum, change := range batchResult.Changes {
		incChange, ok := builder.Changes[lineNum]
		assert.True(t, ok, "incremental builder should have change at line")

		// Types might differ slightly (e.g., ReplaceChars vs Modification)
		// but both should identify it as a modification-like change
		batchIsMod := change.Type != ChangeAddition && change.Type != ChangeDeletion
		incIsMod := incChange.Type != ChangeAddition && incChange.Type != ChangeDeletion
		assert.Equal(t, batchIsMod, incIsMod, "batch and incremental should identify same change type")
	}
}

// TestIncrementalDiffBuilder_AllLinesIdentical verifies no changes when all lines match.
func TestIncrementalDiffBuilder_AllLinesIdentical(t *testing.T) {
	oldLines := []string{"line1", "line2", "line3", "line4", "line5"}
	builder := NewIncrementalDiffBuilder(oldLines)

	for _, line := range oldLines {
		change := builder.AddLine(line)
		assert.Nil(t, change, "expected no change for identical line")
	}

	assert.Equal(t, 0, len(builder.Changes), "change count")
}

// TestIncrementalDiffBuilder_AllLinesModified verifies all lines are detected as modified.
func TestIncrementalDiffBuilder_AllLinesModified(t *testing.T) {
	oldLines := []string{"old1", "old2", "old3"}
	builder := NewIncrementalDiffBuilder(oldLines)

	newLines := []string{"new1", "new2", "new3"}
	for _, line := range newLines {
		builder.AddLine(line)
	}

	// All lines should have changes
	assert.Equal(t, 3, len(builder.Changes), "change count")
}

// TestIncrementalDiffBuilder_WhitespaceOnlyLines tests handling of whitespace-only lines.
func TestIncrementalDiffBuilder_WhitespaceOnlyLines(t *testing.T) {
	oldLines := []string{"", "   ", "\t", "content"}
	builder := NewIncrementalDiffBuilder(oldLines)

	// Exact matches
	for _, line := range oldLines {
		change := builder.AddLine(line)
		assert.Nil(t, change, "expected no change for whitespace match")
	}
}

// TestIncrementalStageBuilder_EmptyInput verifies handling of empty input.
func TestIncrementalStageBuilder_EmptyInput(t *testing.T) {
	builder := NewIncrementalStageBuilder(
		[]string{}, // empty old lines
		1,          // baseLineOffset
		3,          // proximityThreshold
		0, 0,       // viewport disabled
		1, // cursorRow
		"test.go",
	)

	builder.AddLine("new content")
	// Adding to empty may return nil change or an addition

	result := builder.Finalize()
	// Result may be nil for empty old + single new line (no meaningful changes)
	if result != nil && len(result.Stages) > 0 {
		// Verify the stage has valid structure
		for _, stage := range result.Stages {
			assert.GreaterOrEqual(t, stage.BufferStart, 1, "stage BufferStart valid")
		}
	}
}

// TestIncrementalStageBuilder_SingleLine verifies handling of single-line files.
func TestIncrementalStageBuilder_SingleLine(t *testing.T) {
	oldLines := []string{"single line"}
	builder := NewIncrementalStageBuilder(
		oldLines,
		1,    // baseLineOffset
		3,    // proximityThreshold
		0, 0, // viewport disabled
		1, // cursorRow
		"test.go",
	)

	// Modify the single line
	builder.AddLine("modified single line")

	result := builder.Finalize()
	assert.NotNil(t, result, "expected staging result")

	assert.Equal(t, 1, len(result.Stages), "stage count")
}

// TestIncrementalDiffBuilder_VeryLongLines tests handling of very long lines.
func TestIncrementalDiffBuilder_VeryLongLines(t *testing.T) {
	longLine := ""
	for i := 0; i < 1000; i++ {
		longLine += "x"
	}

	oldLines := []string{longLine}
	builder := NewIncrementalDiffBuilder(oldLines)

	// Exact match
	change := builder.AddLine(longLine)
	assert.Nil(t, change, "expected no change for identical long line")

	// Slight modification
	builder2 := NewIncrementalDiffBuilder(oldLines)
	change = builder2.AddLine(longLine + "y")
	assert.NotNil(t, change, "expected change for modified long line")
}

// TestIncrementalStageBuilder_LargeGap verifies stage splitting with large gaps.
func TestIncrementalStageBuilder_LargeGap(t *testing.T) {
	// Create old lines with changes at beginning and end
	oldLines := make([]string, 100)
	for i := range oldLines {
		oldLines[i] = "line"
	}

	builder := NewIncrementalStageBuilder(
		oldLines,
		1,    // baseLineOffset
		3,    // proximityThreshold
		0, 0, // viewport disabled
		1, // cursorRow
		"test.go",
	)

	// Modify first line
	builder.AddLine("modified first")
	// Match lines 2-90
	for i := 1; i < 90; i++ {
		builder.AddLine(oldLines[i])
	}
	// Modify last few lines
	builder.AddLine("modified 90")
	for i := 91; i < 100; i++ {
		builder.AddLine(oldLines[i])
	}

	result := builder.Finalize()
	assert.NotNil(t, result, "expected staging result")

	// Should have 2 stages due to large gap
	assert.True(t, len(result.Stages) >= 2, "expected at least 2 stages")
}

// TestIncrementalStageBuilder_ConsecutiveModifications verifies consecutive modifications
// stay in the same stage.
func TestIncrementalStageBuilder_ConsecutiveModifications(t *testing.T) {
	oldLines := []string{"a", "b", "c", "d", "e"}
	builder := NewIncrementalStageBuilder(
		oldLines,
		1,    // baseLineOffset
		3,    // proximityThreshold
		0, 0, // viewport disabled
		1, // cursorRow
		"test.go",
	)

	// Modify lines 2-4 consecutively
	builder.AddLine("a")          // match
	builder.AddLine("B_modified") // modify
	builder.AddLine("C_modified") // modify
	builder.AddLine("D_modified") // modify
	builder.AddLine("e")          // match

	result := builder.Finalize()
	assert.NotNil(t, result, "expected staging result")

	// All consecutive modifications should be in one stage
	assert.Equal(t, 1, len(result.Stages), "stage count")

	if len(result.Stages) > 0 {
		assert.Equal(t, 3, len(result.Stages[0].Changes), "changes in stage")
	}
}

// TestIncrementalDiffBuilder_SpecialCharacters tests handling of special characters.
func TestIncrementalDiffBuilder_SpecialCharacters(t *testing.T) {
	oldLines := []string{
		"line with 'quotes'",
		"line with \"double quotes\"",
		"line with `backticks`",
		"line with special: !@#$%^&*()",
		"line with unicode: 日本語",
	}

	builder := NewIncrementalDiffBuilder(oldLines)

	// Exact matches should work
	for _, line := range oldLines {
		change := builder.AddLine(line)
		assert.Nil(t, change, "expected no change for line with special chars")
	}
}

// TestLineSimilarity_EdgeCases tests similarity calculation edge cases.
func TestLineSimilarity_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		line1  string
		line2  string
		minSim float64
		maxSim float64
	}{
		{"both empty", "", "", 1.0, 1.0},
		{"one empty", "content", "", 0.0, 0.1},
		{"single char same", "x", "x", 1.0, 1.0},
		{"single char different", "x", "y", 0.0, 0.5},
		{"whitespace same", "   ", "   ", 1.0, 1.0},
		{"whitespace different", "   ", "\t\t", 0.0, 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sim := LineSimilarity(tt.line1, tt.line2)
			assert.True(t, sim >= tt.minSim && sim <= tt.maxSim, "similarity in expected range")
		})
	}
}
