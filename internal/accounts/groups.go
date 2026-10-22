package accounts

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"strings"
)

func cloneGroups(groups map[string][]string) map[string][]string {
	if groups == nil {
		return nil
	}
	out := map[string][]string{}
	for k, v := range groups {
		out[k] = append([]string{}, v...)
	}
	return out
}
func validateGroups(c Config) error {
	urls := map[string]string{}
	for _, a := range c.Accounts {
		urls[a.ID] = a.BridgeURL
	}
	for primary, ids := range c.AccountGroups {
		if urls[primary] == "" || len(ids) > 1 {
			return ErrInvalid
		}
		platform := strings.Split(urls[primary], "/")[2]
		for _, id := range ids {
			if urls[id] == "" || id == primary || strings.Split(urls[id], "/")[2] == platform {
				return ErrInvalid
			}
		}
	}
	owners := map[string]string{}
	claim := func(primary, owner string) error {
		for _, id := range append([]string{primary}, c.AccountGroups[primary]...) {
			if previous := owners[id]; previous != "" && previous != owner {
				return ErrConflict
			}
			owners[id] = owner
		}
		return nil
	}
	for guid, id := range c.Bindings {
		owner := "guid:" + guid
		if i, ok := c.Identities[guid]; ok {
			owner = "user:" + i.Username
		}
		if err := claim(id, owner); err != nil {
			return err
		}
	}
	for name, id := range c.UserBindings {
		if err := claim(id, "user:"+name); err != nil {
			return err
		}
	}
	return nil
}

// SetGroup explicitly grants a primary account's user access to one account of
// the other platform. Groups are one level deep; no transitive grants occur.
func (m *Manager) SetGroup(primary string, additional []string) error {
	return m.update(func(c *Config) error {
		if c.AccountGroups == nil {
			c.AccountGroups = map[string][]string{}
		}
		c.AccountGroups[primary] = append([]string{}, additional...)
		return nil
	})
}
func (m *Manager) ResolveSources(user provider.User) []provider.AccountSource {
	// ResolveUser validates both the authenticated music identity and current UID.
	_, scope, ok := m.ResolveUser(user)
	if !ok {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	primary := strings.SplitN(scope, ":", 2)[0]
	_, current, found := m.resolveLocked(user.FnOSGUID)
	if m.config.Isolation {
		if identity, known := m.config.Identities[user.FnOSGUID]; known {
			if assigned, exists := m.config.UserBindings[identity.Username]; exists {
				current = assigned
				found = true
			}
		}
	}
	if !found || current != primary {
		return nil
	}
	ids := append([]string{primary}, m.config.AccountGroups[primary]...)
	out := []provider.AccountSource{}
	for _, id := range ids {
		for _, a := range m.config.Accounts {
			if a.ID != id {
				continue
			}
			sum := sha256.Sum256([]byte(a.BridgeURL + ":" + a.CacheToken))
			current := id + ":" + hex.EncodeToString(sum[:8])
			if id == primary && current != scope {
				return nil
			}
			if adapter := m.adapters[id]; adapter != nil {
				out = append(out, provider.AccountSource{Adapter: adapter, Scope: current})
			}
		}
	}
	return out
}
