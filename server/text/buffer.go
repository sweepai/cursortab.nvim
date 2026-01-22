package text

import (
	"cursortab/logger"
	"cursortab/types"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/neovim/go-client/nvim"
)

type BufferConfig struct {
	NsID int
}

type Buffer struct {
	Lines         []string
	Row           int // 1-indexed
	Col           int // 0-indexed
	Path          string
	Version       int
	DiffHistories []string

	originalLines    []string // Original file content when editing session started
	lastModifiedLine int      // Track which line was last modified
	id               nvim.Buffer
	scrollOffsetX    int // Horizontal scroll offset (leftcol)

	config BufferConfig

	// Pending completion state (committed only on accept)
	pendingStartLine        int
	pendingEndLineInclusive int
	pendingLines            []string
	hasPending              bool
}

func NewBuffer(config BufferConfig) (*Buffer, error) {
	return &Buffer{
		Lines:                   []string{},
		Row:                     1,
		Col:                     0,
		Path:                    "",
		Version:                 0,
		DiffHistories:           []string{},
		originalLines:           []string{},
		lastModifiedLine:        -1,
		id:                      nvim.Buffer(0),
		scrollOffsetX:           0,
		config:                  config,
		pendingStartLine:        0,
		pendingEndLineInclusive: 0,
		pendingLines:            nil,
		hasPending:              false,
	}, nil
}

func (b *Buffer) MoveCursorToStartOfLine(n *nvim.Nvim, line int, center bool, mark bool) error {
	batch := n.NewBatch()
	applyCursorMove(batch, line, 0, center, mark)
	batch.ExecLua("vim.cmd('normal! ^')", nil, nil) // Move cursor to start of line
	return batch.Execute()
}

func applyCursorMove(batch *nvim.Batch, line, col int, center bool, mark bool) {
	if mark {
		batch.ExecLua("vim.cmd('normal! m\\'')", nil, nil)
	}
	batch.SetWindowCursor(0, [2]int{line, col})
	if center {
		batch.ExecLua("vim.cmd('normal! zz')", nil, nil)
	}
}

func (b *Buffer) executeLuaFunction(n *nvim.Nvim, luaCode string, args ...any) {
	batch := n.NewBatch()
	if len(args) > 0 {
		batch.ExecLua(luaCode, nil, args...)
	} else {
		batch.ExecLua(luaCode, nil, nil)
	}
	if err := batch.Execute(); err != nil {
		log.Printf("error executing lua function: %v", err)
	}
}

func (b *Buffer) SyncIn(n *nvim.Nvim, workspacePath string) {
	// Use batch API to make all calls in a single round-trip
	batch := n.NewBatch()

	var currentBuf nvim.Buffer
	var path string
	var lines [][]byte
	var window nvim.Window
	var cursor [2]int
	var scrollOffset int

	batch.CurrentBuffer(&currentBuf)
	batch.BufferName(nvim.Buffer(0), &path) // Use 0 for current buffer
	batch.BufferLines(nvim.Buffer(0), 0, -1, false, &lines)
	batch.CurrentWindow(&window)
	batch.WindowCursor(nvim.Window(0), &cursor) // Use 0 for current window

	// Get horizontal scroll offset (leftcol) from current window
	batch.ExecLua(`
		local view = vim.fn.winsaveview()
		return view.leftcol or 0
	`, &scrollOffset, nil)

	if err := batch.Execute(); err != nil {
		log.Printf("error executing sync batch: %v", err)
		return
	}

	linesStr := make([]string, len(lines))
	for i, line := range lines {
		linesStr[i] = string(line[:])
	}

	// Update buffer state
	b.Lines = linesStr
	b.Row = cursor[0]              // Line (vertical position, 1-based in nvim cursor)
	b.Col = cursor[1]              // Column (horizontal position, 0-based in nvim cursor)
	b.scrollOffsetX = scrollOffset // Horizontal scroll offset

	// Convert absolute path to relative workspace path
	relativePath := makeRelativeToWorkspace(path, workspacePath)
	b.Path = relativePath

	// Handle buffer change
	if b.id != currentBuf {
		// New buffer - capture original state
		b.id = currentBuf
		b.originalLines = make([]string, len(linesStr))
		copy(b.originalLines, linesStr)
		b.DiffHistories = []string{}
		b.lastModifiedLine = -1
		b.Version = 0
	} else {
		// Existing buffer - keep current diff histories; no action on sync
	}
}

