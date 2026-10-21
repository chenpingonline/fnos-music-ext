package fnmusic

import (
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"testing"
)

func TestQQMIDRoundTripAndSourceSeparation(t *testing.T) {
	for _, id := range []string{"0039MnYb0qxYhV", "00000000000000", "ZZZZZZZZZZZZZZ", "zzzzzzzzzzzzzz", "12345678901234"} {
		guid, ok := EncodeTrackID("qqmusic", id)
		if !ok || len(guid) != 32 {
			t.Fatal(id, guid)
		}
		source, decoded, ok := DecodeTrackID("track_" + guid)
		if !ok || source != "qqmusic" || decoded != id {
			t.Fatal(source, decoded, ok)
		}
		if _, ok := DecodeNetEaseID(guid); ok {
			t.Fatal("QQ MID confused with NetEase ID")
		}
		replacement := "0"
		if guid[:1] == replacement {
			replacement = "1"
		}
		if _, _, ok := DecodeTrackID(replacement + guid[1:]); ok {
			t.Fatal("modified checksum accepted")
		}
		vo, ok := TrackVO(provider.Track{ID: id, Source: "qqmusic", Title: "Test"}, true)
		if !ok || vo["source"] != "qqmusic" || vo["title"] != "[QQ音乐] Test" {
			t.Fatal(vo)
		}
	}
	for _, id := range []string{"short", "1234567890123/", "123456789012345"} {
		if _, ok := EncodeTrackID("qqmusic", id); ok {
			t.Fatal("invalid MID accepted")
		}
	}
	old, _ := EncodeNetEaseID("123")
	same, _ := EncodeTrackID("netease", "123")
	if same != old {
		t.Fatal("existing GUID changed")
	}
}
