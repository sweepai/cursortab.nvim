package sweep

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

// Provider implements the types.Provider interface for Sweep Next-Edit model
// Uses the Qwen2.5-Coder pretrained format with <|file_sep|> tokens
// Note: Generation limit uses config.MaxTokens (max_context_tokens) since Sweep regenerates the entire window
type Provider struct {
	config      *types.ProviderConfig
	httpClient  *http.Client
	url         string
	model       string
	temperature float64
}

// completionRequest matches the OpenAI Completion API format
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

// NewProvider creates a new Sweep provider instance
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

// GetCompletion implements types.Provider.GetCompletion for Sweep
// Uses the Sweep Next-Edit format with <|file_sep|> tokens
func (p *Provider) GetCompletion(ctx context.Context, req *types.CompletionRequest) (*types.CompletionResponse, error) {
	// Build the prompt in Sweep's format (also returns window bounds for parseCompletion)
	prompt, windowStart, windowEnd := p.buildPrompt(req)

	// Create the completion request
	// Sweep regenerates the entire window, so max_tokens must match max_context_tokens
	completionReq := completionRequest{
		Model:       p.model,
		Prompt:      prompt,
		Temperature: p.temperature,
		MaxTokens:   p.config.MaxTokens,
		Stop:        []string{"<|file_sep|>", "</s>"},
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
	logger.Debug("sweep provider request to %s:\n  Model: %s\n  Temperature: %.2f\n  MaxTokens: %d\n  Prompt length: %d chars\n  Prompt:\n%s",
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

	// Read the response body
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
	logger.Debug("sweep provider response:\n  ID: %s\n  Model: %s\n  Choices: %d\n  Usage: prompt=%d, completion=%d, total=%d",
		completionResp.ID,
		completionResp.Model,
		len(completionResp.Choices),
		completionResp.Usage.PromptTokens,
		completionResp.Usage.CompletionTokens,
		completionResp.Usage.TotalTokens)

	// Check if we got any completions
	if len(completionResp.Choices) == 0 {
		logger.Debug("sweep provider returned no completions")
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil,
		}, nil
	}

	// Extract the completion text (this is the predicted updated content)
	completionText := completionResp.Choices[0].Text
	logger.Debug("sweep completion text (%d chars):\n%s", len(completionText), completionText)

	// If the completion is empty or just whitespace, return empty response
	if strings.TrimSpace(completionText) == "" {
		logger.Debug("sweep completion text is empty after trimming")
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil,
		}, nil
	}

	// Parse the completion into a result (pass window bounds from buildPrompt)
	finishReason := completionResp.Choices[0].FinishReason
	completion := p.parseCompletion(req, completionText, finishReason, windowStart, windowEnd)
	if completion == nil {
		logger.Debug("sweep parseCompletion returned nil (no changes detected)")
		return &types.CompletionResponse{
			Completions:  []*types.Completion{},
			CursorTarget: nil,
		}, nil
	}

	logger.Debug("sweep parsed completion: StartLine=%d, EndLineInc=%d, Lines=%d", completion.StartLine, completion.EndLineInc, len(completion.Lines))

	return &types.CompletionResponse{
		Completions:  []*types.Completion{completion},
		CursorTarget: nil,
	}, nil
}

// buildPrompt constructs the prompt in Sweep Next-Edit format:
//
//	<|file_sep|>{changed_file}.diff
//	original:
//	{before_changes}
//	updated:
//	{after_changes}
//	<|file_sep|>original/{file_path}
//	{contents_prior_to_most_recent_change}
//	<|file_sep|>current/{file_path}
//	{current_state_of_contents}
//	<|file_sep|>updated/{file_path}
//
// The model generates the updated content after the last marker.
// Uses token-based context limiting (max_context_tokens) around the cursor.
// Returns: (prompt, windowStart, windowEnd) where window bounds are 0-indexed line numbers.
func (p *Provider) buildPrompt(req *types.CompletionRequest) (string, int, int) {
	var promptBuilder strings.Builder

	// Handle empty file edge case
	if len(req.Lines) == 0 {
		promptBuilder.WriteString("<|file_sep|>original/")
		promptBuilder.WriteString(req.FilePath)
		promptBuilder.WriteString("\n\n")
		promptBuilder.WriteString("<|file_sep|>current/")
		promptBuilder.WriteString(req.FilePath)
		promptBuilder.WriteString("\n\n")
		promptBuilder.WriteString("<|file_sep|>updated/")
		promptBuilder.WriteString(req.FilePath)
		promptBuilder.WriteString("\n")
		return promptBuilder.String(), 0, 0
	}

	// 1. Build diff history section (already trimmed by engine)
	diffSection := p.buildDiffSection(req)

	// 2. Trim content around cursor using max_context_tokens
	cursorLine := req.CursorRow - 1 // Convert to 0-indexed
	trimmedLines, _, _, trimOffset := utils.TrimContentAroundCursor(
		req.Lines, cursorLine, req.CursorCol, p.config.MaxTokens)

	// Calculate window bounds for parseCompletion
	windowStart := trimOffset
	windowEnd := trimOffset + len(trimmedLines)

	// 3. Get original content with same trim window
	originalLines := p.getTrimmedOriginalContent(req, trimOffset, len(trimmedLines))

	// Write diff section if present
	if diffSection != "" {
		promptBuilder.WriteString(diffSection)
	}

	// Add original/{file_path} section
	promptBuilder.WriteString("<|file_sep|>original/")
	promptBuilder.WriteString(req.FilePath)
	promptBuilder.WriteString("\n")
	promptBuilder.WriteString(strings.Join(originalLines, "\n"))
	promptBuilder.WriteString("\n")

	// Add current/{file_path} section
	promptBuilder.WriteString("<|file_sep|>current/")
	promptBuilder.WriteString(req.FilePath)
	promptBuilder.WriteString("\n")
	promptBuilder.WriteString(strings.Join(trimmedLines, "\n"))
	promptBuilder.WriteString("\n")

	// Add updated/{file_path} marker - model generates from here
	promptBuilder.WriteString("<|file_sep|>updated/")
	promptBuilder.WriteString(req.FilePath)
	promptBuilder.WriteString("\n")

	return promptBuilder.String(), windowStart, windowEnd
}

