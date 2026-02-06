package ctx

import (
	"context"

	"cursortab/buffer"
	"cursortab/types"
)

// treesitter gathers scope context from Neovim's built-in treesitter.
type treesitter struct {
	buffer *buffer.NvimBuffer
}

func (t *treesitter) Gather(_ context.Context, req *SourceRequest) *types.ContextResult {
	ts := t.buffer.TreesitterSymbols(req.CursorRow, req.CursorCol, req.MaxSiblings)
	if ts == nil {
		return nil
	}
	return &types.ContextResult{Treesitter: ts}
}
