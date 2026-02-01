package engine

import (
	"cursortab/assert"
	"cursortab/text"
	"cursortab/types"
	"testing"
)

func TestAcceptCompletion_TriggersPrefetch_ShouldRetrigger(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{"hello"}
	buf.row = 1
	buf.col = 5
	prov := newMockProvider()
	clock := newMockClock()
	eng, cancel := createTestEngineWithContext(buf, prov, clock)
	defer cancel()

	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  1,
		EndLineInc: 1,
		Lines:      []string{"hello world"},
	}}
	eng.applyBatch = &mockBatch{}
	eng.stagedCompletion = nil
	eng.cursorTarget = &types.CursorPredictionTarget{
		LineNumber:      5,
		ShouldRetrigger: true,
	}

	eng.doAcceptCompletion(Event{Type: EventAccept})

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

	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  2,
		EndLineInc: 2,
		Lines:      []string{"modified line 2"},
	}}
	eng.applyBatch = &mockBatch{}

	eng.prefetchState = prefetchWaitingForCursorPrediction
	eng.prefetchedCompletions = []*types.Completion{{
		StartLine:  4,
		EndLineInc: 4,
		Lines:      []string{"modified line 4"},
	}}

	initialShowCursorTargetCalls := buf.showCursorTargetLine

	eng.handlePrefetchReady(&types.CompletionResponse{
		Completions: eng.prefetchedCompletions,
	})

	assert.Equal(t, stateHasCompletion, eng.state, "state should remain HasCompletion, not interrupted by cursor prediction")
	assert.Equal(t, initialShowCursorTargetCalls, buf.showCursorTargetLine, "should not show cursor target while completion is active")
	assert.Equal(t, prefetchReady, eng.prefetchState, "prefetch state should be ready")
}

func TestAcceptLastStage_UsesPrefetchForCursorPrediction(t *testing.T) {
	buf := newMockBuffer()
	buf.lines = []string{
		"line 1", "line 2", "line 3", "line 4", "line 5",
		"line 6", "line 7", "line 8", "line 9", "old 10",
		"line 11", "line 12", "line 13", "line 14", "old 15",
		"line 16", "line 17", "line 18", "line 19", "line 20",
		"line 21", "line 22", "line 23", "line 24", "old 25",
	}
	buf.row = 15
	buf.col = 0
	buf.viewportTop = 1
	buf.viewportBottom = 30
	prov := newMockProvider()
	clock := newMockClock()
	eng, cancel := createTestEngineWithContext(buf, prov, clock)
	defer cancel()

	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  15,
		EndLineInc: 15,
		Lines:      []string{"new 15"},
	}}
	eng.applyBatch = &mockBatch{}

	eng.stagedCompletion = &types.StagedCompletion{
		CurrentIdx: 1,
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
					LineNumber:      15,
					ShouldRetrigger: true,
				},
				IsLastStage: true,
			},
		},
	}

	eng.cursorTarget = &types.CursorPredictionTarget{
		LineNumber:      15,
		ShouldRetrigger: true,
	}

	eng.prefetchState = prefetchReady
	eng.prefetchedCompletions = []*types.Completion{{
		StartLine:  25,
		EndLineInc: 25,
		Lines:      []string{"new 25"},
	}}

	eng.doAcceptCompletion(Event{Type: EventAccept})

	assert.Equal(t, stateHasCursorTarget, eng.state, "should be HasCursorTarget showing prediction to line 25")
	assert.Equal(t, 25, buf.showCursorTargetLine, "should show cursor target at line 25")
}

func TestAcceptLastStage_ClearsStalePrefetch_WhenOverlaps(t *testing.T) {
	// Test that prefetch is cleared when it overlaps with the stage just applied.
	// This prevents showing the same content twice after accepting a stage.
	buf := newMockBuffer()
	buf.lines = []string{
		"line 1", "line 2", "line 3", "line 4", "line 5",
		"line 6", "line 7", "line 8", "line 9", "old 10",
		"line 11", "line 12", "line 13", "line 14", "old 15",
	}
	buf.row = 15
	buf.col = 0
	buf.viewportTop = 1
	buf.viewportBottom = 20
	prov := newMockProvider()
	clock := newMockClock()
	eng, cancel := createTestEngineWithContext(buf, prov, clock)
	defer cancel()

	eng.state = stateHasCompletion
	eng.completions = []*types.Completion{{
		StartLine:  15,
		EndLineInc: 15,
		Lines:      []string{"new 15"},
	}}
	eng.applyBatch = &mockBatch{}

	eng.stagedCompletion = &types.StagedCompletion{
		CurrentIdx: 1,
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
					LineNumber:      15,
					ShouldRetrigger: true,
				},
				IsLastStage: true,
			},
		},
	}

	eng.cursorTarget = &types.CursorPredictionTarget{
		LineNumber:      15,
		ShouldRetrigger: true,
	}

	// Prefetch is for line 15 - same as the stage being applied (overlaps)
	eng.prefetchState = prefetchReady
	eng.prefetchedCompletions = []*types.Completion{{
		StartLine:  15,
		EndLineInc: 15,
		Lines:      []string{"new 15"},
	}}

	eng.doAcceptCompletion(Event{Type: EventAccept})

	// Stale prefetch should be cleared because it overlaps with the applied stage.
	// Then a new prefetch is requested (since ShouldRetrigger=true).
	assert.Equal(t, prefetchWaitingForCursorPrediction, eng.prefetchState, "new prefetch should be requested after clearing stale")
	assert.Nil(t, eng.prefetchedCompletions, "stale prefetched completions should be nil")
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
	eng.cursorTarget = &types.CursorPredictionTarget{
		LineNumber:      5,
		ShouldRetrigger: true,
	}
	eng.stagedCompletion = nil

	initialSyncCalls := buf.syncCalls

	eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})

	assert.True(t, buf.syncCalls > initialSyncCalls, "buffer should be synced after finish")
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

	eng.cursorTarget = &types.CursorPredictionTarget{
		LineNumber:      2,
		ShouldRetrigger: false,
	}

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
				BufferStart: 2,
				BufferEnd:   2,
				Lines:       []string{"new line 2"},
				Groups:      []*text.Group{{Type: "modification", BufferLine: 2, Lines: []string{"new line 2"}}},
				CursorTarget: &types.CursorPredictionTarget{
					LineNumber:      3,
					ShouldRetrigger: true,
				},
			},
		},
	}

	eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})

	assert.NotNil(t, eng.stagedCompletion, "stagedCompletion should not be nil")
	assert.Equal(t, 1, eng.stagedCompletion.CurrentIdx, "should be at stage 1")
	assert.Equal(t, prefetchWaitingForCursorPrediction, eng.prefetchState, "prefetch should be waiting for cursor prediction at n-1 stage")
}
