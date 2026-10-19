// Package fnmusic adapts the public-facing shape observed in fnOS music clients.
// The protocol is undocumented and must be tested against each official update.
package fnmusic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

// EncodeNetEaseID is a reversible 32-hex virtual ID, not an official library ID.
// A domain-separated checksum prevents mistaking arbitrary official IDs for online songs.
func EncodeNetEaseID(id string) (string, bool) {
	n, e := strconv.ParseUint(id, 10, 64)
	if e != nil || n == 0 {
		return "", false
	}
	canon := strconv.FormatUint(n, 10)
	hash := sha256.Sum256([]byte("fnmusic-go:v2:netease:" + canon))
	return hex.EncodeToString(hash[:8]) + fmt.Sprintf("%016x", n), true
}
func DecodeNetEaseID(guid string) (string, bool) {
	guid = strings.TrimPrefix(guid, "track_")
	if len(guid) != 32 {
		return "", false
	}
	raw, e := hex.DecodeString(guid)
	if e != nil || len(raw) != 16 {
		return "", false
	}
	n, e := strconv.ParseUint(guid[16:], 16, 64)
	if e != nil || n == 0 {
		return "", false
	}
	id := strconv.FormatUint(n, 10)
	check, ok := EncodeNetEaseID(id)
	return id, ok && guid == check
}

// TrackVO builds a field-compatible virtual track. It never writes trim.music DB.
func TrackVO(t provider.Track, markSource bool) (map[string]any, bool) {
	guid, ok := EncodeNetEaseID(t.ID)
	if !ok {
		return nil, false
	}
	title := t.Title
	if markSource {
		title = "[music box] " + title
	}
	artists := make([]map[string]any, 0, len(t.Artists))
	for i, artist := range t.Artists {
		if artist != "" {
			artists = append(artists, map[string]any{"name": artist, "guid": fmt.Sprintf("%s:artist:%d", guid, i),
				"coverId": nil, "createdAt": int64(0), "updatedAt": int64(0)})
		}
	}
	album := map[string]any{"name": t.Album, "guid": guid + ":album", "artists": artists, "coverId": guid,
		"createdAt": int64(0), "updatedAt": int64(0), "releaseDate": nil, "barcode": nil}
	format, codec := "", ""
	var size int64
	if t.Audio != nil {
		format, codec, size = t.Audio.Format, t.Audio.Codec, t.Audio.Size
	}
	path := "online/netease/" + guid
	if format != "" {
		path += "." + format
	}
	spec := map[string]any{"path": path, "format": format, "codec": codec, "container": format, "duration": t.DurationMS, "size": size, "channel": 0, "sampleRate": 0, "bitrate": 0}
	artistName := strings.Join(t.Artists, " ")
	// The official playlist client calls new Date(value * 1000).toISOString()
	// without a fallback. Use Unix epoch for unknown source timestamps; omitted
	// fields become NaN and abort conversion of the entire track list.
	return map[string]any{
		"createdAt": int64(0), "updatedAt": int64(0),
		"year": nil, "discNo": nil, "trackNo": nil, "isrc": "",
		"guid": guid, "id": guid, "title": title, "name": title, "artist": artistName, "artists": artists, "album": album, "albumName": t.Album,
		"audioSpec": spec, "duration": t.DurationMS, "duration_ms": t.DurationMS, "durationMs": t.DurationMS, "duration_s": float64(t.DurationMS) / 1000,
		"codec": codec, "codecName": codec, "format": format, "ext": format, "size": size, "file_size": size,
		"coverId": guid, "cover_url": t.CoverURL, "coverUrl": t.CoverURL, "coverURL": t.CoverURL, "source": "netease", "is_online": true,
		"isFavorite": false, "isCue": false, "hasLyric": true, "genres": []any{}, "accessStatus": t.AccessStatus,
	}, true
}
func Metadata(t provider.Track) (map[string]any, bool) {
	vo, ok := TrackVO(t, false)
	if !ok {
		return nil, false
	}
	track := make(map[string]any, len(vo))
	for k, v := range vo {
		track[k] = v
	}
	data := make(map[string]any, len(vo)+1)
	for k, v := range vo {
		data[k] = v
	}
	data["track"] = track
	return map[string]any{"code": 0, "msg": "ok", "data": data}, true
}
func Lyrics(guid string, l provider.Lyrics, list bool) map[string]any {
	if list {
		entries := []any{}
		preferred := ""
		if strings.TrimSpace(l.LRC) != "" {
			preferred = guid + ":lyric"
			entries = append(entries, map[string]any{"guid": preferred, "content": l.LRC, "source": 2, "isLRC": true, "offset": 0})
		}
		return map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"list": entries, "preferred": preferred}}
	}
	return map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"guid": guid, "lyric": l.LRC}}
}
