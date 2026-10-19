package netease

import (
	"bytes"
	"context"
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// endpoint is an internal label, never a URL or host supplied by upstream.
func playbackHTTPError(endpoint string, status int) error {
	return &diagnostics.Failure{Kind: endpoint + "-http-" + strconv.Itoa(status), Cause: &diagnostics.HTTPError{Status: status}}
}

const mediaUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// Prevent a compromised bridge from issuing SSRF requests into the NAS/LAN.
func safeMediaURL(u *url.URL) bool {
	if (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || isPublicIP(ip)
}
func isPublicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		// Carrier NAT, documentation/test-net, benchmarking and reserved ranges.
		for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "169.254.0.0/16", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4"} {
			_, n, _ := net.ParseCIDR(cidr)
			if n.Contains(ip4) {
				return false
			}
		}
	}
	return true
}
func newMediaClient() *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{ResponseHeaderTimeout: 15 * time.Second, MaxIdleConns: 20, IdleConnTimeout: 30 * time.Second, DisableCompression: true}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil {
			return nil, e
		}
		for _, v := range ips {
			if !isPublicIP(v.IP) {
				return nil, diagnostics.New("non-public-address")
			}
		}
		if len(ips) == 0 {
			return nil, errors.New("media host has no resolved addresses")
		}
		return dialMediaAddresses(ctx, network, port, ips, d.DialContext)
	}
	return &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !safeMediaURL(req.URL) {
			return http.ErrUseLastResponse
		}
		return nil
	}}
}

// Keep DNS validation above, then try the validated addresses. A NAS may have
// IPv6 DNS answers without a usable IPv6 route, or an unreachable CDN address.
func dialMediaAddresses(ctx context.Context, network, port string, ips []net.IPAddr, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	var last error
	for _, ip := range ips {
		attempt, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		conn, err := dial(attempt, network, net.JoinHostPort(ip.IP.String(), port))
		cancel()
		if err == nil {
			return conn, nil
		}
		last = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if last == nil {
		last = errors.New("media host has no resolved addresses")
	}
	return nil, last
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
func (b *Bridge) OpenPlaylistCover(ctx context.Context, u provider.User, p provider.Playlist) (provider.Stream, error) {
	return openPlaylistImage(ctx, b.media, u, p)
}

func (n *Native) OpenPlaylistCoverSized(ctx context.Context, u provider.User, p provider.Playlist, size int) (provider.Stream, error) {
	return openPlaylistImageSized(ctx, n.media, u, p, size)
}
func (b *Bridge) OpenPlaylistCoverSized(ctx context.Context, u provider.User, p provider.Playlist, size int) (provider.Stream, error) {
	return openPlaylistImageSized(ctx, b.media, u, p, size)
}
