package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

const playlistBase = "/music/api/v1/"

func (g *Gateway) playlistRoute(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch r.URL.Path {
	case playlistBase + "playlist/list":
		return g.playlistList(w, r)
	case playlistBase + "playlist/detail":
		return g.playlistDetail(w, r)
	case playlistBase + "track/playlist-detail/list":
		return g.playlistTracks(w, r)
	}
	return false
}
func (g *Gateway) playlistSource(r *http.Request) (provider.User, provider.Adapter, string, bool) {
	user, ok := g.identify(r.Context(), r)
	if !ok {
		return provider.User{}, nil, "", false
	}
	source, account, ok := g.sourceFor(user)
	return user, source, account, ok
}
func (g *Gateway) ownedList(ctx context.Context, user provider.User, a provider.Adapter) ([]provider.Playlist, error) {
	ctx, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	return a.ListPlaylists(ctx, user)
}
func hasPlaylist(lists []provider.Playlist, id string) (provider.Playlist, bool) {
	for _, p := range lists {
		if p.ID == id {
			return p, true
		}
	}
	return provider.Playlist{}, false
}
func playlistGUID(r *http.Request) string {
	for _, key := range []string{"playlistGUID", "playlistGuid", "guid"} {
		if x := r.URL.Query().Get(key); x != "" {
			return x
		}
	}
	return ""
}
func (g *Gateway) playlistList(w http.ResponseWriter, r *http.Request) bool {
	user, src, scope, ok := g.playlistSource(r)
	if !ok {
		return false
	}
	// Existing official playlists are authoritative. Failure to read them
	// never results in online-only replacements.
	req := r.Clone(r.Context())
	u := *r.URL
	u.Scheme = "http"
	u.Host = "trim-music"
	req.URL = &u
	req.RequestURI = ""
	resp, err := g.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOfficialSearchBody+1))
	if err != nil || len(body) > maxOfficialSearchBody {
		return false
	}
	if resp.StatusCode != 200 {
		writeOfficial(w, resp, body)
		return true
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil {
		writeOfficial(w, resp, body)
		return true
	}
	var code int
	if json.Unmarshal(envelope["code"], &code) != nil || code != 0 {
		writeOfficial(w, resp, body)
		return true
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(envelope["data"], &data) != nil {
		writeOfficial(w, resp, body)
		return true
	}
	var existing []json.RawMessage
	if json.Unmarshal(data["list"], &existing) != nil {
		writeOfficial(w, resp, body)
		return true
	}
	var oldTotal int
	if json.Unmarshal(data["total"], &oldTotal) != nil || oldTotal < 0 {
		oldTotal = len(existing)
	}
	playlists, err := g.ownedList(r.Context(), user, src)
	if err != nil || len(playlists) == 0 {
		writeOfficial(w, resp, body)
		return true
	}
	cards := make([]json.RawMessage, 0, len(playlists))
	for _, p := range playlists {
		if v, ok := fnmusic.PlaylistVO(scope, p); ok {
			raw, _ := json.Marshal(v)
			cards = append(cards, raw)
		}
	}
	if len(cards) == 0 {
		writeOfficial(w, resp, body)
		return true
	}
	data["list"], _ = json.Marshal(append(cards, existing...))
	data["total"], _ = json.Marshal(oldTotal + len(cards))
	envelope["data"], _ = json.Marshal(data)
	writeJSON(w, 200, envelope)
	return true
}
func (g *Gateway) playlistDetail(w http.ResponseWriter, r *http.Request) bool {
	user, src, scope, ok := g.playlistSource(r)
	if !ok {
		return false
	}
	id, ok := fnmusic.DecodePlaylistID(scope, playlistGUID(r))
	if !ok {
		return false
	}
	lists, err := g.ownedList(r.Context(), user, src)
	if err != nil {
		writeJSON(w, 502, map[string]any{"code": -1, "msg": "playlist source unavailable"})
		return true
	}
	p, found := hasPlaylist(lists, id)
	if !found {
		writeJSON(w, 404, map[string]any{"code": -1, "msg": "playlist not found"})
		return true
	}
	card, _ := fnmusic.PlaylistVO(scope, p)
	writeJSON(w, 200, map[string]any{"code": 0, "msg": "ok", "data": card})
	return true
}
func (g *Gateway) playlistTracks(w http.ResponseWriter, r *http.Request) bool {
	user, src, scope, ok := g.playlistSource(r)
	if !ok {
		return false
	}
	id, ok := fnmusic.DecodePlaylistID(scope, playlistGUID(r))
	if !ok {
		return false
	}
	lists, err := g.ownedList(r.Context(), user, src)
	if err != nil {
		writeJSON(w, 502, map[string]any{"code": -1, "msg": "playlist source unavailable"})
		return true
	}
	if _, ok := hasPlaylist(lists, id); !ok {
		writeJSON(w, 404, map[string]any{"code": -1, "msg": "playlist not found"})
		return true
	}
	bridge, ok := src.(interface {
		PlaylistTracks(context.Context, provider.User, string) ([]provider.Track, error)
	})
	if !ok {
		writeJSON(w, 501, map[string]any{"code": -1, "msg": "playlist tracks unsupported"})
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	tracks, err := bridge.PlaylistTracks(ctx, user, id)
	if err != nil {
		writeJSON(w, 502, map[string]any{"code": -1, "msg": "playlist tracks unavailable"})
		return true
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size == -1 {
		size = len(tracks)
	} else if size < 1 {
		size = 50
	}
	if size > 1000 {
		size = 1000
	}
	if page > 10000 {
		page = 10000
	}
	start := (page - 1) * size
	end := start + size
	if start > len(tracks) {
		start = len(tracks)
	}
	if end > len(tracks) {
		end = len(tracks)
	}
	rows := make([]map[string]any, 0, end-start)
	for _, t := range tracks[start:end] {
		if v, ok := fnmusic.TrackVO(t, false); ok {
			rows = append(rows, v)
			g.cache.put(scope+":"+t.ID, t)
		}
	}
	writeJSON(w, 200, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"list": rows, "total": len(tracks), "sort": strings.TrimSpace(r.URL.Query().Get("sort"))}})
	return true
}
