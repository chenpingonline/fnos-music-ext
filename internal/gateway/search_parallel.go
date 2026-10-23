package gateway

import (
	"context"
	"sync"

	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

// Read the first page to learn the actual total before scheduling more work.
// Page requests are bounded across sources and within each source. Results are
// consumed in page order, never completion order; a failed page does not erase
// successful pages from either the same source or another source.
func (g *Gateway) parallelQuickSource(ctx context.Context, user provider.User, source provider.Adapter, keyword string, budget int, slots chan struct{}) (provider.SearchPage, error) {
	type pageResult struct {
		page     provider.SearchPage
		err      error
		rawCount int
	}
	cfg := g.policy()
	sourceSlots := make(chan struct{}, 2)
	fetch := func(offset, limit int) pageResult {
		select {
		case sourceSlots <- struct{}{}:
		case <-ctx.Done():
			return pageResult{err: ctx.Err()}
		}
		defer func() { <-sourceSlots }()
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return pageResult{err: ctx.Err()}
		}
		defer func() { <-slots }()
		if err := ctx.Err(); err != nil {
			return pageResult{err: err}
		}
		page, err := source.Search(ctx, user, provider.SearchRequest{Keyword: keyword, Offset: offset, Limit: limit})
		if len(page.Tracks) > limit {
			page.Tracks = page.Tracks[:limit]
		}
		rawCount := len(page.Tracks)
		tracks, filterErr := filterSearchTracks(ctx, user, source, page.Tracks, map[string]bool{}, cfg)
		page.Tracks = tracks
		if err == nil {
			err = filterErr
		}
		return pageResult{page: page, err: err, rawCount: rawCount}
	}
	first := fetch(0, min(budget, rankedSearchBatch))
	results := []pageResult{first}
	if first.err == nil && first.page.HasMore && first.rawCount > 0 {
		end := min(budget, first.page.Total)
		next := first.rawCount
		if end > next {
			remaining := (end - next + rankedSearchBatch - 1) / rankedSearchBatch
			results = append(results, make([]pageResult, remaining)...)
			var wg sync.WaitGroup
			for i := 0; i < remaining; i++ {
				offset := next + i*rankedSearchBatch
				wg.Add(1)
				go func(i, offset int) { defer wg.Done(); results[i+1] = fetch(offset, min(rankedSearchBatch, end-offset)) }(i, offset)
			}
			wg.Wait()
		}
	}
	out := provider.SearchPage{Tracks: []provider.Track{}}
	var failure error
	seen := map[string]bool{}
	for _, result := range results {
		if result.err != nil && failure == nil {
			failure = result.err
		}
		for _, track := range result.page.Tracks {
			if seen[track.ID] || seen["meta:"+trackKey(track)] {
				continue
			}
			seen[track.ID], seen["meta:"+trackKey(track)] = true, true
			out.Tracks = append(out.Tracks, track)
		}
	}
	out.Total = len(out.Tracks)
	return out, failure
}
