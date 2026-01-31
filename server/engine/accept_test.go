package engine

import (
	"cursortab/assert"
	"cursortab/text"
	"cursortab/types"
	"testing"
)

func TestEventTypeFromString_PartialAccept(t *testing.T) {
	got := EventTypeFromString("partial_accept")
	assert.Equal(t, EventPartialAccept, got, "partial_accept event")
}

func TestFindTransition_PartialAccept(t *testing.T) {
	tests := []struct {
		from state
		want bool
	}{
		{stateIdle, false},               // No partial accept from idle
		{statePendingCompletion, false},  // No partial accept while pending
		{stateHasCompletion, true},       // Valid: partial accept a completion
		{stateHasCursorTarget, false},    // No partial accept from cursor target
		{stateStreamingCompletion, true}, // Valid: partial accept during streaming if stage rendered
	}

	for _, tt := range tests {
		trans := findTransition(tt.from, EventPartialAccept)
		got := trans != nil
		assert.Equal(t, tt.want, got, "findTransition for PartialAccept")
	}
}

func TestPartialAccept_AppendChars_SingleWord(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"func"} // Current buffer state
	buf.row = 1
	buf.col = 4
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Set up append_chars completion: "func" -> "function foo()"
	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"function foo()"},
	}}
	eng.completionOriginalLines = []string{"func"}
	// Store groups with append_chars hint
	eng.currentGroups = []*text.Group{{
		Type:       "modification",
		BufferLine: 1,
		RenderHint: "append_chars",
		ColStart:   4,
		Lines:      []string{"function foo()"},
	}}

	// Trigger partial accept
	eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})

	// Should have inserted "tion " (up to and including space)
	assert.Equal(t, "tion ", buf.lastInsertedText, "inserted text")
	// Should still be in HasCompletion state (remaining: "foo()")
	assert.Equal(t, stateHasCompletion, eng.state, "state after partial accept")
}

func TestPartialAccept_AppendChars_Punctuation(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"foo"}
	buf.row = 1
	buf.col = 3
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// "foo" -> "foo.bar.baz"
	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"foo.bar.baz"},
	}}
	eng.completionOriginalLines = []string{"foo"}
	eng.currentGroups = []*text.Group{{
		Type:       "modification",
		BufferLine: 1,
		RenderHint: "append_chars",
		ColStart:   3,
		Lines:      []string{"foo.bar.baz"},
	}}

	eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})

	// First boundary is "." so should accept just "."
	assert.Equal(t, ".", buf.lastInsertedText, "inserted text at punctuation")
	assert.Equal(t, stateHasCompletion, eng.state, "state after partial accept")
}

func TestPartialAccept_AppendChars_NoRemaining(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"hello"}
	buf.row = 1
	buf.col = 5
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// "hello" -> "hello!" - only one char remaining, and it's a punctuation
	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"hello!"},
	}}
	eng.completionOriginalLines = []string{"hello"}
	eng.currentGroups = []*text.Group{{
		Type:       "modification",
		BufferLine: 1,
		RenderHint: "append_chars",
		ColStart:   5,
		Lines:      []string{"hello!"},
	}}

	eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})

	// Should accept "!" and go to idle (nothing remaining)
	assert.Equal(t, "!", buf.lastInsertedText, "inserted text")
	assert.Equal(t, stateIdle, eng.state, "state when nothing remaining")
}

func TestPartialAccept_MultiLine_FirstLine(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"line 1", "line 2", "line 3"}
	buf.row = 1
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Multi-line completion (no append_chars hint)
	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 3,
		Lines:      []string{"new line 1", "new line 2", "new line 3"},
	}}
	eng.completionOriginalLines = []string{"line 1", "line 2", "line 3"}
	eng.currentGroups = []*text.Group{{
		Type:       "modification",
		BufferLine: 1,
		Lines:      []string{"new line 1", "new line 2", "new line 3"},
	}}

	eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})

	// Should apply only first line
	assert.Equal(t, 1, buf.lastReplacedLine, "replaced line number")
	assert.Equal(t, "new line 1", buf.lastReplacedContent, "replaced content")
	// Should still be in HasCompletion (2 lines remaining)
	assert.Equal(t, stateHasCompletion, eng.state, "state after partial line accept")
	// Completion should be updated to remaining lines
	assert.Equal(t, 2, len(eng.completions[0].Lines), "remaining lines")
	assert.Equal(t, 2, eng.completions[0].StartLine, "updated start line")
	assert.Equal(t, 3, eng.completions[0].EndLineInc, "end line unchanged for equal line count")
}

