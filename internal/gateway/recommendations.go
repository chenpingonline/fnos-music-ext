package gateway

import (
	"context"
	"fmt"
	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"time"
)

type recommendationAdapter interface {
	Recommendations(context.Context, provider.User, string) ([]provider.Track, error)
	PublicPlaylistTracks(context.Context, provider.User, string) ([]provider.Track, error)
}
type recommendationCache struct {
	tracks  []provider.Track
	expires time.Time
}

func cardScope(scope string, p provider.Playlist) string {
	if p.Kind != "" {
		return scope + ":" + p.Kind
	}
	return scope
}
func decodeList(scope, guid string) (string, string, bool) {
	for _, kind := range []string{"", "public", "recommend"} {
		s := scope
		if kind != "" {
			s += ":" + kind
		}
		if id, ok := fnmusic.DecodePlaylistID(s, guid); ok {
			return id, kind, true
		}
	}
	return "", "", false
}
func (g *Gateway) availableLists(ctx context.Context, user provider.User, source provider.Adapter, scope string) ([]provider.Playlist, error) {
	c := g.policy()
	if !c.PersonalPlaylists {
		return g.buildAvailableLists(ctx, user, source, scope, false)
	}
	g.listMu.Lock()
	epoch := g.listEpoch
	key := fmt.Sprintf("%s\x00%s\x00%d\x00%d", user.FnOSGUID, scope, g.revision(), epoch)
	hit, found := g.lists[key]
	g.listMu.Unlock()
	if found && time.Now().Before(hit.until) {
		return append([]provider.Playlist{}, hit.items...), nil
	}
	rows, err := g.listFlights.Do(ctx, key, 8*time.Second, func(shared context.Context) ([]provider.Playlist, error) {
		g.listMu.Lock()
		hit, ok := g.lists[key]
		g.listMu.Unlock()
		if ok && time.Now().Before(hit.until) {
			return hit.items, nil
		}
		rows, err := g.buildAvailableLists(shared, user, source, scope, true)
		if err == nil {
			g.listMu.Lock()
			if epoch == g.listEpoch {
				if g.lists == nil || len(g.lists) >= 256 {
					g.lists = map[string]cachedPlaylists{}
				}
				g.lists[key] = cachedPlaylists{items: append([]provider.Playlist{}, rows...), until: time.Now().Add(15 * time.Second)}
			}
			g.listMu.Unlock()
		}
		return rows, err
	})
	return append([]provider.Playlist{}, rows...), err
}

type cachedPlaylists struct {
	items []provider.Playlist
	until time.Time
}

func (g *Gateway) buildAvailableLists(ctx context.Context, user provider.User, source provider.Adapter, scope string, personal bool) ([]provider.Playlist, error) {
	c := g.policy()
	var lists []provider.Playlist
	var err error
	if personal {
		lists, err = g.ownedList(ctx, user, source)
	}
	if _, ok := source.(recommendationAdapter); !ok {
		return lists, err
	}
	if c.Hot {
		lists = append(lists, provider.Playlist{ID: "1", Title: "热门推荐", Kind: "recommend", ReadOnly: true})
	}
	if c.Daily {
		lists = append(lists, provider.Playlist{ID: "2", Title: "每日推荐", Kind: "recommend", ReadOnly: true})
	}
	if c.PublicEnabled {
		for _, p := range c.Public {
			lists = append(lists, provider.Playlist{ID: p.ID, Title: p.Title, CoverURL: p.CoverURL, Kind: "public", ReadOnly: true})
		}
	}
	if len(lists) > 0 {
		return lists, nil
	}
	return lists, err
}
func (g *Gateway) listTracks(ctx context.Context, u provider.User, s provider.Adapter, scope string, p provider.Playlist) ([]provider.Track, error) {
	key := fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s", u.FnOSGUID, scope, g.revision(), p.Kind, p.ID)
	rows, err := g.trackFlights.Do(ctx, key, 30*time.Second, func(shared context.Context) ([]provider.Track, error) {
		return g.loadListTracks(shared, u, s, scope, p)
	})
	return append([]provider.Track{}, rows...), err
}
func (g *Gateway) loadListTracks(ctx context.Context, u provider.User, s provider.Adapter, scope string, p provider.Playlist) ([]provider.Track, error) {
	if p.Kind == "" {
		a, ok := s.(interface {
			PlaylistTracks(context.Context, provider.User, string) ([]provider.Track, error)
		})
		if !ok {
			return nil, ErrNoSource
		}
		return a.PlaylistTracks(ctx, u, p.ID)
	}
	key := fmt.Sprintf("%s:%s:%d:%s:%s:%s", u.FnOSGUID, scope, g.revision(), time.Now().Format("2006-01-02"), p.Kind, p.ID)
	g.recoMu.Lock()
	if hit, ok := g.recommendations[key]; ok && time.Now().Before(hit.expires) {
		g.recoMu.Unlock()
		return append([]provider.Track{}, hit.tracks...), nil
	}
	g.recoMu.Unlock()
	adapter, ok := s.(recommendationAdapter)
	if !ok {
		return nil, ErrNoSource
	}
	var tracks []provider.Track
	var err error
	if p.Kind == "public" {
		tracks, err = adapter.PublicPlaylistTracks(ctx, u, p.ID)
	} else {
		kind := "hot"
		if p.ID == "2" {
			kind = "daily"
		}
		tracks, err = adapter.Recommendations(ctx, u, kind)
	}
	if err != nil {
		return nil, err
	}
	g.recoMu.Lock()
	if g.recommendations == nil {
		g.recommendations = map[string]recommendationCache{}
	}
	if len(g.recommendations) >= 256 {
		for k := range g.recommendations {
			delete(g.recommendations, k)
			break
		}
	}
	g.recommendations[key] = recommendationCache{append([]provider.Track{}, tracks...), time.Now().Add(10 * time.Minute)}
	g.recoMu.Unlock()
	return tracks, nil
}
