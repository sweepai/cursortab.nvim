package utils

import (
	"cursortab/assert"
	"testing"
)

func TestTrimContentAroundCursor_EmptyFile(t *testing.T) {
	lines := []string{}
	trimmed, cursorRow, cursorCol, offset, didTrim := TrimContentAroundCursor(lines, 0, 0, 100)

	assert.Equal(t, 0, len(trimmed), "trimmed length")
	assert.Equal(t, 0, cursorRow, "cursorRow")
	assert.Equal(t, 0, cursorCol, "cursorCol")
	assert.Equal(t, 0, offset, "offset")
	assert.False(t, didTrim, "didTrim should be false")
}

func TestTrimContentAroundCursor_SmallFile(t *testing.T) {
	lines := []string{"line 1", "line 2", "line 3"}
	trimmed, cursorRow, cursorCol, offset, didTrim := TrimContentAroundCursor(lines, 1, 5, 1000)

	// Small file should not be trimmed
	assert.Equal(t, 3, len(trimmed), "trimmed length")
	assert.Equal(t, 1, cursorRow, "cursorRow")
	assert.Equal(t, 5, cursorCol, "cursorCol")
	assert.Equal(t, 0, offset, "offset")
	assert.False(t, didTrim, "didTrim should be false")
}

func TestTrimContentAroundCursor_LargeFileTrims(t *testing.T) {
	// Create a large file
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "this is line content that takes up space"
	}

	// Very small token limit forces trimming
	trimmed, cursorRow, _, _, didTrim := TrimContentAroundCursor(lines, 50, 0, 20)

	assert.True(t, didTrim, "didTrim should be true")

	assert.True(t, len(trimmed) < 100, "trimmed length should be less than 100")

	// Cursor should be within trimmed lines
	assert.True(t, cursorRow >= 0 && cursorRow < len(trimmed), "cursorRow should be within trimmed range")
}

func TestTrimContentAroundCursor_CursorClamping(t *testing.T) {
	lines := []string{"line 1", "line 2", "line 3"}

	// Test cursor beyond file
	_, cursorRow, _, _, _ := TrimContentAroundCursor(lines, 100, 0, 1000)
	assert.Equal(t, 2, cursorRow, "cursorRow clamped to last line")

	// Test negative cursor
	_, cursorRow, _, _, _ = TrimContentAroundCursor(lines, -5, 0, 1000)
	assert.Equal(t, 0, cursorRow, "cursorRow clamped to first line")
}

func TestTrimContentAroundCursor_ZeroMaxTokens(t *testing.T) {
	lines := []string{"line 1", "line 2", "line 3"}
	trimmed, _, _, _, didTrim := TrimContentAroundCursor(lines, 1, 0, 0)

	// maxTokens <= 0 should return content as-is
	assert.Equal(t, 3, len(trimmed), "trimmed length")
	assert.False(t, didTrim, "didTrim should be false")
}

func TestTrimContentAroundCursor_BalancedTrimming(t *testing.T) {
	// Create 50 lines
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "x" // Each line is 1 char + newline = 2 chars
	}

	// Cursor at line 25 (middle), budget for ~10 lines
	// Each line is 2 chars, so 20 tokens = 40 chars = ~20 lines
	_, _, _, _, didTrim := TrimContentAroundCursor(lines, 25, 0, 20)

	assert.True(t, didTrim, "didTrim should be true")
}

// Mock DiffEntry for testing TrimDiffEntries
type mockDiffEntry struct {
	original string
	updated  string
}

func (m *mockDiffEntry) GetOriginal() string { return m.original }
func (m *mockDiffEntry) GetUpdated() string  { return m.updated }

func TestTrimDiffEntries_EmptySlice(t *testing.T) {
	var diffs []*mockDiffEntry
	result := TrimDiffEntries(diffs, 100)

	assert.Equal(t, 0, len(result), "result length")
}

func TestTrimDiffEntries_ZeroMaxTokens(t *testing.T) {
	diffs := []*mockDiffEntry{
		{original: "old", updated: "new"},
	}
	result := TrimDiffEntries(diffs, 0)

	// Should return as-is when maxTokens <= 0
	assert.Equal(t, 1, len(result), "result length")
}

func TestTrimDiffEntries_FitsWithinLimit(t *testing.T) {
	diffs := []*mockDiffEntry{
		{original: "a", updated: "b"},
		{original: "c", updated: "d"},
	}

	// Each entry is ~2 chars, total ~4 chars = ~2 tokens
	result := TrimDiffEntries(diffs, 100)

	assert.Equal(t, 2, len(result), "result length")
}

func TestTrimDiffEntries_ExceedsLimit(t *testing.T) {
	diffs := []*mockDiffEntry{
		{original: "old1", updated: "new1"},
		{original: "old2", updated: "new2"},
		{original: "old3", updated: "new3"},
		{original: "old4", updated: "new4"},
	}

	// Very small limit - should keep only most recent
	result := TrimDiffEntries(diffs, 5)

	// Should keep only the most recent entries that fit
	assert.Less(t, len(result), 4, "result length")

	// Most recent entry should be last in the result
	if len(result) > 0 {
		assert.Equal(t, "old4", result[len(result)-1].original, "most recent entry")
	}
}

func TestTrimDiffEntries_KeepsMostRecent(t *testing.T) {
	diffs := []*mockDiffEntry{
		{original: "oldest", updated: "oldest_new"},
		{original: "middle", updated: "middle_new"},
		{original: "newest", updated: "newest_new"},
	}

	// Limit that allows only one or two entries
	result := TrimDiffEntries(diffs, 10)

	// Check that newest is included
	found := false
	for _, d := range result {
		if d.original == "newest" {
			found = true
			break
		}
	}
	if len(result) > 0 {
		assert.True(t, found, "newest entry should be included when space allows")
	}
}
