package admin

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenpingonline/fnos-music-ext/internal/library"
	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

func TestMusicSettingsAuthorizationValidationAndPersistence(t *testing.T) {
	root := t.TempDir()
	settings, e := preferences.New(filepath.Join(root, "settings.json"))
	if e != nil {
		t.Fatal(e)
	}
	media, e := library.New(root, settings, func(string, string) (provider.Adapter, bool) { return nil, false })
	if e != nil {
		t.Fatal(e)
	}
	defer media.Close()
	h := New(Config{Settings: settings, Media: media})
	cfg := settings.Snapshot()
	cfg.Quality = "smooth"
	cfg.CoverCacheDir = filepath.Join(root, "covers-custom")
	body, _ := json.Marshal(cfg)
	call := func(method, path, body string, admin, management bool, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost/app/fnmusic-go/api"+path, strings.NewReader(body))
		if admin {
			r.Header.Set("X-Trim-Isadmin", "true")
		}
		if management {
			r.Header.Set("X-FnMusic-Admin", "1")
			r.Header.Set("Content-Type", "application/json")
		}
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, test := range []struct {
		admin, management bool
		origin            string
	}{{false, true, ""}, {true, false, ""}, {true, true, "https://evil.example"}} {
		if w := call("PUT", "/settings", string(body), test.admin, test.management, test.origin); w.Code != 403 {
			t.Fatal("settings guard failed", w.Code)
		}
	}
	w := call("PUT", "/settings", string(body), true, true, "")
	if w.Code != 200 || settings.Snapshot().Quality != "smooth" {
		t.Fatal(w.Code, w.Body.String())
	}
	again, e := preferences.New(filepath.Join(root, "settings.json"))
	if e != nil || again.Snapshot().Quality != "smooth" || again.Snapshot().CoverCacheDir != cfg.CoverCacheDir {
		t.Fatal("not persisted", e)
	}
	cfg.SearchTimeout = 0
	body, _ = json.Marshal(cfg)
	if w = call("PUT", "/settings", string(body), true, true, ""); w.Code != 400 {
		t.Fatal("invalid timeout accepted")
	}
	if settings.Snapshot().SearchTimeout != 15 {
		t.Fatal("failed update changed policy")
	}
	if w = call("POST", "/directories/check", `{"path":"/etc/music-test"}`, true, true, ""); w.Code != 400 {
		t.Fatal("unsafe directory accepted")
	}
	if w = call("GET", "/tasks", "", false, false, ""); w.Code != 403 {
		t.Fatal("private tasks exposed")
	}
}

func TestCoverCacheAdminAndWriteGuards(t *testing.T) {
	settings, _ := preferences.New("")
	media, e := library.New(t.TempDir(), settings, func(string, string) (provider.Adapter, bool) { return nil, false })
	if e != nil {
		t.Fatal(e)
	}
	defer media.Close()
	h := New(Config{Settings: settings, Media: media})
	for _, tc := range []struct {
		method, path, origin string
		admin, management    bool
		status               int
	}{
		{"GET", "/diagnostics", "", false, false, 403},
		{"GET", "/diagnostics", "", true, false, 200},
		{"GET", "/cover-cache", "", false, false, 403},
		{"GET", "/cover-cache", "", true, false, 200},
		{"POST", "/cover-cache/clear", "", true, false, 403},
		{"POST", "/cover-cache/clear", "https://evil.example", true, true, 403},
		{"POST", "/cover-cache/clear", "", true, true, 200},
	} {
		r := httptest.NewRequest(tc.method, "http://localhost/app/fnmusic-go/api"+tc.path, strings.NewReader(`{}`))
		if tc.admin {
			r.Header.Set("X-Trim-Isadmin", "true")
		}
		if tc.management {
			r.Header.Set("X-FnMusic-Admin", "1")
			r.Header.Set("Content-Type", "application/json")
		}
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
