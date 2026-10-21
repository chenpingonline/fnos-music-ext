package mediahttp

import (
	"context"
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Prevent a compromised upstream from issuing SSRF requests into the NAS/LAN.
func SafeURL(u *url.URL) bool {
	if (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || IsPublicIP(ip)
}
func IsPublicIP(ip net.IP) bool {
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
func NewClient() *http.Client {
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
			if !IsPublicIP(v.IP) {
				return nil, diagnostics.New("non-public-address")
			}
		}
		if len(ips) == 0 {
			return nil, errors.New("media host has no resolved addresses")
		}
		return DialAddresses(ctx, network, port, ips, d.DialContext)
	}
	return &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !SafeURL(req.URL) {
			return http.ErrUseLastResponse
		}
		return nil
	}}
}

// Keep DNS validation above, then try the validated addresses. A NAS may have
// IPv6 DNS answers without a usable IPv6 route, or an unreachable CDN address.
func DialAddresses(ctx context.Context, network, port string, ips []net.IPAddr, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
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
