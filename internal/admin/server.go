package admin

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/accounts"
	"github.com/chenpingonline/fnos-music-ext/internal/buildinfo"
	"github.com/chenpingonline/fnos-music-ext/internal/library"
	"github.com/chenpingonline/fnos-music-ext/internal/netease"
	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/takeover"
	"github.com/chenpingonline/fnos-music-ext/internal/userdir"
)

//go:embed assets/*
var embedded embed.FS

type Config struct {
	UserDirectory func() ([]userdir.User, error)
	Socket        takeover.Config
	Prefix        string
	Accounts      *accounts.Manager
	Settings      *preferences.Store
	Media         *library.Service
	RetryTask     func(string) error
}

// Protect mutations against cross-site form submissions. X-Trim-Isadmin is
// meaningful ONLY behind a trusted fnOS gateway with SO_PEERCRED listener;
// client-supplied headers MUST NOT be accepted on a public TCP socket.
func managementRequest(r *http.Request) bool {
	if r.Header.Get("X-FnMusic-Admin") != "1" || !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" || site == "same-site" {
		return false
	}
	// The fnOS gateway may rewrite Host. A token fetched through its authenticated
	// same-origin API proves the browser can read this administrator's responses.
	if valid, _ := r.Context().Value(csrfKey{}).(bool); valid {
		return true
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
	limit := int64(16384)
	if r.URL.Path == "/api/settings" {
		limit = 256 << 10
	}
	if r.ContentLength > limit {
		return errors.New("request too large")
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, limit+1))
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
	login := newLoginManager(c.Accounts)
	enrollments := newEnrollmentManager(c.Accounts)
	csrf := newCSRFGuard()
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
				respond(w, 403, map[string]string{"code": "admin_required", "error": "当前操作需要飞牛管理员权限，请确认登录身份。"})
				return
			}
			if csrf.err != nil {
				respond(w, 503, map[string]string{"error": "写入校验服务初始化失败，请重启应用。"})
				return
			}
			if r.URL.Path == "/api/csrf" {
				if r.Method != "GET" {
					http.Error(w, "method not allowed", 405)
					return
				}
				respond(w, 200, map[string]string{"token": csrf.token(r.Header.Get("X-Trim-Userid"))})
				return
			}
			r = csrf.authorize(r)
			if enrollments.handle(w, r) {
				return
			}
			if handleUsers(w, r, c) {
				return
			}
			if handleMusic(w, r, c) {
				return
			}
			// Route account subresources before the generic DELETE account handler.
			if strings.HasPrefix(r.URL.Path, "/api/accounts/") && handleAccountSubresource(w, r, login) {
				return
			}
			switch {
			case r.URL.Path == "/api/ready" && r.Method == http.MethodGet:
				snapshot := takeover.Inspect(c.Socket)
				target, _ := snapshot["target_state"].(map[string]any)
				upstream, _ := snapshot["upstream_state"].(map[string]any)
				if snapshot["journal"] != true || target["pid"] != os.Getpid() || upstream["connected"] != true {
					http.Error(w, "socket proxy is not ready", http.StatusServiceUnavailable)
					return
				}
				respond(w, 200, map[string]any{"ok": true})
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
				respond(w, 200, map[string]any{"version": buildinfo.Version, "phase": "go-native-eapi-preview", "source_enabled": enabled, "netease_mode": mode, "shared_account": selected, "accounts_count": amount, "socket": takeover.Inspect(c.Socket), "checked_at": time.Now().UTC().Format(time.RFC3339)})
			case r.URL.Path == "/api/accounts" && r.Method == http.MethodGet:
				if c.Accounts == nil {
					respond(w, 200, accounts.Config{Isolation: true})
					return
				}
				respond(w, 200, c.Accounts.Snapshot())
			case r.Method == http.MethodPost && r.URL.Path == "/api/accounts":
				if !managementRequest(r) {
					rejectManagement(w)
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
					rejectManagement(w)
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
					rejectManagement(w)
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
					rejectManagement(w)
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
					rejectManagement(w)
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
		// Vite names bundled resources by content hash, so upgrades use new URLs.
		// Keep the HTML entry point and authenticated API responses uncached.
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			if info, err := fs.Stat(files, strings.TrimPrefix(r.URL.Path, "/")); err == nil && !info.IsDir() {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
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

// handleAccountSubresource exposes account-specific QR and read-only playlists
// only through the existing authenticated, administrator-only gateway.
func handleAccountSubresource(w http.ResponseWriter, r *http.Request, l *loginManager) bool {
	relative := strings.TrimPrefix(r.URL.Path, "/api/accounts/")
	bits := strings.Split(relative, "/")
	if len(bits) < 2 || bits[0] == "" {
		return false
	}
	accountID := bits[0]
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var result any
	var err error
	status := 200
	switch {
	case len(bits) == 3 && bits[1] == "auth" && bits[2] == "status" && r.Method == http.MethodGet:
		result, err = l.status(ctx, accountID)
	case len(bits) == 3 && bits[1] == "auth" && bits[2] == "qr" && r.Method == http.MethodPost:
		if !managementRequest(r) {
			rejectManagement(w)
			return true
		}
		result, err = l.begin(ctx, accountID)
	case len(bits) == 3 && bits[1] == "auth" && bits[2] == "check" && r.Method == http.MethodPost:
		if !managementRequest(r) {
			rejectManagement(w)
			return true
		}
		result, err = l.poll(ctx, accountID)
	case len(bits) == 2 && bits[1] == "playlists" && r.Method == http.MethodGet:
		result, err = l.playlists(ctx, accountID)
	case len(bits) == 4 && bits[1] == "playlists" && bits[3] == "tracks" && r.Method == http.MethodGet:
		result, err = l.tracks(ctx, accountID, bits[2])
	default:
		return false
	}
	if err != nil {
		var qrError *netease.QRStateError
		if errors.As(err, &qrError) {
			respond(w, 502, map[string]any{"code": "qr_state_unexpected", "upstream_code": qrError.Code})
			return true
		}
		switch {
		case errors.Is(err, accounts.ErrNotFound):
			status = 404
		case errors.Is(err, errPollFast):
			status = 429
		case errors.Is(err, errNoChallenge):
			status = 410
		default:
			status = 502
		}
		// Do not echo upstream provider responses or account tokens into UI.
		respond(w, status, map[string]any{"error": fmt.Sprintf("operation failed: %s", err.Error())})
		return true
	}
	respond(w, 200, result)
	return true
}
