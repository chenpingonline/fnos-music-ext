package netease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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
	client    *http.Client
	webClient *http.Client
	webBase   *url.URL
	media     *http.Client
	base      *url.URL
	session   string
	mu        sync.Mutex
	cookie    string
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
	if e := n.initWebSession(); e != nil {
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
	if n.webClient != nil {
		n.seedWebCookies(cookie)
	}
	return nil
}
func (n *Native) post(ctx context.Context, path string, params map[string]any) (map[string]json.RawMessage, error) {
	n.mu.Lock()
	cookie := n.cookie
	n.mu.Unlock()
	if params == nil {
		params = map[string]any{}
	}
	// EAPI authenticates through both its encrypted header and HTTP cookies.
	// Keep its client profile consistent; WEAPI QR login has a separate jar.
	deviceID := ""
	if n.webClient != nil && n.webClient.Jar != nil && n.webBase != nil {
		for _, c := range n.webClient.Jar.Cookies(n.webBase) {
			if c.Name == "deviceId" {
				deviceID = c.Value
				break
			}
		}
	}
	header := eapiHeader(cookie, deviceID)
	payload := make(map[string]any, len(params)+2)
	for k, v := range params {
		payload[k] = v
	}
	payload["header"] = header
	payload["e_r"] = true
	form, e := eapiForm(path, payload)
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
	req.Header.Set("User-Agent", eapiUserAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.8")
	// These are protocol fields, not source response values. No API cookies
	// are copied to media requests, which use a separate client.
	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		req.AddCookie(&http.Cookie{Name: key, Value: fmt.Sprint(header[key])})
	}
	res, e := n.client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("NetEase EAPI transport: %w", e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, playbackHTTPError("netease-api", res.StatusCode)
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
	if intField(obj, "code") == 403 {
		return nil, diagnostics.New("netease-api-code-403")
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
		if p.IfRange != "" {
			req.Header.Set("If-Range", p.IfRange)
		}
	}
	req.Header.Set("User-Agent", mediaUserAgent)
	res, e := n.media.Do(req)
	if e != nil {
		return provider.Stream{}, e
	}
	if res.StatusCode != 200 && res.StatusCode != 206 && res.StatusCode != 416 {
		res.Body.Close()
		return provider.Stream{}, playbackHTTPError("media-cdn", res.StatusCode)
	}
	ct := res.Header.Get("Content-Type")
	if ct == "" {
		ct = "audio/mpeg"
	}
	return provider.Stream{Body: res.Body, StatusCode: res.StatusCode, ContentType: ct, ContentRange: res.Header.Get("Content-Range"), ContentLength: res.ContentLength, ETag: res.Header.Get("ETag"), LastModified: res.Header.Get("Last-Modified")}, nil
}
func (n *Native) OpenCover(ctx context.Context, user provider.User, id string) (provider.Stream, error) {
	return n.OpenCoverSized(ctx, user, id, 0)
}
func (n *Native) OpenCoverSized(ctx context.Context, user provider.User, id string, size int) (provider.Stream, error) {
	track, err := n.GetTrack(ctx, user, id)
	if err != nil {
		return provider.Stream{}, err
	}
	return n.OpenTrackCoverSized(ctx, user, track, size)
}

// OpenTrackCover uses metadata already obtained from a search or playlist.
func (n *Native) OpenTrackCover(ctx context.Context, user provider.User, track provider.Track) (provider.Stream, error) {
	return n.OpenTrackCoverSized(ctx, user, track, 0)
}
func (n *Native) OpenTrackCoverSized(ctx context.Context, user provider.User, track provider.Track, size int) (provider.Stream, error) {
	return openPlaylistImageSized(ctx, n.media, user, provider.Playlist{CoverURL: track.CoverURL}, size)
}
func (n *Native) BeginQRLogin(ctx context.Context) (QRLogin, error) {
	obj, e := n.webPost(ctx, "/weapi/login/qrcode/unikey", map[string]any{"type": 3})
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
	obj, e := n.webPost(ctx, "/weapi/login/qrcode/client/login", map[string]any{"type": 3, "key": key})
	if e != nil {
		return LoginState{}, e
	}
	code := qrLoginCode(obj)
	state := stateFromCode(code)
	if state.State == "unknown" {
		return LoginState{}, &QRStateError{Code: code}
	}
	if state.State == "success" {
		raw := stringField(obj, "cookie")
		if raw == "" {
			raw = stringField(unwrapObject(obj["data"]), "cookie")
		}
		cookie := normalizeCookies(raw)
		if cookie == "" {
			return LoginState{}, errors.New("网易云已确认授权，但未返回有效登录凭证，请重新生成二维码后重试。")
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
	obj, e := n.webPost(ctx, "/weapi/nuser/account/get", nil)
	if e != nil {
		return 0, e
	}
	uid := intField(unwrapObject(obj["account"]), "id", "userId")
	if uid == 0 {
		uid = intField(unwrapObject(obj["profile"]), "userId", "id")
	}
	if uid <= 0 {
		obj, e = n.webPost(ctx, "/weapi/w/nuser/account/get", nil)
		if e != nil {
			return 0, e
		}
		uid = intField(unwrapObject(obj["account"]), "id", "userId")
		if uid == 0 {
			uid = intField(unwrapObject(obj["profile"]), "userId", "id")
		}
		if uid <= 0 {
			return 0, provider.ErrUnauthenticated
		}
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
	return n.playlistTracks(ctx, id)
}
func (n *Native) playlistTracks(ctx context.Context, id string) ([]provider.Track, error) {
	page, err := n.playlistTrackPage(ctx, id, 0, -1)
	return page.Tracks, err
}
