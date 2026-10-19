package accounts

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"github.com/chenpingonline/fnos-music-ext/internal/userdir"
)

type Identity struct {
	Username string `json:"username"`
	UID      string `json:"uid"`
}

func (m *Manager) BindUsername(name, account string) error {
	if account == "" {
		return m.BindDirectoryUser(userdir.User{Username: name}, account)
	}
	u, ok := m.lookupUser(name)
	if !ok {
		return ErrInvalid
	}
	return m.BindDirectoryUser(u, account)
}
func (m *Manager) BindDirectoryUser(user userdir.User, account string) error {
	name := user.Username
	if !userdir.ValidName(name) || account != "" && user.UID == "" {
		return ErrInvalid
	}
	return m.update(func(c *Config) error {
		if c.UserBindings == nil {
			c.UserBindings = map[string]string{}
		}
		if c.UserBindingUIDs == nil {
			c.UserBindingUIDs = map[string]string{}
		}
		for guid, identity := range c.Identities {
			if identity.Username == name {
				delete(c.Bindings, guid)
				if account != "" && identity.UID != user.UID {
					delete(c.Identities, guid)
				}
			}
		}
		if account == "" {
			delete(c.UserBindings, name)
			delete(c.UserBindingUIDs, name)
		} else {
			c.UserBindings[name] = account
			c.UserBindingUIDs[name] = user.UID
		}
		return nil
	})
}

// ObserveMusicIdentity is called only with the official authenticated /user/me
// response, never browser input or unchecked gateway headers on the music socket.
func (m *Manager) ObserveMusicIdentity(guid, name, uid string) error {
	if !userPattern.MatchString(guid) || guid == "shared" || !userdir.ValidName(name) || uid == "" {
		return ErrInvalid
	}
	m.mu.RLock()
	old, ok := m.config.Identities[guid]
	m.mu.RUnlock()
	if ok && old == (Identity{name, uid}) {
		return nil
	}
	return m.update(func(c *Config) error {
		if c.Identities == nil {
			c.Identities = map[string]Identity{}
		}
		for other, identity := range c.Identities {
			if other != guid && identity.Username == name {
				if identity.UID != uid {
					return ErrConflict
				}
				delete(c.Identities, other)
			}
		}
		if old, ok := c.Identities[guid]; ok && old != (Identity{name, uid}) {
			return ErrConflict
		}
		c.Identities[guid] = Identity{name, uid}
		return nil
	})
}

// ResolveUser keeps legacy GUID bindings but resolves username assignments only
// through a previously verified music identity and a still-matching local UID.
func (m *Manager) ResolveUser(user provider.User) (provider.Adapter, string, bool) {
	if user.Validate() != nil {
		return nil, "", false
	}
	m.mu.RLock()
	identity, known := m.config.Identities[user.FnOSGUID]
	m.mu.RUnlock()
	valid := false
	if known {
		u, ok := m.lookupUser(identity.Username)
		valid = ok && u.UID == identity.UID
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, id, ok := m.resolveLocked(user.FnOSGUID)
	if m.config.Isolation && known {
		if assigned, exists := m.config.UserBindings[identity.Username]; exists {
			if !valid || m.config.UserBindingUIDs[identity.Username] != identity.UID {
				return nil, "", false
			}
			id = assigned
			a = m.adapters[id]
			ok = a != nil
		}
	}
	if !ok {
		return nil, "", false
	}
	for _, row := range m.config.Accounts {
		if row.ID == id {
			sum := sha256.Sum256([]byte(row.BridgeURL + ":" + row.CacheToken))
			return a, id + ":" + hex.EncodeToString(sum[:8]), true
		}
	}
	return nil, "", false
}
