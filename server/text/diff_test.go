package text

import (
	"strings"
	"testing"
)

// assertEqual compares two values and reports any differences
func assertEqual(t *testing.T, expected, actual any, label string) {
	t.Helper()
	if expected != actual {
		t.Errorf("Expected %v, got %v for %s", expected, actual, label)
	}
}

// assertDiffResultEqual compares two DiffResult objects and reports any differences
func assertDiffResultEqual(t *testing.T, expected, actual *DiffResult) {
	t.Helper()

	if expected == nil && actual == nil {
		return
	}
	if expected == nil {
		t.Fatalf("Expected nil DiffResult, got %+v", actual)
	}
	if actual == nil {
		t.Fatalf("Expected DiffResult %+v, got nil", expected)
	}

	// Check that all expected changes are present
	for lineNum, expectedChange := range expected.Changes {
		actualChange, exists := actual.Changes[lineNum]
		if !exists {
			t.Errorf("Expected change at line %d but not found: %+v", lineNum, expectedChange)
			continue
		}

		assertLineDiffEqual(t, expectedChange, actualChange)
	}

	// Check that no unexpected changes are present
	for lineNum, actualChange := range actual.Changes {
		if _, exists := expected.Changes[lineNum]; !exists {
			t.Errorf("Unexpected change at line %d: %+v", lineNum, actualChange)
		}
	}

	// Check IsOnlyDeletion flag
	assertEqual(t, expected.IsOnlyLineDeletion, actual.IsOnlyLineDeletion, "IsOnlyLineDeletion")

	// Check Last* properties
	assertEqual(t, expected.LastDeletion, actual.LastDeletion, "LastDeletion")
	assertEqual(t, expected.LastAddition, actual.LastAddition, "LastAddition")
	assertEqual(t, expected.LastLineModification, actual.LastLineModification, "LastLineModification")
	assertEqual(t, expected.LastAppendChars, actual.LastAppendChars, "LastAppendChars")
	assertEqual(t, expected.LastDeleteChars, actual.LastDeleteChars, "LastDeleteChars")
	assertEqual(t, expected.LastReplaceChars, actual.LastReplaceChars, "LastReplaceChars")
	assertEqual(t, expected.CursorLine, actual.CursorLine, "CursorLine")
	assertEqual(t, expected.CursorCol, actual.CursorCol, "CursorCol")
}

// assertLineDiffEqual compares two LineDiff objects
func assertLineDiffEqual(t *testing.T, expected, actual LineDiff) {
	t.Helper()

	assertEqual(t, expected.Type, actual.Type, "Type")
	assertEqual(t, expected.LineNumber, actual.LineNumber, "LineNumber")
	assertEqual(t, expected.Content, actual.Content, "Content")
	assertEqual(t, expected.OldContent, actual.OldContent, "OldContent")
	assertEqual(t, expected.ColStart, actual.ColStart, "ColStart")
	assertEqual(t, expected.ColEnd, actual.ColEnd, "ColEnd")
}

func TestLineDeletion(t *testing.T) {
	text1 := "line 1\nline 2\nline 3\nline 4"
	text2 := "line 1\nline 3\nline 4"

	actual := analyzeDiff(text1, text2)

	expected := &DiffResult{
		Changes: map[int]LineDiff{
			2: {
				Type:       LineDeletion,
				LineNumber: 2,
				Content:    "line 2",
				OldContent: "",
				ColStart:   0,
				ColEnd:     0,
			},
		},
		IsOnlyLineDeletion:   true,
		LastDeletion:         2,
		LastAddition:         -1,
		LastLineModification: -1,
		LastAppendChars:      -1,
		LastDeleteChars:      -1,
		LastReplaceChars:     -1,
		CursorLine:           -1, // No cursor positioning for pure deletions
		CursorCol:            -1,
	}

	assertDiffResultEqual(t, expected, actual)
}

func TestLineAddition(t *testing.T) {
	text1 := "line 1\nline 3\nline 4"
	text2 := "line 1\nline 2\nline 3\nline 4"

	actual := analyzeDiff(text1, text2)

	expected := &DiffResult{
		Changes: map[int]LineDiff{
			2: {
				Type:       LineAddition,
				LineNumber: 2,
				Content:    "line 2",
				OldContent: "",
				ColStart:   0,
				ColEnd:     0,
			},
		},
		IsOnlyLineDeletion:   false,
		LastDeletion:         -1,
		LastAddition:         2,
		LastLineModification: -1,
		LastAppendChars:      -1,
		LastDeleteChars:      -1,
		LastReplaceChars:     -1,
		CursorLine:           2, // Position at last addition
		CursorCol:            6, // End of "line 2"
	}

	assertDiffResultEqual(t, expected, actual)
}

