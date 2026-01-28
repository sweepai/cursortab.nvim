package provider

import (
	"cursortab/client/openai"
	"cursortab/logger"
	"cursortab/text"
	"cursortab/types"
	"cursortab/utils"
	"errors"
	"strings"
)

// Preprocessor processes the context before prompt building.
// Return ErrSkipCompletion to skip without error, or another error to fail.
type Preprocessor func(p *Provider, ctx *Context) error

// PromptBuilder builds the completion request from the context
type PromptBuilder func(p *Provider, ctx *Context) *openai.CompletionRequest

// Postprocessor processes the completion result.
// Returns (response, done) - if done is true, the response is returned immediately.
type Postprocessor func(p *Provider, ctx *Context) (*types.CompletionResponse, bool)

// ErrSkipCompletion is a sentinel error that preprocessors return to skip
// completion without treating it as an error.
var ErrSkipCompletion = errors.New("skip completion")

// --- Preprocessors ---

// TrimContent returns a preprocessor that trims content around the cursor
func TrimContent() Preprocessor {
	return func(p *Provider, ctx *Context) error {
		cursorLine := ctx.Request.CursorRow - 1
		trimmedLines, newCursorLine, _, trimOffset, didTrim := utils.TrimContentAroundCursor(
			ctx.Request.Lines,
			cursorLine,
			ctx.Request.CursorCol,
			p.Config.ProviderMaxTokens,
		)
		ctx.TrimmedLines = trimmedLines
		ctx.CursorLine = newCursorLine
		ctx.WindowStart = trimOffset
		ctx.WindowEnd = trimOffset + len(trimmedLines)

		if didTrim {
			ctx.MaxLines = len(trimmedLines)
		}
		if ctx.Request.ViewportHeight > 0 {
			if ctx.MaxLines == 0 || ctx.Request.ViewportHeight < ctx.MaxLines {
				ctx.MaxLines = ctx.Request.ViewportHeight
			}
		}
		return nil
	}
}

// SkipIfTextAfterCursor returns a preprocessor that skips if there's text after cursor
func SkipIfTextAfterCursor() Preprocessor {
	return func(p *Provider, ctx *Context) error {
		req := ctx.Request
		if req.CursorRow >= 1 && req.CursorRow <= len(req.Lines) {
			currentLine := req.Lines[req.CursorRow-1]
			if req.CursorCol < len(currentLine) {
				logger.Debug("%s: skipping, text after cursor", p.Name)
				return ErrSkipCompletion
			}
		}
		return nil
	}
}

// --- Postprocessors ---

// RejectEmpty returns a postprocessor that rejects empty completions
func RejectEmpty() Postprocessor {
	return func(p *Provider, ctx *Context) (*types.CompletionResponse, bool) {
		if strings.TrimSpace(ctx.Result.Text) == "" {
			logger.Debug("%s: rejected, empty or whitespace-only", p.Name)
			return p.EmptyResponse(), true
		}
		return nil, false
	}
}

// RejectTruncated returns a postprocessor that rejects truncated completions
func RejectTruncated() Postprocessor {
	return func(p *Provider, ctx *Context) (*types.CompletionResponse, bool) {
		if ctx.Result.FinishReason == "length" {
			logger.Info("%s: rejected, truncated (finish_reason=length)", p.Name)
			return p.EmptyResponse(), true
		}
		return nil, false
	}
}

// DropLastLineIfTruncated returns a postprocessor that drops incomplete last line.
// Sets ctx.EndLineInc for use by subsequent postprocessors.
func DropLastLineIfTruncated() Postprocessor {
	return func(p *Provider, ctx *Context) (*types.CompletionResponse, bool) {
		if ctx.Result.FinishReason != "length" && !ctx.Result.StoppedEarly {
			return nil, false
		}

		lines := strings.Split(ctx.Result.Text, "\n")
		originalLineCount := len(lines)

		if len(lines) <= 1 {
			logger.Info("%s: rejected, truncated single line", p.Name)
			return p.EmptyResponse(), true
		}

		lines = lines[:len(lines)-1]
		ctx.Result.Text = strings.Join(lines, "\n")

		if strings.TrimSpace(ctx.Result.Text) == "" {
			logger.Info("%s: rejected, empty after dropping truncated line", p.Name)
			return p.EmptyResponse(), true
		}

		ctx.EndLineInc = ctx.WindowStart + len(lines)
		logger.Info("%s: truncated, dropped last line (%d -> %d lines)",
			p.Name, originalLineCount, len(lines))
		return nil, false
	}
}

