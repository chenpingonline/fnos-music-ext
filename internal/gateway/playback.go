package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/library"
	"github.com/chenpingonline/fnos-music-ext/internal/playback"
	"io"
	"net/http"
	"strings"
	"time"
)

func (g *Gateway) hlsManager() (*playback.Manager, error) {
	g.hlsMu.Lock()
	defer g.hlsMu.Unlock()
	if g.hls != nil {
		return g.hls, nil
	}
	m, err := playback.New(g.bg, library.FFmpeg())
	if err == nil {
		g.hls = m
	}
	return m, err
}
func (g *Gateway) playbackRoute(w http.ResponseWriter, r *http.Request) bool {
	const base = "/music/api/v1/track/"
	path := r.URL.Path
	action := ""
	guid := ""
	file := ""
	switch {
	case path == base+"transcode":
		action = "prepare"
	case path == base+"transcode/heartbeat":
		action = "heartbeat"
	case path == base+"transcode/quit":
		action = "quit"
	case strings.HasPrefix(path, base+"hls/"):
		parts := strings.Split(strings.TrimPrefix(path, base+"hls/"), "/")
		if len(parts) != 2 {
			return false
		}
		guid, file = parts[0], parts[1]
		action = "file"
	default:
		return false
	}
	if action == "file" && r.Method != "GET" && r.Method != "HEAD" {
		return false
	}
	if action != "file" && r.Method != "GET" && r.Method != "POST" {
		return false
	}
	if guid == "" {
		guid = playlistGUID(r)
		if guid == "" {
			for _, k := range []string{"trackGUID", "trackGuid", "id", "trackId"} {
				if x := r.URL.Query().Get(k); x != "" {
					guid = x
					break
				}
			}
		}
		if r.Method == "POST" {
			raw, err := io.ReadAll(io.LimitReader(r.Body, 16385))
			if err != nil || len(raw) > 16384 {
				http.Error(w, "request too large", 413)
				return true
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			if guid == "" {
				var obj map[string]any
				if json.Unmarshal(raw, &obj) == nil {
					for _, k := range []string{"guid", "trackGUID", "trackGuid", "id", "trackId"} {
						if x, ok := obj[k].(string); ok && x != "" {
							guid = x
							break
						}
					}
				}
			}
		}
	}
	id, ok := fnmusic.DecodeNetEaseID(guid)
	if !ok {
		return false
	}
	guid, _ = fnmusic.EncodeNetEaseID(id)
	if !userMutation(r) {
		http.Error(w, "same-origin request required", 403)
		return true
	}
	user, source, scope, ok := g.playlistSource(r)
	if !ok {
		http.Error(w, "verified music account required", 403)
		return true
	}
	if action == "file" && !playback.ValidFile(file) {
		http.NotFound(w, r)
		return true
	}
	m, err := g.hlsManager()
	if err != nil {
		writeJSON(w, 503, map[string]any{"code": -1, "msg": "缺少可用 FFmpeg，无法进行 AAC/HLS 转码播放"})
		return true
	}
	key := playback.Key(user.FnOSGUID, scope, guid, fmt.Sprintf("%s:%d", g.policy().Quality, g.revision()))
	if action == "quit" {
		m.Quit(key)
		writeJSON(w, 200, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"guid": guid}})
		return true
	}
	if action == "heartbeat" {
		m.Touch(key)
		writeJSON(w, 200, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"guid": guid}})
		return true
	}
	quality := g.policy().Quality
	session, err := m.Ensure(key, func(ctx context.Context) (io.ReadCloser, error) {
		if g.media != nil {
			if job, ok := g.media.Cached(user.FnOSGUID, scope, id, quality); ok {
				if f, err := g.media.File(job); err == nil {
					return f, nil
				}
			}
		}
		stream, err := playQuality(ctx, source, user, id, quality, "")
		if err != nil {
			return nil, err
		}
		return stream.Body, nil
	})
	if err != nil {
		writePlaybackError(w, 503, err)
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	waitFile := file
	if action == "prepare" {
		waitFile = "preset.m3u8"
	}
	f, err := m.File(ctx, session, waitFile)
	if err != nil {
		status := 502
		if errors.Is(err, context.DeadlineExceeded) {
			status = 504
		}
		var detail *playback.Failure
		if !errors.As(err, &detail) {
			diagnostics.Record("playback-file", key, err)
		}
		writePlaybackError(w, status, err)
		return true
	}
	defer f.Close()
	if action == "prepare" || file == "preset.m3u8" {
		// HLS playback bypasses /stream; retain the existing background cache/save policy.
		cacheRequest := r.Clone(r.Context())
		cacheRequest.Method = "GET"
		g.cachePlayback(cacheRequest, user, scope, source, id)
	}
	if action == "prepare" {
		writeJSON(w, 200, map[string]any{"code": 0, "msg": "ok", "status": "success", "data": map[string]any{"guid": guid, "status": "ready"}})
		return true
	}
	m.Touch(key)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "segment unavailable", 502)
		return true
	}
	switch file {
	case "preset.m3u8":
		raw, err := io.ReadAll(io.LimitReader(f, 2<<20))
		if err != nil {
			http.Error(w, "playlist unavailable", 502)
			return true
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		http.ServeContent(w, r, file, time.Time{}, bytes.NewReader(playback.Playlist(raw, guid)))
	case "init.mp4":
		w.Header().Set("Content-Type", "audio/mp4")
		http.ServeContent(w, r, file, info.ModTime(), f)
	default:
		w.Header().Set("Content-Type", "video/iso.segment")
		http.ServeContent(w, r, file, info.ModTime(), f)
	}
	return true
}

func writePlaybackError(w http.ResponseWriter, status int, err error) {
	payload := map[string]any{"code": -1, "msg": playback.Message(err)}
	var detail *playback.Failure
	if errors.As(err, &detail) {
		payload["diagnostic"] = detail
	}
	writeJSON(w, status, payload)
}
