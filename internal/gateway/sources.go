package gateway

import (
	"context"
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"net/http"
	"strings"
	"sync"
)

func (g *Gateway) sourcesFor(u provider.User) []provider.AccountSource {
	if u.Validate() != nil {
		return nil
	}
	if resolver, ok := g.accounts.(interface {
		ResolveSources(provider.User) []provider.AccountSource
	}); ok {
		return resolver.ResolveSources(u)
	}
	a, scope, ok := g.sourceFor(u)
	if !ok {
		return nil
	}
	return []provider.AccountSource{{Adapter: a, Scope: scope}}
}
func sourceEnabled(selected []string, name string) bool {
	if selected == nil {
		return true
	}
	for _, s := range selected {
		if s == name {
			return true
		}
	}
	return false
}
func (g *Gateway) visibleSources(u provider.User, playlists bool) []provider.AccountSource {
	selected := g.policy().SearchSources
	if playlists {
		selected = g.policy().PlaylistSources
	}
	out := []provider.AccountSource{}
	for _, s := range g.sourcesFor(u) {
		if sourceEnabled(selected, s.Adapter.ID()) {
			out = append(out, s)
		}
	}
	return out
}
func (g *Gateway) trackSource(u provider.User, guid string) (provider.Adapter, string, bool) {
	name, _, ok := fnmusic.DecodeTrackID(guid)
	if !ok {
		return nil, "", false
	}
	for _, s := range g.sourcesFor(u) {
		if s.Adapter.ID() == name {
			return s.Adapter, s.Scope, true
		}
	}
	return nil, "", false
}
func (g *Gateway) scopeAuthorized(u provider.User, scope string) bool {
	for _, s := range g.sourcesFor(u) {
		if s.Scope == scope {
			return true
		}
	}
	return false
}
func (g *Gateway) playlistAccount(u provider.User, guid string) (provider.Adapter, string, bool) {
	for _, s := range g.visibleSources(u, true) {
		if _, _, ok := decodeList(s.Scope, strings.TrimPrefix(guid, "track_")); ok {
			return s.Adapter, s.Scope, true
		}
	}
	return nil, "", false
}
func requestMusicGUID(r *http.Request) string {
	for _, key := range []string{"guid", "playlistGUID", "playlistGuid", "coverId", "trackGUID", "trackGuid", "id", "trackId"} {
		if v := r.URL.Query().Get(key); v != "" {
			return v
		}
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) > 1 && parts[len(parts)-2] == "hls" {
		return parts[len(parts)-1]
	}
	if len(parts) > 2 && parts[len(parts)-3] == "hls" {
		return parts[len(parts)-2]
	}
	return parts[len(parts)-1]
}

// Each account keeps its own search pool. Round-robin interleaving preserves both
// platforms' editions of the same song and a stable prefix across pages.
func (g *Gateway) selectedSearchWindow(ctx context.Context, u provider.User, keyword string, start, count int) (provider.SearchPage, error) {
	sources := g.visibleSources(u, false)
	if len(sources) == 0 {
		return provider.SearchPage{Tracks: []provider.Track{}}, nil
	}
	results := make([]provider.SearchPage, len(sources))
	errs := make([]error, len(sources))
	var wg sync.WaitGroup
	for i, s := range sources {
		wg.Add(1)
		go func(i int, s provider.AccountSource) {
			defer wg.Done()
			results[i], errs[i] = g.searchWindow(ctx, u, s.Adapter, s.Scope, keyword, 0, start+count)
		}(i, s)
	}
	wg.Wait()
	total, success := 0, false
	merged := []provider.Track{}
	for i, result := range results {
		if errs[i] == nil {
			total += result.Total
			success = true
		}
	}
	if !success {
		return provider.SearchPage{}, errors.New("all selected search sources unavailable")
	}
	for index := 0; len(merged) < start+count; index++ {
		added := false
		for i, r := range results {
			if errs[i] == nil && index < len(r.Tracks) {
				merged = append(merged, r.Tracks[index])
				added = true
			}
		}
		if !added {
			break
		}
	}
	lo := start
	if lo > len(merged) {
		lo = len(merged)
	}
	hi := start + count
	if hi > len(merged) {
		hi = len(merged)
	}
	return provider.SearchPage{Tracks: merged[lo:hi], Total: total, HasMore: hi < total}, nil
}
func (g *Gateway) scopeForTrack(u provider.User, t provider.Track, fallback string) string {
	for _, s := range g.sourcesFor(u) {
		if s.Adapter.ID() == t.Source {
			return s.Scope
		}
	}
	return fallback
}

type scopedPlaylist struct {
	provider.Playlist
	Source provider.Adapter
	Scope  string
}

func (g *Gateway) selectedLists(ctx context.Context, u provider.User) ([]scopedPlaylist, error) {
	sources := g.visibleSources(u, true)
	out := []scopedPlaylist{}
	success := len(sources) == 0
	var last error
	for _, s := range sources {
		lists, err := g.availableLists(ctx, u, s.Adapter, s.Scope)
		if err != nil {
			last = err
			continue
		}
		success = true
		for _, p := range lists {
			out = append(out, scopedPlaylist{Playlist: p, Source: s.Adapter, Scope: s.Scope})
		}
	}
	if !success {
		return nil, last
	}
	return out, nil
}

func (g *Gateway) playlistVO(u provider.User, scope string, p provider.Playlist) (map[string]any, bool) {
	if len(g.visibleSources(u, true)) > 1 {
		name := p.Source
		if name == "" {
			name = "netease"
		}
		p.Title = "[" + provider.SourceName(name) + "] " + p.Title
	}
	return fnmusic.PlaylistVO(cardScope(scope, p), p)
}
