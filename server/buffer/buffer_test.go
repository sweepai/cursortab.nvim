package buffer

import (
	"cursortab/assert"
	"cursortab/types"
	"testing"
)

func TestCommitUserEdits_NoChanges(t *testing.T) {
	buf := New(Config{NsID: 1})
	buf.lines = []string{"line 1", "line 2"}
	buf.originalLines = []string{"line 1", "line 2"}

	result := buf.CommitUserEdits()

	assert.False(t, result, "should return false when no changes")
	assert.Equal(t, 0, len(buf.diffHistories), "no diffs committed")
}

func TestCommitUserEdits_WithChanges(t *testing.T) {
	buf := New(Config{NsID: 1})
	buf.lines = []string{"line 1", "modified line 2"}
	buf.originalLines = []string{"line 1", "line 2"}

	result := buf.CommitUserEdits()

	assert.True(t, result, "should return true when changes exist")
	assert.True(t, len(buf.diffHistories) > 0, "diffs committed")
	// Check checkpoint reset
	assert.Equal(t, buf.lines[1], buf.originalLines[1], "originalLines reset to current")
}

func TestCommitUserEdits_PreventsDuplicates(t *testing.T) {
	buf := New(Config{NsID: 1})
	buf.lines = []string{"line 1", "modified"}
	buf.originalLines = []string{"line 1", "original"}

	// First commit
	result1 := buf.CommitUserEdits()
	assert.True(t, result1, "first commit should succeed")
	diffCount := len(buf.diffHistories)

	// Second commit with no new changes
	result2 := buf.CommitUserEdits()
	assert.False(t, result2, "second commit should return false")
	assert.Equal(t, diffCount, len(buf.diffHistories), "no new diffs added")
}

func TestCommitUserEdits_LineCountChange(t *testing.T) {
	buf := New(Config{NsID: 1})
	buf.lines = []string{"line 1", "line 2", "line 3"} // Added a line
	buf.originalLines = []string{"line 1", "line 2"}

	result := buf.CommitUserEdits()

	assert.True(t, result, "should detect line count changes")
	assert.True(t, len(buf.diffHistories) > 0, "diffs committed")
}

func TestCommitUserEdits_UpdatesPreviousLines(t *testing.T) {
	buf := New(Config{NsID: 1})
	buf.lines = []string{"new content"}
	buf.originalLines = []string{"old content"}

	buf.CommitUserEdits()

	// previousLines should be set to what originalLines was BEFORE the commit
	assert.Equal(t, "old content", buf.previousLines[0], "previousLines set to old checkpoint")
}

// --- HasChanges Tests ---

func TestHasChanges_NoChanges(t *testing.T) {
	buf := New(Config{NsID: 1})
	buf.lines = []string{"line 1", "line 2", "line 3"}

	// Replacing with identical content
	result := buf.HasChanges(1, 3, []string{"line 1", "line 2", "line 3"})

	assert.False(t, result, "should return false for identical content")
}

func TestHasChanges_ContentDiffers(t *testing.T) {
	buf := New(Config{NsID: 1})
	buf.lines = []string{"line 1", "line 2", "line 3"}

	// Replacing with different content
	result := buf.HasChanges(1, 3, []string{"line 1", "modified", "line 3"})

	assert.True(t, result, "should return true when content differs")
}

func TestHasChanges_LineCountIncrease(t *testing.T) {
	buf := New(Config{NsID: 1})
	buf.lines = []string{"line 1", "line 2"}

	// Replacing 2 lines with 3 lines
	result := buf.HasChanges(1, 2, []string{"line 1", "line 2", "new line"})

	assert.True(t, result, "should return true when line count increases")
}

func TestHasChanges_LineCountDecrease(t *testing.T) {
	buf := New(Config{NsID: 1})
	buf.lines = []string{"line 1", "line 2", "line 3"}

	// Replacing 3 lines with 2 lines
	result := buf.HasChanges(1, 3, []string{"line 1", "line 2"})

	assert.True(t, result, "should return true when line count decreases")
}

func TestHasChanges_PartialRange(t *testing.T) {
	buf := New(Config{NsID: 1})
	buf.lines = []string{"line 1", "line 2", "line 3", "line 4"}

	// Replacing only middle lines
	result := buf.HasChanges(2, 3, []string{"modified 2", "modified 3"})

	assert.True(t, result, "should return true when middle lines change")
}

// --- SetFileContext Tests ---

