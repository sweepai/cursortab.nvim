// Package sweepapi implements the Sweep hosted API provider.
//
// Sends a JSON request to the Sweep autocomplete endpoint:
//
//	{
//	  "repo_name":          "my-project",
//	  "file_path":          "src/main.go",
//	  "file_contents":      "...full file text...",
//	  "cursor_position":    1234,                    // byte offset
//	  "recent_changes":     "File: main.go:\n<<<<<<< ORIGINAL\n...\n=======\n...\n>>>>>>> UPDATED\n",
//	  "file_chunks":        [...recent buffer snapshots as FileChunk...],
//	  "recent_user_actions": [...user edit actions...],
//	  "retrieval_chunks": [
//	    {"file_path": "diagnostics",         "content": "Line 10: [gopls] undefined: foo\n", ...},
//	    {"file_path": "treesitter_context",  "content": "Language: go\nEnclosing scope: ...\n", ...},
//	    {"file_path": "staged_git_diff",     "content": "<full diff or +/-symbol lines>", ...}
//	  ]
//	}
//
// The response contains byte-range edits (start_index, end_index, completion text)
// that are converted to line-based completions.
package sweepapi

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"cursortab/client/sweepapi"
	"cursortab/engine"
	"cursortab/logger"
	"cursortab/metrics"
	"cursortab/types"
)

func (p *Provider) truncateContext(lines []string, cursorRow, cursorCol int) ([]string, int, int, int) {
	maxLines := p.limits.MaxInputLines
	maxBytes := p.limits.MaxInputBytes

	if len(lines) == 0 {
		return lines, cursorRow, cursorCol, 0
	}

	if len(lines) <= maxLines {
		totalBytes := 0
		for _, line := range lines {
			totalBytes += len(line) + 1
		}
		if totalBytes <= maxBytes {
			return lines, cursorRow, cursorCol, 0
		}
	}

	if cursorRow < 1 {
		cursorRow = 1
	}
	if cursorRow > len(lines) {
		cursorRow = len(lines)
	}
	cursorIdx := cursorRow - 1

	effectiveMax := min(maxLines, len(lines))
	halfWindow := effectiveMax / 2
	startLine := max(0, cursorIdx-halfWindow)
	endLine := min(len(lines), startLine+effectiveMax)
	if endLine == len(lines) {
		startLine = max(0, endLine-effectiveMax)
	}

	result := lines[startLine:endLine]

	totalBytes := 0
	for _, line := range result {
		totalBytes += len(line) + 1
	}

	if totalBytes > maxBytes {
		result, startLine = p.trimByBytes(result, cursorIdx-startLine, startLine)
	}

	newCursorRow := cursorRow - startLine
	return result, newCursorRow, cursorCol, startLine
}

func (p *Provider) trimByBytes(lines []string, cursorIdxInWindow, baseOffset int) ([]string, int) {
	maxBytes := p.limits.MaxInputBytes

	if len(lines) == 0 {
		return lines, baseOffset
	}

	if cursorIdxInWindow < 0 {
		cursorIdxInWindow = 0
	}
	if cursorIdxInWindow >= len(lines) {
		cursorIdxInWindow = len(lines) - 1
	}

	cursorLineBytes := len(lines[cursorIdxInWindow]) + 1
	remainingBudget := maxBytes - cursorLineBytes
	halfBudget := remainingBudget / 2

	startIdx := cursorIdxInWindow
	bytesBefore := 0
	for startIdx > 0 && bytesBefore < halfBudget {
		newBytes := len(lines[startIdx-1]) + 1
		if bytesBefore+newBytes <= halfBudget {
			startIdx--
			bytesBefore += newBytes
		} else {
			break
		}
	}

	unusedBefore := halfBudget - bytesBefore
	budgetAfter := halfBudget + unusedBefore
	endIdx := cursorIdxInWindow
	bytesAfter := 0
	for endIdx < len(lines)-1 && bytesAfter < budgetAfter {
		newBytes := len(lines[endIdx+1]) + 1
		if bytesAfter+newBytes <= budgetAfter {
			endIdx++
			bytesAfter += newBytes
		} else {
			break
		}
	}

	unusedAfter := budgetAfter - bytesAfter
	if unusedAfter > 0 {
		for startIdx > 0 {
			newBytes := len(lines[startIdx-1]) + 1
			if bytesBefore+newBytes <= halfBudget+unusedAfter {
				startIdx--
				bytesBefore += newBytes
			} else {
				break
			}
		}
	}

	return lines[startIdx : endIdx+1], baseOffset + startIdx
}

