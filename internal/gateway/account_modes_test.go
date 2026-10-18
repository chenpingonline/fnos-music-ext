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

type accountAdapter struct{ label string }

func (a accountAdapter) ID() string                   { return "netease" }
func (a accountAdapter) Health(context.Context) error { return nil }
func (a accountAdapter) Search(_ context.Context, _ provider.User, _ provider.SearchRequest) (provider.SearchPage, error) {
	return provider.SearchPage{Tracks: []provider.Track{{ID: "123", Source: "netease", Title: a.label, Artists: []string{"Demo"}, Playable: true}}, Total: 1}, nil
}
func (a accountAdapter) GetTrack(_ context.Context, _ provider.User, id string) (provider.Track, error) {
	return provider.Track{ID: id, Source: "netease", Title: a.label, Artists: []string{"Demo"}, Playable: true}, nil
}
func (a accountAdapter) Play(_ context.Context, _ provider.User, _ provider.PlaybackRequest) (provider.Stream, error) {
	return provider.Stream{Body: io.NopCloser(strings.NewReader(a.label)), ContentType: "audio/mpeg", StatusCode: 200, ContentLength: int64(len(a.label))}, nil
}
func (a accountAdapter) GetLyrics(context.Context, provider.User, string) (provider.Lyrics, error) {
	return provider.Lyrics{LRC: a.label}, nil
}
func (a accountAdapter) ListPlaylists(context.Context, provider.User) ([]provider.Playlist, error) {
	return nil, nil
}
func TestAccountModeSwitchAndIdentityGate(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "official.sock")
	listener, e := net.Listen("unix", sock)
	if e != nil {
		t.Fatal(e)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/music/api/v1/user/me" {
			who := strings.TrimPrefix(r.Header.Get("Authorization"), "Token ")
			if who == "" {
				http.Error(w, "unauthorized", 401)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"guid": who}})
			return
		}
		if r.URL.Path == "/music/api/v1/search/track" {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"list": []any{}, "total": 0}})
			return
		}
		http.NotFound(w, r)
	})}
	go srv.Serve(listener)
	defer srv.Close()
	manager, e := accounts.New(filepath.Join(t.TempDir(), "private", "accounts.json"), func(u string) (provider.Adapter, error) {
		if strings.Contains(u, "8771") {
			return accountAdapter{"B"}, nil
		}
		return accountAdapter{"A"}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	_ = manager.Add(accounts.Account{ID: "a", Name: "A", BridgeURL: "http://127.0.0.1:8770"})
	_ = manager.Add(accounts.Account{ID: "b", Name: "B", BridgeURL: "http://127.0.0.1:8771"})
	_ = manager.Bind("alice", "a")
	_ = manager.Bind("bob", "b")
	g := New(sock)
	g.SetAccounts(manager)
	defer g.Close()
	query := func(who, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if who != "" {
			r.Header.Set("Authorization", "Token "+who)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	path := "/music/api/v1/search/track?q=whatever&size=3"
	if body := query("alice", path).Body.String(); !strings.Contains(body, "[music box] A") || strings.Contains(body, "[music box] B") {
		t.Fatal("Alice leak", body)
	}
	if body := query("bob", path).Body.String(); !strings.Contains(body, "[music box] B") || strings.Contains(body, "[music box] A") {
		t.Fatal("Bob leak", body)
	}
	if body := query("charlie", path).Body.String(); strings.Contains(body, "music box") {
		t.Fatal("unbound user leak", body)
	}
	if body := query("", path).Body.String(); strings.Contains(body, "music box") {
		t.Fatal("anonymous leak", body)
	}
	guid, _ := fnmusic.EncodeNetEaseID("123")
	metadata := "/music/api/v1/track/metadata?guid=" + guid
	if body := query("alice", metadata).Body.String(); !strings.Contains(body, `"title":"A"`) {
		t.Fatal("Alice metadata", body)
	}
	if body := query("bob", metadata).Body.String(); !strings.Contains(body, `"title":"B"`) {
		t.Fatal("Bob metadata cache leak", body)
	}
	if e := manager.SetShared("a"); e != nil {
		t.Fatal(e)
	}
	if e := manager.SetIsolation(false); e != nil {
		t.Fatal(e)
	}
	for _, who := range []string{"alice", "bob", "charlie"} {
		if body := query(who, path).Body.String(); !strings.Contains(body, "[music box] A") {
			t.Fatal("shared not resolved", who, body)
		}
	}
	if body := query("bob", metadata).Body.String(); !strings.Contains(body, `"title":"A"`) {
		t.Fatal("shared cache stale", body)
	}
	if e := manager.SetShared("b"); e != nil {
		t.Fatal(e)
	}
	if body := query("alice", metadata).Body.String(); !strings.Contains(body, `"title":"B"`) {
		t.Fatal("switch cache stale", body)
	}
	if status := query("", metadata).Code; status != 403 {
		t.Fatal("anonymous metadata", status)
	}
	if e := manager.SetIsolation(true); e != nil {
		t.Fatal(e)
	}
	if body := query("charlie", path).Body.String(); strings.Contains(body, "music box") {
		t.Fatal("isolation not restored", body)
	}
}