func TestPartialAccept_MultiLine_LastLine(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"old line"}
	buf.row = 1
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Single line completion (no append_chars hint)
	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"new line"},
	}}
	eng.completionOriginalLines = []string{"old line"}
	eng.currentGroups = []*text.Group{{
		Type:       "modification",
		BufferLine: 1,
		Lines:      []string{"new line"},
	}}

	eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})

	// Should apply line and go to idle
	assert.Equal(t, "new line", buf.lastReplacedContent, "replaced content")
	assert.Equal(t, stateIdle, eng.state, "state after accepting last line")
}

func TestPartialAccept_WithUserTyping(t *testing.T) {
	buf := newMockBuffer()
	// User already typed "ti" matching the prediction
	buf.lines = []string{"functi"}
	buf.row = 1
	buf.col = 6
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Original was "func", target is "function foo()"
	// User has typed "functi" (2 extra chars)
	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"function foo()"},
	}}
	eng.completionOriginalLines = []string{"func"}
	eng.currentGroups = []*text.Group{{
		Type:       "modification",
		BufferLine: 1,
		RenderHint: "append_chars",
		ColStart:   4,
		Lines:      []string{"function foo()"},
	}}

	eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})

	// Remaining ghost is "on foo()", should accept "on " (up to space)
	assert.Equal(t, "on ", buf.lastInsertedText, "inserted text after user typing")
}

func TestPartialAccept_NoCompletions(t *testing.T) {
	buf := newMockBuffer()
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	eng.state = stateHasCompletion
	eng.completions = nil // No completions

	// Should not panic, just return
	eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})

	// State should be unchanged
	assert.Equal(t, stateHasCompletion, eng.state, "state unchanged when no completions")
}

func TestPartialAccept_NoGroups(t *testing.T) {
	buf := newMockBuffer()
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"test"},
	}}
	eng.currentGroups = nil // No groups

	// Should not panic, just return
	eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})

	// State should be unchanged
	assert.Equal(t, stateHasCompletion, eng.state, "state unchanged when no groups")
}

func TestPartialAccept_AdditionGroup(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"func main() {", "}"}
	buf.row = 1
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Completion adds a new line in the middle
	// Buffer has 2 lines, completion has 3 lines
	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 2,
		Lines:      []string{"func main() {", "    fmt.Println(\"hello\")", "}"},
	}}
	eng.completionOriginalLines = []string{"func main() {", "}"}
	eng.currentGroups = []*text.Group{{
		Type:       "addition",
		BufferLine: 2,
		StartLine:  2,
		EndLine:    2,
		Lines:      []string{"    fmt.Println(\"hello\")"},
	}}

	// First partial accept - replaces first line
	eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})

	assert.Equal(t, 1, buf.lastReplacedLine, "replaced line number")
	assert.Equal(t, "func main() {", buf.lastReplacedContent, "replaced content")
	assert.Equal(t, stateHasCompletion, eng.state, "state after first partial")
	assert.Equal(t, 2, len(eng.completions[0].Lines), "remaining lines")
	assert.Equal(t, 2, eng.completions[0].StartLine, "updated start line")
	// EndLineInc is recalculated based on remaining lines
	assert.Equal(t, 3, eng.completions[0].EndLineInc, "updated end line for addition")
}

func TestPartialAccept_FinishTriggersPrefetch_ShouldRetrigger(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"hello"}
	buf.row = 1
	buf.col = 5
	prov := newMockProvider()
	clock := newMockClock()
	eng, cancel := createTestEngineWithContext(buf, prov, clock)
	defer cancel()

	// Set up single-line completion that will finish on partial accept
	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"hello!"},
	}}
	eng.completionOriginalLines = []string{"hello"}
	eng.currentGroups = []*text.Group{{
		Type:       "modification",
		BufferLine: 1,
		RenderHint: "append_chars",
		ColStart:   5,
		Lines:      []string{"hello!"},
	}}
	// Set cursor target with ShouldRetrigger=true (auto-advance)
	// This simulates the state when showing a completion with auto-advance enabled
	eng.cursorTarget = &types.CursorPredictionTarget{
		LineNumber:      5,
		ShouldRetrigger: true,
	}
	eng.stagedCompletion = nil // Non-staged completion

	initialSyncCalls := buf.syncCalls

	// Partial accept the last char - should trigger finishPartialAccept
	eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})

	// Should have synced buffer after finishing
	assert.True(t, buf.syncCalls > initialSyncCalls, "buffer should be synced after finish")
	// Should have triggered prefetch for cursor prediction due to ShouldRetrigger
	assert.Equal(t, prefetchWaitingForCursorPrediction, eng.prefetchState, "prefetch should be waiting for cursor prediction")
}

