package netease

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

// Native implements a NetEase provider without Python, Node.js or MusicBox.
// IMPORTANT: NetEase's EAPI is undocumented; compatibility with current live
// servers is not guaranteed. The provider does not bypass paywalls or DRM.
// Each Native instance persists only its own authenticated cookie to a 0600 file.
type Native struct {
	client  *http.Client
	media   *http.Client
	base    *url.URL
	session string
	mu      sync.Mutex
	cookie  string
}

func NewNative(address, directory string) (*Native, error) {
	u, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "native" || u.Host != "netease" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("native provider must be native://netease/<account-id>")
	}
	id := strings.TrimPrefix(u.Path, "/")
	if !nativeAccountID(id) || strings.Contains(u.Path[1:], "/") {
		return nil, errors.New("invalid native account ID")
	}
	return newNative(address, filepath.Join(directory, "netease-sessions", id+".json"), "https://interface.music.163.com", nil)
}
func nativeAccountID(id string) bool {
	if len(id) < 1 || len(id) > 48 {
		return false
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// The injected base/client exist only to exercise this volatile upstream
// protocol with an in-process fake server; they are never user configuration.
func newNative(_ string, session, base string, client *http.Client) (*Native, error) {
	u, e := url.Parse(base)
	if e != nil || u.Host == "" || u.Scheme != "https" && u.Scheme != "http" {
		return nil, errors.New("bad EAPI origin")
	}
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	n := &Native{base: u, session: session, client: client, media: newMediaClient()}
	if e := n.loadSession(); e != nil {
		return nil, e
	}
	return n, nil
}
func (n *Native) ID() string { return "netease" }
func (n *Native) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, n.base.String(), nil)
	if err != nil {
		return err
	}
	res, err := n.client.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode >= 500 {
		return ErrUnavailable
	}
	return nil
}
func (n *Native) loadSession() error {
	st, e := os.Lstat(n.session)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return errors.New("insecure NetEase session file; expected 0600")
	}
	b, e := os.ReadFile(n.session)
	if e != nil {
		return e
	}
	if len(b) > 8192 {
		return errors.New("session file too large")
	}
	var v struct {
		Cookie string `json:"cookie"`
	}
	if e := json.Unmarshal(b, &v); e != nil {
		return e
	}
	if !validSessionCookie(v.Cookie) && v.Cookie != "" {
		return errors.New("invalid stored session")
	}
	n.cookie = v.Cookie
	return nil
}
func validSessionCookie(v string) bool {
	if len(v) < 4 || len(v) > 4096 || strings.ContainsAny(v, "\r\n\x00") {
		return false
	}
	for _, part := range strings.Split(v, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 || kv[0] == "" || kv[1] == "" {
			return false
		}
		if kv[0] != "MUSIC_U" && kv[0] != "__csrf" && kv[0] != "NMTID" {
			return false
		}
	}
	return strings.Contains(v, "MUSIC_U=")
}
func normalizeCookies(s string) string {
	var out []string
	for _, part := range strings.Split(s, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.TrimSpace(kv[0])
		if k == "MUSIC_U" || k == "__csrf" || k == "NMTID" {
			out = append(out, k+"="+strings.TrimSpace(kv[1]))
		}
	}
	return strings.Join(out, "; ")
}
func (n *Native) persistCookie(cookie string) error {
	if !validSessionCookie(cookie) {
		return errors.New("NetEase did not provide a valid MUSIC_U login cookie")
	}
	dir := filepath.Dir(n.session)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	st, e := os.Lstat(dir)
	if e != nil || !st.IsDir() || st.Mode().Perm()&0077 != 0 {
		return errors.New("insecure session directory")
	}
	raw, _ := json.Marshal(struct {
		Cookie string `json:"cookie"`
	}{cookie})
	file, e := os.CreateTemp(dir, ".session-*")
	if e != nil {
		return e
	}
	defer os.Remove(file.Name())
	if e = file.Chmod(0600); e != nil {
		file.Close()
		return e
	}
	if _, e = file.Write(raw); e != nil {
		file.Close()
		return e
	}
	if e = file.Sync(); e != nil {
		file.Close()
		return e
	}
	if e = file.Close(); e != nil {
		return e
	}
	if e = os.Rename(file.Name(), n.session); e != nil {
		return e
	}
	n.cookie = cookie
	return nil
}
func (n *Native) post(ctx context.Context, path string, params map[string]any) (map[string]json.RawMessage, error) {
	n.mu.Lock()
	cookie := n.cookie
	n.mu.Unlock()
	if params == nil {
		params = map[string]any{}
	}
	// Explicit header is part of current EAPI protocol; keep user credentials
	// solely in the authenticated request, never in paths, logs, or responses.
	csrf := ""
	for _, part := range strings.Split(cookie, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 && kv[0] == "__csrf" {
			csrf = kv[1]
		}
	}
	params["header"] = map[string]any{"os": "pc", "appver": "2.10.0", "__csrf": csrf, "requestId": strconv.FormatInt(time.Now().UnixMilli(), 10)}
	form, e := eapiForm(path, params)
	if e != nil {
		return nil, e
	}
	u := *n.base
	u.Path = strings.Replace(path, "/api/", "/eapi/", 1)
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(form.Encode()))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", "https://music.163.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 fnMusicGo/0.5")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	res, e := n.client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("NetEase EAPI transport: %w", e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("NetEase EAPI HTTP %d", res.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, 4<<20+1))
	if e != nil || len(raw) > 4<<20 {
		return nil, errors.New("NetEase EAPI reply too large")
	}
	raw, e = decodeEAPIResponse(raw)
	if e != nil {
		return nil, e
	}
	var obj map[string]json.RawMessage
	if e = json.Unmarshal(raw, &obj); e != nil {
		return nil, e
	}
	if intField(obj, "code") == 301 || intField(obj, "code") == 401 {
		return nil, provider.ErrUnauthenticated
	}
	return obj, nil
}
func (n *Native) Search(ctx context.Context, u provider.User, q provider.SearchRequest) (provider.SearchPage, error) {
	if e := u.Validate(); e != nil {
		return provider.SearchPage{}, e
	}
	if strings.TrimSpace(q.Keyword) == "" || len([]rune(q.Keyword)) > 120 || q.Offset < 0 || q.Limit < 1 || q.Limit > 100 {
		return provider.SearchPage{}, errors.New("invalid search window")
	}
	obj, e := n.post(ctx, "/api/cloudsearch/pc", map[string]any{"s": q.Keyword, "type": 1, "offset": q.Offset, "limit": q.Limit, "total": true})
	if e != nil {
		return provider.SearchPage{}, e
	}
	var result struct {
		SongCount int               `json:"songCount"`
		Songs     []json.RawMessage `json:"songs"`
	}
	if e = json.Unmarshal(obj["result"], &result); e != nil {
		return provider.SearchPage{}, errors.New("unexpected native search response")
	}
	page := provider.SearchPage{Tracks: []provider.Track{}}
	for _, v := range result.Songs {
		t, e := parseTrack(v)
		if e == nil {
			page.Tracks = append(page.Tracks, t)
		}
	}
	page.Total = result.SongCount
	if page.Total < q.Offset+len(page.Tracks) {
		page.Total = q.Offset + len(page.Tracks)
	}
	page.HasMore = q.Offset+len(result.Songs) < page.Total
	return page, nil
}
func (n *Native) GetTrack(ctx context.Context, u provider.User, id string) (provider.Track, error) {
	if e := u.Validate(); e != nil {
		return provider.Track{}, e
	}
	if !validID(id) {
		return provider.Track{}, errors.New("invalid song ID")
	}
	obj, e := n.post(ctx, "/api/v3/song/detail", map[string]any{"c": "[{\"id\":" + id + "}]"})
	if e != nil {
		return provider.Track{}, e
	}
	var songs []json.RawMessage
	if json.Unmarshal(obj["songs"], &songs) != nil {
		return provider.Track{}, ErrUnavailable
	}
	for _, v := range songs {
		t, e := parseTrack(v)
		if e == nil && t.ID == id {
			return t, nil
		}
	}
	return provider.Track{}, ErrUnavailable
}
func (n *Native) GetLyrics(ctx context.Context, u provider.User, id string) (provider.Lyrics, error) {
	if e := u.Validate(); e != nil {
		return provider.Lyrics{}, e
	}
	if !validID(id) {
		return provider.Lyrics{}, errors.New("invalid song ID")
	}
	obj, e := n.post(ctx, "/api/song/lyric", map[string]any{"id": id, "lv": -1, "tv": -1})
	if e != nil {
		return provider.Lyrics{}, e
	}
	var l, t struct {
		Lyric string `json:"lyric"`
	}
	_ = json.Unmarshal(obj["lrc"], &l)
	_ = json.Unmarshal(obj["tlyric"], &t)
	return provider.Lyrics{LRC: l.Lyric, TranslatedLRC: t.Lyric}, nil
}
func (n *Native) Play(ctx context.Context, u provider.User, p provider.PlaybackRequest) (provider.Stream, error) {
	if e := u.Validate(); e != nil {
		return provider.Stream{}, e
	}
	if !validID(p.TrackID) {
		return provider.Stream{}, errors.New("invalid song ID")
	}
	quality := p.Quality
	if quality == "" {
		quality = "exhigh"
	}
	switch quality {
	case "standard", "higher", "exhigh", "lossless", "hires", "jymaster":
	default:
		return provider.Stream{}, errors.New("unsupported quality")
	}
	if p.Range != "" && p.Range != "__HEAD__" && !validRange(p.Range) {
		return provider.Stream{}, errors.New("invalid Range")
	}
	format := "mp3"
	if quality == "lossless" || quality == "hires" || quality == "jymaster" {
		format = "flac"
	}
	obj, e := n.post(ctx, "/api/song/enhance/player/url/v1", map[string]any{"ids": "[" + p.TrackID + "]", "level": quality, "encodeType": format, "e_r": true})
	if e != nil {
		return provider.Stream{}, e
	}
	var rows []struct {
		URL           string          `json:"url"`
		Code          int             `json:"code"`
		FreeTrialInfo json.RawMessage `json:"freeTrialInfo"`
	}
	if json.Unmarshal(obj["data"], &rows) != nil || len(rows) != 1 || rows[0].URL == "" || rows[0].Code != 200 || len(rows[0].FreeTrialInfo) > 0 && string(rows[0].FreeTrialInfo) != "null" {
		return provider.Stream{}, provider.ErrNotEntitled
	}
	uri, e := url.Parse(rows[0].URL)
	if e != nil || !safeMediaURL(uri) {
		return provider.Stream{}, errors.New("invalid media URL")
	}
	method := http.MethodGet
	if p.Range == "__HEAD__" {
		method = http.MethodHead
	}
	req, e := http.NewRequestWithContext(ctx, method, uri.String(), nil)
	if e != nil {
		return provider.Stream{}, e
	}
	if p.Range != "" && p.Range != "__HEAD__" {
		req.Header.Set("Range", p.Range)
	}
	res, e := n.media.Do(req)
	if e != nil {
		return provider.Stream{}, e
	}
	if res.StatusCode != 200 && res.StatusCode != 206 && res.StatusCode != 416 {
		res.Body.Close()
		return provider.Stream{}, fmt.Errorf("media HTTP %d", res.StatusCode)
	}
	ct := res.Header.Get("Content-Type")
	if ct == "" {
		ct = "audio/mpeg"
	}
	return provider.Stream{Body: res.Body, StatusCode: res.StatusCode, ContentType: ct, ContentRange: res.Header.Get("Content-Range"), ContentLength: res.ContentLength}, nil
}
func (n *Native) OpenCover(ctx context.Context, u provider.User, id string) (provider.Stream, error) {
	t, e := n.GetTrack(ctx, u, id)
	if e != nil {
		return provider.Stream{}, e
	}
	uri, e := url.Parse(t.CoverURL)
	if e != nil || !safeMediaURL(uri) {
		return provider.Stream{}, ErrUnavailable
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, uri.String(), nil)
	if e != nil {
		return provider.Stream{}, e
	}
	res, e := n.media.Do(req)
	if e != nil {
		return provider.Stream{}, e
	}
	if res.StatusCode != 200 || res.ContentLength > 6<<20 || !strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), "image/") {
		res.Body.Close()
		return provider.Stream{}, ErrUnavailable
	}
	data, e := io.ReadAll(io.LimitReader(res.Body, 6<<20+1))
	res.Body.Close()
	if e != nil || len(data) > 6<<20 {
		return provider.Stream{}, ErrUnavailable
	}
	return provider.Stream{Body: io.NopCloser(bytes.NewReader(data)), StatusCode: 200, ContentType: res.Header.Get("Content-Type"), ContentLength: int64(len(data))}, nil
}
func (n *Native) BeginQRLogin(ctx context.Context) (QRLogin, error) {
	obj, e := n.post(ctx, "/api/login/qrcode/unikey", map[string]any{"type": 3})
	if e != nil {
		return QRLogin{}, e
	}
	nested := unwrapObject(obj["data"])
	key := stringField(nested, "unikey", "codekey")
	if key == "" {
		key = stringField(obj, "unikey")
	}
	if len(key) == 0 || len(key) > 256 {
		return QRLogin{}, errors.New("native QR key unavailable")
	}
	return QRLogin{Key: key, QRURL: "https://music.163.com/login?codekey=" + url.QueryEscape(key)}, nil
}
func (n *Native) CheckQRLogin(ctx context.Context, key string) (LoginState, error) {
	if len(key) < 1 || len(key) > 256 {
		return LoginState{}, errors.New("invalid QR key")
	}
	obj, e := n.post(ctx, "/api/login/qrcode/client/login", map[string]any{"type": 3, "key": key})
	if e != nil {
		return LoginState{}, e
	}
	state := stateFromCode(intField(obj, "code"))
	if state.State == "success" {
		raw := stringField(obj, "cookie")
		cookie := normalizeCookies(raw)
		if cookie == "" {
			return LoginState{}, errors.New("login confirmed without a usable session cookie")
		}
		n.mu.Lock()
		e = n.persistCookie(cookie)
		n.mu.Unlock()
		if e != nil {
			return LoginState{}, e
		}
	}
	return state, nil
}
func (n *Native) account(ctx context.Context) (int64, error) {
	n.mu.Lock()
	cookie := n.cookie
	n.mu.Unlock()
	if cookie == "" {
		return 0, provider.ErrUnauthenticated
	}
	obj, e := n.post(ctx, "/api/nuser/account/get", map[string]any{})
	if e != nil {
		return 0, e
	}
	uid := intField(unwrapObject(obj["account"]), "id", "userId")
	if uid == 0 {
		uid = intField(unwrapObject(obj["profile"]), "userId", "id")
	}
	if uid <= 0 {
		return 0, provider.ErrUnauthenticated
	}
	return uid, nil
}
func (n *Native) AuthStatus(ctx context.Context) (LoginState, error) {
	uid, e := n.account(ctx)
	if errors.Is(e, provider.ErrUnauthenticated) {
		return LoginState{State: "pending"}, nil
	}
	if e != nil {
		return LoginState{}, e
	}
	if uid > 0 {
		return LoginState{State: "success", LoggedIn: true}, nil
	}
	return LoginState{State: "unknown"}, nil
}
func (n *Native) ListPlaylists(ctx context.Context, u provider.User) ([]provider.Playlist, error) {
	if e := u.Validate(); e != nil {
		return nil, e
	}
	uid, e := n.account(ctx)
	if e != nil {
		return nil, e
	}
	obj, e := n.post(ctx, "/api/user/playlist", map[string]any{"uid": uid, "limit": 100, "offset": 0})
	if e != nil {
		return nil, e
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(obj["playlist"], &rows) != nil {
		return nil, ErrUnavailable
	}
	out := []provider.Playlist{}
	for _, v := range rows {
		if intField(v, "userId", "user_id") != uid {
			continue
		}
		id := intField(v, "id")
		if id < 1 {
			continue
		}
		out = append(out, provider.Playlist{ID: strconv.FormatInt(id, 10), Title: stringField(v, "name"), CoverURL: stringField(v, "coverImgUrl"), TrackCount: int(intField(v, "trackCount")), ReadOnly: true})
	}
	return out, nil
}
func (n *Native) PlaylistTracks(ctx context.Context, u provider.User, id string) ([]provider.Track, error) {
	if e := u.Validate(); e != nil {
		return nil, e
	}
	if !validID(id) {
		return nil, errors.New("invalid playlist ID")
	}
	// Verify ownership before loading arbitrary IDs. No stale shared-account cache.
	lists, e := n.ListPlaylists(ctx, u)
	if e != nil {
		return nil, e
	}
	owned := false
	for _, p := range lists {
		if p.ID == id {
			owned = true
			break
		}
	}
	if !owned {
		return nil, provider.ErrUnauthenticated
	}
	obj, e := n.post(ctx, "/api/v6/playlist/detail", map[string]any{"id": id, "n": 1000, "s": 0})
	if e != nil {
		return nil, e
	}
	playlist := unwrapObject(obj["playlist"])
	var ids []struct {
		ID json.Number `json:"id"`
	}
	if json.Unmarshal(playlist["trackIds"], &ids) != nil {
		return nil, ErrUnavailable
	}
	if len(ids) > 1000 {
		ids = ids[:1000]
	}
	result := make([]provider.Track, 0, len(ids))
	for i := 0; i < len(ids); i += 100 {
		end := i + 100
		if end > len(ids) {
			end = len(ids)
		}
		batch := make([]map[string]int64, 0, end-i)
		for _, v := range ids[i:end] {
			n, e := v.ID.Int64()
			if e == nil && n > 0 {
				batch = append(batch, map[string]int64{"id": n})
			}
		}
		if len(batch) == 0 {
			continue
		}
		buf, _ := json.Marshal(batch)
		d, e := n.post(ctx, "/api/v3/song/detail", map[string]any{"c": string(buf)})
		if e != nil {
			return nil, e
		}
		var songs []json.RawMessage
		if json.Unmarshal(d["songs"], &songs) != nil {
			return nil, ErrUnavailable
		}
		for _, v := range songs {
			t, e := parseTrack(v)
			if e == nil {
				result = append(result, t)
			}
		}
	}
	return result, nil
}
