package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type pagedPlaylistSource struct {
	playlistAdapter
	listCalls int
	pageCalls []int
}

func (s *pagedPlaylistSource) ListPlaylists(context.Context, provider.User) ([]provider.Playlist, error) {
	s.listCalls++
	return []provider.Playlist{{ID: "901", Title: "large", TrackCount: 1205}}, nil
}
func (s *pagedPlaylistSource) PlaylistTracks(context.Context, provider.User, string) ([]provider.Track, error) {
	panic("full list loaded for a paged source")
}
func (s *pagedPlaylistSource) PlaylistTrackPage(_ context.Context, _ provider.User, _ string, offset, limit int) (provider.TrackPage, error) {
	s.pageCalls = append(s.pageCalls, offset)
	out := provider.TrackPage{Total: 1205, Tracks: []provider.Track{}}
	end := 1205
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	for i := offset; i < end; i++ {
		out.Tracks = append(out.Tracks, provider.Track{ID: fmt.Sprint(i + 1), Title: fmt.Sprint(i + 1), Playable: true})
	}
	return out, nil
}
func TestGatewayLargePlaylistPagesAndMetadataCache(t *testing.T) {
	g := New("/unused")
	defer g.Close()
	source := &pagedPlaylistSource{}
	g.SetOnline(source, "alice")
	settings, _ := preferences.New("")
	g.SetPreferences(settings)
	g.client = &http.Client{Transport: coverTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"guid":"alice"}}`))}, nil
	})}
	guid, _ := fnmusic.EncodePlaylistID("legacy", "901")
	for _, tc := range []struct {
		query string
		count int
		first string
	}{{"page=41&size=30", 5, "1201"}, {"page=1&size=30", 30, "1"}, {"page=1&size=-1", 1205, "1"}, {"page=9999&size=30", 0, ""}} {
		w := httptest.NewRecorder()
		g.ServeHTTP(w, httptest.NewRequest("GET", "/music/api/v1/track/playlist-detail/list?playlistGUID="+guid+"&"+tc.query, nil))
		var result struct {
			Data struct {
				List []struct {
					Title string `json:"title"`
				} `json:"list"`
				Total int `json:"total"`
			} `json:"data"`
		}
		e := json.Unmarshal(w.Body.Bytes(), &result)
		if e != nil || w.Code != 200 || len(result.Data.List) != tc.count || result.Data.Total != 1205 {
			t.Fatalf("%s code=%d %s", tc.query, w.Code, w.Body.String())
		}
		if tc.count > 0 && result.Data.List[0].Title != tc.first {
			t.Fatal(result.Data.List[0])
		}
	}
	if source.listCalls != 1 {
		t.Fatal("metadata not reused", source.listCalls)
	}
	cfg := settings.Snapshot()
	cfg.PersonalPlaylists = false
	settings.Update(cfg)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest("GET", "/music/api/v1/track/playlist-detail/list?playlistGUID="+guid, nil))
	if w.Code != 404 {
		t.Fatal("disabled list exposed by cached metadata", w.Code, w.Body.String())
	}
}

func TestIdentityIsVerifiedOncePerRequest(t *testing.T) {
	g := New("/unused")
	defer g.Close()
	source := &cachedCoverSource{}
	g.SetOnline(source, "alice")
	g.cache.put("legacy:123", provider.Track{ID: "123", CoverURL: "https://p1.music.126.net/known.jpg"})
	calls := 0
	g.client = &http.Client{Transport: coverTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"guid":"alice"}}`))}, nil
	})}
	guid, _ := fnmusic.EncodeNetEaseID("123")
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		g.ServeHTTP(w, httptest.NewRequest("GET", "/music/api/v1/static/cover?coverId=track_"+guid, nil))
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if calls != 2 {
		t.Fatal("verification should happen once for each request, without caching across requests", calls)
	}
}
