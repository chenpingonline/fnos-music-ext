package accounts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

type fakeAdapter struct{ name string }

func (a fakeAdapter) ID() string                 { return a.name }
func (fakeAdapter) Health(context.Context) error { return nil }
func (fakeAdapter) Search(context.Context, provider.User, provider.SearchRequest) (provider.SearchPage, error) {
	return provider.SearchPage{}, nil
}
func (fakeAdapter) GetTrack(context.Context, provider.User, string) (provider.Track, error) {
	return provider.Track{}, nil
}
func (fakeAdapter) Play(context.Context, provider.User, provider.PlaybackRequest) (provider.Stream, error) {
	return provider.Stream{}, nil
}
func (fakeAdapter) GetLyrics(context.Context, provider.User, string) (provider.Lyrics, error) {
	return provider.Lyrics{}, nil
}
func (fakeAdapter) ListPlaylists(context.Context, provider.User) ([]provider.Playlist, error) {
	return nil, nil
}
func build(url string) (provider.Adapter, error) { return fakeAdapter{url}, nil }
func TestIsolationSharedAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "accounts.json")
	m, e := New(path, build)
	if e != nil {
		t.Fatal(e)
	}
	if !m.Snapshot().Isolation {
		t.Fatal("default must be isolated")
	}
	for _, a := range []Account{{ID: "alice", Name: "Alice", BridgeURL: "native://netease/alice"}, {ID: "bob", Name: "Bob", BridgeURL: "native://netease/bob"}} {
		if e := m.Add(a); e != nil {
			t.Fatal(e)
		}
	}
	if e := m.Bind("fn-alice", "alice"); e != nil {
		t.Fatal(e)
	}
	if e := m.Bind("fn-bob", "bob"); e != nil {
		t.Fatal(e)
	}
	if _, _, ok := m.Resolve("fn-charlie"); ok {
		t.Fatal("unbound user has an account")
	}
	if a, id, ok := m.Resolve("fn-alice"); !ok || id != "alice" || a.ID() != "native://netease/alice" {
		t.Fatal("wrong isolated account")
	}
	if e := m.SetShared("bob"); e != nil {
		t.Fatal(e)
	}
	if e := m.SetIsolation(false); e != nil {
		t.Fatal(e)
	}
	for _, user := range []string{"fn-alice", "fn-bob", "fn-charlie"} {
		if _, id, ok := m.Resolve(user); !ok || id != "bob" {
			t.Fatal("shared mode failed for", user)
		}
	}
	if _, _, ok := m.Resolve("shared"); ok {
		t.Fatal("unverified shared identity bypass")
	}
	stat, e := os.Stat(path)
	if e != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("permissions %v: %v", e, stat)
	}
	loaded, e := New(path, build)
	if e != nil {
		t.Fatal(e)
	}
	if loaded.Snapshot().Isolation {
		t.Fatal("mode did not persist")
	}
	if _, id, ok := loaded.Resolve("fn-charlie"); !ok || id != "bob" {
		t.Fatal("shared selection lost after restart")
	}
	if e := loaded.SetIsolation(true); e != nil {
		t.Fatal(e)
	}
	if _, _, ok := loaded.Resolve("fn-charlie"); ok {
		t.Fatal("binding was not restored")
	}
	if _, id, ok := loaded.Resolve("fn-alice"); !ok || id != "alice" {
		t.Fatal("isolated binding lost")
	}
	if e := loaded.Remove("alice"); e != nil {
		t.Fatal(e)
	}
	if _, _, ok := loaded.Resolve("fn-alice"); ok {
		t.Fatal("removed account still selected")
	}
	if _, ok := loaded.Snapshot().Bindings["fn-alice"]; ok {
		t.Fatal("orphan binding")
	}
}
func TestRejectAmbiguousAndInvalid(t *testing.T) {
	m, e := New(filepath.Join(t.TempDir(), "private", "accounts.json"), nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Add(Account{ID: "first", Name: "A", BridgeURL: "native://netease/first"}); e != nil {
		t.Fatal(e)
	}
	if e = m.Add(Account{ID: "first", Name: "B", BridgeURL: "native://netease/first"}); !errors.Is(e, ErrConflict) {
		t.Fatal("duplicate account accepted", e)
	}
	if e = m.Add(Account{ID: "attack", Name: "Bad", BridgeURL: "http://192.168.1.1:8770"}); e == nil {
		t.Fatal("non-local URL accepted")
	}
	if e = m.SetShared("missing"); e == nil {
		t.Fatal("unknown account accepted")
	}
	if e = m.Bind("fn-alice", "first"); e != nil {
		t.Fatal(e)
	}
	if e = m.Bind("fn-bob", "first"); !errors.Is(e, ErrConflict) {
		t.Fatal("private account reused", e)
	}
	if len(m.Snapshot().Accounts) != 1 {
		t.Fatal("invalid mutation committed")
	}
}
func TestConcurrentSwitching(t *testing.T) {
	m, e := New(filepath.Join(t.TempDir(), "private", "accounts.json"), build)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Add(Account{ID: "a", Name: "A", BridgeURL: "native://netease/a"}); e != nil {
		t.Fatal(e)
	}
	if e = m.Bind("user", "a"); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if i%2 == 0 {
					m.Resolve("user")
					m.Snapshot()
				} else {
					_ = m.SetIsolation(j%2 == 0)
					_ = m.SetShared("a")
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestNativeAccountsAreIndependentAndRevoked(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	m, e := New(filepath.Join(root, "accounts.json"), nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Add(Account{ID: "home", Name: "Home", BridgeURL: "native://netease/home"}); e != nil {
		t.Fatal(e)
	}
	if e = m.Add(Account{ID: "guest", Name: "Guest", BridgeURL: "native://netease/guest"}); e != nil {
		t.Fatal(e)
	}
	if e = m.Add(Account{ID: "bad", Name: "Bad", BridgeURL: "native://netease/else"}); e == nil {
		t.Fatal("native account id mismatch accepted")
	}
	if e = m.Bind("user-home", "home"); e != nil {
		t.Fatal(e)
	}
	if e = m.Bind("user-guest", "guest"); e != nil {
		t.Fatal(e)
	}
	a, id, ok := m.ResolveScoped("user-home")
	if !ok || id == "" || a.ID() != "netease" {
		t.Fatal("native account routing", id)
	}
	b, other, ok := m.ResolveScoped("user-guest")
	if !ok || other == id || b.ID() != "netease" {
		t.Fatal("native sessions not isolated")
	}
	state := filepath.Join(root, "netease-sessions", "home.json")
	if e = os.MkdirAll(filepath.Dir(state), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(state, []byte(`{"cookie":"MUSIC_U=stale"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e = m.Remove("home"); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(state); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("session persisted after account revocation %v", e)
	}
	if _, _, ok = m.Resolve("user-home"); ok {
		t.Fatal("removed native account still selected")
	}
}

func TestReaddedNativeAccountHasFreshCacheScope(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	m, e := New(filepath.Join(root, "accounts.json"), nil)
	if e != nil {
		t.Fatal(e)
	}
	a := Account{ID: "home", Name: "Home", BridgeURL: "native://netease/home"}
	if e = m.Add(a); e != nil {
		t.Fatal(e)
	}
	if e = m.Bind("user", "home"); e != nil {
		t.Fatal(e)
	}
	_, before, ok := m.ResolveScoped("user")
	if !ok {
		t.Fatal("missing account")
	}
	if e = m.Remove("home"); e != nil {
		t.Fatal(e)
	}
	if e = m.Add(a); e != nil {
		t.Fatal(e)
	}
	if e = m.Bind("user", "home"); e != nil {
		t.Fatal(e)
	}
	_, after, ok := m.ResolveScoped("user")
	if !ok || before == after {
		t.Fatalf("account ID reuse leaked cache scope %q", before)
	}
}

func TestRemovedBridgeConfigurationIsRejectedWithoutMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	raw := []byte(`{"isolation":true,"accounts":[{"id":"old","name":"Old bridge","bridge_url":"http://127.0.0.1:8770"}],"bindings":{}}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	_, err := New(path, func(string) (provider.Adapter, error) { called = true; return fakeAdapter{}, nil })
	if !errors.Is(err, ErrInvalid) || called {
		t.Fatal("bridge configuration reached builder", err, called)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(raw) {
		t.Fatal("rejected configuration was modified", err)
	}
}
