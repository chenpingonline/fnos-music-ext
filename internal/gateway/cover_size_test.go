package gateway

import (
	"context"
	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestRequestedCoverSize(t *testing.T) {
	for _, tc := range []struct {
		query string
		size  int
		valid bool
	}{{"", 0, true}, {"?size=0", 0, true}, {"?size=160", 160, true}, {"?size=600", 600, true}, {"?size=9999", 2048, true}, {"?size=-1", 0, false}, {"?size=invalid", 0, false}} {
		got, e := requestedCoverSize(httptest.NewRequest("GET", "/cover"+tc.query, nil))
		if got != tc.size || (e == nil) != tc.valid {
			t.Fatalf("%s got=%d err=%v", tc.query, got, e)
		}
	}
}

type sizedCoverSource struct {
	cachedCoverSource
	sizes []int
}

func (s *sizedCoverSource) OpenTrackCoverSized(_ context.Context, _ provider.User, _ provider.Track, size int) (provider.Stream, error) {
	return s.image(size)
}
func (s *sizedCoverSource) OpenCoverSized(_ context.Context, _ provider.User, _ string, size int) (provider.Stream, error) {
	return s.image(size)
}
func (s *sizedCoverSource) ListPlaylists(context.Context, provider.User) ([]provider.Playlist, error) {
	return []provider.Playlist{{ID: "901", CoverURL: "https://p1.music.126.net/playlist.jpg"}}, nil
}
func (s *sizedCoverSource) OpenPlaylistCover(context.Context, provider.User, provider.Playlist) (provider.Stream, error) {
	panic("unsized playlist path used")
}
func (s *sizedCoverSource) OpenPlaylistCoverSized(_ context.Context, _ provider.User, _ provider.Playlist, size int) (provider.Stream, error) {
	return s.image(size)
}
func (s *sizedCoverSource) image(size int) (provider.Stream, error) {
	s.sizes = append(s.sizes, size)
	data := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 24) + strconv.Itoa(size)
	return provider.Stream{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data)), ContentType: "image/png", ContentLength: int64(len(data))}, nil
}
func TestCoverCacheVariantsAndUpgrade(t *testing.T) {
	for _, kind := range []string{"track", "playlist"} {
		t.Run(kind, func(t *testing.T) {
			g := New("/unused")
			defer g.Close()
			settings, _ := preferences.New("")
			if e := g.Configure(t.TempDir(), settings); e != nil {
				t.Fatal(e)
			}
			g.client = &http.Client{Transport: coverTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"guid":"alice"}}`))}, nil
			})}
			source := &sizedCoverSource{}
			g.SetOnline(source, "alice")
			guid, _ := fnmusic.EncodeNetEaseID("123")
			if kind == "playlist" {
				guid, _ = fnmusic.EncodePlaylistID("legacy", "901")
			}
			oldKey := "alice\x00legacy\x00" + kind + ":" + guid
			old, e := g.Media().Cover(context.Background(), oldKey, func(context.Context) (provider.Stream, error) { return source.image(999) })
			if e != nil {
				t.Fatal(e)
			}
			old.Body.Close()
			source.sizes = nil
			g.cache.put("legacy:123", provider.Track{ID: "123", CoverURL: "https://p1.music.126.net/known.jpg"})
			for _, size := range []int{160, 600, 0, 160} {
				r := httptest.NewRequest("GET", "/music/api/v1/static/cover?coverId=track_"+guid+"&size="+strconv.Itoa(size), nil)
				w := httptest.NewRecorder()
				g.ServeHTTP(w, r)
				if w.Code != 200 || !strings.HasSuffix(w.Body.String(), strconv.Itoa(size)) {
					t.Fatalf("size=%d status=%d body=%q", size, w.Code, w.Body.String())
				}
			}
			if len(source.sizes) != 3 || source.sizes[0] != 160 || source.sizes[1] != 600 || source.sizes[2] != 0 {
				t.Fatalf("wrong cache variants %v", source.sizes)
			}
			// Metadata miss must also pass the size to the detail fallback.
			if kind == "track" {
				g.cache = newTrackCache()
				g.Media().CoverCache(true)
				r := httptest.NewRequest("GET", "/music/api/v1/static/cover?coverId=track_"+guid+"&size=320", nil)
				w := httptest.NewRecorder()
				g.ServeHTTP(w, r)
				if w.Code != 200 || source.sizes[len(source.sizes)-1] != 320 {
					t.Fatal("detail fallback lost requested size", w.Code)
				}
			}
		})
	}
}