// AnchorTruncation returns a postprocessor that handles truncation with anchor matching.
// Sets ctx.EndLineInc for use by subsequent postprocessors.
func AnchorTruncation(threshold float64) Postprocessor {
	return func(p *Provider, ctx *Context) (*types.CompletionResponse, bool) {
		if ctx.Result.FinishReason != "length" && !ctx.Result.StoppedEarly {
			return nil, false
		}

		finishReason := ctx.Result.FinishReason
		if ctx.Result.StoppedEarly {
			finishReason = "length"
		}

		newLines := strings.Split(ctx.Result.Text, "\n")
		originalLineCount := len(newLines)
		oldLines := ctx.Request.Lines[ctx.WindowStart:ctx.WindowEnd]

		processedLines, endLineInc, shouldReject := handleTruncatedCompletionWithAnchor(
			newLines, oldLines, finishReason, ctx.WindowStart, ctx.WindowEnd,
		)
		if shouldReject {
			logger.Debug("%s: rejected, truncation handling failed", p.Name)
			return p.EmptyResponse(), true
		}

		if len(oldLines) > 10 {
			minAllowedLines := int(float64(len(oldLines)) * threshold)
			if len(processedLines) < minAllowedLines {
				logger.Debug("%s: rejected, too few lines (%d < %d min)",
					p.Name, len(processedLines), minAllowedLines)
				return p.EmptyResponse(), true
			}
		}

		ctx.Result.Text = strings.Join(processedLines, "\n")
		ctx.EndLineInc = endLineInc

		logger.Info("%s: truncated, replacing lines %d-%d (%d -> %d lines)",
			p.Name, ctx.WindowStart+1, endLineInc, originalLineCount, len(processedLines))
		return nil, false
	}
}

// ValidateAnchorPosition returns a postprocessor that validates first line anchors near start
func ValidateAnchorPosition(maxAnchorRatio float64) Postprocessor {
	return func(p *Provider, ctx *Context) (*types.CompletionResponse, bool) {
		newLines := strings.Split(ctx.Result.Text, "\n")
		oldLines := ctx.Request.Lines[ctx.WindowStart:ctx.WindowEnd]

		if len(newLines) == 0 || len(oldLines) <= 10 {
			return nil, false
		}

		firstLineAnchor := findAnchorLineFullSearch(newLines[0], oldLines)
		maxAllowedAnchor := int(float64(len(oldLines)) * maxAnchorRatio)

		if firstLineAnchor > maxAllowedAnchor {
			logger.Debug("%s: rejected, first line anchors at %d (max allowed %d)",
				p.Name, firstLineAnchor, maxAllowedAnchor)
			return p.EmptyResponse(), true
		}

		return nil, false
	}
}

// --- Helper functions ---

// findAnchorLine searches for the best matching line in oldLines for the given needle.
// Searches in a window around expectedPos to handle structural changes (adds/removes).
// Returns the index in oldLines or -1 if no good match found.
func findAnchorLine(needle string, oldLines []string, expectedPos int) int {
	if len(oldLines) == 0 {
		return -1
	}

	bestIdx := -1
	bestSimilarity := 0.7

	searchStart := max(0, expectedPos-2)
	searchEnd := min(len(oldLines), expectedPos+5)

	for i := searchStart; i < searchEnd; i++ {
		similarity := text.LineSimilarity(needle, oldLines[i])
		if similarity > bestSimilarity {
			bestSimilarity = similarity
			bestIdx = i
		}
	}

	return bestIdx
}

// findAnchorLineFullSearch searches the entire oldLines array for the best matching line.
// Used for validation to detect if model output is misaligned with expected window.
// Returns the index in oldLines or -1 if no good match found.
func findAnchorLineFullSearch(needle string, oldLines []string) int {
	if len(oldLines) == 0 {
		return -1
	}

	bestIdx := -1
	bestSimilarity := 0.7

	for i, line := range oldLines {
		similarity := text.LineSimilarity(needle, line)
		if similarity > bestSimilarity {
			bestSimilarity = similarity
			bestIdx = i
		}
	}

	return bestIdx
}

// handleTruncatedCompletionWithAnchor processes completion lines when the model hits max_tokens,
// using anchor matching to find the correct replacement range.
func handleTruncatedCompletionWithAnchor(
	newLines []string,
	oldLines []string,
	finishReason string,
	windowStart, windowEnd int,
) ([]string, int, bool) {
	endLineInc := windowEnd

	if finishReason == "length" && len(newLines) > 0 {
		newLines = newLines[:len(newLines)-1]

		if len(newLines) == 0 {
			return nil, 0, true
		}

		lastModelLine := newLines[len(newLines)-1]
		expectedPos := len(newLines) - 1
		anchorIdx := findAnchorLine(lastModelLine, oldLines, expectedPos)

		if anchorIdx != -1 {
			endLineInc = windowStart + anchorIdx + 1
		} else {
			endLineInc = windowStart + len(newLines)
		}
	}

	return newLines, endLineInc, false
}

// IsNoOpReplacement checks if replacing oldLines with newLines would result in no change.
func IsNoOpReplacement(newLines, oldLines []string) bool {
	newText := strings.TrimRight(strings.Join(newLines, "\n"), " \t\n\r")
	oldText := strings.TrimRight(strings.Join(oldLines, "\n"), " \t\n\r")
	return newText == oldText
}
