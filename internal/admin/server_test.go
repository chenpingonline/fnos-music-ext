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
