package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenpingonline/fnos-music-ext/internal/accounts"
)

func TestAccountAdminAPI(t *testing.T) {
	m, e := accounts.New(filepath.Join(t.TempDir(), "private", "accounts.json"), nil)
	if e != nil {
		t.Fatal(e)
	}
	h := New(Config{Accounts: m})
	call := func(method, path, body string, admin bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/app/fnmusic-go/api"+path, strings.NewReader(body))
		if admin {
			r.Header.Set("X-Trim-Isadmin", "true")
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-FnMusic-Admin", "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/accounts", `{"id":"a","name":"Private","bridge_url":"http://127.0.0.1:8770"}`, false); w.Code != 403 {
		t.Fatal("no auth", w.Code)
	}
	if w := call("POST", "/accounts", `{"id":"a","name":"Private","bridge_url":"http://127.0.0.1:8770"}`, true); w.Code != 201 {
		t.Fatal("add", w.Code, w.Body.String())
	}
	if w := call("PUT", "/accounts/shared", `{"account_id":"a"}`, true); w.Code != 200 {
		t.Fatal("select shared", w.Code, w.Body.String())
	}
	if w := call("PUT", "/accounts/mode", `{"isolation":false}`, true); w.Code != 200 {
		t.Fatal("disable isolation", w.Code)
	}
	var snapshot accounts.Config
	w := call("GET", "/accounts", "", true)
	if e := json.Unmarshal(w.Body.Bytes(), &snapshot); e != nil || snapshot.Isolation || snapshot.SharedAccount != "a" {
		t.Fatal("mode state", w.Body.String(), e)
	}
	if w := call("PUT", "/accounts/bindings/alice", `{"account_id":"a"}`, true); w.Code != 200 {
		t.Fatal("binding", w.Code)
	}
	if w := call("DELETE", "/accounts/a", `{}`, true); w.Code != 200 {
		t.Fatal("delete", w.Code)
	}
	if m.Snapshot().SharedAccount != "" || len(m.Snapshot().Bindings) != 0 {
		t.Fatal("orphaned state")
	}
	// Mutation requires custom header to prevent cross-origin form submissions.
	r := httptest.NewRequest("PUT", "/app/fnmusic-go/api/accounts/mode", strings.NewReader(`{"isolation":false}`))
	r.Header.Set("X-Trim-Isadmin", "true")
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("missing anti-CSRF header", w.Code)
	}
	r.Header.Set("X-FnMusic-Admin", "1")
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin accepted", w.Code)
	}
	_ = http.MethodGet
}
