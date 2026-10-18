package admin

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/chenpingonline/fnmusic-go/internal/takeover"
)

//go:embed assets/*
var embedded embed.FS

type Config struct {
	Socket takeover.Config
	Prefix string
}

func New(c Config) http.Handler {
	prefix := c.Prefix
	if prefix == "" {
		prefix = "/app/fnmusic-go"
	}
	files, _ := fs.Sub(embedded, "assets")
	public := http.FileServer(http.FS(files))
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Header.Get("X-Trim-Isadmin") != "true" {
				http.Error(w, "administrator login required", http.StatusForbidden)
				return
			}
			switch r.URL.Path {
			case "/api/status":
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				_ = json.NewEncoder(w).Encode(map[string]any{"version": "0.1.0", "phase": "safe-transparent-proxy", "source_enabled": false, "features": []string{"unix-socket-proxy", "health", "read-only-dashboard", "safe-restore"}, "socket": takeover.Inspect(c.Socket), "checked_at": time.Now().UTC().Format(time.RFC3339)})
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
			h.ServeHTTP(w, rr)
			return
		}
		http.NotFound(w, r)
	})
}
