package admin

import (
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/accounts"
	"net/http"
	"strings"
)

func handleGroups(w http.ResponseWriter, r *http.Request, c Config) bool {
	const prefix = "/api/accounts/groups/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	if r.Method != "PUT" {
		http.Error(w, "method not allowed", 405)
		return true
	}
	if !managementRequest(r) {
		rejectManagement(w)
		return true
	}
	if c.Accounts == nil {
		http.Error(w, "account manager unavailable", 503)
		return true
	}
	var body struct {
		AccountIDs []string `json:"account_ids"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return true
	}
	if err := c.Accounts.SetGroup(strings.TrimPrefix(r.URL.Path, prefix), body.AccountIDs); err != nil {
		if errors.Is(err, accounts.ErrConflict) {
			respond(w, 409, map[string]string{"error": "附加账号已分配给其他用户，请先解除原分配。"})
		} else {
			fail(w, err)
		}
		return true
	}
	respond(w, 200, map[string]bool{"ok": true})
	return true
}
