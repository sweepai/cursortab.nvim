package engine

import (
	"cursortab/assert"
	"testing"
)

func TestCopyLines(t *testing.T) {
	original := []string{"a", "b", "c"}
	copied := copyLines(original)

	assert.Equal(t, len(original), len(copied), "copied length")

	// Modify original
	original[0] = "modified"

	assert.NotEqual(t, original[0], copied[0], "copyLines should create a deep copy")
}

func TestCopyLines_Nil(t *testing.T) {
	copied := copyLines(nil)
	assert.Nil(t, copied, "copyLines(nil)")
}

func TestIsFileStateValid(t *testing.T) {
	buf := newMockBuffer()
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	tests := []struct {
		name         string
		state        *FileState
		currentLines []string
		want         bool
	}{
		{
			name:         "empty original lines",
			state:        &FileState{OriginalLines: []string{}},
			currentLines: []string{"a", "b"},
			want:         false,
		},
		{
			name:         "same content",
			state:        &FileState{OriginalLines: []string{"a", "b", "c"}},
			currentLines: []string{"a", "b", "c"},
			want:         true,
		},
		{
			name:         "minor difference",
			state:        &FileState{OriginalLines: []string{"a", "b", "c"}},
			currentLines: []string{"a", "b", "c", "d"},
			want:         true,
		},
		{
			name:         "major line count difference",
			state:        &FileState{OriginalLines: []string{"a", "b", "c"}},
			currentLines: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n"},
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := eng.isFileStateValid(tt.state, tt.currentLines)
			assert.Equal(t, tt.want, got, "isFileStateValid")
		})
	}
}

func TestTrimFileStateStore(t *testing.T) {
	buf := newMockBuffer()
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Add 5 file states
	for i := 0; i < 5; i++ {
		eng.fileStateStore[string(rune('a'+i))+".go"] = &FileState{
			LastAccessNs: int64(i * 1000),
		}
	}

	eng.trimFileStateStore(2)

	assert.Equal(t, 2, len(eng.fileStateStore), "file state store size")

	// Should keep the most recently accessed (highest LastAccessNs)
	_, existsD := eng.fileStateStore["d.go"]
	assert.True(t, existsD, "should keep d.go (second most recent)")
	_, existsE := eng.fileStateStore["e.go"]
	assert.True(t, existsE, "should keep e.go (most recent)")
}
