package inline

import (
	"cursortab/assert"
	"cursortab/client/openai"
	"cursortab/provider"
	"cursortab/types"
	"testing"
)

func TestBuildPrompt_EmptyLines(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel:       "test-model",
		ProviderTemperature: 0.5,
		ProviderMaxTokens:   50,
	}
	p := NewProvider(config)

	ctx := &provider.Context{
		Request:      &types.CompletionRequest{},
		TrimmedLines: []string{},
		CursorLine:   0,
	}

	req := p.PromptBuilder(p, ctx)

	assert.Equal(t, "", req.Prompt, "prompt should be empty")
	assert.Equal(t, "test-model", req.Model, "model")
	assert.Equal(t, 0.5, req.Temperature, "temperature")
	assert.Equal(t, 50, req.MaxTokens, "max tokens")
}

func TestBuildPrompt_SingleLine(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
	}
	p := NewProvider(config)

	ctx := &provider.Context{
		Request: &types.CompletionRequest{
			CursorCol: 5,
		},
		TrimmedLines: []string{"hello world"},
		CursorLine:   0,
	}

	req := p.PromptBuilder(p, ctx)

	assert.Equal(t, "hello", req.Prompt, "prompt should include text before cursor")
}

func TestBuildPrompt_MultiLine(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
	}
	p := NewProvider(config)

	ctx := &provider.Context{
		Request: &types.CompletionRequest{
			CursorCol: 4,
		},
		TrimmedLines: []string{"line 1", "line 2", "line 3"},
		CursorLine:   2,
	}

	req := p.PromptBuilder(p, ctx)

	expected := "line 1\nline 2\nline"
	assert.Equal(t, expected, req.Prompt, "prompt should include lines before and partial current line")
}

func TestBuildPrompt_CursorBeyondLineLength(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
	}
	p := NewProvider(config)

	ctx := &provider.Context{
		Request: &types.CompletionRequest{
			CursorCol: 100, // Beyond line length
		},
		TrimmedLines: []string{"short"},
		CursorLine:   0,
	}

	req := p.PromptBuilder(p, ctx)

	assert.Equal(t, "short", req.Prompt, "prompt should include entire line when cursor is beyond")
}

func TestParseCompletion(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
	}
	p := NewProvider(config)

	ctx := &provider.Context{
		Request: &types.CompletionRequest{
			Lines:     []string{"func main() {"},
			CursorRow: 1,
			CursorCol: 13,
		},
		Result: &openai.StreamResult{
			Text: " fmt.Println()",
		},
	}

	resp, ok := parseCompletion(p, ctx)

	assert.True(t, ok, "should succeed")
	assert.NotNil(t, resp, "response should not be nil")
	assert.Equal(t, 1, len(resp.Completions), "should have 1 completion")
	assert.Equal(t, "func main() { fmt.Println()", resp.Completions[0].Lines[0], "completion merged with line")
}

func TestParseCompletion_CursorClamped(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
	}
	p := NewProvider(config)

	ctx := &provider.Context{
		Request: &types.CompletionRequest{
			Lines:     []string{"abc"},
			CursorRow: 1,
			CursorCol: 100, // Beyond line length
		},
		Result: &openai.StreamResult{
			Text: "def",
		},
	}

	resp, ok := parseCompletion(p, ctx)

	assert.True(t, ok, "should succeed")
	assert.Equal(t, "abcdef", resp.Completions[0].Lines[0], "cursor clamped to line end")
}