func TestLineAppendChars(t *testing.T) {
	text1 := "Hello world"
	text2 := "Hello world!"

	actual := analyzeDiff(text1, text2)

	expected := &DiffResult{
		Changes: map[int]LineDiff{
			1: {
				Type:       LineAppendChars,
				LineNumber: 1,
				Content:    "Hello world!", // Full new line content
				OldContent: "Hello world",  // Full old line content
				ColStart:   11,
				ColEnd:     12,
			},
		},
		IsOnlyLineDeletion:   false,
		LastDeletion:         -1,
		LastAddition:         -1,
		LastLineModification: -1,
		LastAppendChars:      1,
		LastDeleteChars:      -1,
		LastReplaceChars:     -1,
		CursorLine:           1,  // Position at last append chars
		CursorCol:            12, // End of "Hello world!"
	}

	assertDiffResultEqual(t, expected, actual)
}

func TestLineDeleteChars(t *testing.T) {
	text1 := "Hello world!"
	text2 := "Hello world"

	actual := analyzeDiff(text1, text2)

	expected := &DiffResult{
		Changes: map[int]LineDiff{
			1: {
				Type:       LineDeleteChars,
				LineNumber: 1,
				Content:    "Hello world",  // Full new line content
				OldContent: "Hello world!", // Full old line content
				ColStart:   11,
				ColEnd:     12,
			},
		},
		IsOnlyLineDeletion:   false,
		LastDeletion:         -1,
		LastAddition:         -1,
		LastLineModification: -1,
		LastAppendChars:      -1,
		LastDeleteChars:      1,
		LastReplaceChars:     -1,
		CursorLine:           1,  // Position at last delete chars
		CursorCol:            11, // End of "Hello world"
	}

	assertDiffResultEqual(t, expected, actual)
}

func TestLineDeleteCharsMiddle(t *testing.T) {
	text1 := "Hello world John"
	text2 := "Hello John"

	actual := analyzeDiff(text1, text2)

	expected := &DiffResult{
		Changes: map[int]LineDiff{
			1: {
				Type:       LineDeleteChars,
				LineNumber: 1,
				Content:    "Hello John",       // Full new line content
				OldContent: "Hello world John", // Full old line content
				ColStart:   6,
				ColEnd:     12,
			},
		},
		IsOnlyLineDeletion:   false,
		LastDeletion:         -1,
		LastAddition:         -1,
		LastLineModification: -1,
		LastAppendChars:      -1,
		LastDeleteChars:      1,
		LastReplaceChars:     -1,
		CursorLine:           1,  // Position at last delete chars
		CursorCol:            10, // End of "Hello John"
	}

	assertDiffResultEqual(t, expected, actual)
}

func TestLineReplaceChars(t *testing.T) {
	text1 := "Hello world"
	text2 := "Hello there"

	actual := analyzeDiff(text1, text2)

	expected := &DiffResult{
		Changes: map[int]LineDiff{
			1: {
				Type:       LineReplaceChars,
				LineNumber: 1,
				Content:    "Hello there", // Full new line content
				OldContent: "Hello world", // Full old line content
				ColStart:   6,
				ColEnd:     11,
			},
		},
		IsOnlyLineDeletion:   false,
		LastDeletion:         -1,
		LastAddition:         -1,
		LastLineModification: -1,
		LastAppendChars:      -1,
		LastDeleteChars:      -1,
		LastReplaceChars:     1,
		CursorLine:           1,  // Position at last replace chars
		CursorCol:            11, // End of "Hello there"
	}

	assertDiffResultEqual(t, expected, actual)
}

func TestLineReplaceCharsMiddle(t *testing.T) {
	text1 := "Hello world John"
	text2 := "Hello there John"

	actual := analyzeDiff(text1, text2)

	expected := &DiffResult{
		Changes: map[int]LineDiff{
			1: {
				Type:       LineReplaceChars,
				LineNumber: 1,
				Content:    "Hello there John", // Full new line content
				OldContent: "Hello world John", // Full old line content
				ColStart:   6,
				ColEnd:     11,
			},
		},
		IsOnlyLineDeletion:   false,
		LastDeletion:         -1,
		LastAddition:         -1,
		LastLineModification: -1,
		LastAppendChars:      -1,
		LastDeleteChars:      -1,
		LastReplaceChars:     1,
		CursorLine:           1,  // Position at last replace chars
		CursorCol:            16, // End of "Hello there John"
	}

	assertDiffResultEqual(t, expected, actual)
}