// buildDiffSection builds the diff history section in Sweep's format.
// Diff entries are already trimmed by the engine using max_diff_history_tokens.
func (p *Provider) buildDiffSection(req *types.CompletionRequest) string {
	if len(req.FileDiffHistories) == 0 {
		return ""
	}

	var builder strings.Builder

	for _, fileHistory := range req.FileDiffHistories {
		for _, diffEntry := range fileHistory.DiffHistory {
			if diffEntry.Original == "" && diffEntry.Updated == "" {
				continue
			}

			builder.WriteString("<|file_sep|>")
			builder.WriteString(fileHistory.FileName)
			builder.WriteString(".diff\n")
			builder.WriteString("original:\n")
			builder.WriteString(diffEntry.Original)
			builder.WriteString("\nupdated:\n")
			builder.WriteString(diffEntry.Updated)
			builder.WriteString("\n")
		}
	}

	return builder.String()
}

// getTrimmedOriginalContent returns the original content (before most recent change)
// trimmed to match the same window as the current content
func (p *Provider) getTrimmedOriginalContent(req *types.CompletionRequest, trimOffset, lineCount int) []string {
	// Use PreviousLines if available, otherwise fall back to current Lines
	sourceLines := req.PreviousLines
	if len(sourceLines) == 0 {
		sourceLines = req.Lines
	}

	// Calculate window bounds
	windowStart := trimOffset
	windowEnd := trimOffset + lineCount

	// Clamp to available lines
	if windowStart >= len(sourceLines) {
		return []string{}
	}
	if windowEnd > len(sourceLines) {
		windowEnd = len(sourceLines)
	}

	return sourceLines[windowStart:windowEnd]
}

// parseCompletion parses the model's completion (the predicted updated content)
// finishReason indicates why the model stopped: "stop" (hit stop token) or "length" (hit max_tokens)
// windowStart and windowEnd are 0-indexed line bounds from buildPrompt
func (p *Provider) parseCompletion(req *types.CompletionRequest, completionText string, finishReason string, windowStart, windowEnd int) *types.Completion {
	// The model outputs the updated window content directly
	// Strip any trailing <|file_sep|> or </s> tokens that might have leaked
	completionText = strings.TrimSuffix(completionText, "<|file_sep|>")
	completionText = strings.TrimSuffix(completionText, "</s>")
	// Trim only leading newlines (preserve indentation) and trailing whitespace
	completionText = strings.TrimLeft(completionText, "\n")
	completionText = strings.TrimRight(completionText, " \t\n\r")

	// Bounds check for window (handles empty file and edge cases)
	if windowStart < 0 {
		windowStart = 0
	}
	if windowEnd > len(req.Lines) {
		windowEnd = len(req.Lines)
	}
	if windowStart >= windowEnd || windowStart >= len(req.Lines) {
		return nil
	}

	// Get the original window content for comparison
	oldLines := req.Lines[windowStart:windowEnd]
	oldText := strings.TrimRight(strings.Join(oldLines, "\n"), " \t\n\r")

	// If the new text equals old text, no completion needed
	if completionText == oldText {
		return nil
	}

	// Split new text into lines
	newLines := strings.Split(completionText, "\n")

	// Handle truncated output (finish_reason == "length") with anchor matching
	processedLines, endLineInc, shouldReject := utils.HandleTruncatedCompletionWithAnchor(newLines, oldLines, finishReason, windowStart, windowEnd)
	if shouldReject {
		logger.Debug("sweep completion rejected: only truncated content")
		return nil
	}
	newLines = processedLines

	// Log if we had to adjust for truncation
	if finishReason == "length" {
		originalLineCount := len(strings.Split(completionText, "\n"))
		logger.Info("sweep completion truncated: dropped last line, replacing lines %d-%d only (window was %d-%d, original_lines=%d, kept_lines=%d)",
			windowStart+1, endLineInc, windowStart+1, windowEnd, originalLineCount, len(newLines))
	}

	// Final check: if after processing the text equals the portion we're replacing, no completion needed
	replaceEnd := min(endLineInc-windowStart, len(oldLines))
	if utils.IsNoOpReplacement(newLines, oldLines[:replaceEnd]) {
		return nil
	}

	return &types.Completion{
		StartLine:  windowStart + 1, // Convert back to 1-indexed
		EndLineInc: endLineInc,
		Lines:      newLines,
	}
}
