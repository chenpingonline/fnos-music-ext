package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/buildinfo"
	"github.com/chenpingonline/fnos-music-ext/internal/library"
	"github.com/chenpingonline/fnos-music-ext/internal/playback"
	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"github.com/chenpingonline/fnos-music-ext/internal/requestgroup"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"
)

// Gateway preserves official API semantics. No third-party music or writes are enabled in v0.1.
type Gateway struct {
	listMu       sync.Mutex
	listEpoch    uint64
	lists        map[string]cachedPlaylists
	listFlights  requestgroup.Group[[]provider.Playlist]
	trackFlights requestgroup.Group[[]provider.Track]
	pageFlights  requestgroup.Group[provider.TrackPage]
	hlsMu        sync.Mutex
	hls          *playback.Manager
	sessionMu    sync.Mutex
	sessions     map[string]userSession

	media           *library.Service
	ledger          *library.Ledger
	bg              context.Context
	cancel          context.CancelFunc
	bindMu          sync.Mutex
	bindings        map[string]*bindingState
	bindWrites      sync.Mutex
	bindWG          sync.WaitGroup
	upstream        string
	proxy           *httputil.ReverseProxy
	client          *http.Client
	online          provider.Adapter
	ownerGUID       string
	accounts        AccountResolver
	cache           *trackCache
	settings        *preferences.Store
	searchMu        sync.Mutex
	searches        map[string]*searchPool
	rankedSearches  map[string]*rankedSearchPool
	recoMu          sync.Mutex
	recommendations map[string]recommendationCache
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
	bg, cancel := context.WithCancel(context.Background())
	return &Gateway{bg: bg, cancel: cancel, upstream: upstream, proxy: rp, client: &http.Client{Transport: transport, Timeout: 3 * time.Second}, cache: newTrackCache()}
}
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(context.WithValue(r.Context(), identityContextKey{}, &requestIdentity{}))
	switch r.URL.Path {
	case "/_ext/livez":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "fnmusic-go", "version": buildinfo.Version})
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
	g.hlsMu.Lock()
	if g.hls != nil {
		g.hls.Close()
	}
	g.hlsMu.Unlock()
	if g.cancel != nil {
		g.cancel()
	}
	if g.media != nil {
		g.media.Close()
	}
	g.bindWG.Wait()
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
