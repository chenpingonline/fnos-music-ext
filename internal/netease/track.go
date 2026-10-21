package netease

import (
	"encoding/json"
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"strconv"
	"strings"
)

var ErrUnavailable = errors.New("netease service unavailable")

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
	} // Normalize duration values expressed in seconds.
	return provider.Track{ID: id, Source: "netease", Title: title, Artists: []string{artist}, Album: album, DurationMS: duration, CoverURL: cover, Playable: true}, nil
}

// OpenCover streams a bounded same-origin image. External redirects and
// non-public destination IPs are blocked by the media transport.
