package netease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

// QRLogin is an ephemeral MusicBox QR challenge. It is never persisted.
// The challenge key must remain server-side and is used only for polling.
type QRLogin struct {
	Key     string `json:"-"`
	QRURL   string `json:"qr_url"`
	QRASCII string `json:"qr_ascii,omitempty"`
}
type LoginState struct {
	State    string `json:"state"` // pending|scanned|success|expired|unknown
	LoggedIn bool   `json:"logged_in"`
}

type LoginAdapter interface {
	BeginQRLogin(context.Context) (QRLogin, error)
	CheckQRLogin(context.Context, string) (LoginState, error)
	AuthStatus(context.Context) (LoginState, error)
}

// requestMusicBox bounds responses and rejects provider errors without
// including raw (possibly sensitive) responses in logs or admin messages.
func (b *Bridge) requestMusicBox(ctx context.Context, method, path string, q url.Values) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, method, b.url(path, q), nil)
	if err != nil {
		return nil, err
	}
	res, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: connection failed", ErrUnavailable)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("%w: HTTP %d", ErrUnavailable, res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, errors.New("MusicBox reply too large or unreadable")
	}
	var v map[string]json.RawMessage
	if json.Unmarshal(raw, &v) != nil {
		return nil, errors.New("MusicBox reply is not JSON")
	}
	var ok bool
	if json.Unmarshal(v["ok"], &ok) == nil && !ok {
		return nil, ErrUnavailable
	}
	if dat := v["data"]; len(dat) > 0 && string(dat) != "null" {
		return dat, nil
	}
	return raw, nil
}
func unwrapObject(raw json.RawMessage) map[string]json.RawMessage {
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(raw, &obj)
	return obj
}
func stringField(obj map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		var s string
		if json.Unmarshal(obj[k], &s) == nil && s != "" {
			return s
		}
	}
	return ""
}
func intField(obj map[string]json.RawMessage, keys ...string) int64 {
	for _, k := range keys {
		if len(obj[k]) == 0 {
			continue
		}
		var n json.Number
		d := json.NewDecoder(strings.NewReader(string(obj[k])))
		d.UseNumber()
		if d.Decode(&n) == nil {
			if i, e := n.Int64(); e == nil {
				return i
			}
		}
		var s string
		if json.Unmarshal(obj[k], &s) == nil {
			if i, e := strconv.ParseInt(s, 10, 64); e == nil {
				return i
			}
		}
	}
	return 0
}
func (b *Bridge) BeginQRLogin(ctx context.Context) (QRLogin, error) {
	raw, err := b.requestMusicBox(ctx, http.MethodPost, "/api/v1/auth/login", nil)
	if err != nil {
		return QRLogin{}, err
	}
	obj := unwrapObject(raw)
	key := stringField(obj, "unikey", "codekey")
	if key == "" {
		obj = unwrapObject(obj["data"])
		key = stringField(obj, "unikey", "codekey")
	}
	if key == "" || len(key) > 256 {
		return QRLogin{}, errors.New("MusicBox did not supply a QR challenge key")
	}
	// Construct locally, do not trust an arbitrary URL supplied by the bridge.
	return QRLogin{Key: key, QRURL: "https://music.163.com/login?codekey=" + url.QueryEscape(key), QRASCII: stringField(obj, "qr_ascii")}, nil
}
func stateFromCode(code int64) LoginState {
	switch code {
	case 800:
		return LoginState{State: "expired"}
	case 801:
		return LoginState{State: "pending"}
	case 802:
		return LoginState{State: "scanned"}
	case 803:
		return LoginState{State: "success", LoggedIn: true}
	default:
		return LoginState{State: "unknown"}
	}
}
func (b *Bridge) CheckQRLogin(ctx context.Context, key string) (LoginState, error) {
	if key == "" || len(key) > 256 {
		return LoginState{}, errors.New("invalid QR challenge")
	}
	raw, err := b.requestMusicBox(ctx, http.MethodGet, "/api/v1/auth/login/check", url.Values{"unikey": {key}})
	if err != nil {
		return LoginState{}, err
	}
	obj := unwrapObject(raw)
	code := intField(obj, "code")
	if code == 0 {
		code = intField(unwrapObject(obj["data"]), "code")
	}
	return stateFromCode(code), nil
}
func (b *Bridge) AuthStatus(ctx context.Context) (LoginState, error) {
	raw, err := b.requestMusicBox(ctx, http.MethodGet, "/api/v1/auth/status", nil)
	if err != nil {
		return LoginState{}, err
	}
	obj := unwrapObject(raw)
	for _, k := range []string{"logged_in", "is_logged_in", "login"} {
		var v bool
		if json.Unmarshal(obj[k], &v) == nil {
			if v {
				return LoginState{State: "success", LoggedIn: true}, nil
			}
			return LoginState{State: "pending"}, nil
		}
	}
	if nested := unwrapObject(obj["profile"]); intField(nested, "userId", "user_id") > 0 {
		return LoginState{State: "success", LoggedIn: true}, nil
	}
	if intField(obj, "userId", "uid") > 0 {
		return LoginState{State: "success", LoggedIn: true}, nil
	}
	// Some versions return a CLI-specific object; do not guess login success.
	return LoginState{State: "unknown"}, nil
}

