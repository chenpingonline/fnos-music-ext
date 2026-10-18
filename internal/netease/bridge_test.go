package netease

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

func user() provider.User { return provider.User{FnOSGUID: "alice", Verified: true} }
func TestBridgeValidatesLoopback(t *testing.T) {
	for _, addr := range []string{"http://10.0.0.5:8770", "https://127.0.0.1:8770", "http://127.0.0.1:8770/foo", "http://localhost:8770/?x=1", "http://user:password@127.0.0.1:8770"} {
		if _, e := NewBridge(addr); e == nil {
			t.Fatalf("accepted %s", addr)
		}
	}
	if _, e := NewBridge("http://127.0.0.1:8770"); e != nil {
		t.Fatal(e)
	}
}
func TestMusicboxEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/search":
			if r.URL.Query().Get("keyword") != "你好" || r.URL.Query().Get("type") != "song" {
				t.Errorf("query %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"ok":true,"data":[{"song_id":123,"title":"晴天","artist":"周杰伦","album":"叶惠美","duration_ms":269000,"cover_url":"https://p1.music.126.net/photo.jpg"}]}`)
		case "/api/v1/song/123/info":
			_, _ = io.WriteString(w, `{"ok":true,"data":{"id":123,"name":"晴天","ar":[{"name":"周杰伦"}],"al":{"name":"叶惠美","picUrl":"https://p1.music.126.net/photo.jpg"},"dt":269000}}`)
		case "/api/v1/song/123/lyric":
			_, _ = io.WriteString(w, `{"ok":true,"data":{"lyric":"[00:00.00]你好","tlyric":"hello"}}`)
		case "/api/v1/song/123/url":
			_, _ = io.WriteString(w, `{"ok":true,"data":{"code":200,"url":"https://audio.example.com/song.mp3"}}`)
		case "/healthz":
			_, _ = io.WriteString(w, `{"status":"ok"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a, _ := NewBridge(srv.URL)
	if e := a.Health(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Search(context.Background(), provider.User{}, provider.SearchRequest{Keyword: "hi", Limit: 20}); e != provider.ErrUnauthenticated {
		t.Fatalf("auth=%v", e)
	}
	page, e := a.Search(context.Background(), user(), provider.SearchRequest{Keyword: "你好", Limit: 20})
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Tracks) != 1 || page.Tracks[0].Title != "晴天" || page.Tracks[0].DurationMS != 269000 {
		t.Fatalf("search=%+v", page)
	}
	track, e := a.GetTrack(context.Background(), user(), "123")
	if e != nil || track.Album != "叶惠美" || track.CoverURL == "" {
		t.Fatalf("track=%+v err=%v", track, e)
	}
	lyrics, e := a.GetLyrics(context.Background(), user(), "123")
	if e != nil || !strings.Contains(lyrics.LRC, "你好") {
		t.Fatalf("lyrics=%+v err=%v", lyrics, e)
	}
	if _, e := a.ListPlaylists(context.Background(), user()); e != ErrUnsupported {
		t.Fatal(e)
	}
}
func TestMediaSafety(t *testing.T) {
	for _, src := range []string{"http://127.0.0.1/song.mp3", "http://192.168.2.1/", "file:///etc/passwd", "http://metadata.google.internal/", "http://169.254.169.254/", "http://[::1]/"} {
		u, _ := url.Parse(src)
		if safeMediaURL(u) {
			t.Errorf("allowed %s", src)
		}
	}
	u, _ := url.Parse("https://music.126.net/song.flac")
	if !safeMediaURL(u) {
		t.Fatal("public CDN refused")
	}
}
