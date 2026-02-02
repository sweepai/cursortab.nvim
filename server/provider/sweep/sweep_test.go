package sweep

import (
	"cursortab/assert"
	"cursortab/client/openai"
	"cursortab/provider"
	"cursortab/types"
	"strings"
	"testing"
)

func TestBuildPrompt_EmptyLines(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
	}
	p := NewProvider(config, "")

	ctx := &provider.Context{
		Request: &types.CompletionRequest{
			FilePath: "main.go",
			Lines:    []string{},
		},
		TrimmedLines: []string{},
	}

	req := p.PromptBuilder(p, ctx)

	assert.True(t, strings.Contains(req.Prompt, "<|file_sep|>original/main.go"), "should have original marker")
	assert.True(t, strings.Contains(req.Prompt, "<|file_sep|>current/main.go"), "should have current marker")
	assert.True(t, strings.Contains(req.Prompt, "<|file_sep|>updated/main.go"), "should have updated marker")
}

func TestBuildPrompt_WithContent(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
	}
	p := NewProvider(config, "")

	ctx := &provider.Context{
		Request: &types.CompletionRequest{
			FilePath: "main.go",
			Lines:    []string{"line 1", "line 2"},
		},
		TrimmedLines: []string{"line 1", "line 2"},
		WindowStart:  0,
		WindowEnd:    2,
	}

	req := p.PromptBuilder(p, ctx)

	assert.True(t, strings.Contains(req.Prompt, "line 1\nline 2"), "should contain file content")
}

func TestBuildPrompt_WithDiffHistory(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
	}
	p := NewProvider(config, "")

	ctx := &provider.Context{
		Request: &types.CompletionRequest{
			FilePath: "main.go",
			Lines:    []string{"line 1"},
			FileDiffHistories: []*types.FileDiffHistory{
				{
					FileName: "other.go",
					DiffHistory: []*types.DiffEntry{
						{Original: "old code", Updated: "new code"},
					},
				},
			},
		},
		TrimmedLines: []string{"line 1"},
		WindowStart:  0,
		WindowEnd:    1,
	}

	req := p.PromptBuilder(p, ctx)

	assert.True(t, strings.Contains(req.Prompt, "other.go.diff"), "should have diff section")
	assert.True(t, strings.Contains(req.Prompt, "original:\nold code"), "should have original in diff")
	assert.True(t, strings.Contains(req.Prompt, "updated:\nnew code"), "should have updated in diff")
}

func TestGetTrimmedOriginalContent(t *testing.T) {
	tests := []struct {
		name        string
		req         *types.CompletionRequest
		trimOffset  int
		lineCount   int
		wantLen     int
		wantContent string
	}{
		{
			name: "uses previous lines",
			req: &types.CompletionRequest{
				PreviousLines: []string{"prev 1", "prev 2", "prev 3"},
				Lines:         []string{"curr 1", "curr 2"},
			},
			trimOffset:  0,
			lineCount:   3,
			wantLen:     3,
			wantContent: "prev 1",
		},
		{
			name: "falls back to lines",
			req: &types.CompletionRequest{
				PreviousLines: nil,
				Lines:         []string{"curr 1", "curr 2"},
			},
			trimOffset:  0,
			lineCount:   2,
			wantLen:     2,
			wantContent: "curr 1",
		},
		{
			name: "respects window",
			req: &types.CompletionRequest{
				Lines: []string{"line 0", "line 1", "line 2", "line 3"},
			},
			trimOffset:  1,
			lineCount:   2,
			wantLen:     2,
			wantContent: "line 1",
		},
		{
			name: "handles out of bounds",
			req: &types.CompletionRequest{
				Lines: []string{"line 0"},
			},
			trimOffset:  5,
			lineCount:   2,
			wantLen:     0,
			wantContent: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getTrimmedOriginalContent(tt.req, tt.trimOffset, tt.lineCount)
			assert.Equal(t, tt.wantLen, len(result), "length")
			if tt.wantLen > 0 {
				assert.Equal(t, tt.wantContent, result[0], "first element")
			}
		})
	}
}

func TestParseCompletion_NoChange(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
	}
	p := NewProvider(config, "")

	ctx := &provider.Context{
		Request: &types.CompletionRequest{
			Lines: []string{"line 1", "line 2"},
		},
		Result: &openai.StreamResult{
			Text: "line 1\nline 2", // Same as original
		},
		WindowStart: 0,
		WindowEnd:   2,
	}

	resp, ok := parseCompletion(p, ctx)

	assert.True(t, ok, "should succeed")
	assert.Nil(t, resp.Completions, "no completions when text is same")
}

func TestParseCompletion_WithChange(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
	}
	p := NewProvider(config, "")

	ctx := &provider.Context{
		Request: &types.CompletionRequest{
			Lines: []string{"line 1", "line 2"},
		},
		Result: &openai.StreamResult{
			Text: "line 1\nmodified line 2",
		},
		WindowStart: 0,
		WindowEnd:   2,
	}

	resp, ok := parseCompletion(p, ctx)

	assert.True(t, ok, "should succeed")
	assert.NotNil(t, resp, "should have response")
	assert.True(t, len(resp.Completions) > 0, "should have completions")
}

func TestParseCompletion_StripsStopTokens(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
	}
	p := NewProvider(config, "")

	ctx := &provider.Context{
		Request: &types.CompletionRequest{
			Lines: []string{"line 1"},
		},
		Result: &openai.StreamResult{
			Text: "modified line 1<|file_sep|>",
		},
		WindowStart: 0,
		WindowEnd:   1,
	}

	resp, ok := parseCompletion(p, ctx)

	assert.True(t, ok, "should succeed")
	assert.NotNil(t, resp, "should have response")
}

func TestParseCompletion_InvalidWindow(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
	}
	p := NewProvider(config, "")

	ctx := &provider.Context{
		Request: &types.CompletionRequest{
			Lines: []string{"line 1"},
		},
		Result: &openai.StreamResult{
			Text: "modified",
		},
		WindowStart: 5, // Invalid
		WindowEnd:   2,
	}

	resp, ok := parseCompletion(p, ctx)

	assert.True(t, ok, "should succeed but return empty")
	assert.Nil(t, resp.Completions, "should have no completions for invalid window")
}