// Helper function to convert absolute path to relative workspace path
func makeRelativeToWorkspace(absolutePath, workspacePath string) string {
	absolutePath = filepath.Clean(absolutePath)
	workspacePath = filepath.Clean(workspacePath)

	// If the file is within the workspace, make it relative
	if relativePath, found := strings.CutPrefix(absolutePath, workspacePath); found {
		relativePath = strings.TrimPrefix(relativePath, string(filepath.Separator))
		return relativePath
	}

	return absolutePath
}

func (b *Buffer) getDiffResult(startLine, endLineInclusive int, lines []string) *DiffResult {
	originalLines := []string{}
	for i := startLine; i <= endLineInclusive && i-1 < len(b.Lines); i++ {
		originalLines = append(originalLines, b.Lines[i-1])
	}
	oldText := strings.Join(originalLines, "\n")
	newText := strings.Join(lines, "\n")
	return analyzeDiff(oldText, newText)
}

func (b *Buffer) getApplyBatch(n *nvim.Nvim, startLine, endLineInclusive int, lines []string, diffResult *DiffResult) *nvim.Batch {
	// Create apply batch for the completion
	applyBatch := n.NewBatch()

	b.clearNamespace(applyBatch, b.config.NsID)

	placeBytes := make([][]byte, len(lines))
	for i, line := range lines {
		placeBytes[i] = []byte(line)
	}

	// execute lua to actually clear the lines within the range beforehand
	applyBatch.ExecLua(fmt.Sprintf("vim.cmd('normal! %v,%vd')", startLine, endLineInclusive), nil, nil)

	applyBatch.SetBufferLines(b.id, startLine-1, endLineInclusive, false, placeBytes)

	// Apply cursor positioning from diff result
	if diffResult.CursorLine >= 0 && diffResult.CursorCol >= 0 {
		// Convert from diff line numbers (relative to new text) to buffer line numbers
		bufferLine := startLine + diffResult.CursorLine - 1
		applyCursorMove(applyBatch, bufferLine, diffResult.CursorCol, false, true)
	}

	// Mark as pending; actual commit happens on accept
	b.pendingStartLine = startLine
	b.pendingEndLineInclusive = endLineInclusive
	b.pendingLines = append([]string{}, lines...)
	b.hasPending = true

	return applyBatch
}

func (b *Buffer) OnCompletionReady(n *nvim.Nvim, startLine, endLineInclusive int, lines []string) *nvim.Batch {
	diffResult := b.getDiffResult(startLine, endLineInclusive, lines)
	applyBatch := b.getApplyBatch(n, startLine, endLineInclusive, lines, diffResult)

	// Convert diffResult to Lua format with additional context fields
	luaDiffResult := diffResult.ToLuaFormat(
		"startLine", startLine,
		"endLineInclusive", endLineInclusive,
	)

	// Debug logging for data sent to Lua
	if jsonData, err := json.Marshal(luaDiffResult); err == nil {
		logger.Debug("Sending to Lua on_completion_ready:\n  startLine: %d\n  endLineInclusive: %d\n  lines: %d\n  diffResult: %s",
			startLine, endLineInclusive, len(lines), string(jsonData))
	}

	b.executeLuaFunction(n, "require('cursortab').on_completion_ready(...)", luaDiffResult)

	return applyBatch
}

