package accounts

import (
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"github.com/chenpingonline/fnos-music-ext/internal/userdir"
	"path/filepath"
	"testing"
)

func TestUsernameAssignmentWaitsForVerifiedMusicIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "accounts.json")
	build := func(s string) (provider.Adapter, error) { return fakeAdapter{name: s}, nil }
	m, err := New(path, build)
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(name string) (userdir.User, bool) {
		return userdir.User{Username: "alice", UID: "1000"}, name == "alice"
	}
	m.lookupUser = lookup
	if err := m.Add(Account{ID: "a", Name: "A", BridgeURL: "http://127.0.0.1:8770"}); err != nil {
		t.Fatal(err)
	}
	if err := m.BindUsername("alice", "a"); err != nil {
		t.Fatal(err)
	}
	for _, guid := range []string{"1000", "music-alice", "music-bob"} {
		if _, _, ok := m.ResolveUser(provider.User{FnOSGUID: guid, Verified: true}); ok {
			t.Fatal("UID or unknown GUID treated as mapped", guid)
		}
	}
	if err := m.ObserveMusicIdentity("music-alice", "alice", "1000"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := m.ResolveUser(provider.User{FnOSGUID: "music-alice", Verified: true}); !ok {
		t.Fatal("verified identity not resolved")
	}
	if _, _, ok := m.ResolveUser(provider.User{FnOSGUID: "music-alice"}); ok {
		t.Fatal("unverified user resolved")
	}
	reloaded, err := New(path, build)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.lookupUser = lookup
	if _, _, ok := reloaded.ResolveUser(provider.User{FnOSGUID: "music-alice", Verified: true}); !ok {
		t.Fatal("mapping did not persist")
	}
	reloaded.lookupUser = func(string) (userdir.User, bool) { return userdir.User{Username: "alice", UID: "1001"}, true }
	if _, _, ok := reloaded.ResolveUser(provider.User{FnOSGUID: "music-alice", Verified: true}); ok {
		t.Fatal("reused username/changed UID reused private binding")
	}
	if err := m.BindUsername("bob", "a"); err == nil {
		t.Fatal("private account assigned to multiple users")
	}
	snap := m.Snapshot()
	snap.UserBindings["alice"] = "bad"
	delete(snap.Identities, "music-alice")
	if m.Snapshot().UserBindings["alice"] != "a" || len(m.Snapshot().Identities) != 1 {
		t.Fatal("snapshot aliases internal maps")
	}
	if err := m.BindUsername("alice", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := m.ResolveUser(provider.User{FnOSGUID: "music-alice", Verified: true}); ok {
		t.Fatal("removed assignment still resolves")
	}
}
