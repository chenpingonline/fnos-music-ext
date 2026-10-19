package gateway

import (
	"context"
	"fmt"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"time"
)

// Every caller has already checked current account and playlist visibility.
func (g *Gateway) listTrackPage(ctx context.Context, u provider.User, s provider.Adapter, scope string, p provider.Playlist, offset, limit int) (provider.TrackPage, bool, error) {
	var load func(context.Context) (provider.TrackPage, error)
	if p.Kind == "" {
		if adapter, ok := s.(interface {
			PlaylistTrackPage(context.Context, provider.User, string, int, int) (provider.TrackPage, error)
		}); ok {
			load = func(ctx context.Context) (provider.TrackPage, error) {
				return adapter.PlaylistTrackPage(ctx, u, p.ID, offset, limit)
			}
		}
	} else if p.Kind == "public" || p.Kind == "recommend" && p.ID == "1" {
		if adapter, ok := s.(interface {
			PublicPlaylistTrackPage(context.Context, provider.User, string, int, int) (provider.TrackPage, error)
		}); ok {
			id := p.ID
			if p.Kind == "recommend" {
				id = "3778678"
			}
			load = func(ctx context.Context) (provider.TrackPage, error) {
				return adapter.PublicPlaylistTrackPage(ctx, u, id, offset, limit)
			}
		}
	}
	if load == nil {
		return provider.TrackPage{}, false, nil
	}
	key := fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s\x00%d:%d", u.FnOSGUID, scope, g.revision(), p.Kind, p.ID, offset, limit)
	result, err := g.pageFlights.Do(ctx, key, 60*time.Second, load)
	result.Tracks = append([]provider.Track{}, result.Tracks...)
	return result, true, err
}

func (g *Gateway) listTrackCount(ctx context.Context, u provider.User, s provider.Adapter, scope string, p provider.Playlist) (int, error) {
	result, paged, err := g.listTrackPage(ctx, u, s, scope, p, 0, 1)
	if paged {
		return result.Total, err
	}
	tracks, err := g.listTracks(ctx, u, s, scope, p)
	return len(tracks), err
}