func TestLineModificationAndAddition(t *testing.T) {
	// Simple example with clear line changes
	text1 := `function hello() {
    console.log("old message");
    return true;
}`

	text2 := `function hello() {
    console.log("new message");
    return true;
    console.log("added line");
}`

	actual := analyzeDiff(text1, text2)

	expected := &DiffResult{
		Changes: map[int]LineDiff{
			2: {
				Type:       LineReplaceChars, // "old" -> "new" replacement
				LineNumber: 2,
				Content:    `    console.log("new message");`, // Full new line content
				OldContent: `    console.log("old message");`, // Full old line content
				ColStart:   17,                                // Start of "new"
				ColEnd:     20,                                // End of "new"
			},
			4: {
				Type:       LineAddition,
				LineNumber: 4,
				Content:    `    console.log("added line");`,
				OldContent: "",
				ColStart:   0,
				ColEnd:     0,
			},
		},
		IsOnlyLineDeletion:   false,
		LastDeletion:         -1,
		LastAddition:         4,
		LastLineModification: -1, // Only LineReplaceChars, no LineModification
		LastAppendChars:      -1,
		LastDeleteChars:      -1,
		LastReplaceChars:     2,
		CursorLine:           4,  // Position at last addition (since no line modification)
		CursorCol:            30, // End of "    console.log("added line");"
	}

	assertDiffResultEqual(t, expected, actual)
}

func TestMultipleDeletions(t *testing.T) {
	text1 := "line 1\nline 2\nline 3\nline 4\nline 5"
	text2 := "line 1\nline 3\nline 5"

	actual := analyzeDiff(text1, text2)

	expected := &DiffResult{
		Changes: map[int]LineDiff{
			2: {
				Type:       LineDeletion,
				LineNumber: 2,
				Content:    "line 2",
				OldContent: "",
				ColStart:   0,
				ColEnd:     0,
			},
			4: {
				Type:       LineDeletion,
				LineNumber: 4,
				Content:    "line 4",
				OldContent: "",
				ColStart:   0,
				ColEnd:     0,
			},
		},
		IsOnlyLineDeletion:   true,
		LastDeletion:         4, // Last deletion should be line 4
		LastAddition:         -1,
		LastLineModification: -1,
		LastAppendChars:      -1,
		LastDeleteChars:      -1,
		LastReplaceChars:     -1,
		CursorLine:           -1, // No cursor positioning for pure deletions
		CursorCol:            -1,
	}

	assertDiffResultEqual(t, expected, actual)
}

func TestMultipleAdditions(t *testing.T) {
	text1 := "line 1\nline 3\nline 5"
	text2 := "line 1\nline 2\nline 3\nline 4\nline 5"

	actual := analyzeDiff(text1, text2)

	expected := &DiffResult{
		Changes: map[int]LineDiff{
			2: {
				Type:       LineAddition,
				LineNumber: 2,
				Content:    "line 2",
				OldContent: "",
				ColStart:   0,
				ColEnd:     0,
			},
			4: {
				Type:       LineAddition,
				LineNumber: 4,
				Content:    "line 4",
				OldContent: "",
				ColStart:   0,
				ColEnd:     0,
			},
		},
		IsOnlyLineDeletion:   false,
		LastDeletion:         -1,
		LastAddition:         4, // Last addition should be line 4
		LastLineModification: -1,
		LastAppendChars:      -1,
		LastDeleteChars:      -1,
		LastReplaceChars:     -1,
		CursorLine:           4, // Position at last addition
		CursorCol:            6, // End of "line 4"
	}

	assertDiffResultEqual(t, expected, actual)
}

func TestMultipleCharacterChanges(t *testing.T) {
	text1 := "Hello world\nGoodbye world\nWelcome world"
	text2 := "Hello there\nGoodbye there\nWelcome there"

	actual := analyzeDiff(text1, text2)

	expected := &DiffResult{
		Changes: map[int]LineDiff{
			1: {
				Type:       LineReplaceChars,
				LineNumber: 1,
				Content:    "Hello there",
				OldContent: "Hello world",
				ColStart:   6,
				ColEnd:     11,
			},
			2: {
				Type:       LineReplaceChars,
				LineNumber: 2,
				Content:    "Goodbye there",
				OldContent: "Goodbye world",
				ColStart:   8,
				ColEnd:     13,
			},
			3: {
				Type:       LineReplaceChars,
				LineNumber: 3,
				Content:    "Welcome there",
				OldContent: "Welcome world",
				ColStart:   8,
				ColEnd:     13,
			},
		},
		IsOnlyLineDeletion:   false,
		LastDeletion:         -1,
		LastAddition:         -1,
		LastLineModification: -1, // No line modifications, only replace chars
		LastAppendChars:      -1,
		LastDeleteChars:      -1,
		LastReplaceChars:     3,  // Last replace should be line 3
		CursorLine:           3,  // Position at last replace chars
		CursorCol:            13, // End of "Welcome there"
	}

	assertDiffResultEqual(t, expected, actual)
}