func TestSetFileContext_WithAllValues(t *testing.T) {
	buf := New(Config{NsID: 1})
	prev := []string{"prev 1", "prev 2"}
	orig := []string{"orig 1", "orig 2"}
	diffs := []*types.DiffEntry{{Original: "a", Updated: "b"}}

	buf.SetFileContext(prev, orig, diffs)

	assert.Equal(t, 2, len(buf.previousLines), "previousLines set")
	assert.Equal(t, 2, len(buf.originalLines), "originalLines set")
	assert.Equal(t, 1, len(buf.diffHistories), "diffHistories set")
}

func TestSetFileContext_NilPrevWithOrig(t *testing.T) {
	buf := New(Config{NsID: 1})
	orig := []string{"orig 1", "orig 2"}

	// When prev is nil but orig is provided, previousLines should be set to orig
	buf.SetFileContext(nil, orig, nil)

	assert.Equal(t, 2, len(buf.previousLines), "previousLines set from orig")
	assert.Equal(t, "orig 1", buf.previousLines[0], "previousLines matches orig")
}

func TestSetFileContext_AllNil(t *testing.T) {
	buf := New(Config{NsID: 1})
	buf.previousLines = []string{"should be cleared"}

	buf.SetFileContext(nil, nil, nil)

	assert.True(t, buf.previousLines == nil, "previousLines should be nil")
	assert.Equal(t, 0, len(buf.diffHistories), "diffHistories empty")
}

// --- extractGranularDiffs Tests ---

func TestExtractGranularDiffs_NoChanges(t *testing.T) {
	oldLines := []string{"line 1", "line 2"}
	newLines := []string{"line 1", "line 2"}

	result := extractGranularDiffs(oldLines, newLines)

	assert.True(t, len(result) == 0 || result == nil, "no diffs for identical content")
}

func TestExtractGranularDiffs_Modification(t *testing.T) {
	oldLines := []string{"line 1", "original"}
	newLines := []string{"line 1", "modified"}

	result := extractGranularDiffs(oldLines, newLines)

	assert.True(t, len(result) > 0, "should have diffs")
	// Should capture the change from "original" to "modified"
	found := false
	for _, diff := range result {
		if diff.Original == "original" && diff.Updated == "modified" {
			found = true
			break
		}
	}
	assert.True(t, found, "should capture modification")
}

func TestExtractGranularDiffs_Addition(t *testing.T) {
	oldLines := []string{"line 1"}
	newLines := []string{"line 1", "new line"}

	result := extractGranularDiffs(oldLines, newLines)

	assert.True(t, len(result) > 0, "should have diffs for addition")
}

func TestExtractGranularDiffs_Deletion(t *testing.T) {
	oldLines := []string{"line 1", "line 2"}
	newLines := []string{"line 1"}

	result := extractGranularDiffs(oldLines, newLines)

	assert.True(t, len(result) > 0, "should have diffs for deletion")
}

// --- Helper Function Tests ---

func TestMakeRelativeToWorkspace(t *testing.T) {
	tests := []struct {
		name          string
		absolutePath  string
		workspacePath string
		want          string
	}{
		{
			name:          "file in workspace",
			absolutePath:  "/home/user/project/src/main.go",
			workspacePath: "/home/user/project",
			want:          "src/main.go",
		},
		{
			name:          "file outside workspace",
			absolutePath:  "/other/path/file.go",
			workspacePath: "/home/user/project",
			want:          "/other/path/file.go",
		},
		{
			name:          "file at workspace root",
			absolutePath:  "/home/user/project/main.go",
			workspacePath: "/home/user/project",
			want:          "main.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makeRelativeToWorkspace(tt.absolutePath, tt.workspacePath)
			assert.Equal(t, tt.want, got, "relative path mismatch")
		})
	}
}

func TestGetString(t *testing.T) {
	m := map[string]any{
		"key1": "value1",
		"key2": 123, // not a string
	}

	assert.Equal(t, "value1", getString(m, "key1"), "should get string value")
	assert.Equal(t, "", getString(m, "key2"), "should return empty for non-string")
	assert.Equal(t, "", getString(m, "missing"), "should return empty for missing key")
}

func TestGetNumber(t *testing.T) {
	m := map[string]any{
		"int":     42,
		"float64": float64(3.14),
		"int32":   int32(100),
		"int64":   int64(1000),
		"string":  "not a number",
	}

	assert.Equal(t, 42, getNumber(m, "int"), "should get int")
	assert.Equal(t, 3, getNumber(m, "float64"), "should convert float64")
	assert.Equal(t, 100, getNumber(m, "int32"), "should convert int32")
	assert.Equal(t, 1000, getNumber(m, "int64"), "should convert int64")
	assert.Equal(t, -1, getNumber(m, "string"), "should return -1 for non-number")
	assert.Equal(t, -1, getNumber(m, "missing"), "should return -1 for missing key")
}
