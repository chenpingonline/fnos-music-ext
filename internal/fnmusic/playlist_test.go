package fnmusic

import "testing"

func TestPlaylistScope(t *testing.T) {
	a, ok := EncodePlaylistID("alice:somebridge", "12345")
	if !ok || len(a) != 32 {
		t.Fatal(a, ok)
	}
	if id, ok := DecodePlaylistID("alice:somebridge", a); !ok || id != "12345" {
		t.Fatal("round trip", id, ok)
	}
	if _, ok := DecodePlaylistID("bob:somebridge", a); ok {
		t.Fatal("cross account playlist leak")
	}
	if _, ok := DecodePlaylistID("alice:somebridge", "12345"); ok {
		t.Fatal("accepted unsafe raw ID")
	}
}
