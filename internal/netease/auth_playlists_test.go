package netease

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

func TestQRAndOwnPlaylists(t *testing.T) {
	var pollKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/login":
			if r.Method != "POST" {
				t.Error("login should POST")
			}
			fmt.Fprint(w, `{"data":{"unikey":"secret-123","qr_ascii":"##\\n##"}}`)
		case "/api/v1/auth/login/check":
			pollKey = r.URL.Query().Get("unikey")
			fmt.Fprint(w, `{"data":{"code":803}}`)
		case "/api/v1/auth/status":
			fmt.Fprint(w, `{"data":{"logged_in":true}}`)
		case "/api/v1/user/playlists":
			fmt.Fprint(w, `{"ok":true,"logged_in":true,"account_uid":222,"data":[{"playlist_id":111,"name":"我的收藏","user_id":222,"track_count":1},{"playlist_id":333,"name":"其他人的歌单","user_id":991,"track_count":50}]}`)
		case "/api/v1/user/playlists/111/tracks":
			fmt.Fprint(w, `{"ok":true,"data":[{"id":123,"name":"歌一","ar":[{"name":"歌手"}],"dt":200000}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	bridge, _ := NewBridge(srv.URL)
	qr, err := bridge.BeginQRLogin(context.Background())
	if err != nil || qr.Key != "secret-123" || !strings.Contains(qr.QRURL, "codekey=secret-123") {
		t.Fatalf("qr=%+v, err=%v", qr, err)
	}
	raw, _ := json.Marshal(qr)
	if strings.Contains(string(raw), `"key"`) {
		t.Fatal("sensitive key leaked by JSON")
	}
	state, err := bridge.CheckQRLogin(context.Background(), qr.Key)
	if err != nil || !state.LoggedIn || state.State != "success" || pollKey != "secret-123" {
		t.Fatal(state, err, pollKey)
	}
	status, err := bridge.AuthStatus(context.Background())
	if err != nil || !status.LoggedIn {
		t.Fatal(status, err)
	}
	playlists, err := bridge.ListPlaylists(context.Background(), provider.User{FnOSGUID: "alice", Verified: true})
	if err != nil || len(playlists) != 1 || playlists[0].ID != "111" || playlists[0].TrackCount != 1 {
		t.Fatal(playlists, err)
	}
	tracks, err := bridge.PlaylistTracks(context.Background(), provider.User{FnOSGUID: "alice", Verified: true}, "111")
	if err != nil || len(tracks) != 1 || tracks[0].Title != "歌一" {
		t.Fatal(tracks, err)
	}
	if _, err := bridge.ListPlaylists(context.Background(), provider.User{}); err != provider.ErrUnauthenticated {
		t.Fatal(err)
	}
	if _, err := bridge.PlaylistTracks(context.Background(), provider.User{}, "111"); err != provider.ErrUnauthenticated {
		t.Fatal(err)
	}
}
func TestQRStateCodes(t *testing.T) {
	for _, tc := range []struct {
		code   int64
		state  string
		logged bool
	}{{800, "expired", false}, {801, "pending", false}, {802, "scanned", false}, {803, "success", true}, {404, "unknown", false}} {
		s := stateFromCode(tc.code)
		if s.State != tc.state || s.LoggedIn != tc.logged {
			t.Fatalf("%d: %+v", tc.code, s)
		}
	}
}
