package admin

import (
	"context"
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/library"
	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

type discoverySource interface {
	Discover(context.Context, provider.User, string, string, int, int) ([]provider.Playlist, error)
	Categories(context.Context, provider.User) ([]string, error)
}

func handleMusic(w http.ResponseWriter, r *http.Request, c Config) bool {
	path := r.URL.Path
	if path != "/api/diagnostics" && path != "/api/cover-cache" && path != "/api/cover-cache/clear" && path != "/api/settings" && path != "/api/tasks" && path != "/api/discovery" && !strings.HasPrefix(path, "/api/tasks/") && !strings.HasPrefix(path, "/api/directories/") {
		return false
	}
	if c.Settings == nil || c.Media == nil {
		respond(w, 503, map[string]any{"error": "音乐任务服务尚未初始化"})
		return true
	}
	if r.Method != "GET" && !managementRequest(r) {
		rejectManagement(w)
		return true
	}
	switch {
	case path == "/api/diagnostics" && r.Method == "GET":
		respond(w, 200, diagnostics.Snapshot())
	case path == "/api/cover-cache" && r.Method == "GET", path == "/api/cover-cache/clear" && r.Method == "POST":
		stats, e := c.Media.CoverCache(r.Method == "POST")
		if e != nil {
			fail(w, e)
			break
		}
		respond(w, 200, stats)
	case path == "/api/settings" && r.Method == "GET":
		respond(w, 200, c.Settings.Snapshot())
	case path == "/api/settings" && r.Method == "PUT":
		var config preferences.Config
		if e := decode(r, &config); e != nil {
			fail(w, e)
			break
		}
		if e := c.Media.Apply(config); e != nil {
			fail(w, e)
			break
		}
		respond(w, 200, c.Settings.Snapshot())
	case path == "/api/tasks" && r.Method == "GET":
		respond(w, 200, map[string]any{"list": c.Media.Snapshot(), "ffmpeg": library.FFmpeg() != ""})
	case strings.HasPrefix(path, "/api/tasks/") && r.Method == "POST":
		parts := strings.Split(strings.TrimPrefix(path, "/api/tasks/"), "/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			break
		}
		var e error
		switch parts[1] {
		case "retry":
			if c.RetryTask != nil {
				e = c.RetryTask(parts[0])
			} else {
				e = c.Media.Retry(parts[0])
			}
		case "cancel":
			e = c.Media.Cancel(parts[0])
		default:
			e = errors.New("未知任务操作")
		}
		if e != nil {
			fail(w, e)
			break
		}
		respond(w, 200, map[string]bool{"ok": true})
	case path == "/api/directories/check" && r.Method == "POST":
		var body struct {
			Path string `json:"path"`
		}
		if e := decode(r, &body); e != nil {
			fail(w, e)
			break
		}
		p, e := c.Media.CheckDirectory(body.Path)
		if e != nil {
			fail(w, e)
			break
		}
		respond(w, 200, map[string]string{"path": p})
	case path == "/api/directories/detect" && r.Method == "GET":
		p, e := library.DetectLibraryDir()
		if e != nil {
			fail(w, e)
			break
		}
		respond(w, 200, map[string]string{"path": p})
	case path == "/api/discovery" && r.Method == "GET":
		if c.Accounts == nil {
			fail(w, errors.New("请先添加音源账号"))
			break
		}
		adapter, _, ok := c.Accounts.LookupAccount(r.URL.Query().Get("account"))
		if !ok {
			fail(w, errors.New("音源账号不存在"))
			break
		}
		source, ok := adapter.(discoverySource)
		if !ok {
			fail(w, errors.New("此音源不支持公共歌单"))
			break
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		u := provider.User{FnOSGUID: "admin-preview", Verified: true}
		if r.URL.Query().Get("kind") == "categories" {
			rows, e := source.Categories(ctx, u)
			if e != nil {
				fail(w, e)
				break
			}
			respond(w, 200, map[string]any{"list": rows})
			break
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		rows, e := source.Discover(ctx, u, r.URL.Query().Get("kind"), r.URL.Query().Get("category"), page, 24)
		if e != nil {
			fail(w, e)
			break
		}
		respond(w, 200, map[string]any{"list": rows, "page": page, "has_more": len(rows) == 24})
	default:
		http.Error(w, "method not allowed", 405)
	}
	return true
}
