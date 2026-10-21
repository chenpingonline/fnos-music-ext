package admin

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminGuard(t *testing.T) {
	h := New(Config{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/app/fnmusic-go/api/status", nil))
	if w.Code != 403 {
		t.Fatalf("expected 403 got %d", w.Code)
	}
	req := httptest.NewRequest("GET", "/app/fnmusic-go/api/status", nil)
	req.Header.Set("X-Trim-Isadmin", "true")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"source_enabled":false`) {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/app/fnmusic-go/", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "fnMusic Go") {
		t.Fatalf("web: %d", w.Code)
	}
}

func TestReadinessRequiresPublishedProxy(t *testing.T) {
	h := New(Config{})
	req := httptest.NewRequest("GET", "/app/fnmusic-go/api/ready", nil)
	req.Header.Set("X-Trim-Isadmin", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 503 {
		t.Fatalf("unpublished proxy reported ready: %d", w.Code)
	}
}

func TestEmbeddedProductionAssets(t *testing.T) {
	entries, err := embedded.ReadDir("assets/assets")
	if err != nil || len(entries) == 0 {
		t.Fatalf("production assets missing: %v", err)
	}
	h := New(Config{})
	for _, entry := range entries {
		req := httptest.NewRequest("GET", "/app/fnmusic-go/assets/"+entry.Name(), nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Fatalf("asset %s is not cacheable: %s", entry.Name(), w.Header().Get("Cache-Control"))
		}
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("embedded asset %s: status %d", entry.Name(), w.Code)
		}
	}
}

func TestAdminEntryAndAPIStayUncached(t *testing.T) {
	h := New(Config{})
	for _, path := range []string{"/", "/index.html", "/api/status"} {
		req := httptest.NewRequest("GET", "/app/fnmusic-go"+path, nil)
		req.Header.Set("X-Trim-Isadmin", "true")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if got := w.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s cache policy = %q", path, got)
		}
	}
}

func TestMissingAssetIsNotCached(t *testing.T) {
	w := httptest.NewRecorder()
	New(Config{}).ServeHTTP(w, httptest.NewRequest("GET", "/app/fnmusic-go/assets/missing.png", nil))
	if w.Code != 404 || strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("missing asset: status %d cache %q", w.Code, w.Header().Get("Cache-Control"))
	}
}
