package fim

import (
	"cursortab/assert"
	"cursortab/client/openai"
	"cursortab/provider"
	"cursortab/types"
	"strings"
	"testing"
)

func TestGetFIMTokens(t *testing.T) {
	config := &types.ProviderConfig{
		FIMTokens: types.FIMTokenConfig{
			Prefix: "<PRE>",
			Suffix: "<SUF>",
			Middle: "<MID>",
		},
	}

	prefix, suffix, middle := getFIMTokens(config)

	assert.Equal(t, "<PRE>", prefix, "prefix token")
	assert.Equal(t, "<SUF>", suffix, "suffix token")
	assert.Equal(t, "<MID>", middle, "middle token")
}

func TestBuildPrompt_EmptyLines(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
		FIMTokens: types.FIMTokenConfig{
			Prefix: "<PRE>",
			Suffix: "<SUF>",
			Middle: "<MID>",
		},
	}
	p := NewProvider(config)

	ctx := &provider.Context{
		Request:      &types.CompletionRequest{},
		TrimmedLines: []string{},
		CursorLine:   0,
	}

	req := p.PromptBuilder(p, ctx)

	assert.Equal(t, "<PRE><SUF><MID>", req.Prompt, "empty prompt should have FIM tokens only")
}

func TestBuildPrompt_SingleLineMiddle(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
		FIMTokens: types.FIMTokenConfig{
			Prefix: "<PRE>",
			Suffix: "<SUF>",
			Middle: "<MID>",
		},
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

	assert.True(t, strings.HasPrefix(req.Prompt, "<PRE>hello"), "prefix should have content before cursor")
	assert.True(t, strings.Contains(req.Prompt, "<SUF> world"), "suffix should have content after cursor")
	assert.True(t, strings.HasSuffix(req.Prompt, "<MID>"), "should end with middle token")
}

func TestBuildPrompt_MultiLine(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
		FIMTokens: types.FIMTokenConfig{
			Prefix: "<PRE>",
			Suffix: "<SUF>",
			Middle: "<MID>",
		},
	}
	p := NewProvider(config)

	ctx := &provider.Context{
		Request: &types.CompletionRequest{
			CursorCol: 4,
		},
		TrimmedLines: []string{"line 1", "line 2", "line 3"},
		CursorLine:   1,
	}

	req := p.PromptBuilder(p, ctx)

	// Should have line 1 before cursor, partial line 2 before cursor
	// And rest of line 2 + line 3 after cursor
	assert.True(t, strings.Contains(req.Prompt, "line 1\n"), "should include line before cursor")
	assert.True(t, strings.Contains(req.Prompt, "<PRE>line 1\nline"), "prefix with lines before")
	assert.True(t, strings.Contains(req.Prompt, "<SUF> 2\nline 3"), "suffix with lines after")
}

func TestBuildPrompt_CursorBeyondLine(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
		FIMTokens: types.FIMTokenConfig{
			Prefix: "<PRE>",
			Suffix: "<SUF>",
			Middle: "<MID>",
		},
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

	assert.True(t, strings.Contains(req.Prompt, "<PRE>short<SUF><MID>"), "should handle cursor beyond line")
}

func TestParseCompletion_SingleLine(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
	}
	p := NewProvider(config)

	ctx := &provider.Context{
		Request: &types.CompletionRequest{
			Lines:     []string{"hello world"},
			CursorRow: 1,
			CursorCol: 5,
		},
		Result: &openai.StreamResult{
			Text: " there",
		},
	}

	resp, ok := parseCompletion(p, ctx)

	assert.True(t, ok, "should succeed")
	assert.NotNil(t, resp, "response should not be nil")
	assert.Len(t, 1, resp.Completions, "completions count")
	// "hello" + " there" + " world"
	assert.Equal(t, "hello there world", resp.Completions[0].Lines[0], "completion inserted at cursor")
}

func TestParseCompletion_MultiLineCompletion(t *testing.T) {
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
			Text: "\n  fmt.Println()\n",
		},
	}

	resp, ok := parseCompletion(p, ctx)

	assert.True(t, ok, "should succeed")
	assert.Len(t, 1, resp.Completions, "completions count")
	assert.Equal(t, 3, len(resp.Completions[0].Lines), "should have 3 lines")
	assert.Equal(t, "func main() {", resp.Completions[0].Lines[0], "first line")
	assert.Equal(t, "  fmt.Println()", resp.Completions[0].Lines[1], "middle line")
}

func TestParseCompletion_SingleLineWithAfterCursor(t *testing.T) {
	config := &types.ProviderConfig{
		ProviderModel: "test-model",
	}
	p := NewProvider(config)

	ctx := &provider.Context{
		Request: &types.CompletionRequest{
			Lines:     []string{"func()"},
			CursorRow: 1,
			CursorCol: 4, // After "func"
		},
		Result: &openai.StreamResult{
			Text: "tion",
		},
	}

	resp, ok := parseCompletion(p, ctx)

	assert.True(t, ok, "should succeed")
	assert.NotNil(t, resp, "response should not be nil")
	// "func" + "tion" + "()"
	assert.Equal(t, "function()", resp.Completions[0].Lines[0], "completion inserted at cursor with suffix")
}