func (p *Provider) truncateDiffHistories(histories []*types.FileDiffHistory) []*types.FileDiffHistory {
	maxBytes := p.limits.MaxInputBytes
	maxLines := p.limits.MaxInputLines

	if len(histories) == 0 {
		return histories
	}

	totalBytes := 0
	totalLines := 0
	for _, h := range histories {
		for _, entry := range h.DiffHistory {
			totalBytes += len(entry.Original) + len(entry.Updated)
			totalLines += strings.Count(entry.Original, "\n") + strings.Count(entry.Updated, "\n") + 2
		}
	}

	if totalBytes <= maxBytes && totalLines <= maxLines {
		return histories
	}

	result := make([]*types.FileDiffHistory, 0, len(histories))
	remainingBytes := maxBytes
	remainingLines := maxLines

	for i := len(histories) - 1; i >= 0 && remainingBytes > 0 && remainingLines > 0; i-- {
		h := histories[i]
		if len(h.DiffHistory) == 0 {
			continue
		}

		var keptEntries []*types.DiffEntry
		for j := len(h.DiffHistory) - 1; j >= 0 && remainingBytes > 0 && remainingLines > 0; j-- {
			entry := h.DiffHistory[j]
			entryBytes := len(entry.Original) + len(entry.Updated)
			entryLines := strings.Count(entry.Original, "\n") + strings.Count(entry.Updated, "\n") + 2
			if entryBytes <= remainingBytes && entryLines <= remainingLines {
				keptEntries = append([]*types.DiffEntry{entry}, keptEntries...)
				remainingBytes -= entryBytes
				remainingLines -= entryLines
			}
		}

		if len(keptEntries) > 0 {
			result = append([]*types.FileDiffHistory{{
				FileName:    h.FileName,
				DiffHistory: keptEntries,
			}}, result...)
		}
	}

	return result
}

// Provider implements the Sweep hosted API provider
type Provider struct {
	config *types.ProviderConfig
	client *sweepapi.Client
	limits engine.ContextLimits
}

// NewProvider creates a new Sweep API provider
func NewProvider(config *types.ProviderConfig) *Provider {
	// Use constant URL, but allow override for testing (httptest servers use 127.0.0.1)
	url := sweepapi.CompletionURL
	if strings.HasPrefix(config.ProviderURL, "http://127.0.0.1") {
		url = config.ProviderURL
	}

	return &Provider{
		config: config,
		client: sweepapi.NewClient(url, config.APIKey, config.CompletionTimeout),
		limits: engine.ContextLimits{
			MaxInputLines: 50_000,
			MaxInputBytes: 10_000_000,
		},
	}
}

// SendMetric implements metrics.Sender
func (p *Provider) SendMetric(ctx context.Context, event metrics.Event) {
	var sweepEvent sweepapi.EventType
	switch event.Type {
	case metrics.EventShown:
		sweepEvent = sweepapi.EventShown
	case metrics.EventAccepted:
		sweepEvent = sweepapi.EventAccepted
	case metrics.EventRejected, metrics.EventIgnored:
		// SweepAPI doesn't support rejected/ignored events
		return
	default:
		return
	}

	req := &sweepapi.MetricsRequest{
		EventType:          sweepEvent,
		SuggestionType:     sweepapi.SuggestionGhostText,
		Additions:          event.Info.Additions,
		Deletions:          event.Info.Deletions,
		AutocompleteID:     event.Info.ID,
		DebugInfo:          "cursortab-nvim",
		PrivacyModeEnabled: p.config.PrivacyMode,
	}
	if err := p.client.TrackMetrics(ctx, req); err != nil {
		logger.Warn("sweepapi: failed to track %s: %v", event.Type, err)
	}
}

// GetContextLimits implements engine.Provider
func (p *Provider) GetContextLimits() engine.ContextLimits {
	return p.limits.WithDefaults()
}

