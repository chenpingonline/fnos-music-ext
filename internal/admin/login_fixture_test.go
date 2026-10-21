package admin

import (
	"context"
	"github.com/chenpingonline/fnos-music-ext/internal/netease"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"sync/atomic"
)

type testLoginAdapter struct {
	provider.Adapter
	code   *atomic.Int64
	logged *atomic.Bool
}

func (a *testLoginAdapter) BeginQRLogin(context.Context) (netease.QRLogin, error) {
	return netease.QRLogin{Key: "test-key", QRURL: "https://music.163.com/login?codekey=test-key"}, nil
}
func (a *testLoginAdapter) CheckQRLogin(context.Context, string) (netease.LoginState, error) {
	if a.code.Load() == 803 {
		return netease.LoginState{State: "success", LoggedIn: true}, nil
	}
	return netease.LoginState{State: "pending"}, nil
}
func (a *testLoginAdapter) AuthStatus(context.Context) (netease.LoginState, error) {
	return netease.LoginState{LoggedIn: a.logged.Load()}, nil
}
func (a *testLoginAdapter) ListPlaylists(context.Context, provider.User) ([]provider.Playlist, error) {
	return []provider.Playlist{{ID: "11", Title: "Owned", ReadOnly: true}}, nil
}
func (a *testLoginAdapter) PlaylistTracks(context.Context, provider.User, string) ([]provider.Track, error) {
	return []provider.Track{}, nil
}
