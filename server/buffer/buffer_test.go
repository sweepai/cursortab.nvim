package buffer

import (
	"cursortab/assert"
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