// GetCompletion implements engine.Provider
func (p *Provider) GetCompletion(ctx context.Context, req *types.CompletionRequest) (*types.CompletionResponse, error) {
	defer logger.Trace("sweepapi.GetCompletion")()

	// Apply context limits
	lines, cursorRow, cursorCol, trimOffset := p.truncateContext(req.Lines, req.CursorRow, req.CursorCol)
	if trimOffset > 0 {
		logger.Debug("sweepapi: truncated context, removed %d lines from start", trimOffset)
	}

	// Build file contents from lines
	fileContents := strings.Join(lines, "\n")

	// Convert cursor to byte offset
	cursorPosition := sweepapi.CursorToByteOffset(lines, cursorRow, cursorCol)

	// Truncate and format recent changes from diff histories
	diffHistories := p.truncateDiffHistories(req.FileDiffHistories)
	recentChanges := formatRecentChanges(diffHistories)

	// Format diagnostics, treesitter, and git diff as retrieval chunks
	retrievalChunks := p.formatDiagnostics(req.GetDiagnostics())
	retrievalChunks = append(retrievalChunks, formatTreesitterChunk(req.GetTreesitter())...)
	retrievalChunks = append(retrievalChunks, formatGitDiffChunk(req.GetGitDiff())...)

	// Extract repo name from workspace path
	repoName := filepath.Base(req.WorkspacePath)
	if repoName == "" || repoName == "." {
		repoName = "untitled"
	}

	// Build API request
	apiReq := &sweepapi.AutocompleteRequest{
		RepoName:             repoName,
		FilePath:             req.FilePath,
		FileContents:         fileContents,
		OriginalFileContents: fileContents,
		CursorPosition:       cursorPosition,
		RecentChanges:        recentChanges,
		ChangesAboveCursor:   true,
		MultipleSuggestions:  false,
		UseBytes:             true,
		PrivacyModeEnabled:   p.config.PrivacyMode,
		FileChunks:           p.buildFileChunks(req.RecentBufferSnapshots),
		RecentUserActions:    convertUserActions(req.UserActions),
		RetrievalChunks:      retrievalChunks,
	}

	// Call API
	apiResp, err := p.client.DoCompletion(ctx, apiReq)
	if err != nil {
		return nil, err
	}

	// Handle empty response
	if apiResp.Completion == "" {
		return &types.CompletionResponse{}, nil
	}

	// Convert byte offsets to line-based completion
	newText, startLine, endLine := sweepapi.ApplyByteRangeEdit(
		fileContents,
		apiResp.StartIndex,
		apiResp.EndIndex,
		apiResp.Completion,
	)

	// Extract the affected lines from the new text
	newLines := sweepapi.ExtractLines(newText, startLine, endLine)
	if len(newLines) == 0 {
		return &types.CompletionResponse{}, nil
	}

	// Calculate original end line for buffer replacement
	// Use endIdx-1 to get the line of the last replaced byte (not the position after)
	endOffset := apiResp.EndIndex
	if endOffset > apiResp.StartIndex {
		endOffset--
	}
	origEndLine, _ := sweepapi.ByteOffsetToLineCol(fileContents, endOffset)

	logger.Debug("sweepapi: byte range [%d:%d] -> lines [%d:%d], origEndLine=%d",
		apiResp.StartIndex, apiResp.EndIndex, startLine, endLine, origEndLine)

	// Calculate metrics info for the engine
	additions, deletions := countChanges(origEndLine-startLine+1, len(newLines))

	return &types.CompletionResponse{
		Completions: []*types.Completion{{
			StartLine:  startLine + trimOffset,
			EndLineInc: origEndLine + trimOffset,
			Lines:      newLines,
		}},
		MetricsInfo: &types.MetricsInfo{
			ID:        apiResp.AutocompleteID,
			Additions: additions,
			Deletions: deletions,
		},
	}, nil
}

// countChanges calculates additions and deletions based on line counts.
func countChanges(oldLineCount, newLineCount int) (additions, deletions int) {
	return max(newLineCount, 1), max(oldLineCount, 1)
}