func TestMixedCharacterOperations(t *testing.T) {
	text1 := "Hello world\nGoodbye world!\nWelcome world"
	text2 := "Hello there\nGoodbye world\nWelcome there!"

	actual := analyzeDiff(text1, text2)

	expected := &DiffResult{
		Changes: map[int]LineDiff{
			1: {
				Type:       LineReplaceChars,
				LineNumber: 1,
				Content:    "Hello there",
				OldContent: "Hello world",
				ColStart:   6,
				ColEnd:     11,
			},
			2: {
				Type:       LineDeleteChars,
				LineNumber: 2,
				Content:    "Goodbye world",
				OldContent: "Goodbye world!",
				ColStart:   13,
				ColEnd:     14,
			},
			3: {
				Type:       LineReplaceChars,
				LineNumber: 3,
				Content:    "Welcome there!",
				OldContent: "Welcome world",
				ColStart:   8,
				ColEnd:     14,
			},
		},
		IsOnlyLineDeletion:   false,
		LastDeletion:         -1,
		LastAddition:         -1,
		LastLineModification: -1, // No LineModification, only char-level operations
		LastAppendChars:      -1, // No append operations
		LastDeleteChars:      2,  // Last delete should be line 2
		LastReplaceChars:     3,  // Last replace should be line 3
		CursorLine:           3,  // Position at last replace chars
		CursorCol:            14, // End of "Welcome there!"
	}

	assertDiffResultEqual(t, expected, actual)
}

func TestLineModification(t *testing.T) {
	// Complex changes that result in multiple insertions and deletions
	// This should trigger the default case in categorizeLineChangeWithColumns
	text1 := "start middle end"
	text2 := "beginning middle finish extra"

	actual := analyzeDiff(text1, text2)

	expected := &DiffResult{
		Changes: map[int]LineDiff{
			1: {
				Type:       LineModification,
				LineNumber: 1,
				Content:    "beginning middle finish extra",
				OldContent: "start middle end",
				ColStart:   0,
				ColEnd:     0,
			},
		},
		IsOnlyLineDeletion:   false,
		LastDeletion:         -1,
		LastAddition:         -1,
		LastLineModification: 1, // LineModification should set this
		LastAppendChars:      -1,
		LastDeleteChars:      -1,
		LastReplaceChars:     -1,
		CursorLine:           1,  // Position at last line modification
		CursorCol:            29, // End of "beginning middle finish extra"
	}

	assertDiffResultEqual(t, expected, actual)
}

func TestNoChanges(t *testing.T) {
	text1 := "line 1\nline 2\nline 3"
	text2 := "line 1\nline 2\nline 3"

	actual := analyzeDiff(text1, text2)

	expected := &DiffResult{
		Changes:              map[int]LineDiff{},
		IsOnlyLineDeletion:   false,
		LastDeletion:         -1,
		LastAddition:         -1,
		LastLineModification: -1,
		LastAppendChars:      -1,
		LastDeleteChars:      -1,
		LastReplaceChars:     -1,
		CursorLine:           -1, // No cursor positioning when no changes
		CursorCol:            -1,
	}

	assertDiffResultEqual(t, expected, actual)
}

func TestGenerateCursorDiffFormat(t *testing.T) {
	tests := []struct {
		name     string
		oldText  string
		newText  string
		expected string
	}{
		{
			name:     "Simple line addition",
			oldText:  "line 1\nline 3",
			newText:  "line 1\nline 2\nline 3",
			expected: "2+|line 2\n",
		},
		{
			name:     "Simple line deletion",
			oldText:  "line 1\nline 2\nline 3",
			newText:  "line 1\nline 3",
			expected: "2-|line 2\n",
		},
		{
			name:     "Multiple changes - deletions first",
			oldText:  "line 1\nline 2\nline 3\nline 4",
			newText:  "line 1\nline 5\nline 3\nline 6",
			expected: "2-|line 2\n2+|line 5\n4-|line 4\n4+|line 6\n",
		},
		{
			name:     "Character-level changes",
			oldText:  "Hello world",
			newText:  "Hello there",
			expected: "1-|Hello world\n1+|Hello there\n",
		},
		{
			name:     "Mixed line and character changes",
			oldText:  "function test() {\n    return true;\n}",
			newText:  "function test() {\n    return false;\n    console.log('added');\n}",
			expected: "2-|    return true;\n2+|    return false;\n3+|    console.log('added');\n",
		},
		{
			name:     "Empty old text",
			oldText:  "",
			newText:  "line 1\nline 2",
			expected: "1+|line 1\n2+|line 2\n",
		},
		{
			name:     "Empty new text",
			oldText:  "line 1\nline 2",
			newText:  "",
			expected: "1-|line 1\n2-|line 2\n",
		},
		{
			name:     "No changes",
			oldText:  "line 1\nline 2",
			newText:  "line 1\nline 2",
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := generateCursorDiffFormat(test.oldText, test.newText)
			if actual != test.expected {
				t.Errorf("Expected:\n%q\nGot:\n%q", test.expected, actual)
			}
		})
	}
}

