package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

type coverTransport func(*http.Request) (*http.Response, error)

func (f coverTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type cachedCoverSource struct {
	stubMusic
	cachedCalls, detailCalls int
	cachedFail               bool
	validImage               bool
}

func (s *cachedCoverSource) OpenTrackCover(_ context.Context, u provider.User, track provider.Track) (provider.Stream, error) {
	s.cachedCalls++
	if u.FnOSGUID != "alice" || track.CoverURL != "https://p1.music.126.net/known.jpg" {
		return provider.Stream{}, errors.New("unexpected cached metadata")
	}
	if s.cachedFail {
		return provider.Stream{}, errors.New("expired image URL")
	}
	data := "image"
	if s.validImage {
		data = "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 24)
	}
	return provider.Stream{Body: io.NopCloser(strings.NewReader(data)), ContentType: "image/png", ContentLength: int64(len(data)), StatusCode: 200}, nil
}
func (s *cachedCoverSource) OpenCover(_ context.Context, _ provider.User, _ string) (provider.Stream, error) {
	s.detailCalls++
	if !s.cachedFail {
		return provider.Stream{}, errors.New("song detail unavailable")
	}
	return provider.Stream{Body: io.NopCloser(strings.NewReader("fresh")), ContentType: "image/png", ContentLength: 5, StatusCode: 200}, nil
}
func TestCoverUsesScopedMetadataAndFallsBack(t *testing.T) {
	for _, tc := range []struct {
		name, scope, method    string
		fail                   bool
		status, cached, detail int
		body                   string
	}{
		{"playlist metadata survives detail outage", "legacy", "GET", false, 200, 1, 0, "image"},
		{"head uses same path", "legacy", "HEAD", false, 200, 1, 0, ""},
		{"stale URL refreshes through source", "legacy", "GET", true, 200, 1, 1, "fresh"},
		{"other account metadata is ignored", "other-account", "GET", false, 502, 0, 1, "cover unavailable\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := New("/unused")
			defer g.Close()
			g.client = &http.Client{Transport: coverTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"guid":"alice"}}`))}, nil
			})}
			source := &cachedCoverSource{cachedFail: tc.fail}
			g.SetOnline(source, "alice")
			g.cache.put(tc.scope+":123", provider.Track{ID: "123", CoverURL: "https://p1.music.126.net/known.jpg"})
			guid, _ := fnmusic.EncodeNetEaseID("123")
			req := httptest.NewRequest(tc.method, "/music/api/v1/static/cover?coverId=track_"+guid+"&size=160&url=http://127.0.0.1/private", nil)
			w := httptest.NewRecorder()
			g.ServeHTTP(w, req)
			if w.Code != tc.status || w.Body.String() != tc.body || source.cachedCalls != tc.cached || source.detailCalls != tc.detail {
				t.Fatalf("status=%d body=%q cached=%d detail=%d", w.Code, w.Body.String(), source.cachedCalls, source.detailCalls)
			}
			if w.Code == 200 && w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing image content protection")
			}
		})
	}
}

type fallbackPlaylistCoverSource struct {
	playlistAdapter
	urls                []string
	tracksFail, allFail bool
}

func (s *fallbackPlaylistCoverSource) ListPlaylists(context.Context, provider.User) ([]provider.Playlist, error) {
	return []provider.Playlist{{ID: "901", CoverURL: "https://cdn.example/expired.jpg"}}, nil
}
func (s *fallbackPlaylistCoverSource) PlaylistTracks(context.Context, provider.User, string) ([]provider.Track, error) {
	if s.tracksFail {
		return nil, errors.New("source unavailable")
	}
	return []provider.Track{{ID: "1", CoverURL: "https://cdn.example/expired.jpg"}, {ID: "2", CoverURL: "https://cdn.example/other-broken.jpg"}, {ID: "3", CoverURL: "https://cdn.example/good.jpg"}}, nil
}
func (s *fallbackPlaylistCoverSource) OpenPlaylistCover(_ context.Context, u provider.User, p provider.Playlist) (provider.Stream, error) {
	s.urls = append(s.urls, p.CoverURL)
	if u.FnOSGUID != "alice" || s.allFail || p.CoverURL != "https://cdn.example/good.jpg" {
		return provider.Stream{}, errors.New("CDN image unavailable")
	}
	return provider.Stream{Body: io.NopCloser(strings.NewReader("artwork")), ContentType: "image/jpeg", ContentLength: 7, StatusCode: 200}, nil
}
func TestPlaylistCoverFallsBackWhenSavedURLFails(t *testing.T) {
	for _, tc := range []struct {
		name, method        string
		tracksFail, allFail bool
		status              int
		stage, body         string
	}{
		{"saved cover failure uses track artwork", "GET", false, false, 200, "", "artwork"},
		{"HEAD follows fallback without sending body", "HEAD", false, false, 200, "", ""},
		{"all images fail with diagnostic stage", "GET", false, true, 502, "playlist-image-fetch", "playlist cover unavailable\n"},
		{"track source fails with diagnostic stage", "GET", true, false, 502, "playlist-tracks", "playlist cover unavailable\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := New("/unused")
			defer g.Close()
			g.client = &http.Client{Transport: coverTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"guid":"alice"}}`))}, nil
			})}
			source := &fallbackPlaylistCoverSource{tracksFail: tc.tracksFail, allFail: tc.allFail}
			g.SetOnline(source, "alice")
			guid, _ := fnmusic.EncodePlaylistID("legacy", "901")
			req := httptest.NewRequest(tc.method, "/music/api/v1/static/cover?coverId=track_"+guid+"&size=160", nil)
			w := httptest.NewRecorder()
			g.ServeHTTP(w, req)
			if w.Code != tc.status || w.Body.String() != tc.body || w.Header().Get("X-FnMusic-Cover-Error") != tc.stage {
				t.Fatalf("status=%d body=%q headers=%v", w.Code, w.Body.String(), w.Header())
			}
			want := 3
			if tc.tracksFail {
				want = 1
			}
			if len(source.urls) != want {
				t.Fatalf("duplicate URLs should be skipped; attempts=%v", source.urls)
			}
		})
	}
}

