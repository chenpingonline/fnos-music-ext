package netease

import (
	"context"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"net/http"
	"strings"
	"testing"
)

type imageTransport func(*http.Request) (*http.Response, error)

func (f imageTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPlaylistImageValidationAndNoCredentialForwarding(t *testing.T) {
	image := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 24)
	for _, tc := range []struct {
		name, url, ct, body string
		size                int64
		status              int
		want                bool
	}{
		{"valid", "https://p1.music.126.net/test.jpg", "image/png", image, int64(len(image)), 200, true},
		{"generic content type", "https://p1.music.126.net/test.jpg", "application/octet-stream", image, -1, 200, true},
		{"lying image header", "https://p1.music.126.net/test.jpg", "image/jpeg", "<html>error</html>", -1, 200, false},
		{"private blocked", "http://127.0.0.1/test.jpg", "image/jpeg", "image", 5, 200, false},
		{"non image", "https://p1.music.126.net/test.jpg", "text/html", "html", 4, 200, false},
		{"oversized", "https://p1.music.126.net/test.jpg", "image/jpeg", "image", 7 << 20, 200, false},
		{"upstream error", "https://p1.music.126.net/test.jpg", "image/jpeg", "image", 5, 404, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("User-Agent") != "Mozilla/5.0" {
					t.Error("missing CDN-compatible user agent")
				}
				if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
					t.Error("credentials forwarded to image host")
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.ct}}, Body: io.NopCloser(strings.NewReader(tc.body)), ContentLength: tc.size}, nil
			})}
			img, err := openPlaylistImage(context.Background(), client, user(), provider.Playlist{CoverURL: tc.url})
			if tc.want {
				if err != nil {
					t.Fatal(err)
				}
				defer img.Body.Close()
				raw, _ := io.ReadAll(img.Body)
				if string(raw) != image || img.ContentLength != int64(len(image)) || img.ContentType != "image/png" {
					t.Fatal("invalid image")
				}
			} else if err == nil {
				img.Body.Close()
				t.Fatal("accepted invalid image")
			}
			if tc.name == "private blocked" && calls != 0 {
				t.Fatal("private address requested")
			}
			if _, err := openPlaylistImage(context.Background(), client, provider.User{}, provider.Playlist{CoverURL: tc.url}); err == nil {
				t.Fatal("unauthenticated image fetched")
			}
		})
	}
}
