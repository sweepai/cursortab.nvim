package engine

import (
	"cursortab/assert"
	"cursortab/text"
	"cursortab/types"
	"testing"
)

// TestPartialAccept_MultiLineCompletion_CursorTargetConsistency tests that cursor targets
// remain consistent when using partial accept vs full accept on the same multi-line completion.
// Regression test for bug where partial accept would offset the cursor target by the number
// of partial accepts performed.
func TestPartialAccept_MultiLineCompletion_CursorTargetConsistency(t *testing.T) {
	t.Run("full_accept_preserves_cursor_target", func(t *testing.T) {
		buf := newMockBuffer()
		buf.lines = []string{"old line 1", "old line 2", "old line 3", "old line 4"}
		buf.row = 1
		prov := newMockProvider()
		clock := newMockClock()
		eng, cancel := createTestEngineWithContext(buf, prov, clock)
		defer cancel()

		// Set up 4-line completion
		eng.state = stateHasCompletion
		eng.completions = []*types.Completion{{
			StartLine:  1,
			EndLineInc: 4,
			Lines:      []string{"new line 1", "new line 2", "new line 3", "new line 4"},
		}}
		eng.completionOriginalLines = buf.lines
		eng.currentGroups = []*text.Group{
			{Type: "modification", BufferLine: 1},
			{Type: "modification", BufferLine: 2},
			{Type: "modification", BufferLine: 3},
			{Type: "modification", BufferLine: 4},
		}

		// Set cursor target for this completion
		expectedCursorTarget := int32(8)
		eng.cursorTarget = &types.CursorPredictionTarget{
			RelativePath:    "test.go",
			LineNumber:      expectedCursorTarget,
			ShouldRetrigger: true,
		}
		eng.applyBatch = &mockBatch{}
		eng.stagedCompletion = nil

		// Full accept all lines at once
		eng.doAcceptCompletion(Event{Type: EventTab})

		assert.Equal(t, int(expectedCursorTarget), buf.showCursorTargetLine, "cursor target should be preserved after full accept")
	})

	t.Run("partial_accept_4_lines_one_by_one_same_target", func(t *testing.T) {
		buf := newMockBuffer()
		buf.lines = []string{"old line 1", "old line 2", "old line 3", "old line 4"}
		buf.row = 1
		prov := newMockProvider()
		clock := newMockClock()
		eng, cancel := createTestEngineWithContext(buf, prov, clock)
		defer cancel()

		// Set up 4-line completion (same as full accept test)
		eng.state = stateHasCompletion
		eng.completions = []*types.Completion{{
			StartLine:  1,
			EndLineInc: 4,
			Lines:      []string{"new line 1", "new line 2", "new line 3", "new line 4"},
		}}
		eng.completionOriginalLines = buf.lines
		eng.currentGroups = []*text.Group{
			{Type: "modification", BufferLine: 1},
			{Type: "modification", BufferLine: 2},
			{Type: "modification", BufferLine: 3},
			{Type: "modification", BufferLine: 4},
		}

		// Set cursor target (should stay consistent through partial accepts)
		expectedCursorTarget := int32(8)
		eng.cursorTarget = &types.CursorPredictionTarget{
			RelativePath:    "test.go",
			LineNumber:      expectedCursorTarget,
			ShouldRetrigger: true,
		}
		eng.stagedCompletion = nil

		// Partial accept line 1
		eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})
		assert.Equal(t, stateHasCompletion, eng.state, "should stay in HasCompletion after partial accept")
		assert.Equal(t, 3, len(eng.completions[0].Lines), "remaining lines")
		assert.Equal(t, 2, eng.completions[0].StartLine, "start line increments")

		// Partial accept line 2
		eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})
		assert.Equal(t, 2, len(eng.completions[0].Lines), "remaining lines")
		assert.Equal(t, 3, eng.completions[0].StartLine, "start line increments")

		// Partial accept line 3
		eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})
		assert.Equal(t, 1, len(eng.completions[0].Lines), "remaining lines")
		assert.Equal(t, 4, eng.completions[0].StartLine, "start line increments")

		// Partial accept line 4 (final)
		eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})

		// Cursor target should be same as full accept case
		assert.Equal(t, int(expectedCursorTarget), buf.showCursorTargetLine, "cursor target should be preserved through partial accepts")
	})

	t.Run("partial_accept_cursor_target_consistency_through_all_accepts", func(t *testing.T) {
		// Verify cursor target doesn't change during partial accepts
		buf := newMockBuffer()
		buf.lines = []string{"x", "y", "z", "w"}
		buf.row = 1
		prov := newMockProvider()
		clock := newMockClock()
		eng, cancel := createTestEngineWithContext(buf, prov, clock)
		defer cancel()

		cursorTarget := int32(12)
		eng.state = stateHasCompletion
		eng.completions = []*types.Completion{{
			StartLine:  1,
			EndLineInc: 4,
			Lines:      []string{"X", "Y", "Z", "W"},
		}}
		eng.completionOriginalLines = buf.lines
		eng.currentGroups = []*text.Group{
			{Type: "modification", BufferLine: 1},
			{Type: "modification", BufferLine: 2},
			{Type: "modification", BufferLine: 3},
			{Type: "modification", BufferLine: 4},
		}
		eng.cursorTarget = &types.CursorPredictionTarget{
			RelativePath:    "test.go",
			LineNumber:      cursorTarget,
			ShouldRetrigger: false,
		}
		eng.stagedCompletion = nil

		// Do 3 partial accepts
		for i := 0; i < 3; i++ {
			eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})
			// Cursor target should not be modified during partial accepts
			if i < 2 {
				assert.Equal(t, cursorTarget, eng.cursorTarget.LineNumber, "cursor target should be unchanged")
			}
		}

		// Final partial accept
		eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})

		// Cursor target shown should match the original
		assert.Equal(t, int(cursorTarget), buf.showCursorTargetLine, "final cursor target should be original value")
	})

	t.Run("partial_accept_with_staged_completion", func(t *testing.T) {
		// Test with staged completion where cursor target comes from stage
		buf := newMockBuffer()
		buf.lines = []string{"a", "b", "c", "d", "e", "f"}
		buf.row = 1
		prov := newMockProvider()
		clock := newMockClock()
		eng, cancel := createTestEngineWithContext(buf, prov, clock)
		defer cancel()

		// Create a staged completion with 2 stages
		// Create stages to test the cursor target issue through processCompletion
		// Stage 1: lines 1-2, cursor target points to stage 2 (line 3)
		// Stage 2: lines 3-4, cursor target points to end (line 5)
		stage1 := &text.Stage{
			BufferStart:   1,
			BufferEnd:     2,
			Lines:         []string{"A", "B"},
			Groups:        []*text.Group{{Type: "modification", BufferLine: 1}},
			CursorLine:    1,
			CursorCol:     0,
			IsLastStage:   false,
			CursorTarget: &types.CursorPredictionTarget{
				LineNumber:      3, // Points to next stage
				ShouldRetrigger: false,
			},
		}

		stage2 := &text.Stage{
			BufferStart:   3,
			BufferEnd:     4,
			Lines:         []string{"C", "D"},
			Groups:        []*text.Group{{Type: "modification", BufferLine: 3}},
			CursorLine:    1,
			CursorCol:     0,
			IsLastStage:   true,
			CursorTarget: &types.CursorPredictionTarget{
				LineNumber:      5, // Points to end
				ShouldRetrigger: true,
			},
		}

		// Set up staged completion
		eng.state = stateHasCompletion
		eng.completions = []*types.Completion{{
			StartLine:  1,
			EndLineInc: 2,
			Lines:      []string{"A", "B"},
		}}
		eng.completionOriginalLines = []string{"a", "b"}
		eng.currentGroups = []*text.Group{{Type: "modification", BufferLine: 1}}
		eng.stagedCompletion = &types.StagedCompletion{
			Stages: []any{stage1, stage2},
			CurrentIdx: 0,
		}
		eng.applyBatch = &mockBatch{}
		eng.cursorTarget = stage1.CursorTarget

		// First partial accept on stage 1
		eng.doPartialAcceptCompletion(Event{Type: EventPartialAccept})

		// Cursor target should remain from stage 1 (not jump to stage 2)
		assert.Equal(t, int32(3), eng.cursorTarget.LineNumber, "cursor target should be preserved from stage 1")
	})
}
