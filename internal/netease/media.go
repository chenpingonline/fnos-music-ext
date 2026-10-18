package netease

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

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
				return nil, errors.New("media host resolves to a non-public IP")
			}
		}
		if len(ips) == 0 {
			return nil, errors.New("media host has no resolved addresses")
		}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	return &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !safeMediaURL(req.URL) {
			return http.ErrUseLastResponse
		}
		return nil
	}}
}