func TestPartialAccept_FinishTriggersPrefetch_N1Stage(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"line 1", "line 2", "line 3"}
	buf.row = 1
	prov := newMockProvider()
	clock := newMockClock()
	eng, cancel := createTestEngineWithContext(buf, prov, clock)
	defer cancel()

	// Set up staged completion with 2 stages, currently at stage 0
	// After finishing partial accept, we'll be at stage 1 (n-1 where n=2)
	// This is a simpler setup to test the n-1 prefetch logic
	stage0Groups := []*text.Group{{
		Type:       "modification",
		BufferLine: 1,
		Lines:      []string{"new line 1"},
	}}

	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"new line 1"},
	}}
	eng.completionOriginalLines = []string{"line 1"}
	eng.currentGroups = stage0Groups

	// Set cursorTarget to stage 1's position (needed for handleCursorTarget)
	eng.cursorTarget = &types.CursorPredictionTarget{
		LineNumber:      2,
		ShouldRetrigger: false,
	}

	// Create staged completion with 2 stages
	eng.stagedCompletion = &types.StagedCompletion{
		CurrentIdx: 0,
		Stages: []any{
			&text.Stage{
				BufferStart: 1,
				BufferEnd:   1,
				Lines:       []string{"new line 1"},
				Groups:      stage0Groups,
				CursorTarget: &types.CursorPredictionTarget{
					LineNumber:      2,
					ShouldRetrigger: false,
				},
			},
			&text.Stage{
				BufferStart:  2,
				BufferEnd:    2,
				Lines:        []string{"new line 2"},
				Groups:       []*text.Group{{Type: "modification", BufferLine: 2, Lines: []string{"new line 2"}}},
				CursorTarget: &types.CursorPredictionTarget{
					LineNumber:      3,
					ShouldRetrigger: true, // Last stage wants retrigger
				},
			},
		},
	}

	// Partial accept completes the first stage (line-by-line, single line)
	eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})

	// Should still have stagedCompletion (not cleared)
	assert.NotNil(t, eng.stagedCompletion, "stagedCompletion should not be nil")
	// Now at stage 1, which is n-1 (last stage)
	assert.Equal(t, 1, eng.stagedCompletion.CurrentIdx, "should be at stage 1")

	// Should have triggered prefetch for cursor prediction at n-1 stage because last stage has ShouldRetrigger=true
	assert.Equal(t, prefetchWaitingForCursorPrediction, eng.prefetchState, "prefetch should be waiting for cursor prediction at n-1 stage")
}

func TestPartialAccept_FinishSyncsBuffer_NonStaged(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"test"}
	buf.row = 1
	buf.col = 4
	prov := newMockProvider()
	clock := newMockClock()
	eng := createTestEngine(buf, prov, clock)

	// Set up single-line completion (non-staged)
	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"test!"},
	}}
	eng.completionOriginalLines = []string{"test"}
	eng.currentGroups = []*text.Group{{
		Type:       "modification",
		BufferLine: 1,
		RenderHint: "append_chars",
		ColStart:   4,
		Lines:      []string{"test!"},
	}}
	eng.stagedCompletion = nil // Explicitly non-staged
	eng.cursorTarget = nil     // No cursor target

	initialSyncCalls := buf.syncCalls

	// Partial accept finishes the completion
	eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})

	// Should have synced buffer after finishing (non-staged path)
	assert.True(t, buf.syncCalls > initialSyncCalls, "buffer should be synced after finish")
	// Should be idle (no cursor target, no more stages)
	assert.Equal(t, stateIdle, eng.state, "should be idle after finish")
}

func TestAcceptCompletion_TriggersPrefetch_ShouldRetrigger(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"hello"}
	buf.row = 1
	buf.col = 5
	prov := newMockProvider()
	clock := newMockClock()
	eng, cancel := createTestEngineWithContext(buf, prov, clock)
	defer cancel()

	// Set up completion with cursor target that has ShouldRetrigger=true
	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"hello world"},
	}}
	eng.applyBatch = &mockBatch{}
	eng.stagedCompletion = nil // Non-staged completion
	// Cursor target with ShouldRetrigger=true (auto-advance)
	eng.cursorTarget = &types.CursorPredictionTarget{
		LineNumber:      5,
		ShouldRetrigger: true,
	}

	// Accept the completion via Accept event
	eng.doAcceptCompletion(Event{Type: EventAccept})

	// Should have triggered prefetch for cursor prediction due to ShouldRetrigger
	assert.Equal(t, prefetchWaitingForCursorPrediction, eng.prefetchState, "prefetch should be waiting for cursor prediction after accept")
}