func TestConsecutiveModificationGrouping(t *testing.T) {
	text1 := `function test() {
    start middle end
    start middle end  
    start middle end
}`

	text2 := `function test() {
    beginning middle finish extra
    beginning middle finish extra
    beginning middle finish extra
}`

	actual := analyzeDiff(text1, text2)

	// Should create a modification group for consecutive modifications
	expected := &DiffResult{
		Changes: map[int]LineDiff{
			2: {
				Type:       LineModificationGroup,
				LineNumber: 2,
				Content:    "    beginning middle finish extra\n    beginning middle finish extra\n    beginning middle finish extra",
				GroupLines: []string{
					"    beginning middle finish extra",
					"    beginning middle finish extra",
					"    beginning middle finish extra",
				},
				StartLine: 2,
				EndLine:   4,
				MaxOffset: 20, // Width of "    start middle end"
			},
		},
		IsOnlyLineDeletion:   false,
		LastDeletion:         -1,
		LastAddition:         -1,
		LastLineModification: 2, // Uses the first line of the group
		LastAppendChars:      -1,
		LastDeleteChars:      -1,
		LastReplaceChars:     -1,
		CursorLine:           4,  // End of the group
		CursorCol:            33, // End of "    beginning middle finish extra"
	}

	assertDiffResultEqual(t, expected, actual)
}

func TestConsecutiveAdditionGrouping(t *testing.T) {
	text1 := `function test() {
    return true;
}`

	text2 := `function test() {
    let x = 1;
    let y = 2;
    let z = 3;
    return true;
}`

	actual := analyzeDiff(text1, text2)

	// Should create an addition group for consecutive additions
	expected := &DiffResult{
		Changes: map[int]LineDiff{
			2: {
				Type:       LineAdditionGroup,
				LineNumber: 2,
				Content:    "    let x = 1;\n    let y = 2;\n    let z = 3;",
				GroupLines: []string{"    let x = 1;", "    let y = 2;", "    let z = 3;"},
				StartLine:  2,
				EndLine:    4,
				MaxOffset:  0, // No offset for addition groups
			},
		},
		IsOnlyLineDeletion:   false,
		LastDeletion:         -1,
		LastAddition:         2, // Uses the first line of the group
		LastLineModification: -1,
		LastAppendChars:      -1,
		LastDeleteChars:      -1,
		LastReplaceChars:     -1,
		CursorLine:           4,  // End of the group
		CursorCol:            14, // End of "    let z = 3;"
	}

	assertDiffResultEqual(t, expected, actual)
}

func TestMixedChangesNoGrouping(t *testing.T) {
	text1 := `function test() {
    let x = 1;
    console.log("test");
    let y = 2;
}`

	text2 := `function test() {
    let x = 10;
    console.log("test");
    let y = 20;
}`

	actual := analyzeDiff(text1, text2)

	// Should NOT create groups because they're not consecutive (line 3 unchanged)
	// Lines 2 and 4 are modifications but not consecutive
	if len(actual.Changes) == 0 {
		t.Error("Expected to detect changes")
	}

	// Verify that no group types are present
	for _, change := range actual.Changes {
		if change.Type == LineModificationGroup || change.Type == LineAdditionGroup {
			t.Errorf("Expected no grouping for non-consecutive changes, but found group type: %v", change.Type)
		}
	}
}

func TestLineChangeClassification(t *testing.T) {
	// Test the hypothesis: LineReplaceChars should only be used when there's
	// exactly 1 addition and 1 deletion at the same place

	tests := []struct {
		name     string
		oldLine  string
		newLine  string
		expected DiffType
	}{
		{
			name:     "Simple word replacement - should be replace_chars",
			oldLine:  "Hello world",
			newLine:  "Hello there",
			expected: LineReplaceChars,
		},
		{
			name:     "Multiple changes - should be modification",
			oldLine:  "start middle end",
			newLine:  "beginning middle finish extra",
			expected: LineModification,
		},
		{
			name:     "Single word change - should be replace_chars",
			oldLine:  "let x = 1;",
			newLine:  "let x = 10;",
			expected: LineReplaceChars,
		},
		{
			name:     "Complex restructuring - should be modification",
			oldLine:  "function hello() { return true; }",
			newLine:  "async function hello() { const result = await process(); return result; }",
			expected: LineModification,
		},
		{
			name:     "Append at end - should be append_chars",
			oldLine:  "Hello world",
			newLine:  "Hello world!",
			expected: LineAppendChars,
		},
		{
			name:     "App to server replacement - should be replace_chars",
			oldLine:  `app.route("/health", health);`,
			newLine:  `server.route("/health", health);`,
			expected: LineReplaceChars,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Test single line change classification
			diffType, _, _ := categorizeLineChangeWithColumns(test.oldLine, test.newLine)
			if diffType != test.expected {
				t.Errorf("Expected %v, got %v for change: %q -> %q",
					test.expected, diffType, test.oldLine, test.newLine)
			}
		})
	}
}

