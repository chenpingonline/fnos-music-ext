package fnmusic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

// Playlist IDs are scoped to the chosen MusicBox account (including its bridge
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
	return map[string]any{"guid": guid, "id": guid, "name": p.Title, "title": p.Title,
		"coverId": guid, "coverUrl": "", "trackCount": p.TrackCount, "type": 0,
		"isDaily": true, "readOnly": true, "isFavorite": false, "description": "网易云自建歌单（只读）"}, true
}
