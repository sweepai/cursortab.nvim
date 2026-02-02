package sweep

import (
	"net/http"
	"strings"

	"cursortab/client/openai"
	"cursortab/provider"
	"cursortab/types"
)

// userAgentTransport wraps an http.RoundTripper to inject a User-Agent header.
type userAgentTransport struct {
	base      http.RoundTripper
	userAgent string
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", t.userAgent)
	return t.base.RoundTrip(req)
}

// NewProvider creates a new Sweep Next-Edit model provider
func NewProvider(config *types.ProviderConfig, editorInfo string) *provider.Provider {
	client := openai.NewClient(config.ProviderURL, config.CompletionPath, config.APIKey)
	if editorInfo != "" {
		client.HTTPClient.Transport = &userAgentTransport{
			base:      http.DefaultTransport,
			userAgent: editorInfo,
		}
	}
	return &provider.Provider{
		Name:          "sweep",
		Config:        config,
		Client:        client,
		StreamingType: provider.StreamingLines,
		Preprocessors: []provider.Preprocessor{
			provider.TrimContent(),
		},
		DiffBuilder: provider.FormatDiffHistoryOriginalUpdated("<|file_sep|>%s.diff\n"),
		PromptBuilder: buildPrompt,
		Postprocessors: []provider.Postprocessor{
			provider.RejectEmpty(),
			provider.ValidateAnchorPosition(0.25),
			provider.AnchorTruncation(0.75),
			parseCompletion,
		},
		Validators: []provider.Validator{
			provider.ValidateFirstLineAnchor(0.25),
		},
		StopTokens: []string{"<|file_sep|>", "</s>"},
	}
}

func buildPrompt(p *provider.Provider, ctx *provider.Context) *openai.CompletionRequest {
	req := ctx.Request
	var promptBuilder strings.Builder

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

		return &openai.CompletionRequest{
			Model:       p.Config.ProviderModel,
			Prompt:      promptBuilder.String(),
			Temperature: p.Config.ProviderTemperature,
			MaxTokens:   p.Config.ProviderMaxTokens,
			TopK:        p.Config.ProviderTopK,
			Stop:        []string{"<|file_sep|>", "</s>"},
			N:           1,
			Echo:        false,
		}
	}

	diffSection := ""
	if p.DiffBuilder != nil {
		diffSection = p.DiffBuilder(req.FileDiffHistories)
	}
	originalLines := getTrimmedOriginalContent(req, ctx.WindowStart, len(ctx.TrimmedLines))

	if diffSection != "" {
		promptBuilder.WriteString(diffSection)
	}

	promptBuilder.WriteString("<|file_sep|>original/")
	promptBuilder.WriteString(req.FilePath)
	promptBuilder.WriteString("\n")
	promptBuilder.WriteString(strings.Join(originalLines, "\n"))
	promptBuilder.WriteString("\n")

	promptBuilder.WriteString("<|file_sep|>current/")
	promptBuilder.WriteString(req.FilePath)
	promptBuilder.WriteString("\n")
	promptBuilder.WriteString(strings.Join(ctx.TrimmedLines, "\n"))
	promptBuilder.WriteString("\n")

	promptBuilder.WriteString("<|file_sep|>updated/")
	promptBuilder.WriteString(req.FilePath)
	promptBuilder.WriteString("\n")

	return &openai.CompletionRequest{
		Model:       p.Config.ProviderModel,
		Prompt:      promptBuilder.String(),
		Temperature: p.Config.ProviderTemperature,
		MaxTokens:   p.Config.ProviderMaxTokens,
		TopK:        p.Config.ProviderTopK,
		Stop:        []string{"<|file_sep|>", "</s>"},
		N:           1,
		Echo:        false,
	}
}

func getTrimmedOriginalContent(req *types.CompletionRequest, trimOffset, lineCount int) []string {
	sourceLines := req.PreviousLines
	if len(sourceLines) == 0 {
		sourceLines = req.Lines
	}

	windowStart := trimOffset
	windowEnd := trimOffset + lineCount

	if windowStart >= len(sourceLines) {
		return []string{}
	}
	if windowEnd > len(sourceLines) {
		windowEnd = len(sourceLines)
	}

	return sourceLines[windowStart:windowEnd]
}

func parseCompletion(p *provider.Provider, ctx *provider.Context) (*types.CompletionResponse, bool) {
	completionText := ctx.Result.Text
	req := ctx.Request

	completionText = strings.TrimSuffix(completionText, "<|file_sep|>")
	completionText = strings.TrimSuffix(completionText, "</s>")
	completionText = strings.TrimRight(completionText, " \t\n\r")

	windowStart := ctx.WindowStart
	windowEnd := ctx.WindowEnd
	if windowStart < 0 {
		windowStart = 0
	}
	if windowEnd > len(req.Lines) {
		windowEnd = len(req.Lines)
	}
	if windowStart >= windowEnd || windowStart >= len(req.Lines) {
		return p.EmptyResponse(), true
	}

	oldLines := req.Lines[windowStart:windowEnd]
	oldText := strings.TrimRight(strings.Join(oldLines, "\n"), " \t\n\r")

	if completionText == oldText {
		return p.EmptyResponse(), true
	}

	newLines := strings.Split(completionText, "\n")

	endLineInc := ctx.EndLineInc
	if endLineInc == 0 {
		endLineInc = min(windowStart+len(newLines), windowEnd)
	}

	return p.BuildCompletion(ctx, windowStart+1, endLineInc, newLines)
}
