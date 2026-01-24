package zeta

import (
	"bytes"
	"context"
	"cursortab/logger"
	"cursortab/types"
	"cursortab/utils"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Provider implements the types.Provider interface for Zeta (vLLM with OpenAI-style API)
// Note: Generation limit uses config.MaxTokens (max_context_tokens) since Zeta regenerates the editable region
type Provider struct {
	config      *types.ProviderConfig
	httpClient  *http.Client
	url         string
	model       string
	temperature float64
}

// completionRequest matches the OpenAI Completion API format used by vLLM
type completionRequest struct {
	Model       string   `json:"model"`
	Prompt      string   `json:"prompt"`
	Temperature float64  `json:"temperature"`
	MaxTokens   int      `json:"max_tokens"`
	Stop        []string `json:"stop,omitempty"`
	N           int      `json:"n"`
	Echo        bool     `json:"echo"`
}

// completionResponse matches the OpenAI Completion API response format
type completionResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int    `json:"index"`
		Text         string `json:"text"`
		Logprobs     any    `json:"logprobs"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// NewProvider creates a new Zeta provider instance
func NewProvider(config *types.ProviderConfig) (*Provider, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	return &Provider{
		config:      config,
		httpClient:  &http.Client{},
		url:         config.ProviderURL,
		model:       config.ProviderModel,
		temperature: config.ProviderTemperature,
	}, nil
}

// GetCompletion implements types.Provider.GetCompletion for Zeta
// This provider supports multi-line completions using special tokens
func (p *Provider) GetCompletion(ctx context.Context, req *types.CompletionRequest) (*types.CompletionResponse, error) {
	// Build the user excerpt with special tokens
	userExcerpt := p.buildPromptWithSpecialTokens(req)

	// Build the user edits from diff history
	userEdits := p.buildUserEditsFromDiffHistory(req)

	// Format diagnostics for inclusion in prompt
	diagnosticsText := p.formatDiagnosticsForPrompt(req)

	// Build the full prompt with instruction template
	// Extended format includes diagnostics section
	prompt := p.buildInstructionPrompt(userEdits, diagnosticsText, userExcerpt)

	// Create the completion request
	// Zeta regenerates the editable region, so max_tokens must match max_context_tokens
	completionReq := completionRequest{
		Model:       p.model,
		Prompt:      prompt,
		Temperature: p.temperature,
		MaxTokens:   p.config.MaxTokens,
		Stop:        []string{"\n<|editable_region_end|>"},
		N:           1,
		Echo:        false,
	}

	// Marshal the request without HTML escaping
	var reqBodyBuf bytes.Buffer
	encoder := json.NewEncoder(&reqBodyBuf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(completionReq); err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	reqBody := reqBodyBuf.Bytes()

	// Debug logging for request
	logger.Debug("zeta provider request to %s:\n  Model: %s\n  Temperature: %.2f\n  MaxTokens: %d\n  Prompt length: %d chars\n  Prompt:\n%s",
		p.url+"/v1/completions",
		completionReq.Model,
		completionReq.Temperature,
		completionReq.MaxTokens,
		len(prompt),
		prompt)

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.url+"/v1/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Read the response body for debugging
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse the response
	var completionResp completionResponse
	if err := json.Unmarshal(body, &completionResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Debug logging for response
	logger.Debug("zeta provider response:\n  ID: %s\n  Model: %s\n  Choices: %d\n  Usage: prompt=%d, completion=%d, total=%d",
		completionResp.ID,
		completionResp.Model,
		len(completionResp.Choices),
		completionResp.Usage.PromptTokens,
		completionResp.Usage.CompletionTokens,
		completionResp.Usage.TotalTokens)

	// Check if we got any completions
	if len(completionResp.Choices) == 0 {
		logger.Debug("zeta provider returned no completions")
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil,
		}, nil
	}

	// Extract the completion text
	completionText := completionResp.Choices[0].Text
	logger.Debug("zeta completion text (%d chars):\n%s", len(completionText), completionText)

	// If the completion is empty or just whitespace, return empty response
	if strings.TrimSpace(completionText) == "" {
		logger.Debug("zeta completion text is empty after trimming")
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil,
		}, nil
	}

	// Parse the completion into lines and build the result
	finishReason := completionResp.Choices[0].FinishReason
	completion := p.parseCompletion(req, completionText, finishReason)
	if completion == nil {
		logger.Debug("zeta parseCompletion returned nil (no changes detected)")
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil,
		}, nil
	}

	logger.Debug("zeta parsed completion: StartLine=%d, EndLineInc=%d, Lines=%d", completion.StartLine, completion.EndLineInc, len(completion.Lines))

	return &types.CompletionResponse{
		Completions:  []*types.Completion{completion},
		CursorTarget: nil,
	}, nil
}

// formatDiagnosticsForPrompt formats linter errors in a diff-like format for the prompt
// Format similar to User Edits section:
// Diagnostics in "path/to/file":
// ```diagnostics
// line 10: [error] Unused variable 'x' (source: eslint)
// line 15: [warning] Missing return type (source: typescript)
// ```
func (p *Provider) formatDiagnosticsForPrompt(req *types.CompletionRequest) string {
	if req.LinterErrors == nil || len(req.LinterErrors.Errors) == 0 {
		return ""
	}

	var diagBuilder strings.Builder

	diagBuilder.WriteString("Diagnostics in \"")
	diagBuilder.WriteString(req.LinterErrors.RelativeWorkspacePath)
	diagBuilder.WriteString("\":\n")
	diagBuilder.WriteString("```diagnostics\n")

	for _, err := range req.LinterErrors.Errors {
		// Format: line X: [severity] message (source)
		if err.Range != nil {
			fmt.Fprintf(&diagBuilder, "line %d: ", err.Range.StartLine)
		}

		fmt.Fprintf(&diagBuilder, "[%s] %s", err.Severity, err.Message)

		if err.Source != "" {
			fmt.Fprintf(&diagBuilder, " (source: %s)", err.Source)
		}
		diagBuilder.WriteString("\n")
	}

	diagBuilder.WriteString("```")
	return diagBuilder.String()
}

// buildUserEditsFromDiffHistory formats the diff history into Zed's "User Edits" format
// Example format:
// User edited "path/to/file.py":
// ```diff
// @@ -1,1 +1,1 @@
// -def test
// +def testi
// ```
func (p *Provider) buildUserEditsFromDiffHistory(req *types.CompletionRequest) string {
	if len(req.FileDiffHistories) == 0 {
		return ""
	}

	var editsBuilder strings.Builder
	firstEdit := true

	for _, fileHistory := range req.FileDiffHistories {
		if len(fileHistory.DiffHistory) == 0 {
			continue
		}

		// Each file's diffs are concatenated with double newlines
		for _, diffEntry := range fileHistory.DiffHistory {
			// Convert structured diff to unified diff format
			unifiedDiff := p.diffEntryToUnifiedDiff(diffEntry)
			if unifiedDiff == "" {
				continue
			}

			if !firstEdit {
				editsBuilder.WriteString("\n\n")
			}
			firstEdit = false

			// Format: User edited "filename":
			editsBuilder.WriteString("User edited \"")
			editsBuilder.WriteString(fileHistory.FileName)
			editsBuilder.WriteString("\":\n")
			editsBuilder.WriteString("```diff\n")
			editsBuilder.WriteString(unifiedDiff)
			editsBuilder.WriteString("\n```")
		}
	}

	return editsBuilder.String()
}

// diffEntryToUnifiedDiff converts a structured DiffEntry to unified diff format
func (p *Provider) diffEntryToUnifiedDiff(entry *types.DiffEntry) string {
	if entry.Original == entry.Updated {
		return ""
	}

	originalLines := strings.Split(entry.Original, "\n")
	updatedLines := strings.Split(entry.Updated, "\n")

	var diffBuilder strings.Builder

	// Write diff header
	fmt.Fprintf(&diffBuilder, "@@ -%d,%d +%d,%d @@\n",
		1, len(originalLines), 1, len(updatedLines))

	// Write deleted lines (from original)
	for _, line := range originalLines {
		diffBuilder.WriteString("-")
		diffBuilder.WriteString(line)
		diffBuilder.WriteString("\n")
	}

	// Write added lines (from updated)
	for _, line := range updatedLines {
		diffBuilder.WriteString("+")
		diffBuilder.WriteString(line)
		diffBuilder.WriteString("\n")
	}

	return strings.TrimSuffix(diffBuilder.String(), "\n")
}

// buildInstructionPrompt wraps the user excerpt in the instruction template
// Extended version includes diagnostics section
func (p *Provider) buildInstructionPrompt(userEdits, diagnostics, userExcerpt string) string {
	var promptBuilder strings.Builder

	promptBuilder.WriteString("### Instruction:\n")
	promptBuilder.WriteString("You are a code completion assistant and your task is to analyze user edits and then rewrite an excerpt that the user provides, suggesting the appropriate edits within the excerpt, taking into account the cursor location.\n\n")

	promptBuilder.WriteString("### User Edits:\n\n")
	promptBuilder.WriteString(userEdits)
	promptBuilder.WriteString("\n\n")

	// Add diagnostics section if available
	if diagnostics != "" {
		promptBuilder.WriteString("### Diagnostics:\n\n")
		promptBuilder.WriteString(diagnostics)
		promptBuilder.WriteString("\n\n")
	}

	promptBuilder.WriteString("### User Excerpt:\n\n")
	promptBuilder.WriteString(userExcerpt)
	promptBuilder.WriteString("\n\n")

	promptBuilder.WriteString("### Response:\n")

	return promptBuilder.String()
}

// buildPromptWithSpecialTokens constructs the prompt with special tokens matching Zed's format
// Uses max_context_tokens to limit the editable region size
func (p *Provider) buildPromptWithSpecialTokens(req *types.CompletionRequest) string {
	var promptBuilder strings.Builder

	if len(req.Lines) == 0 {
		promptBuilder.WriteString("```")
		promptBuilder.WriteString(req.FilePath)
		promptBuilder.WriteString("\n<|start_of_file|>\n<|editable_region_start|>\n<|user_cursor_is_here|>\n<|editable_region_end|>\n```")
		return promptBuilder.String()
	}

	cursorRow := req.CursorRow // 1-indexed
	cursorCol := req.CursorCol // 0-indexed

	// Convert cursor to 0-indexed for calculations
	cursorLine := cursorRow - 1

	// Use token-based trimming for editable region
	_, _, _, trimOffset := utils.TrimContentAroundCursor(
		req.Lines, cursorLine, cursorCol, p.config.MaxTokens)

	// Calculate editable region bounds from trim result
	// Re-run to get the actual trimmed lines count
	trimmedLines, _, _, _ := utils.TrimContentAroundCursor(
		req.Lines, cursorLine, cursorCol, p.config.MaxTokens)

	editableStart := trimOffset
	editableEnd := trimOffset + len(trimmedLines)

	// Context region: additional 5 lines around editable region
	contextLinesBefore := 5
	contextLinesAfter := 5

	contextStart := max(0, editableStart-contextLinesBefore)
	contextEnd := min(len(req.Lines), editableEnd+contextLinesAfter)

	// Build the prompt in Zed's format: ```filename\n<|start_of_file|>\n...
	promptBuilder.WriteString("```")
	promptBuilder.WriteString(req.FilePath)
	promptBuilder.WriteString("\n")

	// Add start of file marker if we're at the beginning
	if contextStart == 0 {
		promptBuilder.WriteString("<|start_of_file|>\n")
	}

	// Add context lines before editable region
	for i := contextStart; i < editableStart; i++ {
		promptBuilder.WriteString(req.Lines[i])
		promptBuilder.WriteString("\n")
	}

	// Mark the start of the editable region (writeln adds newline after)
	promptBuilder.WriteString("<|editable_region_start|>\n")

	// Add lines in the editable region up to the cursor
	for i := editableStart; i < cursorLine; i++ {
		promptBuilder.WriteString(req.Lines[i])
		promptBuilder.WriteString("\n")
	}

	// Add the current line split at cursor position
	if cursorLine < len(req.Lines) {
		currentLine := req.Lines[cursorLine]
		if cursorCol <= len(currentLine) {
			beforeCursor := currentLine[:cursorCol]
			afterCursor := currentLine[cursorCol:]

			promptBuilder.WriteString(beforeCursor)
			promptBuilder.WriteString("<|user_cursor_is_here|>")
			promptBuilder.WriteString(afterCursor)
		} else {
			promptBuilder.WriteString(currentLine)
			promptBuilder.WriteString("<|user_cursor_is_here|>")
		}
	} else {
		promptBuilder.WriteString("<|user_cursor_is_here|>")
	}

	// Add remaining lines in the editable region after the cursor
	for i := cursorLine + 1; i < editableEnd; i++ {
		promptBuilder.WriteString("\n")
		promptBuilder.WriteString(req.Lines[i])
	}

	// Mark the end of the editable region (write adds newline before, not after)
	promptBuilder.WriteString("\n<|editable_region_end|>")

	// Add context lines after editable region
	for i := editableEnd; i < contextEnd; i++ {
		promptBuilder.WriteString("\n")
		promptBuilder.WriteString(req.Lines[i])
	}

	// Close the code fence (newline before the closing ```)
	promptBuilder.WriteString("\n```")

	return promptBuilder.String()
}

// parseCompletion parses the model's completion text matching Zed's parsing logic
// finishReason indicates why the model stopped: "stop" (hit stop token) or "length" (hit max_tokens)
func (p *Provider) parseCompletion(req *types.CompletionRequest, completionText string, finishReason string) *types.Completion {
	// Remove cursor markers
	content := strings.ReplaceAll(completionText, "<|user_cursor_is_here|>", "")

	// Extract text between editable markers
	startMarker := "<|editable_region_start|>"
	endMarker := "<|editable_region_end|>"

	// Find the start marker
	startIdx := strings.Index(content, startMarker)
	if startIdx == -1 {
		return p.parseSimpleCompletion(req, completionText, finishReason)
	}

	// Slice from the start marker position onward
	content = content[startIdx:]

	// Find the newline after the start marker and skip it
	newlineIdx := strings.Index(content, "\n")
	if newlineIdx == -1 {
		return nil
	}
	content = content[newlineIdx+1:]

	// Find the end marker (looking for "\n<|editable_region_end|>")
	endIdx := strings.Index(content, "\n"+endMarker)
	var newText string
	if endIdx == -1 {
		// If end marker not found, use rest of content
		newText = content
	} else {
		newText = content[:endIdx]
	}

	// Calculate the editable region that was sent in the prompt (same as buildPromptWithSpecialTokens)
	cursorRow := req.CursorRow - 1 // Convert to 0-indexed
	trimmedLines, _, _, trimOffset := utils.TrimContentAroundCursor(
		req.Lines, cursorRow, req.CursorCol, p.config.MaxTokens)
	editableStart := trimOffset
	editableEnd := trimOffset + len(trimmedLines)

	// Get the old text of the editable region
	oldLines := req.Lines[editableStart:editableEnd]
	oldText := strings.Join(oldLines, "\n")

	// If the new text equals old text, no completion needed
	if newText == oldText {
		return nil
	}

	// Split new text into lines
	newLines := strings.Split(newText, "\n")

	// Handle truncated output (finish_reason == "length")
	processedLines, endLineInc, shouldReject := utils.HandleTruncatedCompletion(newLines, finishReason, editableStart, editableEnd)
	if shouldReject {
		logger.Debug("zeta completion rejected: only truncated content")
		return nil
	}
	newLines = processedLines

	// Log if we had to adjust for truncation
	if finishReason == "length" {
		logger.Info("zeta completion truncated: dropped last line, replacing lines %d-%d only (editable region was %d-%d, original_lines=%d, kept_lines=%d)",
			editableStart+1, endLineInc, editableStart+1, editableEnd, len(strings.Split(newText, "\n")), len(newLines))
	}

	// Final check: if after processing the text equals the portion we're replacing, no completion needed
	if utils.IsNoOpReplacement(newLines, req.Lines[editableStart:endLineInc]) {
		return nil
	}

	return &types.Completion{
		StartLine:  editableStart + 1, // Convert back to 1-indexed
		EndLineInc: endLineInc,
		Lines:      newLines,
	}
}

// parseSimpleCompletion is a fallback parser for when markers aren't found
// finishReason indicates why the model stopped: "stop" (hit stop token) or "length" (hit max_tokens)
func (p *Provider) parseSimpleCompletion(req *types.CompletionRequest, completionText string, finishReason string) *types.Completion {
	// Split into lines
	completionLines := strings.Split(completionText, "\n")

	if len(completionLines) == 0 {
		return nil
	}

	cursorRow := req.CursorRow
	cursorCol := req.CursorCol

	// Build the replacement lines
	var resultLines []string

	// First line: combine text before cursor + completion first line
	if cursorRow <= len(req.Lines) {
		currentLine := req.Lines[cursorRow-1]
		beforeCursor := ""
		if cursorCol <= len(currentLine) {
			beforeCursor = currentLine[:cursorCol]
		} else {
			beforeCursor = currentLine
		}
		resultLines = append(resultLines, beforeCursor+completionLines[0])
	} else {
		resultLines = append(resultLines, completionLines[0])
	}

	// Add remaining completion lines
	resultLines = append(resultLines, completionLines[1:]...)

	// Determine the end line (1-indexed inclusive)
	endLine := cursorRow + len(completionLines) - 1

	// Handle truncated output (finish_reason == "length")
	// Note: windowStart is 0-indexed, windowEnd is passed as 1-indexed inclusive (the final EndLineInc value)
	// When not truncated, windowEnd is returned directly; when truncated, windowStart + len(newLines) is calculated
	// which gives the correct 1-indexed inclusive value due to the 0-indexed + count relationship
	processedLines, adjustedEndLine, shouldReject := utils.HandleTruncatedCompletion(resultLines, finishReason, cursorRow-1, endLine)
	if shouldReject {
		logger.Debug("zeta simple completion rejected: only truncated content")
		return nil
	}
	resultLines = processedLines
	endLine = adjustedEndLine

	// Log if we had to adjust for truncation
	if finishReason == "length" {
		logger.Info("zeta simple completion truncated: dropped last line (original_lines=%d, kept_lines=%d)",
			len(completionLines), len(resultLines))
	}

	// Final check: if after processing the text equals the portion we're replacing, no completion needed
	if endLine <= len(req.Lines) && utils.IsNoOpReplacement(resultLines, req.Lines[cursorRow-1:endLine]) {
		return nil
	}

	return &types.Completion{
		StartLine:  cursorRow,
		EndLineInc: endLine,
		Lines:      resultLines,
	}
}