// Edge case tests for cursor at top/end of file

func TestEmptyOldText(t *testing.T) {
	// Edge case: starting from empty file
	text1 := ""
	text2 := "line 1\nline 2\nline 3"

	actual := analyzeDiff(text1, text2)

	// Should detect additions for all lines
	if len(actual.Changes) == 0 {
		t.Error("Expected changes when adding content to empty file")
	}

	// Cursor should be positioned
	if actual.CursorLine == -1 {
		t.Error("Expected cursor positioning when adding to empty file")
	}

	t.Logf("Changes detected: %d", len(actual.Changes))
	for lineNum, change := range actual.Changes {
		t.Logf("  Line %d: %s", lineNum, change.Type.String())
	}
}

func TestEmptyNewText(t *testing.T) {
	// Edge case: deleting everything
	text1 := "line 1\nline 2\nline 3"
	text2 := ""

	actual := analyzeDiff(text1, text2)

	// Should detect deletions for all lines
	if len(actual.Changes) == 0 {
		t.Error("Expected changes when deleting all content")
	}

	// Should be only deletions
	if !actual.IsOnlyLineDeletion {
		t.Error("Expected IsOnlyLineDeletion to be true")
	}

	// No cursor positioning for deletions
	if actual.CursorLine != -1 {
		t.Errorf("Expected no cursor positioning for deletions, got line %d", actual.CursorLine)
	}
}

func TestSingleLineFile(t *testing.T) {
	// Edge case: single line file modification
	text1 := "hello"
	text2 := "hello world"

	actual := analyzeDiff(text1, text2)

	expected := &DiffResult{
		Changes: map[int]LineDiff{
			1: {
				Type:       LineAppendChars,
				LineNumber: 1,
				Content:    "hello world",
				OldContent: "hello",
				ColStart:   5,
				ColEnd:     11,
			},
		},
		IsOnlyLineDeletion:   false,
		LastDeletion:         -1,
		LastAddition:         -1,
		LastLineModification: -1,
		LastAppendChars:      1,
		LastDeleteChars:      -1,
		LastReplaceChars:     -1,
		CursorLine:           1,
		CursorCol:            11,
	}

	assertDiffResultEqual(t, expected, actual)
}

func TestAdditionAtFirstLine(t *testing.T) {
	// Edge case: adding content before line 1
	text1 := "line 2\nline 3"
	text2 := "line 1\nline 2\nline 3"

	actual := analyzeDiff(text1, text2)

	// Should have an addition at line 1
	if change, exists := actual.Changes[1]; !exists {
		t.Error("Expected addition at line 1")
	} else if change.Type != LineAddition {
		t.Errorf("Expected addition type, got %s", change.Type.String())
	}

	// Cursor should be positioned at the addition
	if actual.CursorLine != 1 {
		t.Errorf("Expected cursor at line 1, got %d", actual.CursorLine)
	}
}

func TestMultipleAdditionsAtBeginning(t *testing.T) {
	// Edge case: adding multiple lines at the very beginning
	text1 := "original line"
	text2 := "new line 1\nnew line 2\nnew line 3\noriginal line"

	actual := analyzeDiff(text1, text2)

	// Should group consecutive additions
	t.Logf("Changes detected: %d", len(actual.Changes))
	for lineNum, change := range actual.Changes {
		t.Logf("  Line %d: %s - %q", lineNum, change.Type.String(), change.Content)
		if change.Type == LineAdditionGroup {
			t.Logf("    GroupLines: %v", change.GroupLines)
			t.Logf("    StartLine: %d, EndLine: %d", change.StartLine, change.EndLine)
		}
	}

	// Should have additions (either grouped or individual)
	if len(actual.Changes) == 0 {
		t.Error("Expected changes for additions at beginning")
	}

	// Cursor should be positioned
	if actual.CursorLine == -1 {
		t.Error("Expected cursor positioning for additions")
	}
}

func TestModificationAtFirstLine(t *testing.T) {
	// Edge case: modifying line 1
	text1 := "old content\nline 2"
	text2 := "new content here\nline 2"

	actual := analyzeDiff(text1, text2)

	// Should have a modification at line 1
	if change, exists := actual.Changes[1]; !exists {
		t.Error("Expected change at line 1")
	} else {
		t.Logf("Line 1 change type: %s", change.Type.String())
	}

	// Cursor should be positioned at line 1
	if actual.CursorLine != 1 {
		t.Errorf("Expected cursor at line 1, got %d", actual.CursorLine)
	}
}

