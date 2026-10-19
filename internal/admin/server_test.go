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
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("embedded asset %s: status %d", entry.Name(), w.Code)
		}
	}
}