// CommitPendingEdit applies the pending edit to buffer state, increments version,
// and appends a cumulative diff entry (from original baseline to current). No-op if no pending edit.
func (b *Buffer) CommitPendingEdit() {
	if !b.hasPending {
		return
	}

	startLine := b.pendingStartLine
	endLineInclusive := b.pendingEndLineInclusive
	lines := b.pendingLines

	// Compute cumulative diff from original baseline to post-apply contents
	oldText := strings.Join(b.originalLines, "\n")
	newLines := make([]string, 0, len(b.Lines)-((endLineInclusive-startLine)+1)+len(lines))
	if startLine-1 > 0 && startLine-1 <= len(b.Lines) {
		newLines = append(newLines, b.Lines[:startLine-1]...)
	}
	newLines = append(newLines, lines...)
	if endLineInclusive < len(b.Lines) {
		newLines = append(newLines, b.Lines[endLineInclusive:]...)
	}
	newText := strings.Join(newLines, "\n")
	diff := generateCursorDiffFormat(oldText, newText)
	if diff != "" {
		b.DiffHistories = append(b.DiffHistories, diff)
	}

	// Commit the new content and bump version
	b.Lines = make([]string, len(newLines))
	copy(b.Lines, newLines)
	b.Version++

	// Clear pending
	b.pendingStartLine = 0
	b.pendingEndLineInclusive = 0
	b.pendingLines = nil
	b.hasPending = false
}

func (b *Buffer) OnCursorPredictionReady(n *nvim.Nvim, line int) {
	logger.Debug("Sending to Lua on_cursor_prediction_ready: line=%d", line)
	b.executeLuaFunction(n, "require('cursortab').on_cursor_prediction_ready(...)", line)
}

func (b *Buffer) OnReject(n *nvim.Nvim) {
	logger.Debug("Sending to Lua on_reject")
	b.executeLuaFunction(n, `require('cursortab').on_reject()`)
}

func (b *Buffer) clearNamespace(batch *nvim.Batch, nsID int) {
	batch.ClearBufferNamespace(b.id, nsID, 0, -1)
}

// hasChanges checks if the proposed completion would introduce actual changes
func (b *Buffer) HasChanges(startLine, endLineInclusive int, lines []string) bool {
	// Check the original replacement range for changes
	for i := startLine; i <= endLineInclusive; i++ {
		relativeLineIdx := i - startLine

		var l *string
		var realL *string

		if i-1 >= 0 && i-1 < len(b.Lines) {
			realL = &b.Lines[i-1]
		}

		if relativeLineIdx < len(lines) {
			l = &lines[relativeLineIdx]
		}

		if (l != nil && realL != nil && *l != *realL) ||
			(l != nil && realL == nil) ||
			(l == nil && realL != nil) {
			return true
		}
	}

	// Check if there are additional lines beyond the replacement range (insertions)
	if startLine+len(lines)-1 > endLineInclusive {
		return true
	}

	return false
}

