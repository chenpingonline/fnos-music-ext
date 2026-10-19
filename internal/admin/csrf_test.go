package admin

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenpingonline/fnos-music-ext/internal/accounts"
)

func TestGatewayHostRewriteTokenAndUserIsolation(t *testing.T) {
	manager, e := accounts.New(filepath.Join(t.TempDir(), "private", "accounts.json"), nil)
	if e != nil {
		t.Fatal(e)
	}
	handler := New(Config{Accounts: manager})
	call := func(method, path, body, user, token, site string, admin bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://localhost/app/fnmusic-go/api"+path, strings.NewReader(body))
		req.Header.Set("Origin", "https://nas.example:5667")
		req.Header.Set("X-Trim-Userid", user)
		req.Header.Set("X-FnMusic-Admin", "1")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-FnMusic-CSRF", token)
		req.Header.Set("Sec-Fetch-Site", site)
		if admin {
			req.Header.Set("X-Trim-Isadmin", "true")
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	fetch := call("GET", "/csrf", "", "alice", "", "same-origin", true)
	var challenge struct {
		Token string `json:"token"`
	}
	if fetch.Code != 200 || json.Unmarshal(fetch.Body.Bytes(), &challenge) != nil || len(challenge.Token) < 32 {
		t.Fatal("token unavailable", fetch.Code)
	}
	body := `{"id":"test","name":"Test","bridge_url":"native://netease/test"}`
	if w := call("POST", "/accounts", body, "alice", "", "same-origin", true); w.Code != 403 || !strings.Contains(w.Body.String(), "management_request_rejected") {
		t.Fatal("missing token accepted", w.Code, w.Body.String())
	}
	for _, user := range []string{"bob", ""} {
		if w := call("POST", "/accounts", body, user, challenge.Token, "same-origin", true); w.Code != 403 {
			t.Fatal("cross-user token accepted", user)
		}
	}
	if w := call("POST", "/accounts", body, "alice", challenge.Token, "cross-site", true); w.Code != 403 {
		t.Fatal("cross-site accepted")
	}
	if w := call("POST", "/accounts", body, "alice", challenge.Token, "same-origin", false); w.Code != 403 || !strings.Contains(w.Body.String(), "admin_required") {
		t.Fatal("administrator validation bypassed")
	}
	// Host is rewritten by the Unix-socket gateway. Neither Fetch Metadata nor
	// forwarded-host headers are required when the authenticated token is valid.
	if w := call("POST", "/accounts", body, "alice", challenge.Token, "", true); w.Code != 201 {
		t.Fatal("legitimate proxy write rejected", w.Code, w.Body.String())
	}
	if len(manager.Snapshot().Accounts) != 1 {
		t.Fatal("account not saved")
	}
	if w := call("GET", "/csrf", "", "alice", "", "", false); w.Code != 403 {
		t.Fatal("non-admin received token")
	}
	handler = New(Config{Accounts: manager})
	if w := call("PUT", "/accounts/mode", `{"isolation":false}`, "alice", challenge.Token, "", true); w.Code != 403 {
		t.Fatal("pre-restart token accepted")
	}
}
