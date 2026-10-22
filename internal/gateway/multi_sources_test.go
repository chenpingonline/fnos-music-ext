package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/accounts"
	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type groupedTestSource struct {
	stubMusic
	name, track string
	broken      atomic.Bool
	calls       atomic.Int64
}

func (s *groupedTestSource) ID() string { return s.name }
func (s *groupedTestSource) Search(_ context.Context, _ provider.User, q provider.SearchRequest) (provider.SearchPage, error) {
	if s.broken.Load() {
		return provider.SearchPage{}, errors.New("offline")
	}
	rows := []provider.Track{}
	if q.Offset == 0 {
		rows = append(rows, provider.Track{ID: s.track, Source: s.name, Title: "Same Song", Artists: []string{"Same Artist"}, Playable: true})
	}
	return provider.SearchPage{Tracks: rows, Total: 1}, nil
}
func (s *groupedTestSource) GetTrack(context.Context, provider.User, string) (provider.Track, error) {
	s.calls.Add(1)
	return provider.Track{ID: s.track, Source: s.name, Title: s.name, Playable: true}, nil
}
func (s *groupedTestSource) Play(context.Context, provider.User, provider.PlaybackRequest) (provider.Stream, error) {
	s.calls.Add(1)
	return provider.Stream{Body: io.NopCloser(strings.NewReader("audio")), ContentType: "audio/mpeg", ContentLength: 5, StatusCode: 200}, nil
}
func (s *groupedTestSource) ListPlaylists(context.Context, provider.User) ([]provider.Playlist, error) {
	return []provider.Playlist{{ID: "42", Title: s.name, TrackCount: 1}}, nil
}
func (s *groupedTestSource) PlaylistTracks(context.Context, provider.User, string) ([]provider.Track, error) {
	return []provider.Track{{ID: s.track, Source: s.name, Title: s.name, Playable: true}}, nil
}
func TestMultipleSourcesSearchPlaylistAndPlayback(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "music.sock")
	ln, e := net.Listen("unix", sock)
	if e != nil {
		t.Fatal(e)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/music/api/v1/user/me" {
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"guid": "alice"}})
			return
		}
		if r.URL.Query().Get("q") == "Same" {
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"list": []any{
				map[string]string{"guid": "local-other", "title": "Other", "artist": "Artist"},
				map[string]string{"guid": "local-1", "title": "Same Song", "artist": "Same Artist"},
				map[string]string{"guid": "local-2", "title": "Same Song", "artist": "Same Artist"},
			}, "total": 3}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"list": []any{}, "total": 0}})
	})}
	go srv.Serve(ln)
	defer srv.Close()
	ne := &groupedTestSource{name: "netease", track: "123"}
	qq := &groupedTestSource{name: "qqmusic", track: "0039MnYb0qxYhV"}
	m, e := accounts.New(filepath.Join(dir, "private", "accounts.json"), func(address string) (provider.Adapter, error) {
		if strings.HasPrefix(address, "native://qqmusic/") {
			return qq, nil
		}
		return ne, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	for _, a := range []accounts.Account{{ID: "ne", Name: "NE", BridgeURL: "native://netease/ne"}, {ID: "qq", Name: "QQ", BridgeURL: "native://qqmusic/qq"}} {
		if e = m.Add(a); e != nil {
			t.Fatal(e)
		}
	}
	if e = m.Bind("alice", "ne"); e != nil {
		t.Fatal(e)
	}
	if e = m.SetGroup("ne", []string{"qq"}); e != nil {
		t.Fatal(e)
	}
	prefs, _ := preferences.New(filepath.Join(dir, "settings.json"))
	g := New(sock)
	defer g.Close()
	g.SetAccounts(m)
	if e = g.Configure(dir, prefs); e != nil {
		t.Fatal(e)
	}
	call := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://localhost"+path, nil)
		r.Header.Set("Authorization", "alice")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	mixed := call("/music/api/v1/search/track?q=Same&size=5")
	var mixedData struct {
		Data struct {
			List  []struct{ GUID, Title string }
			Total int
		}
	}
	if err := json.Unmarshal(mixed.Body.Bytes(), &mixedData); err != nil {
		t.Fatal(err)
	}
	if mixedData.Data.Total != 5 || len(mixedData.Data.List) != 5 {
		t.Fatal(mixed.Body.String())
	}
	rows := mixedData.Data.List
	if rows[0].GUID != "local-1" || rows[1].Title != "[网易云] Same Song" || rows[2].Title != "[QQ音乐] Same Song" || rows[3].GUID != "local-2" || rows[4].GUID != "local-other" {
		t.Fatal("three-source ranking lost relevance or round-robin order", mixed.Body.String())
	}
	first := call("/music/api/v1/search/track?q=both&page=1&size=1")
	second := call("/music/api/v1/search/track?q=both&page=2&size=1")
	if !strings.Contains(first.Body.String(), "[网易云] Same Song") || !strings.Contains(second.Body.String(), "[QQ音乐] Same Song") {
		t.Fatal(first.Body.String(), second.Body.String())
	}
	lists := call("/music/api/v1/playlist/list")
	var data struct {
		Data struct{ List []struct{ GUID, Name string } }
	}
	json.Unmarshal(lists.Body.Bytes(), &data)
	if len(data.Data.List) != 2 || data.Data.List[0].GUID == data.Data.List[1].GUID {
		t.Fatal(lists.Body.String())
	}
	for _, p := range data.Data.List {
		for _, path := range []string{"/music/api/v1/playlist/detail?guid=", "/music/api/v1/track/playlist-detail/list?playlistGUID="} {
			w := call(path + p.GUID)
			if w.Code != 200 || !strings.Contains(w.Body.String(), strings.TrimPrefix(strings.TrimPrefix(p.Name, "[网易云] "), "[QQ音乐] ")) {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
	}
	guids := data.Data.List[0].GUID + "," + data.Data.List[1].GUID
	if w := call("/music/api/v1/playlist/batch-detail?guids=" + guids); !strings.Contains(w.Body.String(), "netease") || !strings.Contains(w.Body.String(), "qqmusic") {
		t.Fatal(w.Body.String())
	}
	qqGUID, _ := fnmusic.EncodeTrackID("qqmusic", qq.track)
	neGUID, _ := fnmusic.EncodeTrackID("netease", ne.track)
	for _, guid := range []string{neGUID, qqGUID} {
		w := call("/music/api/v1/track/stream?guid=" + guid)
		if w.Code != 200 || w.Body.String() != "audio" {
			t.Fatal(w.Code, w.Body.String())
		}
	}

	for _, guid := range []string{neGUID, qqGUID} {
		r := httptest.NewRequest("POST", "http://localhost/music/api/v1/favorite-track/create", strings.NewReader(`{"trackGUID":"`+guid+`"}`))
		r.Header.Set("Authorization", "alice")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://localhost")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal("multi-source favorite", w.Code, w.Body.String())
		}
	}
	if w := call("/music/api/v1/favorite-track/list"); !strings.Contains(w.Body.String(), neGUID) || !strings.Contains(w.Body.String(), qqGUID) {
		t.Fatal("favorites lost a source", w.Body.String())
	}

	r := httptest.NewRequest("POST", "http://localhost/music/api/v1/track/transcode/quit", strings.NewReader(`{"guid":"`+qqGUID+`"}`))
	r.Header.Set("Authorization", "alice")
	r.Header.Set("Origin", "http://localhost")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code == 403 {
		t.Fatal("POST body track routed to primary platform", w.Body.String())
	}
	settings := prefs.Snapshot()
	settings.SearchSources = []string{"qqmusic"}
	settings.PlaylistSources = []string{"netease"}
	if e = prefs.Update(settings); e != nil {
		t.Fatal(e)
	}
	if w := call("/music/api/v1/search/track?q=selected&size=5"); strings.Contains(w.Body.String(), "[网易云]") || !strings.Contains(w.Body.String(), "[QQ音乐]") {
		t.Fatal(w.Body.String())
	}
	if w := call("/music/api/v1/playlist/list"); strings.Contains(w.Body.String(), "QQ音乐自建") || !strings.Contains(w.Body.String(), "网易云自建") {
		t.Fatal(w.Body.String())
	}
	// Search visibility is independent from playback authorization.
	if w := call("/music/api/v1/track/stream?guid=" + neGUID); w.Code != 200 {
		t.Fatal(w.Code)
	}
	settings.SearchSources = []string{}
	settings.PlaylistSources = []string{}
	prefs.Update(settings)
	if w := call("/music/api/v1/search/track?q=none"); strings.Contains(w.Body.String(), "[QQ音乐]") || strings.Contains(w.Body.String(), "[网易云]") {
		t.Fatal(w.Body.String())
	}
	if w := call("/music/api/v1/playlist/list"); strings.Contains(w.Body.String(), "自建歌单") {
		t.Fatal(w.Body.String())
	}
	settings.SearchSources = nil
	settings.PlaylistSources = nil
	prefs.Update(settings)
	qq.broken.Store(true)
	if w := call("/music/api/v1/search/track?q=partial&size=5"); !strings.Contains(w.Body.String(), "[网易云]") {
		t.Fatal(w.Body.String())
	}
	if e = m.SetGroup("ne", nil); e != nil {
		t.Fatal(e)
	}
	// A cached search must also lose a revoked source immediately.
	if w := call("/music/api/v1/search/track?q=both&size=5"); strings.Contains(w.Body.String(), "[QQ音乐]") {
		t.Fatal("revoked source remained in ranked snapshot", w.Body.String())
	}
	before := qq.calls.Load()
	if w := call("/music/api/v1/track/stream?guid=" + qqGUID); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if qq.calls.Load() != before {
		t.Fatal("revoked source reached adapter")
	}
}
