package netease

import (
	"context"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCoverSizeTransformsOnlyNetEaseImageURLs(t *testing.T) {
	for _, tc := range []struct {
		name, url, wantParam string
		size                 int
	}{
		{"160 thumbnail", "https://p2.music.126.net/cover.jpg", "160y160", 160},
		{"larger cover", "https://p1.music.126.net/cover.jpg", "600y600", 600},
		{"replace existing size", "https://p3.music.126.net/cover.jpg?param=300y300&other=keep", "160y160", 160},
		{"original", "https://p2.music.126.net/cover.jpg", "", 0},
		{"foreign CDN untouched", "https://example.com/cover.jpg?other=keep", "", 160},
		{"lookalike host untouched", "https://p2.music.126.net.example.com/cover.jpg", "", 160},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			data := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 24)
			client := &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if got := r.URL.Query().Get("param"); got != tc.wantParam {
					t.Fatalf("param=%q want=%q", got, tc.wantParam)
				}
				if strings.Contains(tc.url, "other=keep") && r.URL.Query().Get("other") != "keep" {
					t.Fatal("unrelated parameter lost")
				}
				if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
					t.Fatal("image request carried credentials")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data)), ContentLength: int64(len(data))}, nil
			})}
			native := &Native{media: client}
			for _, open := range []func() (provider.Stream, error){
				func() (provider.Stream, error) {
					return native.OpenTrackCoverSized(context.Background(), user(), provider.Track{CoverURL: tc.url}, tc.size)
				},
				func() (provider.Stream, error) {
					return native.OpenPlaylistCoverSized(context.Background(), user(), provider.Playlist{CoverURL: tc.url}, tc.size)
				},
			} {
				image, e := open()
				if e != nil {
					t.Fatal(e)
				}
				image.Body.Close()
			}
			if calls != 2 {
				t.Fatal(calls)
			}
		})
	}
}
