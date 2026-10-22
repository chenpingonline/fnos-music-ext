package gateway

import (
	"encoding/json"
	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/library"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func downloadMode(config, requested string, isOriginal bool) string {
	requested = strings.ToLower(strings.TrimSpace(requested))
	switch config {
	case "standard":
		return "standard"
	case "original":
		return "original"
	}
	if !isOriginal && (requested == "standard" || requested == "std" || requested == "mp3" || requested == "标准") {
		return "standard"
	}
	return "original"
}
func (s *Gateway) downloadRoute(w http.ResponseWriter, r *http.Request) bool {
	if s.media == nil || !strings.HasPrefix(r.URL.Path, "/music/api/v1/download/track/transcode/") {
		return false
	}
	action := strings.TrimPrefix(r.URL.Path, "/music/api/v1/download/track/transcode/")
	if action != "prepare" && action != "status" && action != "file" && action != "delete" {
		return false
	}
	validMethod := action == "prepare" && (r.Method == "GET" || r.Method == "POST") || action == "status" && (r.Method == "GET" || r.Method == "POST") || action == "file" && (r.Method == "GET" || r.Method == "HEAD") || action == "delete" && (r.Method == "POST" || r.Method == "DELETE")
	if !validMethod {
		return false
	}
	values := map[string]any{}
	if r.Method == "POST" {
		b, e := io.ReadAll(io.LimitReader(r.Body, 16385))
		if e != nil || len(b) > 16384 {
			http.Error(w, "request too large", 413)
			return true
		}
		r.Body = io.NopCloser(strings.NewReader(string(b)))
		_ = json.Unmarshal(b, &values)
	}
	value := func(keys ...string) string {
		for _, k := range keys {
			if v := r.URL.Query().Get(k); v != "" {
				return v
			}
			if v, ok := values[k]; ok {
				return strings.TrimSpace(toString(v))
			}
		}
		return ""
	}
	guid := value("trackGUID", "trackGuid", "guid")
	id, online := fnmusic.DecodeOnlineID(guid)
	taskID := value("downloadId", "downloadID", "id")
	if action == "prepare" && !online {
		return false
	}
	if action != "prepare" && (len(taskID) != 64 || !s.media.Known(taskID)) {
		return false
	}
	if (action == "prepare" || action == "delete") && !userMutation(r) {
		http.Error(w, "same-origin request required", 403)
		return true
	}
	user, ok := s.identify(r.Context(), r)
	if !ok {
		http.Error(w, "verified account required", 403)
		return true
	}
	source, scope, ok := s.sourceFor(user)
	if action == "prepare" {
		source, scope, ok = s.trackSource(user, guid)
	}
	if !ok {
		http.Error(w, "music account required", 403)
		return true
	}
	if action == "prepare" {
		if !trackSourceMatches(source, guid) {
			http.Error(w, "track belongs to another source", 403)
			return true
		}
		track, e := s.getTrack(r.Context(), user, source, scope, id)
		if e != nil {
			http.Error(w, "track unavailable", 502)
			return true
		}
		quality := downloadMode(s.policy().DownloadQuality, value("quality", "qualityType"), value("isOriginal") == "true" || value("isOriginal") == "1")
		if quality == "standard" && library.FFmpeg() == "" {
			writeJSON(w, 503, map[string]any{"code": -1, "msg": "缺少 ffmpeg，无法提供 MP3 320k 下载"})
			return true
		}
		job, e := s.media.Start(r.Context(), user, scope, track, "download", quality)
		if e != nil {
			writeJSON(w, 503, map[string]any{"code": -1, "msg": e.Error()})
			return true
		}
		writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]any{"status": "success", "errno": "", "errmsg": "", "downloadId": job.ID}})
		return true
	}
	job, ok := s.media.Lookup(taskID, user.FnOSGUID, scope)
	if !ok {
		for _, candidate := range s.sourcesFor(user) {
			if found, exists := s.media.Lookup(taskID, user.FnOSGUID, candidate.Scope); exists {
				job = found
				ok = true
				break
			}
		}
	}
	if !ok {
		http.Error(w, "task not found", 404)
		return true
	}
	switch action {
	case "status":
		state := map[string]string{"queued": "waiting", "running": "transcoding", "completed": "ready", "paused": "failed", "failed": "failed", "cancelled": "failed"}[job.State]
		code := ""
		message := job.Error
		if state == "failed" {
			code = "download_failed"
			if message == "" {
				message = "任务已取消或中断"
			}
		}
		writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]any{"status": state, "downloadId": job.ID, "percent": job.Percent, "errno": code, "errmsg": message}})
	case "file":
		s.serveJob(w, r, job, true)
	case "delete":
		if e := s.media.Cancel(job.ID); e != nil {
			http.Error(w, "cannot cancel task", 500)
		} else {
			writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]any{"downloadId": job.ID, "deleted": true}})
		}
	}
	return true
}
func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}
func (g *Gateway) serveJob(w http.ResponseWriter, r *http.Request, j library.Job, attachment bool) {
	f, e := g.media.File(j)
	if e != nil {
		http.Error(w, "audio not ready", 404)
		return
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		http.Error(w, "audio unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", j.ContentType)
	w.Header().Set("Cache-Control", "private, no-store")
	if attachment {
		name := j.Track.Title + "." + strings.TrimPrefix(strings.ToLower(filepathExt(j.Path)), ".")
		w.Header().Set("Content-Disposition", "attachment; filename=track"+filepathExt(j.Path)+"; filename*=UTF-8''"+url.PathEscape(name))
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}
func filepathExt(path string) string {
	idx := strings.LastIndex(path, ".")
	if idx < 0 {
		return ".mp3"
	}
	return path[idx:]
}
func (g *Gateway) cachePlayback(r *http.Request, u provider.User, scope string, source provider.Adapter, id string) {
	if g.media == nil || r.Method != "GET" {
		return
	}
	track, e := g.getTrack(r.Context(), u, source, scope, id)
	if e != nil {
		return
	}
	purpose := "cache"
	if g.policy().SaveEnabled {
		purpose = "save"
	}
	_, _ = g.media.Start(r.Context(), u, scope, track, purpose, g.policy().Quality)
}
