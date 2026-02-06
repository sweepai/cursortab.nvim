package ctx

import (
	"context"

	"cursortab/buffer"
	"cursortab/types"
)

// diagnostics gathers LSP diagnostics from the buffer.
type diagnostics struct {
	buffer *buffer.NvimBuffer
}

func (d *diagnostics) Gather(_ context.Context, _ *SourceRequest) *types.ContextResult {
	errs := d.buffer.LinterErrors()
	if errs == nil {
		return nil
	}
	return &types.ContextResult{Diagnostics: errs}
}