func TestAdditionAtEndOfFile(t *testing.T) {
	// Edge case: adding lines at the end
	// Note: The diff algorithm may detect line 2 as modified due to trailing newline differences
	// when the old text doesn't have a trailing newline but the new text does
	text1 := "line 1\nline 2\n"
	text2 := "line 1\nline 2\nline 3\nline 4\n"

	actual := analyzeDiff(text1, text2)

	// Should have additions at lines 3 and 4 (possibly grouped)
	t.Logf("Changes detected: %d", len(actual.Changes))
	for lineNum, change := range actual.Changes {
		t.Logf("  Line %d: %s - content: %q", lineNum, change.Type.String(), change.Content)
	}

	// Verify we have additions
	hasAddition := false
	for _, change := range actual.Changes {
		if change.Type == LineAddition || change.Type == LineAdditionGroup {
			hasAddition = true
			break
		}
	}
	if !hasAddition {
		t.Error("Expected at least one addition")
	}

	// Cursor should be positioned at some change
	if actual.CursorLine == -1 {
		t.Error("Expected cursor positioning for additions")
	}
	t.Logf("Cursor position: line=%d, col=%d", actual.CursorLine, actual.CursorCol)
}

func TestDeletionAtFirstLine(t *testing.T) {
	// Edge case: deleting line 1
	text1 := "line 1\nline 2\nline 3"
	text2 := "line 2\nline 3"

	actual := analyzeDiff(text1, text2)

	// Should have a deletion at line 1
	if change, exists := actual.Changes[1]; !exists {
		t.Error("Expected deletion at line 1")
	} else if change.Type != LineDeletion {
		t.Errorf("Expected deletion type, got %s", change.Type.String())
	}

	// Should be only deletion
	if !actual.IsOnlyLineDeletion {
		t.Error("Expected IsOnlyLineDeletion to be true")
	}
}

func TestDeletionAtLastLine(t *testing.T) {
	// Edge case: deleting the last line
	text1 := "line 1\nline 2\nline 3"
	text2 := "line 1\nline 2"

	actual := analyzeDiff(text1, text2)

	// Should have a deletion at line 3
	if change, exists := actual.Changes[3]; !exists {
		t.Error("Expected deletion at line 3")
	} else if change.Type != LineDeletion {
		t.Errorf("Expected deletion type, got %s", change.Type.String())
	}

	// LastDeletion should be 3
	if actual.LastDeletion != 3 {
		t.Errorf("Expected LastDeletion=3, got %d", actual.LastDeletion)
	}
}

func TestCursorPositionBeyondBuffer(t *testing.T) {
	// Edge case: new text longer than old, cursor should be within bounds
	text1 := "a"
	text2 := "a\nb\nc\nd\ne"

	actual := analyzeDiff(text1, text2)

	newLines := strings.Split(text2, "\n")

	// Cursor line should be within the new text bounds
	if actual.CursorLine > len(newLines) {
		t.Errorf("Cursor line %d exceeds new text line count %d", actual.CursorLine, len(newLines))
	}

	// Cursor col should be within the line bounds
	if actual.CursorLine > 0 && actual.CursorLine <= len(newLines) {
		lineContent := newLines[actual.CursorLine-1]
		if actual.CursorCol > len(lineContent) {
			t.Errorf("Cursor col %d exceeds line length %d", actual.CursorCol, len(lineContent))
		}
	}

	t.Logf("Cursor position: line=%d, col=%d", actual.CursorLine, actual.CursorCol)
}

func TestIdenticalLineMarkedAsModification(t *testing.T) {
	// Bug from log: line 11 is marked as "modification" even though content == oldContent
	// This happens when adding a new line at the end of a file
	oldText := `def bubble_sort(arr):
    n = len(arr)
    for i in range(n):
        for j in range(0, n - i - 1):
            if arr[j] > arr[j + 1]:
                arr[j], arr[j + 1] = arr[j + 1], arr[j]
    return arr


if __name__ == "__main__":
    arr = [64, 34, 25, 12, 22, 11, 90]`

	newText := `def bubble_sort(arr):
    n = len(arr)
    for i in range(n):
        for j in range(0, n - i - 1):
            if arr[j] > arr[j + 1]:
                arr[j], arr[j + 1] = arr[j + 1], arr[j]
    return arr


if __name__ == "__main__":
    arr = [64, 34, 25, 12, 22, 11, 90]
    print(bubble_sort(arr))`

	actual := analyzeDiff(oldText, newText)

	// Check that line 11 is NOT in changes (it's identical in both)
	if change, exists := actual.Changes[11]; exists {
		if change.Content == change.OldContent {
			t.Errorf("BUG: Line 11 is marked as %s but content == oldContent (both are %q)",
				change.Type.String(), change.Content)
		}
	}

	// Line 12 should be an addition
	if change, exists := actual.Changes[12]; !exists {
		t.Error("Expected line 12 to be an addition")
	} else if change.Type != LineAddition {
		t.Errorf("Expected line 12 to be addition, got %s", change.Type.String())
	}
}

