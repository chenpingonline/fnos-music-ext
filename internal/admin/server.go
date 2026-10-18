package admin

import (
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/accounts"
	"github.com/chenpingonline/fnos-music-ext/internal/takeover"
)

//go:embed assets/*
var embedded embed.FS

type Config struct {
	Socket   takeover.Config
	Prefix   string
	Accounts *accounts.Manager
}

// Protect mutations against cross-site form submissions. X-Trim-Isadmin is
// meaningful ONLY behind a trusted fnOS gateway with SO_PEERCRED listener;
// client-supplied headers MUST NOT be accepted on a public TCP socket.
func managementRequest(r *http.Request) bool {
	if r.Header.Get("X-FnMusic-Admin") != "1" || !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		// Exact origin comparison, no permissive substring matching.
		expected := "https://" + r.Host
		if origin != expected && origin != "http://"+r.Host {
			return false
		}
	}
	return true
}
func decode(r *http.Request, v any) error {
	if r.ContentLength > 16384 {
		return errors.New("request too large")
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 16385))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}
func respond(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func fail(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, accounts.ErrNotFound) {
		status = 404
	}
	if errors.Is(err, accounts.ErrConflict) {
		status = 409
	}
	respond(w, status, map[string]any{"error": err.Error()})
}
func New(c Config) http.Handler {
	prefix := c.Prefix
	if prefix == "" {
		prefix = "/app/fnmusic-go"
	}
	files, _ := fs.Sub(embedded, "assets")
	public := http.FileServer(http.FS(files))
	internal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Header.Get("X-Trim-Isadmin") != "true" {
				http.Error(w, "administrator login required", http.StatusForbidden)
				return
			}
			switch {
			case r.URL.Path == "/api/status" && r.Method == http.MethodGet:
				enabled, mode, selected, amount := false, "isolated", "", 0
				if c.Accounts != nil {
					snap := c.Accounts.Snapshot()
					enabled = len(snap.Accounts) > 0
					mode = "shared"
					if snap.Isolation {
						mode = "isolated"
					}
					selected = snap.SharedAccount
					amount = len(snap.Accounts)
				}
				respond(w, 200, map[string]any{"version": "0.3.0-preview", "phase": "account-selection-preview", "source_enabled": enabled, "netease_mode": mode, "shared_account": selected, "accounts_count": amount, "socket": takeover.Inspect(c.Socket), "checked_at": time.Now().UTC().Format(time.RFC3339)})
			case r.URL.Path == "/api/accounts" && r.Method == http.MethodGet:
				if c.Accounts == nil {
					respond(w, 200, accounts.Config{Isolation: true})
					return
				}
				respond(w, 200, c.Accounts.Snapshot())
			case r.Method == http.MethodPost && r.URL.Path == "/api/accounts":
				if !managementRequest(r) {
					http.Error(w, "JSON management request required", 403)
					return
				}
				if c.Accounts == nil {
					http.Error(w, "account manager unavailable", 503)
					return
				}
				var a accounts.Account
				if err := decode(r, &a); err != nil {
					fail(w, err)
					return
				}
				if err := c.Accounts.Add(a); err != nil {
					fail(w, err)
					return
				}
				respond(w, 201, map[string]any{"ok": true})
			case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/accounts/"):
				if !managementRequest(r) {
					http.Error(w, "JSON management request required", 403)
					return
				}
				if c.Accounts == nil {
					http.Error(w, "account manager unavailable", 503)
					return
				}
				id := strings.TrimPrefix(r.URL.Path, "/api/accounts/")
				if err := c.Accounts.Remove(id); err != nil {
					fail(w, err)
					return
				}
				respond(w, 200, map[string]any{"ok": true})
			case r.Method == http.MethodPut && r.URL.Path == "/api/accounts/mode":
				if !managementRequest(r) {
					http.Error(w, "JSON management request required", 403)
					return
				}
				if c.Accounts == nil {
					http.Error(w, "account manager unavailable", 503)
					return
				}
				var req struct {
					Isolation bool `json:"isolation"`
				}
				if err := decode(r, &req); err != nil {
					fail(w, err)
					return
				}
				if err := c.Accounts.SetIsolation(req.Isolation); err != nil {
					fail(w, err)
					return
				}
				respond(w, 200, map[string]any{"ok": true})
			case r.Method == http.MethodPut && r.URL.Path == "/api/accounts/shared":
				if !managementRequest(r) {
					http.Error(w, "JSON management request required", 403)
					return
				}
				if c.Accounts == nil {
					http.Error(w, "account manager unavailable", 503)
					return
				}
				var req struct {
					AccountID string `json:"account_id"`
				}
				if err := decode(r, &req); err != nil {
					fail(w, err)
					return
				}
				if err := c.Accounts.SetShared(req.AccountID); err != nil {
					fail(w, err)
					return
				}
				respond(w, 200, map[string]any{"ok": true})
			case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/accounts/bindings/"):
				if !managementRequest(r) {
					http.Error(w, "JSON management request required", 403)
					return
				}
				if c.Accounts == nil {
					http.Error(w, "account manager unavailable", 503)
					return
				}
				userGUID := strings.TrimPrefix(r.URL.Path, "/api/accounts/bindings/")
				var req struct {
					AccountID string `json:"account_id"`
				}
				if err := decode(r, &req); err != nil {
					fail(w, err)
					return
				}
				if err := c.Accounts.Bind(userGUID, req.AccountID); err != nil {
					fail(w, err)
					return
				}
				respond(w, 200, map[string]any{"ok": true})
			default:
				http.NotFound(w, r)
			}
			return
		}
		if r.URL.Path == "/" {
			html, err := fs.ReadFile(files, "index.html")
			if err != nil {
				http.Error(w, "dashboard unavailable", 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(html)
			return
		}
		public.ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
			rr := r.Clone(r.Context())
			u := *r.URL
			u.Path = strings.TrimPrefix(r.URL.Path, prefix)
			if u.Path == "" {
				u.Path = "/"
			}
			rr.URL = &u
			internal.ServeHTTP(w, rr)
			return
		}
		http.NotFound(w, r)
	})
}
