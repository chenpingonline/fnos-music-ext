package fnmusic

import (
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"strings"
)

// QQ MIDs preserve their leading zeroes and case. They are carried in 96 bits
// with a domain-separated 32-bit checksum, fitting the official 32-hex GUID.
func EncodeTrackID(source, id string) (string, bool) {
	if source == "" || source == "netease" {
		return EncodeNetEaseID(id)
	}
	if source != "qqmusic" || len(id) != 14 {
		return "", false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return "", false
		}
	}
	n, ok := new(big.Int).SetString("1"+id, 62)
	if !ok || n.BitLen() > 96 {
		return "", false
	}
	payload := make([]byte, 12)
	n.FillBytes(payload)
	sum := sha256.Sum256([]byte("fnmusic-go:v3:qqmusic:" + id))
	return hex.EncodeToString(sum[:4]) + hex.EncodeToString(payload), true
}
func DecodeTrackID(guid string) (source, id string, ok bool) {
	if id, ok = DecodeNetEaseID(guid); ok {
		return "netease", id, true
	}
	guid = strings.TrimPrefix(guid, "track_")
	if len(guid) != 32 {
		return "", "", false
	}
	raw, err := hex.DecodeString(guid)
	if err != nil {
		return "", "", false
	}
	encoded := new(big.Int).SetBytes(raw[4:]).Text(62)
	if len(encoded) != 15 || encoded[0] != '1' {
		return "", "", false
	}
	id = encoded[1:]
	check, valid := EncodeTrackID("qqmusic", id)
	if !valid || check != guid {
		return "", "", false
	}
	return "qqmusic", id, true
}
func DecodeOnlineID(guid string) (string, bool) { _, id, ok := DecodeTrackID(guid); return id, ok }
