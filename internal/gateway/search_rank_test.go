package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

func TestSearchTier(t *testing.T) {
	for _, tc := range []struct {
		query, title, artist string
		tier                 int
	}{
		{"晴天 周杰伦", "晴天", "周杰伦", 0},
		{"周杰伦 晴天", "晴天", "周杰伦", 0},
		{"晴天", "晴天", "周杰伦", 1},
		{"晴天", "晴天 (Live)", "周杰伦", 2},
		{"周杰伦", "晴天", "周杰伦", 3},
		{"unknown", "晴天", "周杰伦", 4},
		{" ＨＥＬＬＯ！ ", "Hello", "Adele", 1},
		{"Love Taylor", "Love Story", "Taylor Swift", 2},
		{"", "", "", 4},
	} {
		if got := searchTier(tc.query, tc.title, tc.artist); got != tc.tier {
			t.Errorf("%+v: tier=%d", tc, got)
		}
	}
}

func TestRankSearchRelevanceAndThreeSources(t *testing.T) {
	candidates := []searchCandidate{}
	add := func(id, title, source string) {
		candidates = append(candidates, searchCandidate{raw: json.RawMessage(strconv.Quote(id)), title: title, source: source})
	}
	add("local-other", "unrelated", "local")
	add("local-1", "晴天", "local")
	add("local-2", "晴天", "local")
	add("local-3", "晴天", "local")
	add("netease-1", "晴天", "netease")
	add("netease-2", "晴天", "netease")
	add("netease-live", "晴天 Live", "netease")
	add("qq-1", "晴天", "qqmusic")
	var got []string
	for _, raw := range rankSearch("晴天", candidates) {
		var id string
		json.Unmarshal(raw, &id)
		got = append(got, id)
	}
	want := []string{"local-1", "netease-1", "qq-1", "local-2", "netease-2", "local-3", "netease-live", "local-other"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestRankedSearchSnapshotPaginationAndIsolation(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "search.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var fetches atomic.Int32
	var changed atomic.Bool
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		size, _ := strconv.Atoi(r.URL.Query().Get("size"))
		if size < 1 {
			size = 50
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		rows := []map[string]any{}
		for i := (page - 1) * size; i < page*size && i < 105; i++ {
			title := "晴天"
			if i == 0 {
				title = "无关结果"
			}
			if changed.Load() {
				title = "已变化"
			}
			rows = append(rows, map[string]any{"guid": fmt.Sprintf("local-%d", i), "title": title, "artist": "歌手", "custom": "preserved"})
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "extra": "keep", "data": map[string]any{"list": rows, "total": 105, "custom": "keep"}})
	})}
	go srv.Serve(listener)
	defer srv.Close()
	g := New(sock)
	defer g.Close()
	settings, _ := preferences.New("")
	cfg := settings.Snapshot()
	cfg.MaxPages = 2
	settings.Update(cfg)
	g.SetPreferences(settings)
	var calls int
	source := &policySource{search: func(q provider.SearchRequest) (provider.SearchPage, error) {
		calls++
		tracks := []provider.Track{
			{ID: "1", Source: "netease", Title: "晴天", Album: "原版", Artists: []string{"歌手"}, Playable: true},
			{ID: "2", Source: "netease", Title: "晴天", Album: "精选", Artists: []string{"歌手"}, Playable: true},
			{ID: "3", Source: "netease", Title: "晴天 Live", Artists: []string{"歌手"}, Playable: true},
		}
		return provider.SearchPage{Tracks: tracks, Total: len(tracks)}, nil
	}}
	user := provider.User{FnOSGUID: "alice", Verified: true}
	request := func(page, size int) ([]map[string]any, int) {
		t.Helper()
		r := httptest.NewRequest("GET", fmt.Sprintf("http://localhost/music/api/v1/search/track?q=晴天&page=%d&size=%d", page, size), nil)
		w := httptest.NewRecorder()
		g.search(w, r, user, source, "account", "晴天")
		var out struct {
			Extra string
			Data  struct {
				List   []map[string]any
				Total  int
				Custom string
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Extra != "keep" || out.Data.Custom != "keep" {
			t.Fatal("official fields lost", w.Body.String())
		}
		return out.Data.List, out.Data.Total
	}
	// A request starting at page two must build the same complete snapshot.
	second, total := request(2, 3)
	if total != 108 || len(second) != 3 || second[0]["title"] != "[网易云] 晴天" {
		t.Fatalf("page2=%v total=%d", second, total)
	}
	changed.Store(true)
	before := fetches.Load()
	first, total := request(1, 6)
	if fetches.Load() != before+1 || calls != 1 {
		t.Fatal("snapshot was rebuilt", fetches.Load(), calls)
	}
	if total != 108 || len(first) != 6 || first[0]["guid"] != "local-1" || first[1]["title"] != "[网易云] 晴天" {
		t.Fatal(first, total)
	}
	if first[0]["custom"] != "preserved" {
		t.Fatal("local fields lost")
	}
	for i := range second {
		if second[i]["guid"] != first[i+3]["guid"] {
			t.Fatal("page size changed order")
		}
	}
	var ids = map[string]bool{}
	for page := 1; page <= 17; page++ {
		rows, _ := request(page, 7)
		for _, row := range rows {
			id := row["guid"].(string)
			if ids[id] {
				t.Fatal("duplicate across pages", id)
			}
			ids[id] = true
		}
	}
	if len(ids) != 108 {
		t.Fatal("missing rows", len(ids))
	}
	r := httptest.NewRequest("GET", "http://localhost/music/api/v1/search/track?q=晴天", nil)
	for _, identity := range []struct{ user, scope string }{{"bob", "account"}, {"alice", "other-account"}} {
		rows, err := g.rankedSearch(context.Background(), r, provider.User{FnOSGUID: identity.user, Verified: true}, source, identity.scope, "晴天")
		if err != nil || !strings.Contains(string(rows[3]), "已变化") {
			t.Fatal("snapshot identity leak", identity, err)
		}
	}
	// Non-pagination filters must never reuse a differently filtered snapshot.
	filters := r.URL.Query()
	filters.Set("artist", "another")
	r.URL.RawQuery = filters.Encode()
	filtered, err := g.rankedSearch(context.Background(), r, user, source, "account", "晴天")
	if err != nil || !strings.Contains(string(filtered[3]), "已变化") {
		t.Fatal("search filters reused a stale snapshot", err)
	}
	cfg.DeepPage = false
	settings.Update(cfg)
	rows, err := g.rankedSearch(context.Background(), r, user, source, "account", "晴天")
	if err != nil || len(rows) != 103 {
		t.Fatal("settings revision/page cap not applied", len(rows), err)
	}
}

func TestRankedSearchFailurePreservesOfficialResponse(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "search.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	const body = `{"code":0,"data":{"list":[{"guid":"local","title":"Local"}],"total":1},"extra":true}`
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	})}
	go srv.Serve(listener)
	defer srv.Close()
	g := New(sock)
	defer g.Close()
	r := httptest.NewRequest("GET", "http://localhost/music/api/v1/search/track?q=x", nil)
	w := httptest.NewRecorder()
	g.search(w, r, provider.User{FnOSGUID: "alice", Verified: true}, stubMusic{fail: true}, "account", "x")
	if w.Code != 200 || w.Body.String() != body {
		t.Fatal("official fallback changed", w.Code, w.Body.String())
	}
	// A failed build must not freeze an empty/local-only snapshot for five minutes.
	w = httptest.NewRecorder()
	g.search(w, r, provider.User{FnOSGUID: "alice", Verified: true}, stubMusic{}, "account", "x")
	if !strings.Contains(w.Body.String(), "NetSong") {
		t.Fatal("search did not recover after provider failure", w.Body.String())
	}
}
