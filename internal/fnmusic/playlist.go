package fnmusic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

// Playlist IDs are scoped to the chosen NetEase account (including its source
// address). Switching accounts cannot decode previous users' playlist IDs.
func EncodePlaylistID(scope, id string) (string, bool) {
	n, e := strconv.ParseUint(id, 10, 64)
	if e != nil || n == 0 || scope == "" {
		return "", false
	}
	canonical := strconv.FormatUint(n, 10)
	sum := sha256.Sum256([]byte("fnmusic-go:v4:playlist:" + scope + ":" + canonical))
	return hex.EncodeToString(sum[:8]) + fmt.Sprintf("%016x", n), true
}
func DecodePlaylistID(scope, guid string) (string, bool) {
	if len(guid) != 32 || scope == "" {
		return "", false
	}
	n, e := strconv.ParseUint(guid[16:], 16, 64)
	if e != nil || n == 0 {
		return "", false
	}
	id := strconv.FormatUint(n, 10)
	want, ok := EncodePlaylistID(scope, id)
	return id, ok && strings.EqualFold(want, guid)
}
func PlaylistVO(scope string, p provider.Playlist) (map[string]any, bool) {
	guid, ok := EncodePlaylistID(scope, p.ID)
	if !ok {
		return nil, false
	}
	description := "网易云自建歌单（只读）"
	if p.Kind == "public" {
		description = "网易云公共歌单（只读）"
	}
	if p.Kind == "recommend" {
		description = "推荐歌单（只读）"
	}
	return map[string]any{"guid": guid, "id": guid, "name": p.Title, "title": p.Title,
		"createdAt": int64(0), "updatedAt": int64(0),
		"coverId": "track_" + guid, "coverUrl": "/music/api/v1/static/cover?coverId=track_" + guid, "trackCount": p.TrackCount, "type": 0,
		"isDaily": true, "readOnly": true, "isFavorite": false, "description": description}, true
}
