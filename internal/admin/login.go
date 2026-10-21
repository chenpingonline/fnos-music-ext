package admin

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/accounts"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

var errNoChallenge = errors.New("QR challenge missing or expired; generate a new QR code")
var errPollFast = errors.New("QR polling is limited to once every 2 seconds")
var errNoLoginAdapter = errors.New("the configured source does not support QR login")

type loginChallenge struct {
	key, url             string
	authorized           bool
	expires, timeChecked time.Time
}
type loginManager struct {
	mu       sync.Mutex
	sessions map[string]loginChallenge
	accounts *accounts.Manager
}

func newLoginManager(m *accounts.Manager) *loginManager {
	return &loginManager{sessions: map[string]loginChallenge{}, accounts: m}
}
func (l *loginManager) adapter(id string) (provider.LoginAdapter, string, error) {
	if l.accounts == nil {
		return nil, "", accounts.ErrNotFound
	}
	a, u, ok := l.accounts.LookupAccount(id)
	if !ok {
		return nil, "", accounts.ErrNotFound
	}
	v, ok := a.(provider.LoginAdapter)
	if !ok {
		return nil, "", errNoLoginAdapter
	}
	return v, u, nil
}
func (l *loginManager) begin(ctx context.Context, id string) (provider.QRLogin, error) {
	a, u, err := l.adapter(id)
	if err != nil {
		return provider.QRLogin{}, err
	}
	qr, err := a.BeginQRLogin(ctx)
	if err != nil {
		return qr, err
	}
	// Configuration may change while the HTTP request is in flight.
	_, current, err := l.adapter(id)
	if err != nil || current != u {
		return provider.QRLogin{}, errNoChallenge
	}
	l.mu.Lock()
	l.sessions[id] = loginChallenge{key: qr.Key, url: u, expires: time.Now().Add(3 * time.Minute)}
	l.mu.Unlock()
	qr.Key = "" // unikey never reaches the browser as an API field
	return qr, nil
}
func (l *loginManager) poll(ctx context.Context, id string) (provider.LoginState, error) {
	a, u, err := l.adapter(id)
	if err != nil {
		return provider.LoginState{}, err
	}
	l.mu.Lock()
	s, ok := l.sessions[id]
	if !ok || s.url != u || time.Now().After(s.expires) {
		delete(l.sessions, id)
		l.mu.Unlock()
		return provider.LoginState{}, errNoChallenge
	}
	if time.Since(s.timeChecked) < 2*time.Second {
		l.mu.Unlock()
		return provider.LoginState{}, errPollFast
	}
	s.timeChecked = time.Now()
	l.sessions[id] = s
	l.mu.Unlock()
	status := provider.LoginState{State: "success", LoggedIn: true}
	if !s.authorized {
		status, err = a.CheckQRLogin(ctx, s.key)
		if err != nil {
			return provider.LoginState{}, err
		}
	}
	if status.State == "success" && status.LoggedIn {
		l.mu.Lock()
		latest, current := l.sessions[id]
		if current && latest.key == s.key {
			latest.authorized = true
			l.sessions[id] = latest
		}
		l.mu.Unlock()
		if !current || latest.key != s.key {
			return provider.LoginState{}, errNoChallenge
		}
		if err = l.accounts.RotateLoginToken(id); err != nil {
			return provider.LoginState{}, err
		}
	}
	if status.State == "success" || status.State == "expired" {
		l.mu.Lock()
		if latest, ok := l.sessions[id]; ok && latest.key == s.key {
			delete(l.sessions, id)
		}
		l.mu.Unlock()
	}
	return status, nil
}
func (l *loginManager) status(ctx context.Context, id string) (provider.LoginState, error) {
	a, _, err := l.adapter(id)
	if err != nil {
		return provider.LoginState{}, err
	}
	return a.AuthStatus(ctx)
}
func (l *loginManager) playlists(ctx context.Context, id string) ([]provider.Playlist, error) {
	if l.accounts == nil {
		return nil, accounts.ErrNotFound
	}
	a, _, ok := l.accounts.LookupAccount(id)
	if !ok {
		return nil, accounts.ErrNotFound
	}
	return a.ListPlaylists(ctx, provider.User{FnOSGUID: "admin-preview", Verified: true})
}
func (l *loginManager) tracks(ctx context.Context, id, playlistID string) ([]provider.Track, error) {
	if l.accounts == nil {
		return nil, accounts.ErrNotFound
	}
	a, _, ok := l.accounts.LookupAccount(id)
	if !ok {
		return nil, accounts.ErrNotFound
	}
	// Never fetch arbitrary playlist IDs. A playlist must belong to this
	// account's logged-in user before any tracks can be requested.
	playlists, err := a.ListPlaylists(ctx, provider.User{FnOSGUID: "admin-preview", Verified: true})
	if err != nil {
		return nil, err
	}
	found := false
	for _, p := range playlists {
		if p.ID == playlistID {
			found = true
			break
		}
	}
	if !found {
		return nil, accounts.ErrNotFound
	}
	t, ok := a.(interface {
		PlaylistTracks(context.Context, provider.User, string) ([]provider.Track, error)
	})
	if !ok {
		return nil, errors.New("playlist track list unsupported")
	}
	return t.PlaylistTracks(ctx, provider.User{FnOSGUID: "admin-preview", Verified: true}, playlistID)
}
