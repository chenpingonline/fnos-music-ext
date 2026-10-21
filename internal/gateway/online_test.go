package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

type stubMusic struct{ fail bool }

func (s stubMusic) ID() string                   { return "netease" }
func (s stubMusic) Health(context.Context) error { return nil }
func (s stubMusic) Search(_ context.Context, u provider.User, q provider.SearchRequest) (provider.SearchPage, error) {
	if e := u.Validate(); e != nil {
		return provider.SearchPage{}, e
	}
	if s.fail {
		return provider.SearchPage{}, errors.New("down")
	}
	items := []provider.Track{{ID: "123", Source: "netease", Title: "NetSong", Artists: []string{"Online"}, DurationMS: 120000, Playable: true}}
	if q.Offset > 0 {
		items = nil
	}
	return provider.SearchPage{Tracks: items, Total: len(items), HasMore: false}, nil
}
func (s stubMusic) GetTrack(_ context.Context, _ provider.User, id string) (provider.Track, error) {
	return provider.Track{ID: id, Source: "netease", Title: "NetSong", Artists: []string{"Online"}, Playable: true}, nil
}
func (s stubMusic) GetLyrics(_ context.Context, _ provider.User, id string) (provider.Lyrics, error) {
	return provider.Lyrics{LRC: "[00:00]test"}, nil
}
func (s stubMusic) ListPlaylists(context.Context, provider.User) ([]provider.Playlist, error) {
	return nil, nil
}
func (s stubMusic) Play(_ context.Context, _ provider.User, q provider.PlaybackRequest) (provider.Stream, error) {
	if q.Range == "__HEAD__" {
		return provider.Stream{Body: io.NopCloser(strings.NewReader("")), StatusCode: 200, ContentType: "audio/mpeg", ContentLength: 4}, nil
	}
	if q.Range == "bytes=0-1" {
		return provider.Stream{Body: io.NopCloser(strings.NewReader("de")), StatusCode: 206, ContentType: "audio/mpeg", ContentRange: "bytes 0-1/4", ContentLength: 2}, nil
	}
	return provider.Stream{Body: io.NopCloser(strings.NewReader("demo")), StatusCode: 200, ContentType: "audio/mpeg", ContentLength: 4}, nil
}
func TestOnlineIntegration(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "original.sock")
	ln, e := net.Listen("unix", sock)
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/music/api/v1/user/me" {
			guid := "alice"
			if r.Header.Get("Authorization") == "Bob" {
				guid = "bob"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"guid": guid}})
			return
		}
		if r.URL.Path == "/music/api/v1/search/track" {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"list": []map[string]any{{"guid": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "title": "Local", "artist": "Me"}}, "total": 1}})
			return
		}
		http.NotFound(w, r)
	})}
	go srv.Serve(ln)
	defer srv.Close()
	g := New(sock)
	defer g.Close()
	g.SetOnline(stubMusic{}, "alice")
	req := httptest.NewRequest("GET", "http://localhost/music/api/v1/search/track?q=x&page=1&size=3", nil)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	var out struct {
		Data struct {
			List  []map[string]any `json:"list"`
			Total int              `json:"total"`
		} `json:"data"`
	}
	if e := json.NewDecoder(rec.Body).Decode(&out); e != nil {
		t.Fatal(e)
	}
	if len(out.Data.List) != 2 || out.Data.Total != 2 || out.Data.List[0]["title"] != "Local" || out.Data.List[1]["title"] != "[网易云] NetSong" {
		t.Fatalf("merged=%+v", out)
	}
	guid, _ := fnmusic.EncodeNetEaseID("123")
	for _, tt := range []struct {
		path, method, rangeV string
		want                 int
		needle               string
	}{{"/music/api/v1/track/metadata?guid=" + guid, "GET", "", 200, "NetSong"}, {"/music/api/v1/track/lyrics?guid=" + guid, "GET", "", 200, "test"}, {"/music/api/v1/lyric/list?guid=" + guid, "GET", "", 200, "preferred"}, {"/music/api/v1/track/stream?guid=" + guid, "GET", "bytes=0-1", 206, "de"}, {"/music/api/v1/track/stream?guid=" + guid, "HEAD", "", 200, ""}} {
		r := httptest.NewRequest(tt.method, "http://localhost"+tt.path, nil)
		if tt.rangeV != "" {
			r.Header.Set("Range", tt.rangeV)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != tt.want || !bytes.Contains(w.Body.Bytes(), []byte(tt.needle)) {
			t.Errorf("%s: status=%d body=%s", tt.path, w.Code, w.Body.String())
		}
	}
	// Owner isolation: Bob's local search still works, but online track cannot play.
	r := httptest.NewRequest("GET", "http://localhost/music/api/v1/search/track?q=x", nil)
	r.Header.Set("Authorization", "Bob")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "NetSong") {
		t.Fatal("Bob saw Alice online tracks")
	}
	r = httptest.NewRequest("GET", "http://localhost/music/api/v1/track/stream?guid="+guid, nil)
	r.Header.Set("Authorization", "Bob")
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("private playback not denied", w.Code)
	}
	g.SetOnline(stubMusic{fail: true}, "alice")
	r = httptest.NewRequest("GET", "http://localhost/music/api/v1/search/track?q=x", nil)
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "Local") || strings.Contains(w.Body.String(), "NetSong") {
		t.Fatal("upstream failure was not local-only")
	}
}
