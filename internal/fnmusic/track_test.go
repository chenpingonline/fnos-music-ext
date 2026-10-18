package fnmusic

import (
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"testing"
)

func TestVirtualID(t *testing.T) {
	for _, id := range []string{"1", "123456789", "18446744073709551615"} {
		guid, ok := EncodeNetEaseID(id)
		if !ok || len(guid) != 32 {
			t.Fatal(id, guid)
		}
		x, ok := DecodeNetEaseID(guid)
		if !ok || x != id {
			t.Fatal(x, ok)
		}
	}
	if _, ok := DecodeNetEaseID("f0000000000000000000000000000000"); ok {
		t.Fatal("fake official id treated as online")
	}
}
func TestTrackVO(t *testing.T) {
	vo, ok := TrackVO(provider.Track{ID: "123", Title: "Hello", Artists: []string{"A"}, Album: "Album", DurationMS: 93000}, true)
	if !ok {
		t.Fatal("vo")
	}
	if vo["title"] != "[music box] Hello" {
		t.Fatal(vo)
	}
	spec := vo["audioSpec"].(map[string]any)
	if spec["duration"] != int64(93000) {
		t.Fatal(spec)
	}
	meta, _ := Metadata(provider.Track{ID: "123", Title: "Hello"})
	if meta["data"].(map[string]any)["track"] == nil {
		t.Fatal(meta)
	}
}