// ListPlaylists returns only self-created playlists, never other users' saved
// lists. Each adapter belongs to a separate MusicBox instance and cookie jar.
func (b *Bridge) ListPlaylists(ctx context.Context, user provider.User) ([]provider.Playlist, error) {
	if err := user.Validate(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.url("/api/v1/user/playlists", url.Values{"limit": {"100"}}), nil)
	if err != nil {
		return nil, err
	}
	res, err := b.client.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("%w: playlist HTTP %d", ErrUnavailable, res.StatusCode)
	}
	var payload struct {
		OK         bool                         `json:"ok"`
		LoggedIn   bool                         `json:"logged_in"`
		AccountUID json.Number                  `json:"account_uid"`
		Data       []map[string]json.RawMessage `json:"data"`
	}
	dec := json.NewDecoder(io.LimitReader(res.Body, 1<<20))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, errors.New("invalid playlist data")
	}
	if !payload.OK || !payload.LoggedIn {
		return nil, provider.ErrUnauthenticated
	}
	uid, _ := payload.AccountUID.Int64()
	if uid <= 0 {
		return nil, errors.New("playlist response missing account UID")
	}
	lists := make([]provider.Playlist, 0, len(payload.Data))
	for _, row := range payload.Data {
		id := intField(row, "playlist_id", "id")
		if id <= 0 || intField(row, "user_id", "userId") != uid {
			continue
		}
		title := stringField(row, "name", "title")
		if title == "" {
			continue
		}
		lists = append(lists, provider.Playlist{ID: strconv.FormatInt(id, 10), Title: title, CoverURL: stringField(row, "cover_url", "coverImgUrl"), ReadOnly: true, TrackCount: int(intField(row, "track_count", "trackCount"))})
	}
	return lists, nil
}

// PlaylistTracks fetches the read-only tracks for a self-created playlist.
// Callers must authorize the playlist against ListPlaylists first.
func (b *Bridge) PlaylistTracks(ctx context.Context, user provider.User, playlistID string) ([]provider.Track, error) {
	if err := user.Validate(); err != nil {
		return nil, err
	}
	if !validID(playlistID) {
		return nil, errors.New("invalid playlist id")
	}
	raw, err := b.get(ctx, "/api/v1/user/playlists/"+playlistID+"/tracks", nil)
	if err != nil {
		return nil, err
	}
	var rows []json.RawMessage
	if json.Unmarshal(raw, &rows) != nil {
		return nil, errors.New("invalid playlist track response")
	}
	if len(rows) > 1000 {
		rows = rows[:1000]
	}
	result := make([]provider.Track, 0, len(rows))
	for _, row := range rows {
		track, e := parseTrack(row)
		if e == nil && track.Playable {
			result = append(result, track)
		}
	}
	return result, nil
}
