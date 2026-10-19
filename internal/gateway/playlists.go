package gateway

import (
	"context"
	"encoding/json"
	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
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
	if r.URL.Path == playlistBase+"playlist/list" || strings.HasPrefix(r.URL.Path, playlistBase+"playlist/list/") {
		return g.playlistList(w, r)
	}
	switch r.URL.Path {
	case playlistBase + "playlist/batch-detail":
		return g.playlistBatchDetail(w, r)
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
			return strings.TrimPrefix(strings.TrimSpace(x), "track_")
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
	if len(envelope["data"]) > 0 && json.Unmarshal(envelope["data"], &data) != nil {
		writeOfficial(w, resp, body)
		return true
	}
	if data == nil {
		data = make(map[string]json.RawMessage)
	}
	var existing []json.RawMessage
	// Empty libraries may omit list or use null, as handled by the upstream
	// extension. Preserve malformed non-empty payloads instead of replacing them.
	rawList := data["list"]
	if len(rawList) > 0 && json.Unmarshal(rawList, &existing) != nil {
		writeOfficial(w, resp, body)
		return true
	}
	var oldTotal int
	if json.Unmarshal(data["total"], &oldTotal) != nil || oldTotal < 0 {
		oldTotal = len(existing)
	}
	playlists, err := g.availableLists(r.Context(), user, src, scope)
	if err != nil || len(playlists) == 0 {
		writeOfficial(w, resp, body)
		return true
	}
	cards := make([]json.RawMessage, 0, len(playlists))
	for _, p := range playlists {
		if v, ok := fnmusic.PlaylistVO(cardScope(scope, p), p); ok {
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
	id, kind, ok := decodeList(scope, playlistGUID(r))
	if !ok {
		return false
	}
	lists, err := g.availableLists(r.Context(), user, src, scope)
	if err != nil {
		writeJSON(w, 502, map[string]any{"code": -1, "msg": "playlist source unavailable"})
		return true
	}
	p, found := hasTypedPlaylist(lists, id, kind)
	if !found {
		writeJSON(w, 404, map[string]any{"code": -1, "msg": "playlist not found"})
		return true
	}
	if p.TrackCount == 0 {
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		count, loadErr := g.listTrackCount(ctx, user, src, scope, p)
		cancel()
		if loadErr != nil {
			writeJSON(w, 502, map[string]any{"code": -1, "msg": "playlist tracks unavailable"})
			return true
		}
		p.TrackCount = count
	}
	card, _ := fnmusic.PlaylistVO(cardScope(scope, p), p)
	writeJSON(w, 200, map[string]any{"code": 0, "msg": "ok", "data": card})
	return true
}
func (g *Gateway) playlistTracks(w http.ResponseWriter, r *http.Request) bool {
	user, src, scope, ok := g.playlistSource(r)
	if !ok {
		return false
	}
	id, kind, ok := decodeList(scope, playlistGUID(r))
	if !ok {
		return false
	}
	lists, err := g.availableLists(r.Context(), user, src, scope)
	if err != nil {
		writeJSON(w, 502, map[string]any{"code": -1, "msg": "playlist source unavailable"})
		return true
	}
	p, found := hasTypedPlaylist(lists, id, kind)
	if !found {
		writeJSON(w, 404, map[string]any{"code": -1, "msg": "playlist not found"})
		return true
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 10000 {
		page = 10000
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	all := size == -1
	if !all && size < 1 {
		size = 50
	}
	if !all && size > 1000 {
		size = 1000
	}
	offset := 0
	if !all {
		offset = (page - 1) * size
	}
	timeout := 20 * time.Second
	if all {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	result, paged, err := g.listTrackPage(ctx, user, src, scope, p, offset, size)
	var tracks []provider.Track
	total := result.Total
	if paged {
		tracks = result.Tracks
	} else {
		tracks, err = g.listTracks(ctx, user, src, scope, p)
		total = len(tracks)
		if err == nil && !all {
			start := offset
			if start > len(tracks) {
				start = len(tracks)
			}
			end := start + size
			if end > len(tracks) {
				end = len(tracks)
			}
			tracks = tracks[start:end]
		}
	}
	if err != nil {
		diagnostics.Record("playlist-tracks", scope+":"+id, err)
		writeJSON(w, 502, map[string]any{"code": -1, "msg": "playlist tracks unavailable"})
		return true
	}
	rows := make([]map[string]any, 0, len(tracks))
	for _, t := range tracks {
		if v, ok := g.trackVO(user, scope, t, false); ok {
			rows = append(rows, v)
			g.cache.put(scope+":"+t.ID, t)
		}
	}
	writeJSON(w, 200, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"list": rows, "total": total, "sort": strings.TrimSpace(r.URL.Query().Get("sort"))}})
	return true
}

func hasTypedPlaylist(lists []provider.Playlist, id, kind string) (provider.Playlist, bool) {
	for _, p := range lists {
		if p.ID == id && p.Kind == kind {
			return p, true
		}
	}
	return provider.Playlist{}, false
}
