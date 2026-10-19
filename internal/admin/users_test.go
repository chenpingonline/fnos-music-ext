package admin

import (
	"encoding/json"
	"github.com/chenpingonline/fnos-music-ext/internal/accounts"
	"github.com/chenpingonline/fnos-music-ext/internal/userdir"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserPickerDirectoryAndValidatedAssignments(t *testing.T) {
	m, err := accounts.New(filepath.Join(t.TempDir(), "private", "accounts.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Add(accounts.Account{ID: "a", Name: "A", BridgeURL: "native://netease/a"}); err != nil {
		t.Fatal(err)
	}
	h := New(Config{Accounts: m, UserDirectory: func() ([]userdir.User, error) {
		return []userdir.User{{Username: "alice", UID: "1000"}, {Username: "bob", UID: "1001"}}, nil
	}})
	query := func(method, path, body string, admin bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/app/fnmusic-go/api"+path, strings.NewReader(body))
		r.Header.Set("X-Trim-Username", "alice")
		r.Header.Set("X-Trim-Userid", "1000")
		if admin {
			r.Header.Set("X-Trim-Isadmin", "true")
		}
		r.Header.Set("X-FnMusic-Admin", "1")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	list := query("GET", "/users", "", true)
	var out struct{ List []selectableUser }
	if err := json.Unmarshal(list.Body.Bytes(), &out); err != nil || len(out.List) != 2 || !out.List[0].Current || out.List[0].Verified {
		t.Fatal(list.Code, list.Body.String(), err)
	}
	if w := query("PUT", "/accounts/users/alice", `{"account_id":"a"}`, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if m.Snapshot().UserBindingUIDs["alice"] != "1000" || len(m.Snapshot().Bindings) != 0 {
		t.Fatal("system UID was used as music GUID")
	}
	if w := query("PUT", "/accounts/users/1000", `{"account_id":"a"}`, true); w.Code != 400 {
		t.Fatal("unknown username accepted", w.Code)
	}
	if w := query("PUT", "/accounts/users/bob", `{"account_id":"a"}`, true); w.Code != 409 {
		t.Fatal("private account shared implicitly", w.Code)
	}
	if w := query("GET", "/users", "", false); w.Code != 403 {
		t.Fatal("user directory exposed", w.Code)
	}
	if w := query("PUT", "/accounts/users/alice", `{"account_id":""}`, true); w.Code != 200 || len(m.Snapshot().UserBindings) != 0 {
		t.Fatal("assignment removal failed", w.Code)
	}
}
