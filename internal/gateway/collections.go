package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

type userSession struct {
	headers http.Header
	expires time.Time
}

func credentials(r *http.Request) http.Header {
	h := http.Header{}
	for _, k := range []string{"Cookie", "Authorization", "X-Trim-Music-Temp-Token"} {
		h[k] = append([]string(nil), r.Header.Values(k)...)
	}
	return h
}
func (g *Gateway) remember(u provider.User, r *http.Request) {
	g.sessionMu.Lock()
	defer g.sessionMu.Unlock()
	if g.sessions == nil {
		g.sessions = map[string]userSession{}
	}
	for key, s := range g.sessions {
		if time.Now().After(s.expires) {
			delete(g.sessions, key)
		}
	}
	if len(g.sessions) >= 128 {
		for key := range g.sessions {
			delete(g.sessions, key)
			break
		}
	}
	g.sessions[u.FnOSGUID] = userSession{credentials(r), time.Now().Add(30 * time.Minute)}
}
func (g *Gateway) session(user string) (http.Header, bool) {
	g.sessionMu.Lock()
	defer g.sessionMu.Unlock()
	s, ok := g.sessions[user]
	return s.headers.Clone(), ok && time.Now().Before(s.expires)
}
func (g *Gateway) official(ctx context.Context, headers http.Header, method, path string, body any) (*http.Response, []byte, error) {
	var reader io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return nil, nil, e
		}
		reader = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, "http://trim-music"+path, reader)
	if e != nil {
		return nil, nil, e
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = http.Header{}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := g.client.Do(req)
	if e != nil {
		return nil, nil, e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, maxOfficialSearchBody+1))
	if len(b) > maxOfficialSearchBody {
		return nil, nil, errors.New("official response too large")
	}
	return resp, b, e
}
func officialOK(resp *http.Response, b []byte) bool {
	var v struct {
		Code *int `json:"code"`
	}
	return resp != nil && resp.StatusCode == 200 && json.Unmarshal(b, &v) == nil && v.Code != nil && *v.Code == 0
}
func userMutation(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" || site == "same-site" {
		return false
	}
	if origin := r.Header.Get("Origin"); r.Header.Get("Sec-Fetch-Site") != "same-origin" && origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
		return false
	}
	return true
}
func (g *Gateway) collectionRoute(w http.ResponseWriter, r *http.Request) bool {
	if g.ledger == nil {
		return false
	}
	path := r.URL.Path
	list := r.Method == "GET" && (path == "/music/api/v1/favorite-track/list" || path == "/music/api/v1/track/playlist-detail/list")
	mutation := r.Method == "POST" && (path == "/music/api/v1/favorite-track/create" || path == "/music/api/v1/favorite-track/delete" || path == "/music/api/v1/playlist/add-track" || path == "/music/api/v1/playlist/remove-track")
	if !list && !mutation {
		return false
	}
	var body struct {
		TrackGUID string   `json:"trackGUID"`
		Guid      string   `json:"guid"`
		Tracks    []string `json:"trackGUIDs"`
	}
	if mutation {
		raw, e := io.ReadAll(io.LimitReader(r.Body, 16385))
		if e != nil || len(raw) > 16384 {
			http.Error(w, "request too large", 413)
			return true
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		if json.Unmarshal(raw, &body) != nil {
			return false
		}
	}
	playlist := "favorites"
	if strings.Contains(path, "/playlist/") || path == "/music/api/v1/track/playlist-detail/list" {
		playlist = body.Guid
		if list {
			playlist = playlistGUID(r)
		}
	}
	u, ok := g.identify(r.Context(), r)
	if !ok {
		return false
	}
	source, scope, ok := g.sourceFor(u)
	if !ok {
		return false
	}
	g.remember(u, r)
	if playlist != "favorites" {
		if _, _, virtual := decodeList(scope, playlist); virtual {
			if mutation {
				writeJSON(w, 409, map[string]any{"code": -1, "msg": "此歌单只读"})
				return true
			}
			return false
		}
	}
	if list {
		return g.collectionList(w, r, u, source, scope, playlist)
	}
	add := strings.HasSuffix(path, "/create") || strings.HasSuffix(path, "/add-track")
	guids := body.Tracks
	if playlist == "favorites" {
		guid := body.TrackGUID
		if guid == "" {
			guid = body.Guid
		}
		guids = []string{guid}
	}
	relevant := false
	for _, guid := range guids {
		if _, ok := fnmusic.DecodeNetEaseID(guid); ok {
			relevant = true
		}
	}
	if !relevant && !add {
		for _, entry := range g.ledger.List(u.FnOSGUID, scope, playlist) {
			for _, guid := range guids {
				if entry.Bound != "" && entry.Bound == guid {
					relevant = true
				}
			}
		}
	}
	if !relevant {
		return false
	}
	if !userMutation(r) {
		http.Error(w, "same-origin request required", 403)
		return true
	}
	if len(guids) > 100 {
		http.Error(w, "too many tracks", 400)
		return true
	}
	if playlist != "favorites" && !g.ownsOfficialPlaylist(r.Context(), credentials(r), u, playlist) {
		writeJSON(w, 403, map[string]any{"code": -1, "msg": "无法验证歌单编辑权限"})
		return true
	}
	officialIDs := []string{}
	onlineIDs := []string{}
	for _, guid := range guids {
		if id, ok := fnmusic.DecodeNetEaseID(guid); ok {
			onlineIDs = append(onlineIDs, id)
		} else {
			officialIDs = append(officialIDs, guid)
		}
	}
	// Preserve the official portion of a mixed write. Never record a local intent
	// if the official server rejected the same operation.
	if len(officialIDs) > 0 {
		payload := map[string]any{"guid": playlist, "trackGUIDs": officialIDs}
		if playlist == "favorites" {
			payload = map[string]any{"trackGUID": officialIDs[0]}
		}
		g.bindWrites.Lock()
		resp, b, e := g.official(r.Context(), credentials(r), "POST", path, payload)
		if e != nil {
			g.bindWrites.Unlock()
			http.Error(w, "official backend unavailable", 502)
			return true
		}
		if !officialOK(resp, b) {
			g.bindWrites.Unlock()
			writeOfficial(w, resp, b)
			return true
		}
		if !add {
			for _, entry := range g.ledger.List(u.FnOSGUID, scope, playlist) {
				for _, guid := range officialIDs {
					if entry.Bound != "" && entry.Bound == guid {
						if e = g.ledger.Set(u.FnOSGUID, scope, playlist, entry.Track, false, false); e != nil {
							g.bindWrites.Unlock()
							http.Error(w, "collection write failed", 500)
							return true
						}
					}
				}
			}
		}
		g.bindWrites.Unlock()
	}

	for _, id := range onlineIDs {
		track, e := g.updateCollection(r, u, source, scope, playlist, id, add)
		if e != nil {
			writeJSON(w, 502, map[string]any{"code": -1, "msg": e.Error()})
			return true
		}
		if add && g.policy().AutoBind && g.media != nil {
			j, e := g.media.Start(g.bg, u, scope, track, "bind", "original")
			if e == nil {
				g.scheduleBind(u, scope, playlist, id, j.ID)
			} else {
				entry, _ := g.ledger.Get(u.FnOSGUID, scope, playlist, id)
				g.ledger.BindingError(u.FnOSGUID, scope, playlist, id, entry.Revision, e.Error())
			}
		}
	}
	writeJSON(w, 200, map[string]any{"code": 0, "msg": "ok", "data": nil})
	return true
}
func (g *Gateway) ownsOfficialPlaylist(ctx context.Context, h http.Header, u provider.User, id string) bool {
	resp, b, e := g.official(ctx, h, "GET", "/music/api/v1/playlist/detail?guid="+url.QueryEscape(id), nil)
	if e != nil || !officialOK(resp, b) {
		return false
	}
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(b, &envelope) != nil {
		return false
	}
	for _, key := range []string{"userGUID", "userGuid", "ownerGUID", "ownerGuid"} {
		var owner string
		if json.Unmarshal(envelope.Data[key], &owner) == nil && owner == u.FnOSGUID {
			return true
		}
	}
	var owner struct {
		GUID string `json:"guid"`
	}
	_ = json.Unmarshal(envelope.Data["owner"], &owner)
	return owner.GUID == u.FnOSGUID
}
func (g *Gateway) collectionList(w http.ResponseWriter, r *http.Request, u provider.User, source provider.Adapter, scope, playlist string) bool {
	entries := g.ledger.List(u.FnOSGUID, scope, playlist)
	if len(entries) == 0 {
		return false
	}
	resp, b, e := g.official(r.Context(), credentials(r), "GET", r.URL.RequestURI(), nil)
	if e != nil || !officialOK(resp, b) {
		return false
	}
	var envelope map[string]json.RawMessage
	var data map[string]json.RawMessage
	if json.Unmarshal(b, &envelope) != nil || json.Unmarshal(envelope["data"], &data) != nil {
		return false
	}
	var rows []json.RawMessage
	if json.Unmarshal(data["list"], &rows) != nil {
		return false
	}
	total := 0
	_ = json.Unmarshal(data["total"], &total)
	if total < len(rows) {
		total = len(rows)
	}
	online := []json.RawMessage{}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Created.After(entries[j].Created) })
	for _, entry := range entries {
		if entry.Bound != "" {
			continue
		}
		vo, ok := fnmusic.TrackVO(entry.Track, false)
		if !ok {
			continue
		}
		vo["isFavorite"] = playlist == "favorites"
		vo["createdAt"] = entry.Created.Unix()
		row, _ := json.Marshal(vo)
		online = append(online, row)
		g.cache.put(scope+":"+entry.Track.ID, entry.Track)
		// Reads may retry an already downloaded binding, never initiate a download.
		if entry.Pending && g.media != nil {
			if j, ok := g.media.Saved(u.FnOSGUID, scope, entry.Track.ID); ok && (j.Purpose == "bind" || j.Purpose == "save") {
				g.scheduleBind(u, scope, playlist, entry.Track.ID, j.ID)
			}
		}
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size == -1 {
		size = total + len(online)
		page = 1
	}
	if size < 1 {
		size = 50
	}
	if size > 1000 {
		size = 1000
	}
	start := (page - 1) * size
	if start < total && len(rows) > size {
		lo := start
		if lo > len(rows) {
			lo = len(rows)
		}
		hi := lo + size
		if hi > len(rows) {
			hi = len(rows)
		}
		rows = rows[lo:hi]
	}
	remaining := size - len(rows)
	if start >= total {
		rows = nil
		remaining = size
	}
	offset := start - total
	if offset < 0 {
		offset = 0
	}
	if offset > len(online) {
		offset = len(online)
	}
	end := offset + remaining
	if end > len(online) {
		end = len(online)
	}
	if remaining > 0 {
		rows = append(rows, online[offset:end]...)
	}
	data["list"], _ = json.Marshal(rows)
	data["total"], _ = json.Marshal(total + len(online))
	envelope["data"], _ = json.Marshal(data)
	writeJSON(w, 200, envelope)
	return true
}
func (g *Gateway) bindWrite(ctx context.Context, h http.Header, playlist, guid string, add bool) error {
	path := "/music/api/v1/favorite-track/create"
	body := map[string]any{"trackGUID": guid}
	if !add {
		path = "/music/api/v1/favorite-track/delete"
	}
	if playlist != "favorites" {
		path = "/music/api/v1/playlist/add-track"
		if !add {
			path = "/music/api/v1/playlist/remove-track"
		}
		body = map[string]any{"guid": playlist, "trackGUIDs": []string{guid}}
	}
	resp, b, e := g.official(ctx, h, "POST", path, body)
	if e != nil {
		return e
	}
	if !officialOK(resp, b) {
		return errors.New("官方收藏或歌单写入未成功")
	}
	return nil
}
func (g *Gateway) scheduleBind(u provider.User, scope, playlist, id, jobID string) {
	if g.media == nil || !g.policy().AutoBind {
		return
	}
	entry, ok := g.ledger.Get(u.FnOSGUID, scope, playlist, id)
	if !ok || !entry.Pending {
		return
	}
	key := u.FnOSGUID + ":" + scope + ":" + playlist + ":" + id
	if !g.startBinding(key) {
		return
	}
	g.bindWG.Add(1)
	go func() {
		defer g.bindWG.Done()
		defer func() {
			g.finishBinding(key)
			if current, ok := g.ledger.Get(u.FnOSGUID, scope, playlist, id); ok && current.Pending && current.Revision != entry.Revision && g.bg.Err() == nil {
				g.scheduleBind(u, scope, playlist, id, jobID)
			}
		}()

		fail := func(message string) {
			g.ledger.BindingError(u.FnOSGUID, scope, playlist, id, entry.Revision, message)
			g.media.Warn(jobID, message)
		}
		job, ok := g.media.Lookup(jobID, u.FnOSGUID, scope)
		wait, cancelWait := context.WithTimeout(g.bg, 10*time.Minute)
		defer cancelWait()
		for ok && (job.State == "queued" || job.State == "running") {
			current, exists := g.ledger.Get(u.FnOSGUID, scope, playlist, id)
			if !exists || current.Revision != entry.Revision || !g.policy().AutoBind {
				return
			}
			select {
			case <-wait.Done():
				fail("下载等待已结束，可通过任务重试")
				return
			case <-time.After(250 * time.Millisecond):
			}
			job, ok = g.media.Lookup(jobID, u.FnOSGUID, scope)
		}
		if !ok || job.State != "completed" {
			fail("下载未完成，可通过任务重试")
			return
		}
		g.media.Warn(jobID, "下载完成，等待官方扫描与收藏绑定")
		headers, ok := g.session(u.FnOSGUID)
		if !ok {
			fail("等待原用户重新打开收藏或歌单以确认登录态")
			return
		}
		ctx, cancel := context.WithTimeout(g.bg, time.Duration(g.policy().BindTimeout)*time.Second)
		defer cancel()
		if scan := g.policy().ScanPath; scan != "" {
			_, _, _ = g.official(ctx, headers, "POST", scan, map[string]any{})
		}
		for {
			current, ok := g.ledger.Get(u.FnOSGUID, scope, playlist, id)
			if !ok || current.Revision != entry.Revision || !current.Pending || !g.policy().AutoBind {
				return
			}
			if _, currentScope, ok := g.sourceFor(u); !ok || currentScope != scope {
				fail("账号访问已变化，绑定已停止")
				return
			}
			if currentJob, exists := g.media.Lookup(jobID, u.FnOSGUID, scope); !exists || currentJob.State != "completed" {
				return
			}
			officialID := g.findSavedTrack(ctx, headers, entry.Track, job.Path)
			if officialID != "" {
				g.bindWrites.Lock()
				current, valid := g.ledger.Get(u.FnOSGUID, scope, playlist, id)
				if !valid || current.Revision != entry.Revision || !current.Pending {
					g.bindWrites.Unlock()
					return
				}
				if playlist != "favorites" && !g.ownsOfficialPlaylist(ctx, headers, u, playlist) {
					g.bindWrites.Unlock()
					fail("歌单编辑权限已变化")
					return
				}
				if !g.sessionOwner(ctx, headers, u.FnOSGUID) {
					g.bindWrites.Unlock()
					fail("原用户登录态已失效，等待重新打开收藏后重试")
					return
				}
				err := g.bindWrite(ctx, headers, playlist, officialID, true)
				if err == nil && !g.ledger.CommitBinding(u.FnOSGUID, scope, playlist, id, entry.Revision, officialID) {
					_ = g.bindWrite(ctx, headers, playlist, officialID, false)
				}
				g.bindWrites.Unlock()
				if err != nil {
					fail("官方写入失败，保留在线映射供重试")
				} else {
					g.media.Warn(jobID, "")
				}
				return
			}
			select {
			case <-ctx.Done():
				fail("等待官方曲库收录超时，已保留在线映射，可重试绑定")
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
}
func (g *Gateway) findSavedTrack(ctx context.Context, h http.Header, track provider.Track, saved string) string {
	resp, b, e := g.official(ctx, h, "GET", "/music/api/v1/search/track?q="+url.QueryEscape(track.Title)+"&page=1&size=100", nil)
	if e != nil || !officialOK(resp, b) {
		return ""
	}
	var result struct {
		Data struct {
			List []struct {
				GUID      string `json:"guid"`
				Title     string `json:"title"`
				Artist    string `json:"artist"`
				AudioSpec struct {
					Path string `json:"path"`
				} `json:"audioSpec"`
			} `json:"list"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &result) != nil {
		return ""
	}
	for _, t := range result.Data.List {
		if t.Title == track.Title && normalizeArtist(t.Artist) == normalizeArtist(strings.Join(track.Artists, " ")) && filepath.Clean(t.AudioSpec.Path) == filepath.Clean(saved) {
			return t.GUID
		}
	}
	return ""
}
func (g *Gateway) RetryTask(id string) error {
	if e := g.media.Retry(id); e != nil {
		return e
	}
	for _, job := range g.media.Snapshot() {
		if job.ID == id {
			u := provider.User{FnOSGUID: job.User, Verified: true}
			for _, intent := range g.ledger.Pending(job.User, job.Scope, job.Track.ID) {
				g.scheduleBind(u, job.Scope, intent.Playlist, job.Track.ID, job.ID)
			}
		}
	}
	return nil
}

// Serialize intent changes with official writes so removing a favorite cannot
// race a completed scan and leave an orphan official favorite behind.
func (g *Gateway) updateCollection(r *http.Request, u provider.User, source provider.Adapter, scope, playlist, id string, add bool) (provider.Track, error) {
	var track provider.Track
	if add {
		var e error
		track, e = g.getTrack(r.Context(), u, source, scope, id)
		if e != nil {
			return track, e
		}
	} else {
		track.ID = id
	}
	g.bindWrites.Lock()
	defer g.bindWrites.Unlock()
	old, exists := g.ledger.Get(u.FnOSGUID, scope, playlist, id)
	if !add && exists && old.Bound != "" {
		if e := g.bindWrite(r.Context(), credentials(r), playlist, old.Bound, false); e != nil {
			return track, e
		}
	}
	return track, g.ledger.Set(u.FnOSGUID, scope, playlist, track, add, add && g.policy().AutoBind)
}
func (g *Gateway) sessionOwner(ctx context.Context, h http.Header, user string) bool {
	resp, b, e := g.official(ctx, h, "GET", "/music/api/v1/user/me", nil)
	if e != nil || !officialOK(resp, b) {
		return false
	}
	var result struct {
		Data struct {
			GUID string `json:"guid"`
		} `json:"data"`
	}
	return json.Unmarshal(b, &result) == nil && result.Data.GUID == user
}

func normalizeArtist(s string) string {
	return strings.ToLower(strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '&' || r == '、' || r == ' ' || r == '\t' }), ""))
}

func (g *Gateway) favorite(u provider.User, scope, id string) (string, bool) {
	if g.ledger == nil {
		return "", false
	}
	entry, ok := g.ledger.Get(u.FnOSGUID, scope, "favorites", id)
	return entry.Bound, ok
}
func (g *Gateway) trackVO(u provider.User, scope string, t provider.Track, mark bool) (map[string]any, bool) {
	vo, ok := fnmusic.TrackVO(t, mark)
	if ok {
		_, favorite := g.favorite(u, scope, t.ID)
		vo["isFavorite"] = favorite
	}
	return vo, ok
}
