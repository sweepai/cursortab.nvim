package engine

import (
	"cursortab/assert"
	"cursortab/types"
	"testing"
)

func TestEngineCreation(t *testing.T) {
	buf := newMockBuffer()
	prov := newMockProvider()
	clock := newMockClock()

	eng := createTestEngine(buf, prov, clock)

	assert.NotNil(t, eng, "NewEngine")
	assert.Equal(t, stateIdle, eng.state, "initial state")
}

func TestStateString(t *testing.T) {
	tests := []struct {
		state state
		want  string
	}{
		{stateIdle, "Idle"},
		{statePendingCompletion, "PendingCompletion"},
		{stateHasCompletion, "HasCompletion"},
		{stateHasCursorTarget, "HasCursorTarget"},
		{stateStreamingCompletion, "StreamingCompletion"},
		{state(99), "Unknown"},
	}

	for _, tt := range tests {
		got := tt.state.String()
		assert.Equal(t, tt.want, got, "state String")
	}
}

func TestFindTransition(t *testing.T) {
	tests := []struct {
		from  state
		event EventType
		want  bool // whether a transition should exist
	}{
		{stateIdle, EventTextChangeTimeout, true},
		{stateIdle, EventIdleTimeout, true},
		{stateIdle, EventTextChanged, true},
		{stateIdle, EventAccept, false}, // No Accept handler from Idle
		{statePendingCompletion, EventTextChanged, true},
		{statePendingCompletion, EventEsc, true},
		{stateHasCompletion, EventAccept, true},
		{stateHasCompletion, EventEsc, true},
		{stateHasCompletion, EventTextChanged, true},
		{stateHasCursorTarget, EventAccept, true},
		{stateStreamingCompletion, EventTextChanged, true},
		{stateStreamingCompletion, EventEsc, true},
	}

	for _, tt := range tests {
		trans := findTransition(tt.from, tt.event)
		got := trans != nil
		assert.Equal(t, tt.want, got, "findTransition")
	}
}

func TestEventTypeFromString(t *testing.T) {
	tests := []struct {
		input string
		want  EventType
	}{
		{"esc", EventEsc},
		{"text_changed", EventTextChanged},
		{"accept", EventAccept},
		{"insert_enter", EventInsertEnter},
		{"insert_leave", EventInsertLeave},
		{"unknown_event", ""},
	}

	for _, tt := range tests {
		got := EventTypeFromString(tt.input)
		assert.Equal(t, tt.want, got, "EventTypeFromString")
	}
}

func TestCheckTypingMatchesPrediction_NoCompletions(t *testing.T) {
	buf := newMockBuffer()
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// No completions set
	matches, hasRemaining := eng.checkTypingMatchesPrediction()
	assert.False(t, matches, "matches when no completions")
	assert.False(t, hasRemaining, "hasRemaining when no completions")
}

func TestCheckTypingMatchesPrediction_MatchesPrefix(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"hello wo"}
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Set up completion state
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"hello world"},
	}}
	eng.completionOriginalLines = []string{"hello "}

	matches, hasRemaining := eng.checkTypingMatchesPrediction()
	assert.True(t, matches, "match when buffer is prefix of target")
	assert.True(t, hasRemaining, "hasRemaining when buffer hasn't fully matched target")
}

func TestCheckTypingMatchesPrediction_FullyTyped(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"hello world"}
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Set up completion state - user typed everything
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"hello world"},
	}}
	eng.completionOriginalLines = []string{"hello "}

	matches, hasRemaining := eng.checkTypingMatchesPrediction()
	assert.True(t, matches, "match when buffer matches target")
	assert.False(t, hasRemaining, "hasRemaining when buffer fully matches target")
}

func TestCheckTypingMatchesPrediction_NoMatch(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"hello universe"}
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Set up completion state - user typed something different
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"hello world"},
	}}
	eng.completionOriginalLines = []string{"hello "}

	matches, _ := eng.checkTypingMatchesPrediction()
	assert.False(t, matches, "match when buffer diverges from target")
}

func TestCheckTypingMatchesPrediction_MultiLine(t *testing.T) {
	buf := newMockBuffer()
	// User typed "co" on line 2, which is a prefix of "complete"
	buf.lines = []string{"line 1", "line 2 co"}
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Multi-line completion
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 2,
		Lines:      []string{"line 1", "line 2 complete"},
	}}
	eng.completionOriginalLines = []string{"line 1", "line 2 "}

	matches, hasRemaining := eng.checkTypingMatchesPrediction()
	assert.True(t, matches, "match for multi-line partial completion")
	assert.True(t, hasRemaining, "hasRemaining for multi-line partial completion")
}