func TestCoverRouteUsesDiskAfterRestartAndClear(t *testing.T) {
	root := t.TempDir()
	settings, _ := preferences.New("")
	source := &cachedCoverSource{validImage: true}
	create := func() *Gateway {
		g := New("/unused")
		t.Cleanup(g.Close)
		g.client = &http.Client{Transport: coverTransport(func(r *http.Request) (*http.Response, error) {
			who := "alice"
			if r.Header.Get("Authorization") == "bob" {
				who = "bob"
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"guid":"` + who + `"}}`))}, nil
		})}
		g.SetOnline(source, "alice")
		if e := g.Configure(root, settings); e != nil {
			t.Fatal(e)
		}
		return g
	}
	guid, _ := fnmusic.EncodeNetEaseID("123")
	call := func(g *Gateway, user string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/music/api/v1/static/cover?coverId=track_"+guid+"&size=160", nil)
		r.Header.Set("Authorization", user)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	first := create()
	first.cache.put("legacy:123", provider.Track{ID: "123", CoverURL: "https://p1.music.126.net/known.jpg"})
	a := call(first, "alice")
	if a.Code != 200 || source.cachedCalls != 1 {
		t.Fatal(a.Code, a.Body.String())
	}
	second := create()
	b := call(second, "alice")
	if b.Code != 200 || b.Body.String() != a.Body.String() || source.cachedCalls != 1 || source.detailCalls != 0 {
		t.Fatal("disk cache not used after restart", b.Code, source.cachedCalls, source.detailCalls)
	}
	if w := call(second, "bob"); w.Code == 200 {
		t.Fatal("unauthorized user received cached cover")
	}
	if _, e := second.Media().CoverCache(true); e != nil {
		t.Fatal(e)
	}
	c := call(first, "alice")
	if c.Code != 200 || source.cachedCalls != 2 {
		t.Fatal("clear did not force download", c.Code, source.cachedCalls)
	}
}
