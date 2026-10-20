package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/accounts"
)

func TestEnrollmentOnlyPersistsAfterVerifiedLogin(t *testing.T) {
	var code atomic.Int64
	code.Store(801)
	var logged atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			fmt.Fprint(w, `{"data":{"unikey":"private-key"}}`)
		case "/api/v1/auth/login/check":
			fmt.Fprintf(w, `{"data":{"code":%d}}`, code.Load())
		case "/api/v1/auth/status":
			fmt.Fprintf(w, `{"data":{"logged_in":%t}}`, logged.Load())
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	path := filepath.Join(t.TempDir(), "private", "accounts.json")
	manager, err := accounts.New(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	enroll := newEnrollmentManager(manager)
	call := func(method, path, body, user string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/account-enrollments"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-FnMusic-Admin", "1")
		req.Header.Set("X-Trim-Userid", user)
		req = req.WithContext(context.WithValue(req.Context(), csrfKey{}, true))
		out := httptest.NewRecorder()
		if !enroll.handle(out, req) {
			t.Fatal("not handled")
		}
		return out
	}
	begin := func() string {
		out := call("POST", "", fmt.Sprintf(`{"name":"My music","provider":"musicbox","bridge_url":%q}`, upstream.URL), "alice")
		if out.Code != 201 {
			t.Fatal(out.Code, out.Body.String())
		}
		if strings.Contains(out.Body.String(), `"key"`) {
			t.Fatal("challenge leaked")
		}
		var result struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.ID
	}
	assertEmpty := func() {
		t.Helper()
		if len(manager.Snapshot().Accounts) != 0 {
			t.Fatal("unfinished account visible")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("unfinished account written", err)
		}
	}
	id := begin()
	assertEmpty()
	if out := call("POST", "/"+id+"/check", "{}", "bob"); out.Code != 410 {
		t.Fatal("cross-user draft accessible", out.Code)
	}
	if out := call("DELETE", "/"+id, "{}", "alice"); out.Code != 200 {
		t.Fatal(out.Code)
	}
	if out := call("POST", "/"+id+"/check", "{}", "alice"); out.Code != 410 {
		t.Fatal("cancelled draft active")
	}
	assertEmpty()
	id = begin()
	enroll.entries[id].expires = time.Now().Add(-time.Second)
	if out := call("POST", "/"+id+"/check", "{}", "alice"); out.Code != 410 {
		t.Fatal("expired draft accepted", out.Code)
	}
	assertEmpty()
	call("DELETE", "/"+id, "{}", "alice")
	id = begin()
	if out := call("POST", "/"+id+"/check", "{}", "alice"); out.Code != 200 {
		t.Fatal(out.Code)
	}
	assertEmpty()
	code.Store(803)
	enroll.entries[id].checked = time.Time{}
	if out := call("POST", "/"+id+"/check", "{}", "alice"); out.Code != 502 {
		t.Fatal("unverified login persisted", out.Code)
	}
	assertEmpty()
	logged.Store(true)
	enroll.entries[id].checked = time.Time{}
	// Persistence failure must leave the account absent and allow retry.
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if out := call("POST", "/"+id+"/check", "{}", "alice"); out.Code != 500 {
		t.Fatal("save failure ignored", out.Code, out.Body.String())
	}
	assertEmpty()
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	enroll.entries[id].checked = time.Time{}
	out := call("POST", "/"+id+"/check", "{}", "alice")
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"saved":true`) {
		t.Fatal(out.Code, out.Body.String())
	}
	if len(manager.Snapshot().Accounts) != 1 {
		t.Fatal("successful account not saved")
	}
	if out := call("POST", "/"+id+"/check", "{}", "alice"); out.Code != 200 {
		t.Fatal("completed poll not idempotent")
	}
	if len(manager.Snapshot().Accounts) != 1 {
		t.Fatal("duplicate account saved")
	}
	reloaded, err := accounts.New(path, nil)
	if err != nil || len(reloaded.Snapshot().Accounts) != 1 {
		t.Fatal("saved account not durable", err)
	}
}
