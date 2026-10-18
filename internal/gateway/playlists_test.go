package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenpingonline/fnos-music-ext/internal/accounts"
	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

type playlistAdapter struct{ accountAdapter }

func (p playlistAdapter) ListPlaylists(ctx context.Context, u provider.User) ([]provider.Playlist, error) {
	if u.Validate() != nil {
		return nil, provider.ErrUnauthenticated
	}
	return []provider.Playlist{{ID: "901", Title: p.label + " 的网易歌单", TrackCount: 1, ReadOnly: true}}, nil
}
func (p playlistAdapter) PlaylistTracks(ctx context.Context, u provider.User, id string) ([]provider.Track, error) {
	if id != "901" {
		return nil, provider.ErrNotEntitled
	}
	return []provider.Track{{ID: "123", Source: "netease", Title: p.label + " 的音乐", Artists: []string{"演唱"}, Playable: true}}, nil
}
func TestOfficialPlaylistInjectionAndCrossAccountBlock(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "official.sock")
	listener, e := net.Listen("unix", socket)
	if e != nil {
		t.Fatal(e)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/music/api/v1/user/me" {
			who := strings.TrimPrefix(r.Header.Get("Authorization"), "Token ")
			if who == "" {
				http.Error(w, "auth required", 401)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"guid": who}})
			return
		}
		if r.URL.Path == "/music/api/v1/playlist/list" {
			io.WriteString(w, `{"code":0,"msg":"OK","data":{"list":[{"guid":"official-1","name":"本地音乐"}],"total":1},"extra":"keep"}`)
			return
		}
		http.NotFound(w, r)
	})}
	go srv.Serve(listener)
	defer srv.Close()
	m, e := accounts.New(filepath.Join(t.TempDir(), "private", "accounts.json"), func(u string) (provider.Adapter, error) {
		if strings.HasSuffix(u, ":8771") {
			return playlistAdapter{accountAdapter{label: "B"}}, nil
		}
		return playlistAdapter{accountAdapter{label: "A"}}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	for _, a := range []accounts.Account{{ID: "a", Name: "a", BridgeURL: "http://127.0.0.1:8770"}, {ID: "b", Name: "b", BridgeURL: "http://127.0.0.1:8771"}} {
		if e := m.Add(a); e != nil {
			t.Fatal(e)
		}
	}
	if e := m.Bind("alice", "a"); e != nil {
		t.Fatal(e)
	}
	if e := m.Bind("bob", "b"); e != nil {
		t.Fatal(e)
	}
	g := New(socket)
	g.SetAccounts(m)
	defer g.Close()
	query := func(user, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if user != "" {
			r.Header.Set("Authorization", "Token "+user)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	listPath := "/music/api/v1/playlist/list"
	aList := query("alice", listPath)
	if aList.Code != 200 || !strings.Contains(aList.Body.String(), "A 的网易歌单") || !strings.Contains(aList.Body.String(), "本地音乐") || !strings.Contains(aList.Body.String(), `"extra":"keep"`) {
		t.Fatal(aList.Code, aList.Body.String())
	}
	if anon := query("", listPath); strings.Contains(anon.Body.String(), "网易歌单") {
		t.Fatal("unauthenticated playlist exposed")
	}
	_, ascope, _ := m.ResolveScoped("alice")
	_, bscope, _ := m.ResolveScoped("bob")
	aid, ok := fnmusic.EncodePlaylistID(ascope, "901")
	if !ok {
		t.Fatal("bad GUID")
	}
	bid, ok := fnmusic.EncodePlaylistID(bscope, "901")
	if !ok || aid == bid {
		t.Fatal("account GUIDs collide")
	}
	detail := "/music/api/v1/playlist/detail?guid=" + aid
	if body := query("alice", detail).Body.String(); !strings.Contains(body, "A 的网易歌单") {
		t.Fatal("own detail missing", body)
	}
	if body := query("bob", detail).Body.String(); strings.Contains(body, "A 的网易歌单") {
		t.Fatal("cross-account detail leak", body)
	}
	tracks := "/music/api/v1/track/playlist-detail/list?playlistGUID=" + aid
	if body := query("alice", tracks).Body.String(); !strings.Contains(body, "A 的音乐") {
		t.Fatal("own playlist tracks missing", body)
	}
	if body := query("bob", tracks).Body.String(); strings.Contains(body, "A 的音乐") {
		t.Fatal("cross-account song leak", body)
	}
	if e := m.SetShared("b"); e != nil {
		t.Fatal(e)
	}
	if e := m.SetIsolation(false); e != nil {
		t.Fatal(e)
	}
	if body := query("alice", tracks).Body.String(); strings.Contains(body, "A 的音乐") {
		t.Fatal("old GUID survives shared account switch", body)
	}
	if body := query("alice", "/music/api/v1/playlist/detail?guid="+bid).Body.String(); !strings.Contains(body, "B 的网易歌单") {
		t.Fatal("shared account not active", body)
	}
}
