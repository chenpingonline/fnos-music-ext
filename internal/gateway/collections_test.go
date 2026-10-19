package gateway

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

type storedSource struct{ accountAdapter }

func (s storedSource) Play(context.Context, provider.User, provider.PlaybackRequest) (provider.Stream, error) {
	raw := make([]byte, 1644)
	copy(raw, "RIFF")
	binary.LittleEndian.PutUint32(raw[4:], uint32(len(raw)-8))
	copy(raw[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(raw[16:], 16)
	binary.LittleEndian.PutUint16(raw[20:], 1)
	binary.LittleEndian.PutUint16(raw[22:], 1)
	binary.LittleEndian.PutUint32(raw[24:], 8000)
	binary.LittleEndian.PutUint32(raw[28:], 16000)
	binary.LittleEndian.PutUint16(raw[32:], 2)
	binary.LittleEndian.PutUint16(raw[34:], 16)
	copy(raw[36:], "data")
	binary.LittleEndian.PutUint32(raw[40:], 1600)
	b := string(raw)
	return provider.Stream{Body: io.NopCloser(strings.NewReader(b)), ContentLength: int64(len(b)), StatusCode: 200}, nil
}
func runtimeGateway(t *testing.T) (*Gateway, func(string, string, string) *httptest.ResponseRecorder, *[]string, *sync.Mutex) {
	t.Helper()
	var g *Gateway
	writes := []string{}
	mu := &sync.Mutex{}
	sock, closeServer := unixBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/music/api/v1/user/me" {
			if r.Header.Get("Authorization") == "" {
				http.Error(w, "unauthorized", 401)
				return
			}
			writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]any{"guid": r.Header.Get("Authorization")}})
			return
		}
		if r.Method == "POST" {
			if r.Header.Get("Authorization") != "alice" {
				t.Error("wrong write identity")
			}
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			writes = append(writes, r.URL.Path+":"+fmtID(body))
			mu.Unlock()
			writeJSON(w, 200, map[string]any{"code": 0})
			return
		}
		rows := []any{}
		if r.URL.Path == "/music/api/v1/search/track" && g != nil {
			for _, j := range g.media.Snapshot() {
				if j.State == "completed" && (j.Purpose == "bind" || j.Purpose == "save") {
					rows = append(rows, map[string]any{"guid": "local-1", "title": "Song", "artist": "Demo", "audioSpec": map[string]any{"path": j.Path}})
				}
			}
		}
		writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]any{"list": rows, "total": len(rows)}})
	}))
	t.Cleanup(closeServer)
	g = New(sock)
	g.SetOnline(storedSource{accountAdapter{"Song"}}, "alice")
	root := t.TempDir()
	settings, e := preferences.New(filepath.Join(root, "settings.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = g.Configure(root, settings); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(g.Close)
	query := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "alice")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	return g, query, &writes, mu
}
func fmtID(body map[string]any) string { v, _ := body["trackGUID"].(string); return v }
func waitFor(t *testing.T, ready func() bool) {
	t.Helper()
	until := time.Now().Add(7 * time.Second)
	for time.Now().Before(until) {
		if ready() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for background operation")
}
func TestOnlineFavoritesBindingAndRemoval(t *testing.T) {
	g, query, writes, mu := runtimeGateway(t)
	cfg := g.policy()
	cfg.AutoBind = true
	cfg.AutoCover = false
	cfg.DownloadDir = filepath.Join(t.TempDir(), "music")
	if e := g.media.Apply(cfg); e != nil {
		t.Fatal(e)
	}
	guid, _ := fnmusic.EncodeNetEaseID("123")
	body := `{"trackGUID":"` + guid + `"}`
	if w := query("POST", "/music/api/v1/favorite-track/create", body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	waitFor(t, func() bool {
		entry, ok := g.ledger.Get("alice", "legacy", "favorites", "123")
		return ok && entry.Bound == "local-1"
	})
	if w := query("POST", "/music/api/v1/favorite-track/delete", body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, ok := g.ledger.Get("alice", "legacy", "favorites", "123"); ok {
		t.Fatal("removed favorite retained")
	}
	// Users can also unfavorite the official local GUID shown after binding.
	query("POST", "/music/api/v1/favorite-track/create", body)
	waitFor(t, func() bool {
		entry, ok := g.ledger.Get("alice", "legacy", "favorites", "123")
		return ok && entry.Bound == "local-1"
	})
	query("POST", "/music/api/v1/favorite-track/delete", `{"trackGUID":"local-1"}`)
	if _, ok := g.ledger.Get("alice", "legacy", "favorites", "123"); ok {
		t.Fatal("official unfavorite retained virtual alias")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*writes) != 4 || (*writes)[0] != "/music/api/v1/favorite-track/create:local-1" || (*writes)[1] != "/music/api/v1/favorite-track/delete:local-1" {
		t.Fatal(*writes)
	}
}
func TestOnlineFavoriteReadOnlyAndTaskOwnership(t *testing.T) {
	g, query, _, _ := runtimeGateway(t)
	guid, _ := fnmusic.EncodeNetEaseID("123")
	body := `{"trackGUID":"` + guid + `"}`
	query("POST", "/music/api/v1/favorite-track/create", body)
	w := query("GET", "/music/api/v1/favorite-track/list?page=1&size=50", "")
	if !strings.Contains(w.Body.String(), guid) {
		t.Fatal("online favorite missing", w.Body.String())
	}
	virtual, _ := fnmusic.EncodePlaylistID("legacy:public", "1")
	w = query("POST", "/music/api/v1/playlist/add-track", `{"guid":"`+virtual+`","trackGUIDs":["official-local"]}`)
	if w.Code != 409 {
		t.Fatal("public playlist write accepted", w.Code)
	}
	w = query("POST", "/music/api/v1/download/track/transcode/prepare", body)
	var result struct {
		Data struct {
			ID string `json:"downloadId"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.Data.ID) != 64 {
		t.Fatal("download task missing", w.Body.String())
	}
	waitFor(t, func() bool { j, _ := g.media.Lookup(result.Data.ID, "alice", "legacy"); return j.State == "completed" })
	w = query("GET", "/music/api/v1/download/track/transcode/file?downloadId="+result.Data.ID, "")
	if w.Code != 200 || !strings.HasPrefix(w.Body.String(), "RIFF") {
		t.Fatal("download file failed", w.Code)
	}
	r := httptest.NewRequest("GET", "http://localhost/music/api/v1/download/track/transcode/file?downloadId="+result.Data.ID, nil)
	r.Header.Set("Authorization", "bob")
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-user file exposed", w.Code)
	}
	r = httptest.NewRequest("GET", "http://localhost/music/api/v1/download/track/transcode/file?downloadId="+result.Data.ID, nil)
	r.Header.Set("Authorization", "alice")
	r.Header.Set("Range", "bytes=0-2")
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "RIF" {
		t.Fatal("range failed", w.Code, w.Body.String())
	}
	r = httptest.NewRequest("POST", "http://localhost/music/api/v1/favorite-track/delete", strings.NewReader(body))
	r.Header.Set("Authorization", "alice")
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-site mutation allowed")
	}
}
