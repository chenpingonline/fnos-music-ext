package qqmusic

import (
	"context"
	"encoding/json"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

func TestQQCoverFromAlbumMIDOrPMID(t *testing.T) {
	for _, tc := range []struct{ name, album, want string }{
		{"prefer MID", `{"mid":"000f01724fd7TH","pmid":"0030lak94GN5Ad_0"}`, "000f01724fd7TH"},
		{"reported song", `{"mid":"","pmid":"0030lak94GN5Ad_0"}`, "0030lak94GN5Ad_0"},
		{"PMID without suffix", `{"pmid":"0030lak94GN5Ad"}`, "0030lak94GN5Ad"},
		{"invalid MID falls back", `{"mid":"bad","pmid":"0030lak94GN5Ad_0"}`, "0030lak94GN5Ad_0"},
		{"missing artwork", `{"mid":"","pmid":""}`, ""},
		{"reject path", `{"pmid":"0030lak94GN5Ad_0/../x"}`, ""},
		{"reject query", `{"pmid":"0030lak94GN5Ad?x=1"}`, ""},
		{"reject long suffix", `{"pmid":"0030lak94GN5Ad_12345678901234567890"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := fixture(t)
			n.client.Transport = transport(func(r *http.Request) (*http.Response, error) {
				return response(r, envelope(`{"track_info":{"mid":"0002o2SE1dANGE","title":"虫洞诗","album":`+tc.album+`}}`)), nil
			})
			track, err := n.GetTrack(context.Background(), user(), "0002o2SE1dANGE")
			if err != nil {
				t.Fatal(err)
			}
			wantURL := ""
			if tc.want != "" {
				wantURL = "https://y.gtimg.cn/music/photo_new/T002R500x500M000" + tc.want + ".jpg"
			}
			if track.CoverURL != wantURL {
				t.Fatalf("cover URL=%q want=%q", track.CoverURL, wantURL)
			}
			calls := 0
			n.media.Transport = transport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != wantURL || r.Header.Get("Cookie") != "" {
					t.Fatal("incorrect artwork request")
				}
				return response(r, string(defaultCoverPNG)), nil
			})
			cover, err := n.OpenTrackCoverSized(context.Background(), user(), track, 160)
			if err != nil {
				t.Fatal(err)
			}
			defer cover.Body.Close()
			if _, err := png.Decode(cover.Body); err != nil {
				t.Fatal("invalid image", err)
			}
			wantCalls := 0
			if tc.want != "" {
				wantCalls = 1
			}
			if calls != wantCalls || cover.StatusCode != 200 {
				t.Fatalf("image requests=%d status=%d", calls, cover.StatusCode)
			}
		})
	}
}

func TestQQCoverErrorsRemainDiagnosable(t *testing.T) {
	n := fixture(t)
	n.media.Transport = transport(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })
	track := provider.Track{CoverURL: "https://y.gtimg.cn/music/photo_new/test.jpg"}
	if _, err := n.OpenTrackCover(context.Background(), user(), track); diagnostics.Code(err) != "timeout" {
		t.Fatal(err)
	}
	n.media.Transport = transport(func(r *http.Request) (*http.Response, error) { return response(r, "not an image"), nil })
	if _, err := n.OpenTrackCover(context.Background(), user(), track); diagnostics.Code(err) != "invalid-cover-image" {
		t.Fatal(err)
	}
	if _, err := n.OpenTrackCover(context.Background(), user(), provider.Track{CoverURL: "http://127.0.0.1/private"}); diagnostics.Code(err) != "invalid-cover-url" {
		t.Fatal(err)
	}
	if _, err := n.OpenTrackCover(context.Background(), provider.User{}, provider.Track{}); err != provider.ErrUnauthenticated {
		t.Fatal(err)
	}
	if _, err := n.OpenTrackCoverSized(context.Background(), user(), provider.Track{}, 2049); err == nil {
		t.Fatal("default cover bypassed size validation")
	}
	// A missing PMID stays empty metadata, while the request itself gets artwork.
	track, err := parseTrack(json.RawMessage(strings.Replace(trackJSON, `"mid":"000f01724fd7TH"`, `"mid":""`, 1)))
	if err != nil || track.CoverURL != "" {
		t.Fatal(track, err)
	}
	cover, err := n.OpenTrackCover(context.Background(), user(), track)
	if err != nil {
		t.Fatal(err)
	}
	defer cover.Body.Close()
	data, err := io.ReadAll(cover.Body)
	if err != nil || int64(len(data)) != cover.ContentLength || cover.ContentType != "image/png" {
		t.Fatal("invalid fallback stream", err)
	}
}
