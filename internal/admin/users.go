package admin

import (
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/accounts"
	"github.com/chenpingonline/fnos-music-ext/internal/userdir"
	"net/http"
	"strconv"
	"strings"
)

type selectableUser struct {
	Username  string `json:"username"`
	UID       string `json:"uid"`
	Current   bool   `json:"current"`
	Verified  bool   `json:"verified"`
	AccountID string `json:"account_id"`
}

func selectableUsers(c Config, r *http.Request) ([]selectableUser, error) {
	list := c.UserDirectory
	if list == nil {
		list = userdir.List
	}
	rows, err := list()
	if err != nil {
		return nil, err
	}
	currentName, currentUID := r.Header.Get("X-Trim-Username"), r.Header.Get("X-Trim-Userid")
	if _, err := strconv.ParseUint(currentUID, 10, 32); err == nil && userdir.ValidName(currentName) {
		found := false
		for _, row := range rows {
			if row.Username == currentName {
				found = true
			}
		}
		if !found {
			rows = append(rows, userdir.User{Username: currentName, UID: currentUID})
		}
	}
	out := make([]selectableUser, 0, len(rows))
	snap := c.Accounts.Snapshot()
	for _, row := range rows {
		u := selectableUser{Username: row.Username, UID: row.UID, Current: row.Username == currentName && row.UID == currentUID, AccountID: snap.UserBindings[row.Username]}
		for guid, identity := range snap.Identities {
			if identity.Username == row.Username && identity.UID == row.UID {
				u.Verified = true
				if u.AccountID == "" {
					u.AccountID = snap.Bindings[guid]
				}
			}
		}
		out = append(out, u)
	}
	return out, nil
}
func handleUsers(w http.ResponseWriter, r *http.Request, c Config) bool {
	if r.URL.Path != "/api/users" && !strings.HasPrefix(r.URL.Path, "/api/accounts/users/") {
		return false
	}
	if c.Accounts == nil {
		http.Error(w, "account manager unavailable", 503)
		return true
	}
	if r.URL.Path == "/api/users" && r.Method == "GET" {
		rows, err := selectableUsers(c, r)
		if err != nil {
			respond(w, 503, map[string]string{"error": "无法读取本机用户列表，请稍后刷新。"})
			return true
		}
		respond(w, 200, map[string]any{"list": rows})
		return true
	}
	if r.Method != "PUT" {
		http.Error(w, "method not allowed", 405)
		return true
	}
	if !managementRequest(r) {
		rejectManagement(w)
		return true
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/accounts/users/")
	var body struct {
		AccountID string `json:"account_id"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return true
	}
	rows, err := selectableUsers(c, r)
	if err != nil {
		respond(w, 503, map[string]string{"error": "用户列表不可用，未保存分配。"})
		return true
	}
	exists := false
	selected := userdir.User{Username: name}
	for _, u := range rows {
		if u.Username == name {
			exists = true
			selected.UID = u.UID
		}
	}
	if !exists && body.AccountID != "" {
		respond(w, 400, map[string]string{"error": "用户已不存在，请刷新后重新选择。"})
		return true
	}
	if err := c.Accounts.BindDirectoryUser(selected, body.AccountID); err != nil {
		if errors.Is(err, accounts.ErrConflict) {
			respond(w, 409, map[string]string{"code": "account_already_assigned"})
			return true
		}
		fail(w, err)
		return true
	}
	respond(w, 200, map[string]bool{"ok": true})
	return true
}
