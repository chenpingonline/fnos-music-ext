package gateway

import (
	"github.com/chenpingonline/fnos-music-ext/internal/library"
	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

func (g *Gateway) Configure(root string, settings *preferences.Store) error {
	g.settings = settings
	ledger, e := library.NewLedger(root)
	if e != nil {
		return e
	}
	g.ledger = ledger
	media, e := library.New(root, settings, func(user, scope string) (provider.Adapter, bool) {
		for _, s := range g.sourcesFor(provider.User{FnOSGUID: user, Verified: true}) {
			if s.Scope == scope {
				return s.Adapter, true
			}
		}
		return nil, false
	})
	if e != nil {
		return e
	}
	g.media = media
	return nil
}
func (g *Gateway) Preferences() *preferences.Store { return g.settings }
func (g *Gateway) Media() *library.Service         { return g.media }

type bindingState struct{}

func (g *Gateway) startBinding(key string) bool {
	g.bindMu.Lock()
	defer g.bindMu.Unlock()
	if g.bindings == nil {
		g.bindings = map[string]*bindingState{}
	}
	if g.bindings[key] != nil || len(g.bindings) >= 512 {
		return false
	}
	g.bindings[key] = &bindingState{}
	return true
}
func (g *Gateway) finishBinding(key string) {
	g.bindMu.Lock()
	delete(g.bindings, key)
	g.bindMu.Unlock()
}