// GetLinterErrors retrieves Neovim diagnostics for the current buffer and returns them in a simple format
func (b *Buffer) GetLinterErrors(n *nvim.Nvim) *LinterErrors {
	// First check if LSP is available for this buffer
	batch := n.NewBatch()
	var hasLsp bool

	// Check if there are any active LSP clients attached to this buffer
	batch.ExecLua(fmt.Sprintf(`
		local clients = vim.lsp.get_clients and vim.lsp.get_clients({bufnr = %d}) or vim.lsp.get_active_clients({bufnr = %d})
		return #clients > 0
	`, int(b.id), int(b.id)), &hasLsp, nil)

	if err := batch.Execute(); err != nil {
		log.Printf("error checking LSP availability: %v", err)
		return nil
	}

	// If no LSP is available, return nil
	if !hasLsp {
		return nil
	}

	// Use batch API to get diagnostics for the current buffer
	batch = n.NewBatch()

	var diagnostics []map[string]any
	var bufferContents [][]byte

	// Get diagnostics using vim.diagnostic.get() for this specific buffer
	batch.ExecLua(fmt.Sprintf(`
		local diagnostics = vim.diagnostic.get(%d)
		return diagnostics
	`, int(b.id)), &diagnostics, nil)

	// Get buffer contents for the linter errors
	batch.BufferLines(b.id, 0, -1, false, &bufferContents)

	if err := batch.Execute(); err != nil {
		log.Printf("error getting linter errors: %v", err)
		return nil
	}

	if len(diagnostics) == 0 {
		return nil
	}

	// Convert buffer contents to string
	contentLines := make([]string, len(bufferContents))
	for i, line := range bufferContents {
		contentLines[i] = string(line)
	}
	fileContents := strings.Join(contentLines, "\n")

	// Convert Neovim diagnostics to LinterError format
	linterErrors := make([]*LinterError, 0, len(diagnostics))

	for _, diag := range diagnostics {
		linterError := &LinterError{
			Message: getString(diag, "message"),
			Source:  getString(diag, "source"),
		}

		// Convert severity
		if severity, ok := diag["severity"].(float64); ok {
			switch int(severity) {
			case 1:
				linterError.Severity = "DIAGNOSTIC_SEVERITY_ERROR"
			case 2:
				linterError.Severity = "DIAGNOSTIC_SEVERITY_WARNING"
			case 3:
				linterError.Severity = "DIAGNOSTIC_SEVERITY_INFORMATION"
			case 4:
				linterError.Severity = "DIAGNOSTIC_SEVERITY_HINT"
			default:
				linterError.Severity = "DIAGNOSTIC_SEVERITY_ERROR"
			}
		} else {
			linterError.Severity = "DIAGNOSTIC_SEVERITY_ERROR"
		}

		// Convert range information
		if lnum := getNumber(diag, "lnum"); lnum != -1 {
			if col := getNumber(diag, "col"); col != -1 {
				endLnum := lnum
				endCol := col

				if endL := getNumber(diag, "end_lnum"); endL != -1 {
					endLnum = endL
				}
				if endC := getNumber(diag, "end_col"); endC != -1 {
					endCol = endC
				}

				linterError.Range = &CursorRange{
					StartLine:      lnum,
					StartCharacter: col,
					EndLine:        endLnum,
					EndCharacter:   endCol,
				}
			}
		}

		linterErrors = append(linterErrors, linterError)
	}

	return &LinterErrors{
		RelativeWorkspacePath: b.Path,
		Errors:                linterErrors,
		FileContents:          fileContents,
	}
}

// GetProviderLinterErrors retrieves Neovim diagnostics and converts them to provider.LinterErrors format
func (b *Buffer) GetProviderLinterErrors(n *nvim.Nvim) *types.LinterErrors {
	linterErrors := b.GetLinterErrors(n)

	if linterErrors == nil {
		return nil
	}

	// Convert to provider format
	providerErrors := make([]*types.LinterError, len(linterErrors.Errors))
	for i, err := range linterErrors.Errors {
		var providerRange *types.CursorRange
		if err.Range != nil {
			providerRange = &types.CursorRange{
				StartLine:      err.Range.StartLine,
				StartCharacter: err.Range.StartCharacter,
				EndLine:        err.Range.EndLine,
				EndCharacter:   err.Range.EndCharacter,
			}
		}

		providerErrors[i] = &types.LinterError{
			Message:  err.Message,
			Source:   err.Source,
			Severity: err.Severity,
			Range:    providerRange,
		}
	}

	return &types.LinterErrors{
		RelativeWorkspacePath: linterErrors.RelativeWorkspacePath,
		Errors:                providerErrors,
		FileContents:          linterErrors.FileContents,
	}
}

// Helper types for internal linter error handling
type LinterErrors struct {
	RelativeWorkspacePath string
	Errors                []*LinterError
	FileContents          string
}

type LinterError struct {
	Message  string
	Source   string
	Severity string
	Range    *CursorRange
}

type CursorRange struct {
	StartLine      int
	StartCharacter int
	EndLine        int
	EndCharacter   int
}

// Helper function to safely get string from map
func getString(m map[string]any, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

// Helper function to safely get number from map, handling both int and float64
func getNumber(m map[string]any, key string) int {
	if val, ok := m[key].(int); ok {
		return val
	}
	if val, ok := m[key].(float64); ok {
		return int(val)
	}
	if val, ok := m[key].(int32); ok {
		return int(val)
	}
	if val, ok := m[key].(int64); ok {
		return int(val)
	}
	return -1
}
