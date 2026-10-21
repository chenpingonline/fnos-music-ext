package qqmusic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chenpingonline/fnos-music-ext/internal/mediahttp"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func text(obj map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		var s string
		if json.Unmarshal(obj[key], &s) == nil && s != "" {
			return s
		}
		var num json.Number
		if json.Unmarshal(obj[key], &num) == nil && num != "" {
			return string(num)
		}
	}
	return ""
}
func object(raw json.RawMessage) map[string]json.RawMessage {
	var v map[string]json.RawMessage
	_ = json.Unmarshal(raw, &v)
	return v
}
func integer(obj map[string]json.RawMessage, keys ...string) int {
	v, _ := strconv.Atoi(text(obj, keys...))
	return v
}
func parseTrack(raw json.RawMessage) (provider.Track, error) {
	row := object(raw)
	id := text(row, "mid", "songmid")
	title := text(row, "title", "name", "songname")
	if !midPattern.MatchString(id) || title == "" {
		return provider.Track{}, errors.New("invalid QQ Music track")
	}
	t := provider.Track{ID: id, Source: "qqmusic", Title: title, Artists: []string{}, DurationMS: int64(integer(row, "interval")) * 1000, Playable: true}
	var singers []map[string]json.RawMessage
	_ = json.Unmarshal(row["singer"], &singers)
	for _, s := range singers {
		if name := text(s, "name"); name != "" {
			t.Artists = append(t.Artists, name)
		}
	}
	album := object(row["album"])
	t.Album = text(album, "title", "name")
	if mid := text(album, "mid"); midPattern.MatchString(mid) {
		t.CoverURL = "https://y.gtimg.cn/music/photo_new/T002R500x500M000" + mid + ".jpg"
	}
	file := object(row["file"])
	t.MediaID = text(file, "media_mid")
	if t.MediaID != "" && !midPattern.MatchString(t.MediaID) {
		t.MediaID = ""
	}
	return t, nil
}
func (n *Native) Search(ctx context.Context, u provider.User, q provider.SearchRequest) (provider.SearchPage, error) {
	if err := u.Validate(); err != nil {
		return provider.SearchPage{}, err
	}
	if strings.TrimSpace(q.Keyword) == "" || len([]rune(q.Keyword)) > 120 || q.Offset < 0 || q.Offset > 20000 || q.Limit < 1 || q.Limit > 100 {
		return provider.SearchPage{}, errors.New("invalid QQ Music search window")
	}
	// QQ desktop search rejects oversized pages with an empty success response.
	// Fetch 20-item pages, then slice exact caller offsets.
	const pageSize = 20
	out := provider.SearchPage{Tracks: []provider.Track{}}
	offset := q.Offset
	for len(out.Tracks) < q.Limit {
		raw, err := n.cgi(ctx, "music.search.SearchCgiService", "DoSearchForQQMusicDesktop", map[string]any{"query": q.Keyword, "search_type": 0, "num_per_page": pageSize, "page_num": offset/pageSize + 1})
		if err != nil {
			return provider.SearchPage{}, err
		}
		var data struct {
			Meta struct {
				Sum      int `json:"sum"`
				Estimate int `json:"estimate_sum"`
				Next     int `json:"nextpage"`
			} `json:"meta"`
			Body struct {
				Song struct {
					List []json.RawMessage `json:"list"`
				} `json:"song"`
			} `json:"body"`
		}
		if json.Unmarshal(raw, &data) != nil || data.Body.Song.List == nil {
			return provider.SearchPage{}, errors.New("QQ 音乐搜索响应缺少歌曲列表")
		}
		out.Total = data.Meta.Sum
		if out.Total == 0 {
			out.Total = data.Meta.Estimate
		}
		rows := data.Body.Song.List
		start := offset % pageSize
		if start >= len(rows) {
			break
		}
		consumed := start
		for consumed < len(rows) && len(out.Tracks) < q.Limit {
			t, err := parseTrack(rows[consumed])
			consumed++
			if err == nil {
				out.Tracks = append(out.Tracks, t)
			}
		}
		offset = offset/pageSize*pageSize + consumed
		if consumed < len(rows) || len(rows) < pageSize || offset >= out.Total {
			break
		}
	}
	if out.Total < q.Offset+len(out.Tracks) {
		out.Total = q.Offset + len(out.Tracks)
	}
	out.HasMore = offset < out.Total && len(out.Tracks) > 0
	return out, nil
}
func (n *Native) GetTrack(ctx context.Context, u provider.User, id string) (provider.Track, error) {
	if err := u.Validate(); err != nil {
		return provider.Track{}, err
	}
	if !midPattern.MatchString(id) {
		return provider.Track{}, errors.New("invalid QQ song MID")
	}
	raw, err := n.cgi(ctx, "music.pf_song_detail_svr", "get_song_detail_yqq", map[string]any{"song_mid": id})
	if err != nil {
		return provider.Track{}, err
	}
	row := object(raw)
	track, err := parseTrack(row["track_info"])
	if err == nil && track.ID != id {
		return provider.Track{}, errors.New("QQ 音乐返回了其他歌曲")
	}
	return track, err
}
func (n *Native) Play(ctx context.Context, u provider.User, p provider.PlaybackRequest) (provider.Stream, error) {
	if err := u.Validate(); err != nil {
		return provider.Stream{}, err
	}
	if !midPattern.MatchString(p.TrackID) || !validRange(p.Range) {
		return provider.Stream{}, errors.New("invalid QQ playback request")
	}
	if !n.snapshot().valid() {
		return provider.Stream{}, provider.ErrUnauthenticated
	}
	prefix, ext, ct := "M500", ".mp3", "audio/mpeg"
	switch p.Quality {
	case "", "exhigh":
		prefix = "M800"
	case "lossless":
		prefix, ext, ct = "F000", ".flac", "audio/flac"
	case "standard", "higher":
	case "hires", "jymaster":
		return provider.Stream{}, provider.ErrNotEntitled
	default:
		return provider.Stream{}, errors.New("unsupported QQ quality")
	}
	track, err := n.GetTrack(ctx, u, p.TrackID)
	if err != nil {
		return provider.Stream{}, err
	}
	file := prefix + p.TrackID + p.TrackID + ext
	if track.MediaID != "" {
		file = prefix + track.MediaID + ext
	}
	raw, err := n.cgi(ctx, "music.vkey.GetVkey", "UrlGetVkey", map[string]any{"uin": n.snapshot().MusicID, "filename": []string{file}, "songmid": []string{p.TrackID}, "songtype": []int{0}, "guid": n.guid, "ctx": 0})
	if err != nil {
		return provider.Stream{}, err
	}
	var data struct {
		SIP  []string `json:"sip"`
		Rows []struct {
			MID    string `json:"songmid"`
			File   string `json:"filename"`
			URL    string `json:"purl"`
			Result *int   `json:"result"`
		} `json:"midurlinfo"`
	}
	if json.Unmarshal(raw, &data) != nil || len(data.Rows) != 1 {
		return provider.Stream{}, provider.ErrNotEntitled
	}
	row := data.Rows[0]
	if row.MID != p.TrackID || row.Result == nil || *row.Result != 0 || row.URL == "" || row.File != file {
		return provider.Stream{}, provider.ErrNotEntitled
	}
	// Never ask for encrypted or trial variants. A signed URL grants access only
	// to the exact ordinary file requested for this user's music credential.
	var target *url.URL
	for _, domain := range append(data.SIP, "https://isure.stream.qqmusic.qq.com/") {
		base, e := url.Parse(domain)
		rel, e2 := url.Parse(row.URL)
		if e != nil || e2 != nil || !mediahttp.SafeURL(base) || rel.IsAbs() || rel.Host != "" {
			continue
		}
		target = base.ResolveReference(rel)
		if mediahttp.SafeURL(target) {
			break
		}
		target = nil
	}
	if target == nil {
		return provider.Stream{}, errors.New("invalid QQ media URL")
	}
	method := "GET"
	if p.Range == "__HEAD__" {
		method = "HEAD"
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), nil)
	if err != nil {
		return provider.Stream{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	if p.Range != "" && p.Range != "__HEAD__" {
		req.Header.Set("Range", p.Range)
		if p.IfRange != "" {
			req.Header.Set("If-Range", p.IfRange)
		}
	}
	res, err := n.media.Do(req)
	if err != nil {
		return provider.Stream{}, errors.New("QQ 音频 CDN 连接失败")
	}
	if res.StatusCode != 200 && res.StatusCode != 206 && res.StatusCode != 416 {
		res.Body.Close()
		return provider.Stream{}, httpFailure("media-cdn", res.StatusCode)
	}
	if res.Header.Get("Content-Type") != "" {
		ct = res.Header.Get("Content-Type")
	}
	return provider.Stream{Body: res.Body, StatusCode: res.StatusCode, ContentType: ct, ContentLength: res.ContentLength, ContentRange: res.Header.Get("Content-Range"), ETag: res.Header.Get("ETag"), LastModified: res.Header.Get("Last-Modified")}, nil
}
func (n *Native) GetLyrics(ctx context.Context, u provider.User, id string) (provider.Lyrics, error) {
	if err := u.Validate(); err != nil {
		return provider.Lyrics{}, err
	}
	if !midPattern.MatchString(id) {
		return provider.Lyrics{}, errors.New("invalid QQ MID")
	}
	raw, err := n.cgi(ctx, "music.musichallSong.PlayLyricInfo", "GetPlayLyricInfo", map[string]any{"songMid": id, "crypt": 0, "qrc": 0, "qrc_t": 0, "lrc_t": 0, "trans": 1, "trans_t": 0, "roma": 0, "roma_t": 0, "type": 1})
	if err != nil {
		return provider.Lyrics{}, err
	}
	obj := object(raw)
	decode := func(s string) string {
		if raw, e := base64.StdEncoding.DecodeString(s); e == nil {
			return string(raw)
		}
		return s
	}
	return provider.Lyrics{LRC: decode(text(obj, "lyric")), TranslatedLRC: decode(text(obj, "trans"))}, nil
}
func (n *Native) ListPlaylists(ctx context.Context, u provider.User) ([]provider.Playlist, error) {
	if err := u.Validate(); err != nil {
		return nil, err
	}
	c := n.snapshot()
	if !c.valid() {
		return nil, provider.ErrUnauthenticated
	}
	state, err := n.AuthStatus(ctx)
	if err != nil {
		return nil, err
	}
	if !state.LoggedIn {
		return nil, provider.ErrUnauthenticated
	}
	raw, err := n.cgi(ctx, "music.musicasset.PlaylistBaseRead", "GetPlaylistByUin", map[string]any{"uin": c.MusicID})
	if err != nil {
		return nil, err
	}
	var data struct {
		Rows []map[string]json.RawMessage `json:"v_playlist"`
	}
	if json.Unmarshal(raw, &data) != nil || data.Rows == nil {
		return nil, errors.New("QQ 音乐歌单响应异常")
	}
	out := []provider.Playlist{}
	for _, row := range data.Rows {
		id := text(row, "tid", "id", "dissid")
		if !numberPattern.MatchString(id) || text(row, "uin") != c.MusicID {
			continue
		}
		out = append(out, provider.Playlist{ID: id, Title: text(row, "title", "dirName", "dissname", "name"), CoverURL: text(row, "picurl", "picUrl", "cover"), TrackCount: integer(row, "songnum", "songNum", "song_cnt"), ReadOnly: true})
	}
	return out, nil
}
func (n *Native) PlaylistTracks(ctx context.Context, u provider.User, id string) ([]provider.Track, error) {
	lists, err := n.ListPlaylists(ctx, u)
	if err != nil {
		return nil, err
	}
	owned := false
	for _, p := range lists {
		if p.ID == id {
			owned = true
		}
	}
	if !owned {
		return nil, provider.ErrUnauthenticated
	}
	out := []provider.Track{}
	for offset := 0; offset < 1000; offset += 100 {
		raw, err := n.cgi(ctx, "music.srfDissInfo.DissInfo", "CgiGetDiss", map[string]any{"disstid": json.Number(id), "dirid": 0, "song_begin": offset, "song_num": 100, "onlysonglist": 1, "userinfo": 1, "tag": 0, "orderlist": 1})
		if err != nil {
			return nil, err
		}
		var data struct {
			Code int               `json:"code"`
			Rows []json.RawMessage `json:"songlist"`
			More int               `json:"hasmore"`
		}
		if json.Unmarshal(raw, &data) != nil || data.Code != 0 || data.Rows == nil {
			return nil, errors.New("QQ 歌单曲目响应异常")
		}
		for _, row := range data.Rows {
			t, e := parseTrack(row)
			if e == nil {
				out = append(out, t)
			}
		}
		if data.More == 0 || len(data.Rows) < 100 {
			break
		}
	}
	return out, nil
}
func (n *Native) OpenTrackCoverSized(ctx context.Context, u provider.User, t provider.Track, size int) (provider.Stream, error) {
	if err := u.Validate(); err != nil {
		return provider.Stream{}, err
	}
	uri, err := url.Parse(t.CoverURL)
	if err != nil || !mediahttp.SafeURL(uri) {
		return provider.Stream{}, errors.New("invalid QQ cover URL")
	}
	if size < 0 || size > 2048 {
		return provider.Stream{}, errors.New("invalid cover size")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", uri.String(), nil)
	req.Header.Set("User-Agent", userAgent)
	res, err := n.media.Do(req)
	if err != nil {
		return provider.Stream{}, errors.New("QQ 封面连接失败")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return provider.Stream{}, httpFailure("media-cdn", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (6<<20)+1))
	if err != nil || len(raw) > 6<<20 || !strings.HasPrefix(http.DetectContentType(raw), "image/") {
		return provider.Stream{}, errors.New("invalid QQ cover image")
	}
	return provider.Stream{Body: io.NopCloser(strings.NewReader(string(raw))), StatusCode: 200, ContentLength: int64(len(raw)), ContentType: http.DetectContentType(raw)}, nil
}
func (n *Native) OpenTrackCover(ctx context.Context, u provider.User, t provider.Track) (provider.Stream, error) {
	return n.OpenTrackCoverSized(ctx, u, t, 0)
}
func (n *Native) OpenCover(ctx context.Context, u provider.User, id string) (provider.Stream, error) {
	t, e := n.GetTrack(ctx, u, id)
	if e != nil {
		return provider.Stream{}, e
	}
	return n.OpenTrackCover(ctx, u, t)
}
func (n *Native) OpenPlaylistCoverSized(ctx context.Context, u provider.User, p provider.Playlist, size int) (provider.Stream, error) {
	return n.OpenTrackCoverSized(ctx, u, provider.Track{CoverURL: p.CoverURL}, size)
}
func (n *Native) OpenPlaylistCover(ctx context.Context, u provider.User, p provider.Playlist) (provider.Stream, error) {
	return n.OpenPlaylistCoverSized(ctx, u, p, 0)
}

var _ provider.Adapter = (*Native)(nil)
var _ provider.LoginAdapter = (*Native)(nil)
var _ = fmt.Sprint
