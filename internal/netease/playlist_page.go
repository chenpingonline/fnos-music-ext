package netease

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"strconv"
)

const maxPlaylistTracks = 10000

func (n *Native) PlaylistTrackPage(ctx context.Context, u provider.User, id string, offset, limit int) (provider.TrackPage, error) {
	if e := u.Validate(); e != nil {
		return provider.TrackPage{}, e
	}
	if !validID(id) {
		return provider.TrackPage{}, errors.New("invalid playlist ID")
	}
	lists, e := n.ListPlaylists(ctx, u)
	if e != nil {
		return provider.TrackPage{}, e
	}
	for _, p := range lists {
		if p.ID == id {
			return n.playlistTrackPage(ctx, id, offset, limit)
		}
	}
	return provider.TrackPage{}, provider.ErrUnauthenticated
}
func (n *Native) PublicPlaylistTrackPage(ctx context.Context, u provider.User, id string, offset, limit int) (provider.TrackPage, error) {
	if e := u.Validate(); e != nil {
		return provider.TrackPage{}, e
	}
	if !validID(id) {
		return provider.TrackPage{}, errors.New("invalid playlist ID")
	}
	return n.playlistTrackPage(ctx, id, offset, limit)
}
func (n *Native) playlistTrackPage(ctx context.Context, id string, offset, limit int) (provider.TrackPage, error) {
	if offset < 0 || limit < -1 || limit == 0 || limit > maxPlaylistTracks {
		return provider.TrackPage{}, errors.New("invalid playlist page")
	}
	obj, e := n.post(ctx, "/api/v6/playlist/detail", map[string]any{"id": id, "n": 1, "s": 0})
	if e != nil {
		return provider.TrackPage{}, e
	}
	var ids []struct {
		ID json.Number `json:"id"`
	}
	playlist := unwrapObject(obj["playlist"])
	if e = json.Unmarshal(playlist["trackIds"], &ids); e != nil {
		return provider.TrackPage{}, ErrUnavailable
	}
	if len(ids) > maxPlaylistTracks {
		return provider.TrackPage{}, diagnostics.New("playlist-size-limit")
	}
	if expected := intField(playlist, "trackCount"); expected > int64(len(ids)) {
		return provider.TrackPage{}, diagnostics.New("incomplete-playlist-index")
	}
	page := provider.TrackPage{Total: len(ids), Tracks: []provider.Track{}}
	if offset >= len(ids) {
		return page, nil
	}
	end := len(ids)
	if limit > 0 && limit < end-offset {
		end = offset + limit
	}
	for i := offset; i < end; i += 100 {
		stop := i + 100
		if stop > end {
			stop = end
		}
		batch := make([]map[string]int64, 0, stop-i)
		for _, v := range ids[i:stop] {
			number, err := v.ID.Int64()
			if err != nil || number <= 0 {
				return provider.TrackPage{}, ErrUnavailable
			}
			batch = append(batch, map[string]int64{"id": number})
		}
		buf, _ := json.Marshal(batch)
		detail, err := n.post(ctx, "/api/v3/song/detail", map[string]any{"c": string(buf)})
		if err != nil {
			return provider.TrackPage{}, err
		}
		var songs []json.RawMessage
		if json.Unmarshal(detail["songs"], &songs) != nil {
			return provider.TrackPage{}, ErrUnavailable
		}
		byID := map[string]provider.Track{}
		for _, row := range songs {
			if t, err := parseTrack(row); err == nil {
				byID[t.ID] = t
			}
		}
		for _, v := range batch {
			id := strconv.FormatInt(v["id"], 10)
			track, found := byID[id]
			if !found {
				track = provider.Track{ID: id, Source: "netease", Title: "暂不可用的歌曲", AccessStatus: 1}
			}
			page.Tracks = append(page.Tracks, track)
		}
	}
	return page, nil
}