func TestCheckTypingMatchesPrediction_DeletionNotSupported(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"line 1"}
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Completion that deletes lines (fewer target lines than original)
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 2,
		Lines:      []string{"combined line"}, // 1 line instead of 2
	}}
	eng.completionOriginalLines = []string{"line 1", "line 2"} // 2 original lines

	matches, _ := eng.checkTypingMatchesPrediction()
	assert.False(t, matches, "match when completion deletes lines")
}

func TestReject(t *testing.T) {
	buf := newMockBuffer()
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Set up some state
	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{StartLine: 1, EndLineInc: 1, Lines: []string{"test"}}}
	eng.cursorTarget = &types.CursorPredictionTarget{LineNumber: 5}

	eng.reject()

	assert.Equal(t, stateIdle, eng.state, "state after reject")
	assert.Nil(t, eng.completions, "completions after reject")
	assert.Nil(t, eng.cursorTarget, "cursorTarget after reject")
	assert.Greater(t, buf.clearUICalls, 0, "ClearUI should have been called")
}

func TestClearState_Options(t *testing.T) {
	buf := newMockBuffer()
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Set up state
	eng.completions = []*types.Completion{{StartLine: 1, EndLineInc: 1, Lines: []string{"test"}}}
	eng.stagedCompletion = &types.StagedCompletion{CurrentIdx: 0}
	eng.cursorTarget = &types.CursorPredictionTarget{LineNumber: 5}

	// Clear with ClearStaged=false should preserve stagedCompletion
	eng.clearState(ClearOptions{
		ClearStaged:       false,
		ClearCursorTarget: true,
		CallOnReject:      true,
	})

	if eng.stagedCompletion == nil {
		assert.NotNil(t, eng.stagedCompletion, "stagedCompletion should be preserved when ClearStaged=false")
	}
	assert.Nil(t, eng.cursorTarget, "cursorTarget should be cleared when ClearCursorTarget=true")
	assert.Nil(t, eng.completions, "completions should always be cleared")
}

func TestAbsFunction(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{5, 5},
		{-5, 5},
		{0, 0},
		{-100, 100},
	}

	for _, tt := range tests {
		got := abs(tt.input)
		assert.Equal(t, tt.want, got, "abs")
	}
}

func TestDispatch_ValidTransition(t *testing.T) {
	buf := newMockBuffer()
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	eng.state = stateIdle

	result := eng.dispatch(Event{Type: EventTextChanged})

	assert.True(t, result, "dispatch valid transition")
}

func TestDispatch_InvalidTransition(t *testing.T) {
	buf := newMockBuffer()
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	eng.state = stateIdle

	// Accept from Idle has no transition defined
	result := eng.dispatch(Event{Type: EventAccept})

	assert.False(t, result, "dispatch invalid transition")
}

func TestHandleCursorTarget_Disabled(t *testing.T) {
	buf := newMockBuffer()
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)
	eng.config.CursorPrediction.Enabled = false

	eng.cursorTarget = &types.CursorPredictionTarget{LineNumber: 10}
	eng.state = stateHasCursorTarget

	eng.handleCursorTarget()

	// Should transition to idle when cursor prediction disabled
	assert.Equal(t, stateIdle, eng.state, "state when cursor prediction disabled")
	// cursorTarget should be cleared
	assert.Nil(t, eng.cursorTarget, "cursorTarget when cursor prediction disabled")
}

func TestHandleCursorTarget_CloseEnough(t *testing.T) {
	buf := newMockBuffer()
	buf.row = 8
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)
	eng.config.CursorPrediction.Enabled = true
	eng.config.CursorPrediction.ProximityThreshold = 3

	// Cursor target is only 2 lines away (within threshold)
	eng.cursorTarget = &types.CursorPredictionTarget{LineNumber: 10}

	eng.handleCursorTarget()

	// Should go idle (no prediction shown when close)
	assert.Equal(t, stateIdle, eng.state, "state when close enough")
}

func TestHandleCursorTarget_FarAway(t *testing.T) {
	buf := newMockBuffer()
	buf.row = 1
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)
	eng.config.CursorPrediction.Enabled = true
	eng.config.CursorPrediction.ProximityThreshold = 3

	// Cursor target is 9 lines away (beyond threshold)
	eng.cursorTarget = &types.CursorPredictionTarget{LineNumber: 10}

	eng.handleCursorTarget()

	// Should show cursor target
	assert.Equal(t, stateHasCursorTarget, eng.state, "state when far away")
	assert.Equal(t, 10, buf.showCursorTargetLine, "showCursorTargetLine")
}
