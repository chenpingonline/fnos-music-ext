package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenpingonline/fnos-music-ext/internal/accounts"
)

func TestLoginAdminChallengeNeverLeaksAndOwnership(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			fmt.Fprint(w, `{"data":{"unikey":"test-key"}}`)
		case "/api/v1/auth/login/check":
			if r.URL.Query().Get("unikey") != "test-key" {
				t.Error("wrong challenge key")
			}
			fmt.Fprint(w, `{"data":{"code":803}}`)
		case "/api/v1/auth/status":
			fmt.Fprint(w, `{"data":{"logged_in":true}}`)
		case "/api/v1/user/playlists":
			fmt.Fprint(w, `{"ok":true,"logged_in":true,"account_uid":8,"data":[{"playlist_id":11,"name":"Owned","user_id":8},{"playlist_id":12,"name":"Saved","user_id":7}]}`)
		case "/api/v1/user/playlists/11/tracks":
			fmt.Fprint(w, `{"ok":true,"data":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	m, e := accounts.New(filepath.Join(t.TempDir(), "private", "accounts.json"), nil)
	if e != nil {
		t.Fatal(e)
	}
	if e := m.Add(accounts.Account{ID: "a", Name: "Account", BridgeURL: server.URL}); e != nil {
		t.Fatal(e)
	}
	h := New(Config{Accounts: m})
	request := func(method, path string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/app/fnmusic-go/api/accounts/a"+path, strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-FnMusic-Admin", "1")
		if auth {
			r.Header.Set("X-Trim-Isadmin", "true")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	if x := request("POST", "/auth/qr", false); x.Code != 403 {
		t.Fatal("bypassed admin", x.Code)
	}
	if x := request("POST", "/auth/check", true); x.Code != 410 {
		t.Fatal("unexpected no-session status", x.Code)
	}
	qr := request("POST", "/auth/qr", true)
	if qr.Code != 200 || strings.Contains(qr.Body.String(), `"key"`) {
		t.Fatal("challenge leaked", qr.Code, qr.Body.String())
	}
	if x := request("POST", "/auth/check", true); x.Code != 200 {
		t.Fatal(x.Code, x.Body.String())
	}
	if x := request("POST", "/auth/check", true); x.Code != 410 {
		t.Fatal("stale challenge reused", x.Code)
	}
	lists := request("GET", "/playlists", true)
	if lists.Code != 200 {
		t.Fatal(lists.Code, lists.Body.String())
	}
	var decoded []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(lists.Body.Bytes(), &decoded) != nil || len(decoded) != 1 || decoded[0].ID != "11" {
		t.Fatal(lists.Body.String())
	}
	if x := request("GET", "/playlists/12/tracks", true); x.Code != 404 {
		t.Fatal("other person's playlist accessible", x.Code)
	}
	if x := request("GET", "/playlists/11/tracks", true); x.Code != 200 {
		t.Fatal(x.Code, x.Body.String())
	}
}
