package engine

import (
	"cursortab/assert"
	"cursortab/types"
	"testing"
)

func TestTokenStreamingKeepPartial_TypingMatchesPartial(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"hello wo"} // User typed "wo" which matches partial stream
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Simulate token streaming state with partial result
	eng.state = stateStreamingCompletion
	eng.tokenStreamingState = &TokenStreamingState{
		AccumulatedText: "world",
		LinePrefix:      "hello ",
		LineNum:         1,
	}
	// This would have been set by handleTokenChunk
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"hello world"},
	}}
	eng.completionOriginalLines = []string{"hello "}
	eng.tokenStreamChan = make(chan string) // Non-nil to indicate active stream

	// Trigger text change during streaming
	eng.doRejectStreamingAndDebounce(Event{Type: EventTextChanged})

	// Should transition to HasCompletion state since typing matches
	assert.Equal(t, stateHasCompletion, eng.state, "state after matching typing during streaming")

	// Completions should be preserved
	assert.Greater(t, len(eng.completions), 0, "completions count")

	// Token streaming state should be cleared
	assert.Nil(t, eng.tokenStreamingState, "tokenStreamingState after cancellation")
}

func TestTokenStreamingKeepPartial_TypingDoesNotMatch(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"hello xyz"} // User typed something different
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Simulate token streaming state with partial result
	eng.state = stateStreamingCompletion
	eng.tokenStreamingState = &TokenStreamingState{
		AccumulatedText: "world",
		LinePrefix:      "hello ",
		LineNum:         1,
	}
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"hello world"},
	}}
	eng.completionOriginalLines = []string{"hello "}
	eng.tokenStreamChan = make(chan string)

	// Trigger text change during streaming
	eng.doRejectStreamingAndDebounce(Event{Type: EventTextChanged})

	// Should transition to Idle state since typing doesn't match
	assert.Equal(t, stateIdle, eng.state, "state after mismatching typing during streaming")

	// Completions should be cleared
	assert.Nil(t, eng.completions, "completions after mismatch")

	// ClearUI should have been called
	assert.Greater(t, buf.clearUICalls, 0, "ClearUI should have been called")
}

func TestTokenStreamingKeepPartial_FullyTyped(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"hello world"} // User typed the full completion
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Simulate token streaming state with partial result
	eng.state = stateStreamingCompletion
	eng.tokenStreamingState = &TokenStreamingState{
		AccumulatedText: "world",
		LinePrefix:      "hello ",
		LineNum:         1,
	}
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"hello world"},
	}}
	eng.completionOriginalLines = []string{"hello "}
	eng.tokenStreamChan = make(chan string)

	// Trigger text change during streaming
	eng.doRejectStreamingAndDebounce(Event{Type: EventTextChanged})

	// Should transition to Idle since fully typed
	assert.Equal(t, stateIdle, eng.state, "state after fully typing completion during streaming")
}

func TestLineStreamingReject_NoKeepPartial(t *testing.T) {
	buf := newMockBuffer()
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Simulate line streaming state (NOT token streaming)
	eng.state = stateStreamingCompletion
	eng.streamingState = &StreamingState{} // Line streaming state
	eng.tokenStreamingState = nil          // No token streaming
	eng.streamLinesChan = make(chan string)

	// Trigger text change during streaming
	eng.doRejectStreamingAndDebounce(Event{Type: EventTextChanged})

	// Should transition to Idle (line streaming doesn't keep partial)
	assert.Equal(t, stateIdle, eng.state, "state after rejecting line streaming")
}

func TestCancelTokenStreamingKeepPartial(t *testing.T) {
	buf := newMockBuffer()
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Set up token streaming state
	eng.tokenStreamChan = make(chan string)
	eng.tokenStreamingState = &TokenStreamingState{
		AccumulatedText: "test",
		LineNum:         1,
	}
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"test line"},
	}}
	eng.completionOriginalLines = []string{""}

	// Cancel keeping partial
	eng.cancelTokenStreamingKeepPartial()

	// Token stream channel should be nil
	assert.Nil(t, eng.tokenStreamChan, "tokenStreamChan after cancel")

	// Token streaming state should be nil
	assert.Nil(t, eng.tokenStreamingState, "tokenStreamingState after cancel")

	// But completions should be preserved
	assert.NotNil(t, eng.completions, "completions after cancel")

	// And completionOriginalLines should be preserved
	assert.NotNil(t, eng.completionOriginalLines, "completionOriginalLines after cancel")
}