func TestIfCompletionBug(t *testing.T) {
	// Reproduce bug: typing "if " and getting completion to "if __name__ == "__main__":"
	// The preview was showing "if " as deleted instead of showing the completion
	oldText := `def bubble_sort(arr):
    n = len(arr)
    for i in range(n):
        for j in range(0, n - i - 1):
            if arr[j] > arr[j + 1]:
                arr[j], arr[j + 1] = arr[j + 1], arr[j]
    return arr

if `

	newText := `def bubble_sort(arr):
    n = len(arr)
    for i in range(n):
        for j in range(0, n - i - 1):
            if arr[j] > arr[j + 1]:
                arr[j], arr[j + 1] = arr[j + 1], arr[j]
    return arr

if __name__ == "__main__":
    arr = [64, 34, 25, 12, 22, 11, 90]
    sorted_arr = bubble_sort(arr)
    print(sorted_arr)`

	actual := analyzeDiff(oldText, newText)

	// Line 9 ("if " -> "if __name__ == "__main__":") should be append_chars, not deletion
	change9, exists := actual.Changes[9]
	if !exists {
		t.Fatal("Expected a change at line 9 (the 'if ' line)")
	}
	if change9.Type == LineDeletion {
		t.Error("Line 9 should not be categorized as deletion")
	}
	if change9.Type != LineAppendChars {
		t.Errorf("Expected append_chars, got %s", change9.Type.String())
	}
	if change9.OldContent != "if " {
		t.Errorf("Expected oldContent='if ', got %q", change9.OldContent)
	}
}

func TestSingleLineToMultipleLinesWithSpacesReproduceBug(t *testing.T) {
	// This reproduces the bug where typing 'def test' in a one-line buffer
	// and getting a completion with multiple new lines only shows the first two changes
	oldText := "def test"
	newText := `def test():
    print("test")

test()



`

	actual := analyzeDiff(oldText, newText)

	// This test verifies what changes are detected by the diff algorithm
	// The user reports only seeing modification of "def test" line and addition of print line
	// but not seeing the rest (empty lines and test() call)

	t.Logf("Detected changes:")
	for lineNum, change := range actual.Changes {
		t.Logf("  Line %d: %s - Content: %q", lineNum, change.Type.String(), change.Content)
		if change.Type == LineAdditionGroup || change.Type == LineModificationGroup {
			t.Logf("    Group lines: %v", change.GroupLines)
			t.Logf("    Start: %d, End: %d", change.StartLine, change.EndLine)
		}
	}

	// Let's also test the cursor diff format which might be what the UI uses
	cursorDiff := generateCursorDiffFormat(oldText, newText)
	t.Logf("Cursor diff format:\n%s", cursorDiff)

	// The bug might be in the grouping logic or how empty lines are handled
	// Check if all lines are properly detected
	_ = []string{
		"def test():",
		`    print("test")`,
		"", // empty line
		"test()",
		"", // empty line
		"", // empty line
		"", // empty line
	}

	// Count non-empty lines that should be changes
	expectedNonEmptyChanges := 3 // "def test()", print line, "test()"
	actualNonEmptyChanges := 0
	for _, change := range actual.Changes {
		if change.Type == LineAdditionGroup {
			for _, line := range change.GroupLines {
				if line != "" {
					actualNonEmptyChanges++
				}
			}
		} else if change.Content != "" {
			actualNonEmptyChanges++
		}
	}

	t.Logf("Expected non-empty changes: %d, Actual: %d", expectedNonEmptyChanges, actualNonEmptyChanges)

	// Check if the issue is in line counting
	newLines := strings.Split(newText, "\n")
	t.Logf("New text has %d lines: %v", len(newLines), newLines)

	// The algorithm should detect at least 2 changes: one append_chars and one addition_group
	// The addition_group should contain all the new lines including empty ones
	if len(actual.Changes) < 2 {
		t.Errorf("Bug confirmed: Only detected %d changes but expected at least 2", len(actual.Changes))
	}

	// Verify that empty lines are included in the diff result
	if !strings.Contains(cursorDiff, "3+|") || !strings.Contains(cursorDiff, "5+|") {
		t.Errorf("Empty lines not included in diff result")
	}
}
