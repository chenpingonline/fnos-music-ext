package netease

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

func TestNativeEAPIPlaybackCarriesMatchingAuthAndProfile(t *testing.T) {
	for _, credential := range []string{"account-one", "account-two"} {
		t.Run(credential, func(t *testing.T) {
			api := &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
				path, body := unpackEAPI(t, r)
				header, ok := body["header"].(map[string]any)
				if !ok || path != "/api/song/enhance/player/url/v1" {
					t.Fatal("invalid encrypted request")
				}
				for k, want := range map[string]string{"MUSIC_U": credential, "__csrf": "csrf", "NMTID": "session", "os": "iphone", "appver": "9.0.90", "osver": "16.2", "channel": "distribution"} {
					if header[k] != want {
						t.Errorf("encrypted field %s is absent or inconsistent", k)
					}
					c, err := r.Cookie(k)
					if err != nil || c.Value != want {
						t.Errorf("HTTP cookie %s is absent or inconsistent", k)
					}
				}
				if header["deviceId"] == "" || body["e_r"] != true || r.UserAgent() != eapiUserAgent {
					t.Error("incomplete EAPI context")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":200,"data":[{"code":200,"url":"https://cdn.example/audio.flac"}]}`)), Request: r}, nil
			})}
			base, _ := url.Parse("https://interface.music.163.com")
			n := &Native{base: base, client: api, cookie: "MUSIC_U=" + credential + "; __csrf=csrf; NMTID=session"}
			if err := n.initWebSession(); err != nil {
				t.Fatal(err)
			}
			n.media = &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Cookie") != "" || r.UserAgent() != mediaUserAgent {
					t.Error("API auth/profile leaked to media client")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("fLaC")), Request: r}, nil
			})}
			s, err := n.Play(context.Background(), user(), provider.PlaybackRequest{TrackID: "123"})
			if err != nil {
				t.Fatal(err)
			}
			s.Body.Close()
		})
	}
}

func TestPlaybackHTTPRejectionIdentifiesEndpoint(t *testing.T) {
	for _, tc := range []struct{ kind, where, want string }{
		{"native", "api", "netease-api-http-403"},
		{"native", "business", "netease-api-code-403"},
		{"native", "cdn", "media-cdn-http-403"},
	} {
		t.Run(tc.kind+"-"+tc.where, func(t *testing.T) {
			mediaCalls := 0
			media := &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
				mediaCalls++
				return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("private CDN body")), Request: r}, nil
			})}
			api := &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
				status := 200
				body := `{"code":200,"data":[{"code":200,"url":"https://cdn.example/audio.flac?token=private"}]}`
				if tc.where == "api" {
					status = 403
					body = "private API body"
				}
				if tc.where == "business" {
					body = `{"code":403,"message":"private reason"}`
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			base, _ := url.Parse("https://api.example")
			source := &Native{base: base, client: api, media: media}
			_, err := source.Play(context.Background(), user(), provider.PlaybackRequest{TrackID: "123"})
			if diagnostics.Code(err) != tc.want {
				t.Fatal("wrong endpoint", diagnostics.Code(err))
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("raw response or URL leaked")
			}
			if tc.where == "cdn" && mediaCalls != 1 || tc.where != "cdn" && mediaCalls != 0 {
				t.Fatal("unexpected CDN request", mediaCalls)
			}
			if tc.where != "business" {
				var status *diagnostics.HTTPError
				if !errors.As(err, &status) || status.Status != 403 {
					t.Fatal("HTTP status cause lost")
				}
			}
		})
	}
}
