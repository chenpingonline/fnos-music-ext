package netease

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func TestNativeLargePlaylistPagesFetchOnlyRequestedTracks(t *testing.T) {
	var mu sync.Mutex
	requested := []int{}
	total := 1205
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, args := unpackEAPI(t, r)
		switch path {
		case "/api/v6/playlist/detail":
			ids := []map[string]int{}
			for i := 1; i <= total; i++ {
				ids = append(ids, map[string]int{"id": i})
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 200, "playlist": map[string]any{"trackIds": ids, "trackCount": total}})
		case "/api/v3/song/detail":
			var batch []struct {
				ID int `json:"id"`
			}
			json.Unmarshal([]byte(args["c"].(string)), &batch)
			songs := []map[string]any{}
			for i := len(batch) - 1; i >= 0; i-- {
				id := batch[i].ID
				mu.Lock()
				requested = append(requested, id)
				mu.Unlock()
				if id == 1202 {
					continue
				}
				songs = append(songs, map[string]any{"id": id, "name": fmt.Sprintf("song-%d", id), "dt": 120000})
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 200, "songs": songs})
		default:
			t.Errorf("unexpected path %s", path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	n, e := newNative("", filepath.Join(t.TempDir(), "session.json"), srv.URL, srv.Client())
	if e != nil {
		t.Fatal(e)
	}
	u := provider.User{FnOSGUID: "alice", Verified: true}
	page, e := n.PublicPlaylistTrackPage(context.Background(), u, "99", 1200, 30)
	if e != nil || page.Total != 1205 || len(page.Tracks) != 5 {
		t.Fatal(page, e)
	}
	for i, track := range page.Tracks {
		if track.ID != strconv.Itoa(1201+i) {
			t.Fatal("upstream reordered page", page)
		}
	}
	if page.Tracks[1].AccessStatus != 1 || page.Tracks[1].Playable {
		t.Fatal("missing song not preserved as unavailable")
	}
	if len(requested) != 5 {
		t.Fatal("fetched entire playlist", len(requested))
	}
	requested = nil
	all, e := n.playlistTracks(context.Background(), "99")
	if e != nil || len(all) != 1205 || len(requested) != 1205 {
		t.Fatal("full playlist truncated", len(all), len(requested), e)
	}
	empty, e := n.PublicPlaylistTrackPage(context.Background(), u, "99", 9999000, 30)
	if e != nil || len(empty.Tracks) != 0 || empty.Total != 1205 {
		t.Fatal(empty, e)
	}
	if _, e = n.PublicPlaylistTrackPage(context.Background(), provider.User{}, "99", 0, 30); e == nil {
		t.Fatal("anonymous allowed")
	}
}
