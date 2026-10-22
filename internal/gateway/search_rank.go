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

	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

type rankedSearchPool struct {
	mu      sync.Mutex
	expires time.Time
	rows    []json.RawMessage
	ready   bool
}

type searchCandidate struct {
	raw                   json.RawMessage
	title, artist, source string
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

// Freeze the bounded candidate set before pagination so later pages cannot
// insert new hits ahead of rows the user has already seen.
func (g *Gateway) rankedSearch(ctx context.Context, r *http.Request, user provider.User, source provider.Adapter, scope, keyword string) ([]json.RawMessage, error) {
	filters := r.URL.Query()
	filters.Del("page")
	filters.Del("size")
	key := fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s\x00%s", user.FnOSGUID, scope, g.revision(), r.URL.Path, keyword, filters.Encode())
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
	if pool.ready {
		return pool.rows, nil
	}
	cfg := g.policy()
	pages := cfg.MaxPages
	if !cfg.DeepPage {
		pages = 1
	}
	var candidates []searchCandidate
	seen := map[string]bool{}
	query := r.URL.Query()
	query.Set("size", "100")
	for page := 1; page <= pages; page++ {
		query.Set("page", strconv.Itoa(page))
		resp, b, err := g.official(ctx, r.Header, http.MethodGet, r.URL.Path+"?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var body struct {
			Data struct {
				List  []json.RawMessage `json:"list"`
				Total int               `json:"total"`
			} `json:"data"`
		}
		if !officialOK(resp, b) || json.Unmarshal(b, &body) != nil || body.Data.Total < 0 {
			return nil, fmt.Errorf("invalid official search page")
		}
		list := body.Data.List
		// Some official versions return all rows regardless of pagination.
		if len(list) > 100 {
			lo := (page - 1) * 100
			if lo > len(list) {
				lo = len(list)
			}
			hi := lo + 100
			if hi > len(list) {
				hi = len(list)
			}
			list = list[lo:hi]
		}
		added := 0
		for _, raw := range list {
			var t struct{ GUID, Title, Artist string }
			if json.Unmarshal(raw, &t) != nil {
				return nil, fmt.Errorf("invalid official search track")
			}
			id := t.GUID
			if id == "" {
				id = string(raw)
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			added++
			candidates = append(candidates, searchCandidate{raw: raw, title: t.Title, artist: t.Artist, source: "local"})
		}
		if len(list) == 0 || added == 0 || page*100 >= body.Data.Total {
			break
		}
	}
	result, err := g.searchWindow(ctx, user, source, scope, keyword, 0, pages*100)
	if err != nil {
		return nil, err
	}
	for _, t := range result.Tracks {
		vo, ok := g.trackVO(user, scope, t, true)
		if !ok {
			continue
		}
		raw, err := json.Marshal(vo)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, searchCandidate{raw: raw, title: t.Title, artist: strings.Join(t.Artists, " "), source: t.Source})
		g.cache.put(scope+":"+t.ID, t)
	}
	pool.rows = rankSearch(keyword, candidates)
	pool.ready = true
	return pool.rows, nil
}
