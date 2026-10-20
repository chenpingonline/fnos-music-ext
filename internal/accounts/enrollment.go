package accounts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"strings"

	"github.com/chenpingonline/fnos-music-ext/internal/netease"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

// PrepareLogin validates a prospective account without changing configuration.
func (m *Manager) PrepareLogin(a Account) (provider.Adapter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := clone(m.config)
	c.Accounts = append(c.Accounts, a)
	adapters, err := m.validate(c)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(a.BridgeURL, "native://") {
		return netease.NewLoginDraft(a.BridgeURL)
	}
	return adapters[a.ID], nil
}

// CommitLogin publishes a successfully authorized account only after both private
// credentials and configuration have been written; a failed config save rolls back credentials.
func (m *Manager) CommitLogin(ctx context.Context, a Account, adapter provider.Adapter) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	a.CacheToken = hex.EncodeToString(token)
	c := clone(m.config)
	c.Accounts = append(c.Accounts, a)
	adapters, err := m.validate(c)
	if err != nil {
		return err
	}
	native, ok := adapter.(*netease.Native)
	if ok {
		if err = native.CommitLogin(filepath.Dir(m.path), a.ID); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		if ok {
			native.RollbackLogin()
		}
		return err
	}
	if err = m.persist(c); err != nil {
		if ok {
			native.RollbackLogin()
		}
		return err
	}
	adapters[a.ID] = adapter
	m.config = c
	m.adapters = adapters
	return nil
}
