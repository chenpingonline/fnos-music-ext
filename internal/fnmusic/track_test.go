package fnmusic

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

func TestTrackTimestampsForOfficialPlaylistClient(t *testing.T) {
	track := provider.Track{ID: "30613049", Title: "多远都要在一起", Artists: []string{"G.E.M.邓紫棋"}, DurationMS: 216880}
	vo, _ := TrackVO(track, false)
	metadata, _ := Metadata(track)
	data := metadata["data"].(map[string]any)
	card, _ := PlaylistVO("account", provider.Playlist{ID: "123", Title: "歌单"})
	for name, payload := range map[string]any{
		"playlist track": vo, "metadata": data, "nested metadata": data["track"],
		"artist": vo["artists"].([]map[string]any)[0], "album": vo["album"], "playlist card": card,
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"createdAt", "updatedAt"} {
				seconds, ok := decoded[field].(float64)
				// ECMAScript Date accepts finite milliseconds within ±8.64e15.
				if !ok || math.IsNaN(seconds) || math.Abs(seconds*1000) > 8.64e15 {
					t.Fatalf("%s cannot be converted by the official client: %v", field, decoded[field])
				}
				if seconds != 0 {
					t.Fatalf("unknown source timestamp must use Unix epoch: %v", seconds)
				}
			}
		})
	}
	for _, field := range []string{"year", "discNo", "trackNo"} {
		if value, exists := vo[field]; !exists || value != nil {
			t.Fatalf("unknown %s must be explicitly null: %v", field, value)
		}
	}
	if value, exists := vo["isrc"]; !exists || value != "" {
		t.Fatalf("unknown isrc must be an empty string: %v", value)
	}
	album := vo["album"].(map[string]any)
	for _, field := range []string{"releaseDate", "barcode"} {
		if value, exists := album[field]; !exists || value != nil {
			t.Fatalf("unknown album %s must be explicitly null: %v", field, value)
		}
	}
}

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

func TestAudioFormatIsObservedRatherThanAssumed(t *testing.T) {
	unknown, _ := TrackVO(provider.Track{ID: "123", Title: "Unknown"}, false)
	if unknown["format"] != "" || unknown["codec"] != "" {
		t.Fatal("unknown source mislabeled MP3")
	}
	known, _ := TrackVO(provider.Track{ID: "123", Title: "FLAC", Audio: &provider.AudioInfo{Format: "flac", Codec: "flac", Size: 12345}}, false)
	spec := known["audioSpec"].(map[string]any)
	if known["format"] != "flac" || spec["codec"] != "flac" || spec["size"] != int64(12345) {
		t.Fatal(known)
	}
}
