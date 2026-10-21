package netease

import (
	"context"
	"encoding/json"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

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
