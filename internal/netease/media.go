package netease

import (
	"bytes"
	"context"
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"github.com/chenpingonline/fnos-music-ext/internal/mediahttp"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// endpoint is an internal label, never a URL or host supplied by upstream.
func playbackHTTPError(endpoint string, status int) error {
	return &diagnostics.Failure{Kind: endpoint + "-http-" + strconv.Itoa(status), Cause: &diagnostics.HTTPError{Status: status}}
}

const mediaUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

func safeMediaURL(u *url.URL) bool { return mediahttp.SafeURL(u) }
func isPublicIP(ip net.IP) bool    { return mediahttp.IsPublicIP(ip) }
func newMediaClient() *http.Client { return mediahttp.NewClient() }
func dialMediaAddresses(ctx context.Context, network, port string, ips []net.IPAddr, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	return mediahttp.DialAddresses(ctx, network, port, ips, dial)
}

// Playlist image URLs come only from authenticated source metadata/selections,
// never a client URL parameter. Reuse the restricted media transport.
func openPlaylistImage(ctx context.Context, client *http.Client, user provider.User, p provider.Playlist) (provider.Stream, error) {
	return openPlaylistImageSized(ctx, client, user, p, 0)
}
func openPlaylistImageSized(ctx context.Context, client *http.Client, user provider.User, p provider.Playlist, size int) (provider.Stream, error) {
	if err := user.Validate(); err != nil {
		return provider.Stream{}, err
	}
	uri, err := url.Parse(p.CoverURL)
	if err != nil || !safeMediaURL(uri) {
		return provider.Stream{}, diagnostics.New("invalid-cover-url")
	}
	if size < 0 || size > 2048 {
		return provider.Stream{}, errors.New("invalid cover size")
	}
	// NetEase's image CDN uses param=WIDTHyHEIGHT, not the music API's size.
	// Keep other hosts and existing query fields unchanged.
	host := strings.ToLower(uri.Hostname())
	if size > 0 && (host == "music.126.net" || strings.HasSuffix(host, ".music.126.net")) {
		q := uri.Query()
		q.Set("param", strconv.Itoa(size)+"y"+strconv.Itoa(size))
		uri.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri.String(), nil)
	if err != nil {
		return provider.Stream{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "image/webp,image/*;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		return provider.Stream{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return provider.Stream{}, &diagnostics.HTTPError{Status: resp.StatusCode}
	}
	if resp.ContentLength > 6<<20 {
		return provider.Stream{}, diagnostics.New("image-too-large")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (6<<20)+1))
	if err != nil || len(body) > 6<<20 {
		return provider.Stream{}, diagnostics.New("image-read-or-limit")
	}
	// Some CDNs use application/octet-stream. Validate the actual bytes instead
	// of trusting a missing/misleading image header (including HTML error pages).
	ct := http.DetectContentType(body)
	if !strings.HasPrefix(ct, "image/") {
		return provider.Stream{}, diagnostics.New("invalid-image")
	}
	return provider.Stream{Body: io.NopCloser(bytes.NewReader(body)), StatusCode: 200, ContentType: ct, ContentLength: int64(len(body))}, nil
}
func (n *Native) OpenPlaylistCover(ctx context.Context, u provider.User, p provider.Playlist) (provider.Stream, error) {
	return openPlaylistImage(ctx, n.media, u, p)
}
func (n *Native) OpenPlaylistCoverSized(ctx context.Context, u provider.User, p provider.Playlist, size int) (provider.Stream, error) {
	return openPlaylistImageSized(ctx, n.media, u, p, size)
}
