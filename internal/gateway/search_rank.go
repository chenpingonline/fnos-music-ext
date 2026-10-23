package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

const rankedSearchBatch = 20
const searchSourceTimeout = 3 * time.Second
const unpagedSearchTimeout = 3 * time.Second
const unpagedSourceBudget = 100

type localSearchSeed struct {
	rows         []json.RawMessage
	start, total int
}

func (s *localSearchSeed) window(offset int) ([]json.RawMessage, int, bool) {
	if s == nil {
		return nil, 0, false
	}
	end := min(offset+rankedSearchBatch, s.total)
	if offset < s.start || offset > end || end > s.start+len(s.rows) {
		return nil, 0, false
	}
	return s.rows[offset-s.start : end-s.start], s.total, true
}

type rankedSourceState struct {
	offset, total int
	done          bool
	failed        bool
}

type rankedSearchPool struct {
	mu         sync.Mutex
	expires    time.Time
	rows       []json.RawMessage
	states     []rankedSourceState
	seenLocal  map[string]bool
	partial    bool
	retryAfter time.Time
}

type searchCandidate struct {
	raw                   json.RawMessage
	title, artist, source string
}

// The official Web track search loads one list and does not request subsequent
// pages. Only callers explicitly passing pagination can consume a short page.
func unpagedSearch(r *http.Request) bool {
	query := r.URL.Query()
	return !query.Has("page") && !query.Has("size")
}

// Normalize typography without discarding version labels such as Live or 伴奏.
func searchText(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '！' && r <= '～' {
			r -= 0xfee0
		}
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, s)
}

func searchTier(keyword, title, artist string) int {
	q, t, a := searchText(keyword), searchText(title), searchText(artist)
	if q == "" {
		return 4
	}
	if t != "" && a != "" && (q == t+a || q == a+t) {
		return 0
	}
	if q == t {
		return 1
	}
	if strings.Contains(t, q) {
		return 2
	}
	if strings.Contains(a, q) {
		return 3
	}
	// Support multiword queries without changing the source order of other hits.
	parts := strings.Fields(strings.ToLower(keyword))
	if len(parts) > 1 {
		all, matched := true, false
		for _, part := range parts {
			p := searchText(part)
			if p == "" {
				continue
			}
			matched = true
			if !strings.Contains(t, p) && !strings.Contains(a, p) {
				all = false
			}
		}
		if all && matched {
			return 2
		}
	}
	return 4
}

// Round-robin within each relevance tier, retaining each source's own order.
func rankSearch(keyword string, candidates []searchCandidate) []json.RawMessage {
	rows := make([]json.RawMessage, 0, len(candidates))
	for tier := 0; tier <= 4; tier++ {
		var sources []string
		groups := map[string][]json.RawMessage{}
		for _, c := range candidates {
			if searchTier(keyword, c.title, c.artist) != tier {
				continue
			}
			if _, ok := groups[c.source]; !ok {
				sources = append(sources, c.source)
			}
			groups[c.source] = append(groups[c.source], c.raw)
		}
		for index := 0; ; index++ {
			added := false
			for _, source := range sources {
				if index < len(groups[source]) {
					rows = append(rows, groups[source][index])
					added = true
				}
			}
			if !added {
				break
			}
		}
	}
	return rows
}

// Extend a stable prefix in small batches. Only newly fetched candidates are
// ranked; previously returned rows never move when another batch arrives.
func (g *Gateway) rankedSearch(ctx context.Context, r *http.Request, user provider.User, source provider.Adapter, scope, keyword string) ([]json.RawMessage, int, error) {
	return g.rankedSearchSeeded(ctx, r, user, source, scope, keyword, nil)
}

