package ctx

import (
	"context"
	"sync"
	"time"

	"cursortab/buffer"
	"cursortab/types"
)

// GatherTimeout is the maximum time allowed for all context sources to complete.
const GatherTimeout = 200 * time.Millisecond

// SourceRequest contains metadata passed to each context source.
type SourceRequest struct {
	FilePath string
}

// NewGatherer creates a Gatherer with all built-in context sources.
func NewGatherer(buf *buffer.NvimBuffer) *Gatherer {
	return &Gatherer{
		sources: []source{
			&diagnostics{buffer: buf},
		},
	}
}

// source gathers additional context for completion requests.
type source interface {
	Gather(ctx context.Context, req *SourceRequest) *types.ContextResult
}

// Gatherer runs context sources in parallel and merges their results.
type Gatherer struct {
	sources []source
}

// Gather runs all sources in parallel with a shared timeout
// and merges their results into a single ContextResult.
func (g *Gatherer) Gather(ctx context.Context, req *SourceRequest) *types.ContextResult {
	if len(g.sources) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, GatherTimeout)
	defer cancel()

	results := make([]*types.ContextResult, len(g.sources))
	var wg sync.WaitGroup

	for i, s := range g.sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = s.Gather(ctx, req)
		}()
	}

	wg.Wait()

	var merged *types.ContextResult
	for _, r := range results {
		if r == nil {
			continue
		}
		if merged == nil {
			merged = &types.ContextResult{}
		}
		if r.Diagnostics != nil {
			merged.Diagnostics = r.Diagnostics
		}
	}

	return merged
}