// formatRecentChanges converts FileDiffHistories to a string for the API
// Format: "File: path:\n{diff}\n"
func formatRecentChanges(histories []*types.FileDiffHistory) string {
	if len(histories) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, history := range histories {
		if len(history.DiffHistory) == 0 {
			continue
		}

		// Format each diff entry for this file
		var diffContent strings.Builder
		for _, entry := range history.DiffHistory {
			if entry.Original != "" || entry.Updated != "" {
				diffContent.WriteString("<<<<<<< ORIGINAL\n")
				diffContent.WriteString(entry.Original)
				if !strings.HasSuffix(entry.Original, "\n") && entry.Original != "" {
					diffContent.WriteString("\n")
				}
				diffContent.WriteString("=======\n")
				diffContent.WriteString(entry.Updated)
				if !strings.HasSuffix(entry.Updated, "\n") && entry.Updated != "" {
					diffContent.WriteString("\n")
				}
				diffContent.WriteString(">>>>>>> UPDATED\n")
			}
		}

		if diffContent.Len() > 0 {
			sb.WriteString("File: ")
			sb.WriteString(history.FileName)
			sb.WriteString(":\n")
			sb.WriteString(diffContent.String())
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

func (p *Provider) formatDiagnostics(linterErrors *types.LinterErrors) []sweepapi.FileChunk {
	if linterErrors == nil || len(linterErrors.Errors) == 0 {
		return []sweepapi.FileChunk{}
	}

	var sb strings.Builder
	lineCount := 0
	for _, err := range linterErrors.Errors {
		if sb.Len() >= p.limits.MaxInputBytes || lineCount >= p.limits.MaxInputLines {
			break
		}
		if err.Range != nil {
			sb.WriteString("Line ")
			sb.WriteString(strconv.Itoa(err.Range.StartLine))
			sb.WriteString(": ")
		}
		if err.Source != "" {
			sb.WriteString("[")
			sb.WriteString(err.Source)
			sb.WriteString("] ")
		}
		sb.WriteString(err.Message)
		sb.WriteString("\n")
		lineCount++
	}

	if sb.Len() == 0 {
		return []sweepapi.FileChunk{}
	}

	return []sweepapi.FileChunk{{
		FilePath:  "diagnostics",
		Content:   sb.String(),
		StartLine: 1,
		EndLine:   lineCount,
	}}
}

func (p *Provider) buildFileChunks(snapshots []*types.RecentBufferSnapshot) []sweepapi.FileChunk {
	if len(snapshots) == 0 {
		return []sweepapi.FileChunk{}
	}

	chunks := make([]sweepapi.FileChunk, 0, len(snapshots))
	totalChars := 0
	totalLines := 0

	for _, snap := range snapshots {
		content := strings.Join(snap.Lines, "\n")
		lineCount := len(snap.Lines)

		// Check if adding this chunk would exceed limits
		if totalChars+len(content) > p.limits.MaxInputBytes || totalLines+lineCount > p.limits.MaxInputLines {
			break
		}

		ts := uint64(snap.TimestampMs)
		chunks = append(chunks, sweepapi.FileChunk{
			FilePath:  snap.FilePath,
			Content:   content,
			StartLine: 0,
			EndLine:   lineCount,
			Timestamp: &ts,
		})

		totalChars += len(content)
		totalLines += lineCount
	}
	return chunks
}

// formatTreesitterChunk converts TreesitterContext to a FileChunk for the API
func formatTreesitterChunk(ts *types.TreesitterContext) []sweepapi.FileChunk {
	if ts == nil {
		return nil
	}

	var sb strings.Builder

	if ts.EnclosingSignature != "" {
		sb.WriteString("Enclosing scope: ")
		sb.WriteString(ts.EnclosingSignature)
		sb.WriteString("\n")
	}

	if len(ts.Siblings) > 0 {
		sb.WriteString("Sibling symbols:\n")
		for _, s := range ts.Siblings {
			sb.WriteString("  line ")
			sb.WriteString(strconv.Itoa(s.Line))
			sb.WriteString(": ")
			sb.WriteString(s.Signature)
			sb.WriteString("\n")
		}
	}

	if len(ts.Imports) > 0 {
		sb.WriteString("Imports:\n")
		for _, imp := range ts.Imports {
			sb.WriteString("  ")
			sb.WriteString(imp)
			sb.WriteString("\n")
		}
	}

	if sb.Len() == 0 {
		return nil
	}

	return []sweepapi.FileChunk{{
		FilePath:  "treesitter_context",
		Content:   sb.String(),
		StartLine: 1,
		EndLine:   strings.Count(sb.String(), "\n"),
	}}
}

// formatGitDiffChunk converts GitDiffContext to a FileChunk for the API
func formatGitDiffChunk(gd *types.GitDiffContext) []sweepapi.FileChunk {
	if gd == nil || gd.Diff == "" {
		return nil
	}

	return []sweepapi.FileChunk{{
		FilePath:  "staged_git_diff",
		Content:   gd.Diff,
		StartLine: 1,
		EndLine:   strings.Count(gd.Diff, "\n"),
	}}
}

// convertUserActions converts types.UserAction to sweepapi.UserAction.
// Since actions are small fixed-size records, we just convert them all
// (the engine already limits to MaxUserActions=16).
func convertUserActions(actions []*types.UserAction) []sweepapi.UserAction {
	if len(actions) == 0 {
		return []sweepapi.UserAction{}
	}

	result := make([]sweepapi.UserAction, 0, len(actions))
	for _, a := range actions {
		result = append(result, sweepapi.UserAction{
			ActionType: string(a.ActionType),
			FilePath:   a.FilePath,
			LineNumber: a.LineNumber,
			Offset:     a.Offset,
			Timestamp:  a.TimestampMs,
		})
	}
	return result
}