func (g *Gateway) rankedSearchSeeded(ctx context.Context, r *http.Request, user provider.User, source provider.Adapter, scope, keyword string, localSeed *localSearchSeed) ([]json.RawMessage, int, error) {
	quick := unpagedSearch(r)
	if quick {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, unpagedSearchTimeout)
		defer cancel()
	}
	cfg := g.policy()
	sources := g.sourcesFor(user)
	if len(sources) == 0 && g.accounts == nil && g.online == nil {
		sources = []provider.AccountSource{{Adapter: source, Scope: scope}}
	}
	selected := make([]provider.AccountSource, 0, len(sources))
	var sourceKeys []string
	for _, s := range sources {
		if sourceEnabled(cfg.SearchSources, s.Adapter.ID()) {
			selected = append(selected, s)
			sourceKeys = append(sourceKeys, s.Adapter.ID()+":"+s.Scope)
		}
	}
	filters := r.URL.Query()
	filters.Del("page")
	filters.Del("size")
	key := fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s\x00%s\x00%s", user.FnOSGUID, scope, g.revision(), r.URL.Path, keyword, filters.Encode(), strings.Join(sourceKeys, "\x01"))
	// Quick Web searches have a smaller budget; never let their exhausted state
	// prevent an explicitly paged client from fetching deeper results.
	if quick {
		key += "\x00quick"
	}
	g.searchMu.Lock()
	if g.rankedSearches == nil {
		g.rankedSearches = map[string]*rankedSearchPool{}
	}
	for k, pool := range g.rankedSearches {
		if time.Now().After(pool.expires) {
			delete(g.rankedSearches, k)
		}
	}
	pool := g.rankedSearches[key]
	if pool == nil {
		if len(g.rankedSearches) >= 64 {
			for k := range g.rankedSearches {
				delete(g.rankedSearches, k)
				break
			}
		}
		pool = &rankedSearchPool{expires: time.Now().Add(5 * time.Minute)}
		g.rankedSearches[key] = pool
	}
	g.searchMu.Unlock()
	pool.mu.Lock()
	defer pool.mu.Unlock()
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size < 1 {
		size = 50
	}
	needed := page * size
	pages := cfg.MaxPages
	if !cfg.DeepPage {
		pages = 1
	}
	budget := pages * 100
	if quick {
		budget = min(budget, unpagedSourceBudget)
		needed = budget * (len(selected) + 1)
	}
	if pool.states == nil || (pool.partial && page == 1 && !time.Now().Before(pool.retryAfter)) {
		pool.rows = make([]json.RawMessage, 0)
		pool.states = make([]rankedSourceState, len(selected)+1)
		pool.seenLocal = map[string]bool{}
		pool.partial = false
	}
	for len(pool.rows) < needed && ctx.Err() == nil {
		results := make([]provider.SearchPage, len(pool.states))
		localRows := []json.RawMessage{}
		errs := make([]error, len(pool.states))
		active := false
		var wg sync.WaitGroup
		for i, state := range pool.states {
			if state.done || state.failed {
				continue
			}
			active = true
			wg.Add(1)
			go func(i int, state rankedSourceState) {
				defer wg.Done()
				fetchCtx, cancel := context.WithTimeout(ctx, searchSourceTimeout)
				defer cancel()
				if i == 0 {
					if rows, total, ok := localSeed.window(state.offset); ok {
						localRows, results[i].Total = rows, total
						return
					}
					localRows, results[i].Total, errs[i] = g.localSearchBatch(fetchCtx, r, state.offset)
				} else {
					s := selected[i-1]
					results[i], errs[i] = g.searchWindowLimited(fetchCtx, user, s.Adapter, s.Scope, keyword, state.offset, rankedSearchBatch, rankedSearchBatch, budget)
				}
			}(i, state)
		}
		if !active {
			break
		}
		wg.Wait()
		candidates := []searchCandidate{}
		success := len(selected) == 0
		for i := range pool.states {
			state := &pool.states[i]
			if state.done || state.failed {
				continue
			}
			if errs[i] != nil {
				state.failed = true
				pool.partial = true
				name, resource := "local", scope
				if i > 0 {
					name = selected[i-1].Adapter.ID()
					resource = selected[i-1].Scope
				}
				diagnostics.Record("search-"+name, resource, errs[i])
			} else if i > 0 {
				success = true
			}
			if i == 0 {
				if errs[i] != nil {
					continue
				}
				added := 0
				for _, raw := range localRows {
					var t struct{ GUID, Title, Artist string }
					if json.Unmarshal(raw, &t) != nil {
						continue
					}
					id := t.GUID
					if id == "" {
						id = string(raw)
					}
					if pool.seenLocal[id] {
						continue
					}
					pool.seenLocal[id] = true
					added++
					candidates = append(candidates, searchCandidate{raw: raw, title: t.Title, artist: t.Artist, source: "local"})
				}
				state.offset += len(localRows)
				state.total = min(results[i].Total, budget)
				state.done = len(localRows) == 0 || added == 0 || state.offset >= state.total
			} else {
				s := selected[i-1]
				if len(results[i].Tracks) > 0 {
					success = true
				}
				for _, track := range results[i].Tracks {
					vo, ok := g.trackVO(user, s.Scope, track, true)
					if !ok {
						continue
					}
					raw, err := json.Marshal(vo)
					if err != nil {
						return nil, 0, err
					}
					candidates = append(candidates, searchCandidate{raw: raw, title: track.Title, artist: strings.Join(track.Artists, " "), source: track.Source})
					g.cache.put(s.Scope+":"+track.ID, track)
				}
				state.offset += len(results[i].Tracks)
				state.total = min(results[i].Total, budget)
				state.done = errs[i] == nil && !results[i].HasMore
			}
		}
		if !success && len(pool.rows) == 0 {
			return nil, 0, fmt.Errorf("all search sources unavailable")
		}
		pool.rows = append(pool.rows, rankSearch(keyword, candidates)...)
		if pool.partial {
			pool.retryAfter = time.Now().Add(10 * time.Second)
		}
	}
	if ctx.Err() != nil && len(pool.rows) == 0 {
		return nil, 0, ctx.Err()
	}
	total := len(pool.rows)
	if !unpagedSearch(r) {
		for _, state := range pool.states {
			if !state.done && !state.failed && state.total > state.offset {
				total += state.total - state.offset
			}
		}
	}
	return pool.rows, total, nil
}

// Local and online sources use the same fixed batch size so page-size changes
// cannot alter the order of the cached prefix.
func (g *Gateway) localSearchBatch(ctx context.Context, r *http.Request, offset int) ([]json.RawMessage, int, error) {
	query := r.URL.Query()
	query.Set("size", strconv.Itoa(rankedSearchBatch))
	query.Set("page", strconv.Itoa(offset/rankedSearchBatch+1))
	resp, b, err := g.official(ctx, r.Header, http.MethodGet, r.URL.Path+"?"+query.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	var body struct {
		Data struct {
			List  []json.RawMessage `json:"list"`
			Total int               `json:"total"`
		} `json:"data"`
	}
	if !officialOK(resp, b) || json.Unmarshal(b, &body) != nil || body.Data.Total < 0 {
		return nil, 0, fmt.Errorf("invalid official search page")
	}
	rows := body.Data.List
	if len(rows) > rankedSearchBatch {
		lo := min(offset, len(rows))
		hi := min(lo+rankedSearchBatch, len(rows))
		rows = rows[lo:hi]
	}
	return rows, body.Data.Total, nil
}
