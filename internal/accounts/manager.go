// Package accounts persists administrator-controlled MusicBox instances and
// resolves which instance a verified fnOS user may access.
// A MusicBox instance has its own cookie jar; accounts MUST NOT share a URL.
package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/chenpingonline/fnos-music-ext/internal/netease"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

var (
	ErrInvalid  = errors.New("invalid account configuration")
	ErrNotFound = errors.New("account does not exist")
	ErrConflict = errors.New("account or bridge address already configured")
	idPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,48}$`)
	userPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

type Account struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BridgeURL string `json:"bridge_url"`
}
type Config struct {
	Isolation     bool              `json:"isolation"`
	SharedAccount string            `json:"shared_account"`
	Accounts      []Account         `json:"accounts"`
	Bindings      map[string]string `json:"bindings"` // fnOS GUID -> account ID
}
type Snapshot struct {
	Config
	// These are public diagnostics for administrator only, not MusicBox cookies.
}

// Builder permits test-specific adapters and validates the backend configuration.
type Builder func(url string) (provider.Adapter, error)
type Manager struct {
	mu       sync.RWMutex
	path     string
	config   Config
	adapters map[string]provider.Adapter
	build    Builder
}

func defaultConfig() Config {
	return Config{Isolation: true, Bindings: map[string]string{}, Accounts: []Account{}}
}
func New(path string, build Builder) (*Manager, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: state path required", ErrInvalid)
	}
	if build == nil {
		build = func(u string) (provider.Adapter, error) { return netease.NewBridge(u) }
	}
	m := &Manager{path: path, build: build, config: defaultConfig(), adapters: map[string]provider.Adapter{}}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}
func (m *Manager) load() error {
	fileStat, err := os.Lstat(m.path)
	if err == nil && (!fileStat.Mode().IsRegular() || fileStat.Mode()&os.ModeSymlink != 0) {
		return errors.New("account state must not be a symlink")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.Open(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return errors.New("account state must be a private regular file (0600)")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return errors.New("account state too large")
	}
	var config Config
	if err = json.Unmarshal(raw, &config); err != nil {
		return err
	}
	if config.Bindings == nil {
		config.Bindings = map[string]string{}
	}
	if config.Accounts == nil {
		config.Accounts = []Account{}
	}
	adapters, err := m.validate(config)
	if err != nil {
		return err
	}
	m.config = config
	m.adapters = adapters
	return nil
}
func (m *Manager) validate(c Config) (map[string]provider.Adapter, error) {
	adapters := make(map[string]provider.Adapter, len(c.Accounts))
	seenURL := map[string]bool{}
	for _, a := range c.Accounts {
		if !idPattern.MatchString(a.ID) || strings.TrimSpace(a.Name) == "" || len(a.Name) > 80 {
			return nil, fmt.Errorf("%w: account ID or name", ErrInvalid)
		}
		if _, exists := adapters[a.ID]; exists {
			return nil, ErrConflict
		}
		var adapter provider.Adapter
		var err error
		for _, old := range m.config.Accounts {
			if old.ID == a.ID && old.BridgeURL == a.BridgeURL {
				adapter = m.adapters[a.ID]
				break
			}
		}
		if adapter == nil {
			adapter, err = m.build(a.BridgeURL)
		}
		if err != nil {
			return nil, fmt.Errorf("%w: account %s: %v", ErrInvalid, a.ID, err)
		}
		if seenURL[strings.ToLower(a.BridgeURL)] {
			return nil, fmt.Errorf("%w: separate accounts need separate MusicBox instances", ErrConflict)
		}
		seenURL[strings.ToLower(a.BridgeURL)] = true
		adapters[a.ID] = adapter
	}
	if c.SharedAccount != "" && adapters[c.SharedAccount] == nil {
		return nil, fmt.Errorf("%w: shared account missing", ErrInvalid)
	}
	boundAccount := map[string]string{}
	for user, account := range c.Bindings {
		if !userPattern.MatchString(user) || user == "shared" || adapters[account] == nil {
			return nil, fmt.Errorf("%w: bad user binding", ErrInvalid)
		}
		if previous := boundAccount[account]; previous != "" && previous != user {
			return nil, fmt.Errorf("%w: one account cannot be privately bound to multiple fnOS users", ErrConflict)
		}
		boundAccount[account] = user
	}
	return adapters, nil
}
func clone(c Config) Config {
	copy := c
	copy.Accounts = append([]Account(nil), c.Accounts...)
	copy.Bindings = make(map[string]string, len(c.Bindings))
	for k, v := range c.Bindings {
		copy.Bindings[k] = v
	}
	return copy
}
func (m *Manager) Snapshot() Config { m.mu.RLock(); defer m.mu.RUnlock(); return clone(m.config) }

func (m *Manager) IsConfigured() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.config.Accounts) > 0
}
func (m *Manager) Resolve(fnOSGUID string) (provider.Adapter, string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.resolveLocked(fnOSGUID)
}
func (m *Manager) resolveLocked(fnOSGUID string) (provider.Adapter, string, bool) {
	if !userPattern.MatchString(fnOSGUID) || fnOSGUID == "shared" {
		return nil, "", false
	}
	accountID := ""
	if m.config.Isolation {
		accountID = m.config.Bindings[fnOSGUID]
	} else {
		accountID = m.config.SharedAccount
	}
	if accountID == "" {
		return nil, "", false
	}
	adapter := m.adapters[accountID]
	return adapter, accountID, adapter != nil
}

// ResolveScoped provides both the selected source and its cache namespace
// under ONE lock, so an administrator switch cannot mix two account versions.
func (m *Manager) ResolveScoped(fnOSGUID string) (provider.Adapter, string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, id, ok := m.resolveLocked(fnOSGUID)
	if !ok {
		return nil, "", false
	}
	for _, row := range m.config.Accounts {
		if row.ID == id {
			sum := sha256.Sum256([]byte(row.BridgeURL))
			return a, id + ":" + hex.EncodeToString(sum[:8]), true
		}
	}
	return nil, "", false
}

func (m *Manager) update(mut func(*Config) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := clone(m.config)
	if err := mut(&c); err != nil {
		return err
	}
	adapters, err := m.validate(c)
	if err != nil {
		return err
	}
	if err = m.persist(c); err != nil {
		return err
	}
	m.config = c
	m.adapters = adapters
	return nil
}
func (m *Manager) persist(c Config) error {
	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	stat, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !stat.IsDir() || stat.Mode().Perm()&0077 != 0 {
		return errors.New("account state directory must be private (0700)")
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".accounts-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), m.path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
func (m *Manager) Add(a Account) error {
	return m.update(func(c *Config) error { c.Accounts = append(c.Accounts, a); return nil })
}
func (m *Manager) Remove(id string) error {
	return m.update(func(c *Config) error {
		found := false
		out := make([]Account, 0, len(c.Accounts))
		for _, a := range c.Accounts {
			if a.ID == id {
				found = true
			} else {
				out = append(out, a)
			}
		}
		if !found {
			return ErrNotFound
		}
		c.Accounts = out
		if c.SharedAccount == id {
			c.SharedAccount = ""
		}
		for user, account := range c.Bindings {
			if account == id {
				delete(c.Bindings, user)
			}
		}
		return nil
	})
}
func (m *Manager) SetIsolation(on bool) error {
	return m.update(func(c *Config) error { c.Isolation = on; return nil })
}
func (m *Manager) SetShared(id string) error {
	return m.update(func(c *Config) error { c.SharedAccount = id; return nil })
}
func (m *Manager) Bind(userGUID, accountID string) error {
	return m.update(func(c *Config) error {
		if !userPattern.MatchString(userGUID) || userGUID == "shared" {
			return ErrInvalid
		}
		if accountID == "" {
			delete(c.Bindings, userGUID)
		} else {
			c.Bindings[userGUID] = accountID
		}
		return nil
	})
}
func (m *Manager) Probe(ctx context.Context, id string) error {
	m.mu.RLock()
	a := m.adapters[id]
	m.mu.RUnlock()
	if a == nil {
		return ErrNotFound
	}
	return a.Health(ctx)
}

// LookupAccount is for trusted administrator requests only. It never exposes
// provider cookies, and binds the adapter to its current local service address.
func (m *Manager) LookupAccount(id string) (provider.Adapter, string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, a := range m.config.Accounts {
		if a.ID == id {
			adapter := m.adapters[id]
			return adapter, a.BridgeURL, adapter != nil
		}
	}
	return nil, "", false
}