func TestPrefetchReady_DoesNotInterruptActiveCompletion(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"line 1", "line 2", "line 3", "line 4", "line 5"}
	buf.row = 2
	buf.col = 0
	prov := newMockProvider()
	clock := newMockClock()
	eng, cancel := createTestEngineWithContext(buf, prov, clock)
	defer cancel()

	// Set up engine with an active completion being shown
	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  2,
		EndLineInc: 2,
		Lines:      []string{"modified line 2"},
	}}
	eng.applyBatch = &mockBatch{}

	// Simulate prefetch that was waiting for cursor prediction
	eng.prefetchState = prefetchWaitingForCursorPrediction
	eng.prefetchedCompletions = []*types.Completion{{
		StartLine:  4,
		EndLineInc: 4,
		Lines:      []string{"modified line 4"},
	}}

	// Record initial state
	initialShowCursorTargetCalls := buf.showCursorTargetLine

	// Simulate prefetch ready event - this should NOT interrupt the active completion
	eng.handlePrefetchReady(&types.CompletionResponse{
		Completions: eng.prefetchedCompletions,
	})

	// State should remain HasCompletion - not changed to HasCursorTarget
	assert.Equal(t, stateHasCompletion, eng.state, "state should remain HasCompletion, not interrupted by cursor prediction")

	// Should NOT have shown a new cursor target
	assert.Equal(t, initialShowCursorTargetCalls, buf.showCursorTargetLine, "should not show cursor target while completion is active")

	// Prefetch state should be ready (the prefetch completed)
	assert.Equal(t, prefetchReady, eng.prefetchState, "prefetch state should be ready")
}

func TestAcceptLastStage_UsesPrefetchForCursorPrediction(t *testing.T) {
	// This test reproduces a bug where accepting the last stage of a staged
	// completion goes to Idle instead of showing cursor prediction from a
	// ready prefetch.
	//
	// Scenario:
	// - User has staged completion with 2 stages (line 10 and line 15)
	// - Prefetch was started when advancing to last stage
	// - Prefetch completes with additional change at line 25
	// - User accepts line 15 (last stage)
	// - Expected: cursor prediction shown to line 25
	// - Bug: goes to Idle because prefetch is ignored

	buf := newMockBuffer()
	buf.lines = []string{
		"line 1",  // 1
		"line 2",  // 2
		"line 3",  // 3
		"line 4",  // 4
		"line 5",  // 5
		"line 6",  // 6
		"line 7",  // 7
		"line 8",  // 8
		"line 9",  // 9
		"old 10",  // 10 - stage 1 change
		"line 11", // 11
		"line 12", // 12
		"line 13", // 13
		"line 14", // 14
		"old 15",  // 15 - stage 2 (last stage) change
		"line 16", // 16
		"line 17", // 17
		"line 18", // 18
		"line 19", // 19
		"line 20", // 20
		"line 21", // 21
		"line 22", // 22
		"line 23", // 23
		"line 24", // 24
		"old 25",  // 25 - prefetch has this change
	}
	buf.row = 15 // Cursor at line 15 (last stage)
	buf.col = 0
	buf.viewportTop = 1
	buf.viewportBottom = 30
	prov := newMockProvider()
	clock := newMockClock()
	eng, cancel := createTestEngineWithContext(buf, prov, clock)
	defer cancel()

	// Set up engine showing the last stage (line 15)
	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  15,
		EndLineInc: 15,
		Lines:      []string{"new 15"},
	}}
	eng.applyBatch = &mockBatch{}

	// Create staged completion - currently at stage 1 (last stage)
	// Stage 0 was already accepted (line 10)
	eng.stagedCompletion = &types.StagedCompletion{
		CurrentIdx: 1, // At last stage
		Stages: []any{
			&text.Stage{
				BufferStart: 10,
				BufferEnd:   10,
				Lines:       []string{"new 10"},
				CursorTarget: &types.CursorPredictionTarget{
					LineNumber:      15,
					ShouldRetrigger: false,
				},
			},
			&text.Stage{
				BufferStart: 15,
				BufferEnd:   15,
				Lines:       []string{"new 15"},
				Groups: []*text.Group{{
					Type:       "modification",
					BufferLine: 15,
					Lines:      []string{"new 15"},
				}},
				CursorTarget: &types.CursorPredictionTarget{
					LineNumber:      15, // Points to self (last stage)
					ShouldRetrigger: true,
				},
				IsLastStage: true,
			},
		},
	}

	// Set cursor target from current stage
	eng.cursorTarget = &types.CursorPredictionTarget{
		LineNumber:      15,
		ShouldRetrigger: true,
	}

	// Simulate prefetch that completed with additional change at line 25
	// The prefetch shows the buffer state AFTER accepting line 15
	eng.prefetchState = prefetchReady
	eng.prefetchedCompletions = []*types.Completion{{
		StartLine:  25,
		EndLineInc: 25,
		Lines:      []string{"new 25"},
	}}

	// Accept the last stage
	eng.doAcceptCompletion(Event{Type: EventAccept})

	// The bug: engine goes to Idle instead of showing cursor prediction
	// Expected: should show cursor prediction to line 25 from prefetch
	assert.Equal(t, stateHasCursorTarget, eng.state, "should be HasCursorTarget showing prediction to line 25")
	assert.Equal(t, 25, buf.showCursorTargetLine, "should show cursor target at line 25")
}
