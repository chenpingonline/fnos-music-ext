// Package netease implements a transitional adapter to the local MusicBox HTTP
// service used by upstream fnmusic-ext. It does NOT bundle MusicBox or bypass
// upstream account or music rights. Multi-user private accounts are not exposed.
package netease

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

var ErrUnavailable = errors.New("netease musicbox bridge unavailable")
var ErrUnsupported = errors.New("operation needs per-user authorized provider and is not enabled")

type Bridge struct {
	base   *url.URL
	client *http.Client
	media  *http.Client
}

// NewBridge restricts the untrusted provider process to a local loopback HTTP
// address. In production this must not be bound to a public interface.
func NewBridge(address string) (*Bridge, error) {
	u, e := url.Parse(strings.TrimSpace(address))
	if e != nil {
		return nil, e
	}
	if u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("MusicBox bridge must be an HTTP loopback URL without path, query, or credentials")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if !(strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())) {
		return nil, errors.New("MusicBox bridge must use a loopback address")
	}
	if u.Port() == "" {
		return nil, errors.New("MusicBox bridge port required")
	}
	return &Bridge{base: u, client: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, media: newMediaClient()}, nil
}
func (b *Bridge) ID() string { return "netease" }
func (b *Bridge) url(path string, q url.Values) string {
	u := *b.base
	u.Path = path
	u.RawQuery = q.Encode()
	return u.String()
}
func (b *Bridge) get(ctx context.Context, path string, q url.Values) (json.RawMessage, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, b.url(path, q), nil)
	if e != nil {
		return nil, e
	}
	resp, e := b.client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	var envelope struct {
		OK   *bool           `json:"ok"`
		Data json.RawMessage `json:"data"`
		Code int             `json:"code"`
	}
	if e := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&envelope); e != nil {
		return nil, fmt.Errorf("MusicBox JSON: %w", e)
	}
	if envelope.OK != nil && !*envelope.OK {
		return nil, ErrUnavailable
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil, ErrUnavailable
	}
	return envelope.Data, nil
}
func (b *Bridge) Health(ctx context.Context) error {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, b.url("/healthz", nil), nil)
	if e != nil {
		return e
	}
	resp, e := b.client.Do(req)
	if e != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ErrUnavailable
	}
	return nil
}
func (b *Bridge) Search(ctx context.Context, user provider.User, query provider.SearchRequest) (provider.SearchPage, error) {
	if e := user.Validate(); e != nil {
		return provider.SearchPage{}, e
	}
	if query.Limit < 1 || query.Limit > 100 || query.Offset < 0 || len([]rune(query.Keyword)) > 120 {
		return provider.SearchPage{}, errors.New("invalid search window")
	}
	q := url.Values{"keyword": {query.Keyword}, "type": {"song"}, "limit": {strconv.Itoa(query.Limit)}, "offset": {strconv.Itoa(query.Offset)}}
	raw, e := b.get(ctx, "/api/v1/search", q)
	if e != nil {
		return provider.SearchPage{}, e
	}
	var items []json.RawMessage
	if e := json.Unmarshal(raw, &items); e != nil {
		return provider.SearchPage{}, fmt.Errorf("invalid MusicBox search items: %w", e)
	}
	page := provider.SearchPage{Tracks: make([]provider.Track, 0, len(items))}
	for _, row := range items {
		t, e := parseTrack(row)
		if e == nil && t.Playable {
			page.Tracks = append(page.Tracks, t)
		}
	}
	// MusicBox only returns an array, not the real global total. A full page is
	// provisional evidence of a next page; never manufacture an exact count.
	page.HasMore = len(items) >= query.Limit
	page.Total = query.Offset + len(page.Tracks)
	if page.HasMore {
		page.Total++
	}
	return page, nil
}
func (b *Bridge) GetTrack(ctx context.Context, user provider.User, id string) (provider.Track, error) {
	if e := user.Validate(); e != nil {
		return provider.Track{}, e
	}
	if !validID(id) {
		return provider.Track{}, errors.New("invalid song ID")
	}
	raw, e := b.get(ctx, "/api/v1/song/"+id+"/info", nil)
	if e != nil {
		return provider.Track{}, e
	}
	// NEMbox info may return a direct object, an array, or an item nested under song.
	var items []json.RawMessage
	if len(raw) > 0 && raw[0] == '[' {
		_ = json.Unmarshal(raw, &items)
	} else {
		items = []json.RawMessage{raw}
	}
	for _, item := range items {
		t, e := parseTrack(item)
		if e == nil && t.ID == id {
			return t, nil
		}
	}
	return provider.Track{}, fmt.Errorf("MusicBox track %s metadata unavailable", id)
}
func (b *Bridge) GetLyrics(ctx context.Context, user provider.User, id string) (provider.Lyrics, error) {
	if e := user.Validate(); e != nil {
		return provider.Lyrics{}, e
	}
	if !validID(id) {
		return provider.Lyrics{}, errors.New("invalid song ID")
	}
	raw, e := b.get(ctx, "/api/v1/song/"+id+"/lyric", nil)
	if e != nil {
		return provider.Lyrics{}, e
	}
	var data struct {
		Lyric         string `json:"lyric"`
		Translated    string `json:"tlyric"`
		TranslatedLRC string `json:"translated_lyric"`
	}
	if e := json.Unmarshal(raw, &data); e != nil {
		return provider.Lyrics{}, e
	}
	if data.Translated == "" {
		data.Translated = data.TranslatedLRC
	}
	return provider.Lyrics{LRC: data.Lyric, TranslatedLRC: data.Translated}, nil
}
func (b *Bridge) Play(ctx context.Context, user provider.User, req provider.PlaybackRequest) (provider.Stream, error) {
	if e := user.Validate(); e != nil {
		return provider.Stream{}, e
	}
	if !validID(req.TrackID) {
		return provider.Stream{}, errors.New("invalid song ID")
	}
	quality := req.Quality
	if quality == "" {
		quality = "exhigh"
	}
	switch quality {
	case "standard", "higher", "exhigh", "lossless", "hires", "jymaster":
	default:
		return provider.Stream{}, errors.New("unsupported quality")
	}
	q := url.Values{"quality": {quality}}
	raw, e := b.get(ctx, "/api/v1/song/"+req.TrackID+"/url", q)
	if e != nil {
		return provider.Stream{}, e
	}
	var data struct {
		URL  string `json:"url"`
		Code int    `json:"code"`
		Type string `json:"type"`
	}
	if e := json.Unmarshal(raw, &data); e != nil {
		return provider.Stream{}, e
	}
	if data.Code == 404 || data.URL == "" {
		return provider.Stream{}, provider.ErrNotEntitled
	}
	uri, e := url.Parse(data.URL)
	if e != nil {
		return provider.Stream{}, e
	}
	if !safeMediaURL(uri) {
		return provider.Stream{}, errors.New("upstream media URL is not a public HTTP(S) endpoint")
	}
	method := http.MethodGet
	if req.Range == "__HEAD__" {
		method = http.MethodHead
	}
	r, e := http.NewRequestWithContext(ctx, method, uri.String(), nil)
	if e != nil {
		return provider.Stream{}, e
	}
	if req.Range != "" && req.Range != "__HEAD__" {
		if !validRange(req.Range) {
			return provider.Stream{}, errors.New("invalid Range")
		}
		r.Header.Set("Range", req.Range)
	}
	resp, e := b.media.Do(r)
	if e != nil {
		return provider.Stream{}, e
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		resp.Body.Close()
		return provider.Stream{}, fmt.Errorf("media HTTP %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "audio/mpeg"
	}
	return provider.Stream{Body: resp.Body, StatusCode: resp.StatusCode, ContentType: ct, ContentRange: resp.Header.Get("Content-Range"), ContentLength: resp.ContentLength}, nil
}
func validID(id string) bool {
	if id == "" || len(id) > 20 {
		return false
	}
	n, e := strconv.ParseUint(id, 10, 64)
	return e == nil && n > 0 && strconv.FormatUint(n, 10) == id
}
func validRange(r string) bool {
	if !strings.HasPrefix(r, "bytes=") || len(r) > 72 {
		return false
	}
	rs := strings.Split(strings.TrimPrefix(r, "bytes="), "-")
	if len(rs) != 2 {
		return false
	}
	if rs[0] == "" && rs[1] == "" {
		return false
	}
	for _, s := range rs {
		for _, c := range s {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

func parseTrack(raw json.RawMessage) (provider.Track, error) {
	var obj map[string]json.RawMessage
	if e := json.Unmarshal(raw, &obj); e != nil {
		return provider.Track{}, e
	}
	str := func(keys ...string) string {
		for _, key := range keys {
			v := obj[key]
			if len(v) == 0 {
				continue
			}
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				return s
			}
			var n json.Number
			dec := json.NewDecoder(strings.NewReader(string(v)))
			dec.UseNumber()
			if dec.Decode(&n) == nil {
				return n.String()
			}
		}
		return ""
	}
	id := str("song_id", "id")
	if !validID(id) {
		return provider.Track{}, errors.New("missing song id")
	}
	title := str("title", "name", "song_name")
	if title == "" {
		return provider.Track{}, errors.New("missing title")
	}
	artist := str("artist", "singer", "artists")
	if artist == "" {
		var rows []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(obj["ar"], &rows) == nil || json.Unmarshal(obj["artists"], &rows) == nil {
			names := make([]string, 0, len(rows))
			for _, it := range rows {
				if it.Name != "" {
					names = append(names, it.Name)
				}
			}
			artist = strings.Join(names, " / ")
		}
	}
	album := str("album", "album_name")
	if album == "" {
		var al struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(obj["al"], &al) == nil || json.Unmarshal(obj["album"], &al) == nil {
			album = al.Name
		}
	}
	cover := str("cover_url", "cover", "pic_url", "picUrl")
	if cover == "" {
		var al struct {
			PicURL string `json:"picUrl"`
		}
		if json.Unmarshal(obj["al"], &al) == nil || json.Unmarshal(obj["album"], &al) == nil {
			cover = al.PicURL
		}
	}
	var duration int64
	for _, key := range []string{"duration_ms", "duration", "dt"} {
		var n json.Number
		d := json.NewDecoder(strings.NewReader(string(obj[key])))
		d.UseNumber()
		if d.Decode(&n) == nil {
			v, e := n.Int64()
			if e == nil && v > 0 {
				duration = v
				break
			}
		}
	}
	if duration == 0 {
		var sec float64
		if json.Unmarshal(obj["duration_s"], &sec) == nil && sec > 0 {
			duration = int64(sec * 1000)
		}
	}
	if duration > 0 && duration < 1000 {
		duration *= 1000
	} // older musicbox CLI returned seconds
	return provider.Track{ID: id, Source: "netease", Title: title, Artists: []string{artist}, Album: album, DurationMS: duration, CoverURL: cover, Playable: true}, nil
}

// OpenCover streams a bounded same-origin image. External redirects and
// non-public destination IPs are blocked by the media transport.
func (b *Bridge) OpenCover(ctx context.Context, user provider.User, id string) (provider.Stream, error) {
	track, e := b.GetTrack(ctx, user, id)
	if e != nil {
		return provider.Stream{}, e
	}
	u, e := url.Parse(track.CoverURL)
	if e != nil || !safeMediaURL(u) {
		return provider.Stream{}, errors.New("untrusted cover URL")
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return provider.Stream{}, e
	}
	resp, e := b.media.Do(req)
	if e != nil {
		return provider.Stream{}, e
	}
	if resp.StatusCode != 200 || resp.ContentLength > 6<<20 {
		resp.Body.Close()
		return provider.Stream{}, errors.New("cover unavailable or exceeds limit")
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(ct), "image/") {
		resp.Body.Close()
		return provider.Stream{}, errors.New("unexpected cover content type")
	}
	// Bounded memory read before answering to avoid partial oversized images.
	body, e := io.ReadAll(io.LimitReader(resp.Body, (6<<20)+1))
	resp.Body.Close()
	if e != nil || len(body) > 6<<20 {
		return provider.Stream{}, errors.New("cover exceeds size limit")
	}
	return provider.Stream{Body: io.NopCloser(bytes.NewReader(body)), StatusCode: 200, ContentType: ct, ContentLength: int64(len(body))}, nil
}
