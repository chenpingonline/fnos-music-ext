package netease

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

func TestPlaybackMediaHeadersForNative(t *testing.T) {
	for _, kind := range []string{"native"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			media := &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.UserAgent() != mediaUserAgent || r.Header.Get("Range") != "bytes=0-15" || r.Header.Get("If-Range") != "etag" {
					t.Error("missing playback headers", r.Header)
				}
				if r.Header.Get("Cookie") != "" {
					t.Error("source credentials leaked to CDN")
				}
				return &http.Response{StatusCode: 206, Header: http.Header{"Content-Type": {"audio/flac"}}, Body: io.NopCloser(strings.NewReader("fLaC")), Request: r}, nil
			})}
			api := &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
				body := `{"data":[{"code":200,"url":"https://cdn.example/audio.flac?token=private"}]}`
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			base, _ := url.Parse("https://api.example")
			source := &Native{base: base, client: api, media: media, cookie: "MUSIC_U=private"}
			s, err := source.Play(context.Background(), user(), provider.PlaybackRequest{TrackID: "123", Range: "bytes=0-15", IfRange: "etag"})
			if err != nil {
				t.Fatal(err)
			}
			s.Body.Close()
			if calls != 1 {
				t.Fatal("media not fetched", calls)
			}
		})
	}
}
