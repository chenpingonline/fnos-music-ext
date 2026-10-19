package gateway

import (
	"context"
	"encoding/json"
	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type playlistImageSource struct{ playlistAdapter }

func (p playlistImageSource) ListPlaylists(context.Context, provider.User) ([]provider.Playlist, error) {
	return []provider.Playlist{{ID: "901", Title: "十指流玉", TrackCount: 1, CoverURL: "https://p1.music.126.net/playlist.jpg"}}, nil
}
func (p playlistImageSource) OpenPlaylistCover(_ context.Context, u provider.User, list provider.Playlist) (provider.Stream, error) {
	if u.FnOSGUID != "alice" || list.ID != "901" || list.CoverURL != "https://p1.music.126.net/playlist.jpg" {
		return provider.Stream{}, provider.ErrUnauthenticated
	}
	return provider.Stream{Body: io.NopCloser(strings.NewReader("test-image")), ContentType: "image/png", ContentLength: 10, StatusCode: 200}, nil
}
func TestPlaylistListBatchTracksAndImageContract(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "official.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/music/api/v1/user/me":
			who := strings.TrimPrefix(r.Header.Get("Authorization"), "Token ")
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"guid": who}})
		case "/music/api/v1/playlist/list":
			io.WriteString(w, `{"code":0,"data":{"list":[],"total":0}}`)
		case "/music/api/v1/playlist/batch-detail":
			if r.URL.Query().Get("guids") != "official-1" {
				t.Errorf("virtual ID forwarded to official: %s", r.URL.RawQuery)
			}
			io.WriteString(w, `{"code":0,"extra":"keep","data":{"list":[{"guid":"official-1","name":"官方本地歌单"}],"extraData":true}}`)
		default:
			http.NotFound(w, r)
		}
	})}
	go srv.Serve(l)
	defer srv.Close()
	g := New(socket)
	defer g.Close()
	g.SetOnline(playlistImageSource{}, "alice")
	store, _ := preferences.New("")
	g.SetPreferences(store)
	query := func(method, path, user string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if user != "" {
			r.Header.Set("Authorization", "Token "+user)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	list := query("GET", "/music/api/v1/playlist/list", "alice")
	var payload map[string]any
	json.Unmarshal(list.Body.Bytes(), &payload)
	cards := payload["data"].(map[string]any)["list"].([]any)
	card := cards[0].(map[string]any)
	guid := card["guid"].(string)
	cover := card["coverId"].(string)
	if cover != "track_"+guid {
		t.Fatal("cover ID incompatible", card)
	}
	for _, path := range []string{"/music/api/v1/playlist/batch-detail?guids=" + guid, "/music/api/v1/playlist/batch-detail?guid=" + guid + "," + guid} {
		w := query("GET", path, "alice")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "十指流玉") || !strings.Contains(w.Body.String(), `"trackCount":1`) {
			t.Fatal("batch detail missing", w.Code, w.Body.String())
		}
	}
	mixed := query("GET", "/music/api/v1/playlist/batch-detail?guids="+guid+",official-1", "alice")
	if mixed.Code != 200 || !strings.Contains(mixed.Body.String(), "十指流玉") || !strings.Contains(mixed.Body.String(), "官方本地歌单") || !strings.Contains(mixed.Body.String(), `"extraData":true`) || !strings.Contains(mixed.Body.String(), `"extra":"keep"`) {
		t.Fatal("mixed batch lost records or fields", mixed.Body.String())
	}
	for _, alias := range []string{"playlistGUID", "playlistGuid", "guid"} {
		tracks := query("GET", "/music/api/v1/track/playlist-detail/list?"+alias+"="+guid+"&size=-1", "alice")
		if tracks.Code != 200 || !strings.Contains(tracks.Body.String(), "的音乐") || !strings.Contains(tracks.Body.String(), `"total":1`) {
			t.Fatal("missing tracks", tracks.Code, tracks.Body.String())
		}
		var result struct {
			Data struct {
				List []map[string]json.RawMessage `json:"list"`
			} `json:"data"`
		}
		if err := json.Unmarshal(tracks.Body.Bytes(), &result); err != nil || len(result.Data.List) != 1 {
			t.Fatal("invalid track list", err, tracks.Body.String())
		}
		for _, field := range []string{"createdAt", "updatedAt"} {
			if string(result.Data.List[0][field]) != "0" {
				t.Fatalf("official playlist date conversion requires numeric %s: %s", field, tracks.Body.String())
			}
		}
	}
	for _, method := range []string{"GET", "HEAD"} {
		image := query(method, "/music/api/v1/static/cover?coverId="+cover, "alice")
		if image.Code != 200 || image.Header().Get("Content-Type") != "image/png" || method == "GET" && image.Body.String() != "test-image" || method == "HEAD" && image.Body.Len() != 0 {
			t.Fatal("playlist image failed", image.Code, image.Body.String())
		}
	}
	config := store.Snapshot()
	config.PersonalPlaylists = false
	store.Update(config)
	hidden := query("GET", "/music/api/v1/playlist/batch-detail?guids="+guid, "alice")
	if strings.Contains(hidden.Body.String(), "十指流玉") {
		t.Fatal("hidden personal playlist leaked through batch")
	}
	if image := query("GET", "/music/api/v1/static/cover?coverId="+cover, "alice"); image.Code != 404 {
		t.Fatal("disabled image served", image.Code)
	}
	if image := query("GET", "/music/api/v1/static/cover?coverId="+cover, "bob"); image.Code == 200 {
		t.Fatal("cross user image exposed")
	}
}
