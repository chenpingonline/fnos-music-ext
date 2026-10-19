package netease

import (
	"context"
	"encoding/json"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestBridgeDiscoveryUsesActualMusicboxEnvelopes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/public-playlists":
			io.WriteString(w, `{"ok":true,"items":[{"id":"3778678","name":"热歌榜","track_count":200,"cover_url":"https://p1.music.126.net/test.jpg"}],"has_more":false}`)
		case "/api/v1/public-playlists/categories":
			io.WriteString(w, `{"ok":true,"categories":[{"name":"全部"},{"name":"语种"}],"tags":{"语种":[{"name":"华语"}]}}`)
		case "/api/v1/public-playlists/3778678/tracks", "/api/v1/recommend/songs":
			io.WriteString(w, `{"ok":true,"data":[{"song_id":123,"title":"Song","artist":"Artist"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	b, e := NewBridge(srv.URL)
	if e != nil {
		t.Fatal(e)
	}
	u := provider.User{FnOSGUID: "alice", Verified: true}
	rows, e := b.Discover(context.Background(), u, "chart", "", 1, 24)
	if e != nil || len(rows) != 1 || rows[0].ID != "3778678" {
		t.Fatal(rows, e)
	}
	tags, e := b.Categories(context.Background(), u)
	if e != nil || strings.Join(tags, ",") != "全部,华语" {
		t.Fatal(tags, e)
	}
	tracks, e := b.PublicPlaylistTracks(context.Background(), u, "3778678")
	if e != nil || len(tracks) != 1 || tracks[0].ID != "123" {
		t.Fatal(tracks, e)
	}
	tracks, e = b.Recommendations(context.Background(), u, "daily")
	if e != nil || len(tracks) != 1 {
		t.Fatal(tracks, e)
	}
}
func TestNativeDiscoveryCursorAndDaily(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, args := unpackEAPI(t, r)
		switch path {
		case "/api/playlist/highquality/list":
			calls++
			id := 1
			if args["before"] != nil {
				id = 2
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 200, "playlists": []any{map[string]any{"id": id, "name": "List", "updateTime": 100}}})
		case "/api/v3/discovery/recommend/songs":
			io.WriteString(w, `{"code":200,"data":{"dailySongs":[{"id":123,"name":"Daily","ar":[{"name":"Artist"}],"al":{"name":"Album"}}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	n, e := newNative("", filepath.Join(t.TempDir(), "session"), srv.URL, srv.Client())
	if e != nil {
		t.Fatal(e)
	}
	u := provider.User{FnOSGUID: "alice", Verified: true}
	rows, e := n.Discover(context.Background(), u, "highquality", "全部", 2, 1)
	if e != nil || calls != 2 || len(rows) != 1 || rows[0].ID != "2" {
		t.Fatal(rows, calls, e)
	}
	tracks, e := n.Recommendations(context.Background(), u, "daily")
	if e != nil || len(tracks) != 1 || tracks[0].ID != "123" {
		t.Fatal(tracks, e)
	}
	if _, e = n.Recommendations(context.Background(), provider.User{}, "daily"); e == nil {
		t.Fatal("anonymous recommendation accepted")
	}
}
