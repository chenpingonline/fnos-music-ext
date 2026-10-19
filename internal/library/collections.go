package library

import (
	"encoding/json"
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Entry struct {
	User     string `json:"user"`
	Scope    string `json:"scope"`
	Playlist string `json:"playlist"`

	Track    provider.Track `json:"track"`
	Created  time.Time      `json:"created_at"`
	Revision uint64         `json:"revision"`
	Bound    string         `json:"bound,omitempty"`
	Pending  bool           `json:"pending"`
	Error    string         `json:"error,omitempty"`
}
type Ledger struct {
	sequence uint64

	mu    sync.Mutex
	path  string
	items map[string]map[string]Entry
}

func NewLedger(root string) (*Ledger, error) {
	l := &Ledger{path: filepath.Join(root, "collections.json"), items: map[string]map[string]Entry{}}
	b, e := preferences.ReadPrivate(l.path)
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	if e == nil {
		if len(b) > 16<<20 {
			return nil, errors.New("collection registry too large")
		}
		if e = json.Unmarshal(b, &l.items); e != nil {
			return nil, e
		}
	}
	for _, bucket := range l.items {
		for _, entry := range bucket {
			if entry.Revision > l.sequence {
				l.sequence = entry.Revision
			}
		}
	}
	return l, nil
}
func bucket(user, scope, playlist string) string { return hash(user, scope, playlist) }
func (l *Ledger) List(user, scope, playlist string) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []Entry{}
	for _, e := range l.items[bucket(user, scope, playlist)] {
		out = append(out, e)
	}
	return out
}
func (l *Ledger) Get(user, scope, playlist, id string) (Entry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.items[bucket(user, scope, playlist)][id]
	return e, ok
}
func (l *Ledger) Set(user, scope, playlist string, t provider.Track, add, pending bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := bucket(user, scope, playlist)
	if len(l.items) >= 10000 && l.items[key] == nil {
		return errors.New("收藏记录达到上限")
	}
	if l.items[key] == nil {
		l.items[key] = map[string]Entry{}
	}
	_, existsBefore := l.items[key][t.ID]
	if add && !existsBefore && len(l.items[key]) >= 10000 {
		return errors.New("单个歌单达到上限")
	}
	old, exists := l.items[key][t.ID]
	if add {
		l.sequence++
		l.items[key][t.ID] = Entry{Track: t, Created: time.Now(), Revision: l.sequence, User: user, Scope: scope, Playlist: playlist, Pending: pending, Bound: old.Bound}
	} else {
		delete(l.items[key], t.ID)
	}
	if e := preferences.WriteJSON(l.path, l.items); e != nil {
		if exists {
			l.items[key][t.ID] = old
		} else {
			delete(l.items[key], t.ID)
		}
		return e
	}
	return nil
}

// CommitBinding returns false if the user removed/replaced this intent while an
// official request was in flight; the caller then compensates that write.
func (l *Ledger) CommitBinding(user, scope, playlist, id string, revision uint64, official string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := bucket(user, scope, playlist)
	e, ok := l.items[key][id]
	if !ok || e.Revision != revision || !e.Pending {
		return false
	}
	old := e
	e.Bound = official
	e.Pending = false
	e.Error = ""
	l.items[key][id] = e
	if preferences.WriteJSON(l.path, l.items) != nil {
		l.items[key][id] = old
		return false
	}
	return true
}
func (l *Ledger) BindingError(user, scope, playlist, id string, revision uint64, message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := bucket(user, scope, playlist)
	e, ok := l.items[key][id]
	if ok && e.Revision == revision {
		e.Error = message
		l.items[key][id] = e
		_ = preferences.WriteJSON(l.path, l.items)
	}
}

func (l *Ledger) Pending(user, scope, id string) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []Entry{}
	for _, bucket := range l.items {
		for _, e := range bucket {
			if e.User == user && e.Scope == scope && e.Track.ID == id && e.Pending {
				out = append(out, e)
			}
		}
	}
	return out
}
