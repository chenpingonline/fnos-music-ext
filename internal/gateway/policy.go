package gateway

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

func (g *Gateway) SetPreferences(s *preferences.Store) { g.settings = s }
func (g *Gateway) policy() preferences.Config {
	if g.settings != nil {
		return g.settings.Snapshot()
	}
	return preferences.Defaults()
}
func (g *Gateway) revision() uint64 {
	if g.settings != nil {
		return g.settings.Revision()
	}
	return 0
}
func playQuality(ctx context.Context, source provider.Adapter, user provider.User, id, mode, byteRange string) (provider.Stream, error) {
	var last error
	for _, q := range preferences.QualityOrder(mode) {
		if ctx.Err() != nil {
			return provider.Stream{}, ctx.Err()
		}
		s, e := source.Play(ctx, user, provider.PlaybackRequest{TrackID: id, Quality: q, Range: byteRange})
		if e == nil && s.Body != nil && (s.StatusCode == 200 || s.StatusCode == 206 || s.StatusCode == 416) {
			return s, nil
		}
		if s.Body != nil {
			_ = s.Body.Close()
		}
		if e == nil {
			e = fmt.Errorf("source status %d", s.StatusCode)
		}
		diagnostics.Record("playback-source-"+q, user.FnOSGUID+"\x00"+id, e)
		last = e
	}
	return provider.Stream{}, last
}

type searchPool struct {
	mu                   sync.Mutex
	tracks               []provider.Track
	offset, total, pages int
	done                 bool
	expires              time.Time
}

func (g *Gateway) searchWindow(ctx context.Context, user provider.User, source provider.Adapter, scope, keyword string, start, count int) (provider.SearchPage, error) {
	return g.searchWindowSized(ctx, user, source, scope, keyword, start, count, 100)
}

func (g *Gateway) searchWindowSized(ctx context.Context, user provider.User, source provider.Adapter, scope, keyword string, start, count, batchSize int) (provider.SearchPage, error) {
	return g.searchWindowLimited(ctx, user, source, scope, keyword, start, count, batchSize, 0)
}

func (g *Gateway) searchWindowLimited(ctx context.Context, user provider.User, source provider.Adapter, scope, keyword string, start, count, batchSize, candidateLimit int) (provider.SearchPage, error) {
	c := g.policy()
	budget := c.MaxPages * 100
	if !c.DeepPage {
		budget = 100
	}
	if candidateLimit > 0 {
		budget = min(budget, candidateLimit)
	}
	limitPages := budget / batchSize
	key := fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%d\x00%d", user.FnOSGUID, scope, g.revision(), keyword, batchSize, budget)
	g.searchMu.Lock()
	if g.searches == nil {
		g.searches = map[string]*searchPool{}
	}
	for k, v := range g.searches {
		if time.Now().After(v.expires) {
			delete(g.searches, k)
		}
	}
	pool := g.searches[key]
	if pool == nil {
		if len(g.searches) >= 256 {
			for k := range g.searches {
				delete(g.searches, k)
				break
			}
		}
		pool = &searchPool{expires: time.Now().Add(5 * time.Minute)}
		g.searches[key] = pool
	}
	g.searchMu.Unlock()
	pool.mu.Lock()
	defer pool.mu.Unlock()
	for !pool.done && len(pool.tracks) < start+count && pool.pages < limitPages {
		result, e := source.Search(ctx, user, provider.SearchRequest{Keyword: keyword, Offset: pool.offset, Limit: batchSize})
		if e != nil {
			return pool.window(start, count, len(pool.tracks)), e
		}
		if len(result.Tracks) == 0 {
			pool.done = true
			break
		}
		seen := map[string]bool{}
		for _, t := range pool.tracks {
			seen[t.ID] = true
			seen["meta:"+trackKey(t)] = true
		}
		for _, t := range result.Tracks {
			if t.Source == "" {
				t.Source = source.ID()
			}
			_, valid := fnmusic.EncodeTrackID(t.Source, t.ID)
			if t.Source != source.ID() {
				continue
			}
			if !valid || !t.Playable || seen[t.ID] || seen["meta:"+trackKey(t)] {
				continue
			}
			if c.SearchProbe {
				probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				s, err := playQuality(probeCtx, source, user, t.ID, c.Quality, "__HEAD__")
				if s.Body != nil {
					io.Copy(io.Discard, io.LimitReader(s.Body, 1))
					s.Body.Close()
				}
				cancel()
				if err != nil || (s.StatusCode != 200 && s.StatusCode != 206) {
					if ctx.Err() != nil {
						return pool.window(start, count, len(pool.tracks)), ctx.Err()
					}
					continue
				}
			}
			seen[t.ID] = true
			seen["meta:"+trackKey(t)] = true
			pool.tracks = append(pool.tracks, t)
		}
		pool.offset += len(result.Tracks)
		pool.total = result.Total
		pool.pages++
		pool.done = !result.HasMore || pool.offset >= result.Total
	}
	total := pool.total
	if pool.done || !c.DeepPage || pool.pages >= limitPages {
		total = len(pool.tracks)
	}
	if total < len(pool.tracks) {
		total = len(pool.tracks)
	}
	return pool.window(start, count, total), nil
}

// An interrupted later page must not discard tracks from completed pages or
// successful probes. The caller receives both the usable prefix and the error.
func (pool *searchPool) window(start, count, total int) provider.SearchPage {
	lo := start
	if lo > len(pool.tracks) {
		lo = len(pool.tracks)
	}
	hi := start + count
	if hi > len(pool.tracks) {
		hi = len(pool.tracks)
	}
	return provider.SearchPage{Tracks: append([]provider.Track{}, pool.tracks[lo:hi]...), Total: total, HasMore: hi < total}
}
func trackKey(t provider.Track) string {
	return strings.ToLower(strings.TrimSpace(t.Title)) + "\x00" + strings.ToLower(strings.Join(t.Artists, " ")) + "\x00" + strings.ToLower(strings.TrimSpace(t.Album)) + fmt.Sprintf("\x00%d", t.DurationMS)
}
