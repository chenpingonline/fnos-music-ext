package netease

import (
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"net/url"
	"testing"
)

func user() provider.User { return provider.User{FnOSGUID: "alice", Verified: true} }
func TestMediaSafety(t *testing.T) {
	for _, src := range []string{"http://127.0.0.1/song.mp3", "http://192.168.2.1/", "file:///etc/passwd", "http://metadata.google.internal/", "http://169.254.169.254/", "http://[::1]/"} {
		u, _ := url.Parse(src)
		if safeMediaURL(u) {
			t.Errorf("allowed %s", src)
		}
	}
	u, _ := url.Parse("https://music.126.net/song.flac")
	if !safeMediaURL(u) {
		t.Fatal("public CDN refused")
	}
}
