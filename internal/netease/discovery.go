package netease

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"strconv"
)

// Discovery is deliberately separate from account-owned playlists: reading a
// public chart never relaxes the ownership checks on the private playlist API.
func (n *Native) Discover(ctx context.Context, u provider.User, kind, category string, page, size int) ([]provider.Playlist, error) {
	if e := u.Validate(); e != nil {
		return nil, e
	}
	if page < 1 || page > 100 || size < 1 || size > 100 {
		return nil, errors.New("invalid discovery page")
	}
	path := "/api/playlist/list"
	params := map[string]any{"cat": category, "order": "hot", "offset": (page - 1) * size, "limit": size, "total": true}
	field := "playlists"
	switch kind {
	case "chart":
		path = "/api/toplist"
		params = map[string]any{}
		field = "list"
	case "recommend":
		path = "/api/personalized/playlist"
		params = map[string]any{"limit": page * size}
		field = "result"
	case "highquality":
		path = "/api/playlist/highquality/list"
	case "category":
	default:
		return nil, errors.New("invalid discovery kind")
	}
	obj, e := n.post(ctx, path, params)
	if e != nil {
		return nil, e
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(obj[field], &rows) != nil {
		return nil, ErrUnavailable
	}
	if kind == "highquality" && page > 1 {
		for current := 1; current < page; current++ {
			if len(rows) < size {
				return []provider.Playlist{}, nil
			}
			last := intField(rows[len(rows)-1], "updateTime")
			if last <= 0 {
				return []provider.Playlist{}, nil
			}
			params["before"] = last
			delete(params, "offset")
			obj, e = n.post(ctx, path, params)
			if e != nil {
				return nil, e
			}
			if json.Unmarshal(obj[field], &rows) != nil {
				return nil, ErrUnavailable
			}
		}
	}
	if kind == "chart" || kind == "recommend" {
		start := (page - 1) * size
		if start >= len(rows) {
			rows = nil
		} else {
			end := start + size
			if end > len(rows) {
				end = len(rows)
			}
			rows = rows[start:end]
		}
	}
	out := []provider.Playlist{}
	for _, row := range rows {
		id := intField(row, "id")
		if id < 1 {
			continue
		}
		out = append(out, provider.Playlist{ID: strconv.FormatInt(id, 10), Title: stringField(row, "name"), CoverURL: stringField(row, "coverImgUrl", "picUrl"), TrackCount: int(intField(row, "trackCount")), ReadOnly: true})
	}
	return out, nil
}
func (n *Native) Categories(ctx context.Context, u provider.User) ([]string, error) {
	if e := u.Validate(); e != nil {
		return nil, e
	}
	obj, e := n.post(ctx, "/api/playlist/catalogue", map[string]any{})
	if e != nil {
		return nil, e
	}
	var rows []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(obj["sub"], &rows) != nil {
		return nil, ErrUnavailable
	}
	out := []string{"全部"}
	for _, v := range rows {
		if v.Name != "" {
			out = append(out, v.Name)
		}
	}
	return out, nil
}
func (n *Native) Recommendations(ctx context.Context, u provider.User, kind string) ([]provider.Track, error) {
	if e := u.Validate(); e != nil {
		return nil, e
	}
	if kind == "hot" {
		return n.PublicPlaylistTracks(ctx, u, "3778678")
	}
	if kind != "daily" {
		return nil, errors.New("invalid recommendation kind")
	}
	obj, e := n.post(ctx, "/api/v3/discovery/recommend/songs", map[string]any{})
	if e != nil {
		return nil, e
	}
	data := unwrapObject(obj["data"])
	var rows []json.RawMessage
	if json.Unmarshal(data["dailySongs"], &rows) != nil {
		if json.Unmarshal(obj["recommend"], &rows) != nil {
			return nil, ErrUnavailable
		}
	}
	tracks := []provider.Track{}
	for _, row := range rows {
		if t, e := parseTrack(row); e == nil {
			tracks = append(tracks, t)
		}
	}
	return tracks, nil
}
func (n *Native) PublicPlaylistTracks(ctx context.Context, u provider.User, id string) ([]provider.Track, error) {
	if e := u.Validate(); e != nil {
		return nil, e
	}
	if !validID(id) {
		return nil, errors.New("invalid playlist ID")
	}
	return n.playlistTracks(ctx, id)
}
