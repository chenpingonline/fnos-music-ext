package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// Gateway preserves official API semantics. No third-party music or writes are enabled in v0.1.
type Gateway struct {
	upstream  string
	proxy     *httputil.ReverseProxy
	client    *http.Client
	online    provider.Adapter
	ownerGUID string
	accounts  AccountResolver
	cache     *trackCache
}

func New(upstream string) *Gateway {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", upstream)
		},
		DisableCompression: true, MaxIdleConns: 64, MaxIdleConnsPerHost: 32, IdleConnTimeout: 45 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	target := &url.URL{Scheme: "http", Host: "trim-music"}
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.Transport = transport
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "official music backend unavailable", http.StatusBadGateway)
	}
	return &Gateway{upstream: upstream, proxy: rp, client: &http.Client{Transport: transport, Timeout: 3 * time.Second}, cache: newTrackCache()}
}
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/_ext/livez":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "fnmusic-go", "version": "0.4.0-preview"})
		return
	case "/_ext/healthz":
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://trim-music/music/api/v1/user/me", nil)
		if err != nil {
			writeJSON(w, 500, map[string]any{"ok": false})
			return
		}
		resp, err := g.client.Do(req)
		if err != nil {
			writeJSON(w, 503, map[string]any{"ok": false, "upstream": "unreachable"})
			return
		}
		_ = resp.Body.Close()
		ok := resp.StatusCode < 500
		status := http.StatusOK
		if !ok {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]any{"ok": ok, "upstream": "reachable", "upstream_http_status": resp.StatusCode})
		return
	}
	// Online interception is opt-in and bound to one verified fnOS account.
	if (g.accounts != nil || (g.online != nil && g.ownerGUID != "")) && g.serveOnline(w, r) {
		return
	}
	// Official music API fallback is unchanged on any provider failure.
	g.proxy.ServeHTTP(w, r)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (g *Gateway) Close() {
	if t, ok := g.client.Transport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
}

var ErrNoSource = errors.New("online provider not configured")

// Provider is intentionally isolated from the transparent proxy; later phases add
// authorized search, track, playlist, metadata and playback contracts here.
type Provider interface {
	ID() string
	Health(ctx context.Context) error
}
