package accounts

import (
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"github.com/chenpingonline/fnos-music-ext/internal/userdir"
	"path/filepath"
	"testing"
)

func TestGroupedSourcesStayPrivateAndRevokeImmediately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "accounts.json")
	m, err := New(path, build)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []Account{{ID: "ne", Name: "NE", BridgeURL: "native://netease/ne"}, {ID: "qq", Name: "QQ", BridgeURL: "native://qqmusic/qq"}, {ID: "ne2", Name: "Other", BridgeURL: "native://netease/ne2"}} {
		if err = m.Add(a); err != nil {
			t.Fatal(err)
		}
	}
	if err = m.Bind("alice", "ne"); err != nil {
		t.Fatal(err)
	}
	if err = m.SetGroup("ne", []string{"qq"}); err != nil {
		t.Fatal(err)
	}
	u := provider.User{FnOSGUID: "alice", Verified: true}
	sources := m.ResolveSources(u)
	if len(sources) != 2 {
		t.Fatal(sources)
	}
	if len(m.ResolveSources(provider.User{FnOSGUID: "bob", Verified: true})) != 0 || len(m.ResolveSources(provider.User{FnOSGUID: "alice"})) != 0 {
		t.Fatal("unauthorized access")
	}
	if err = m.Bind("bob", "qq"); err != ErrConflict {
		t.Fatal("shared private account accepted", err)
	}
	if err = m.SetGroup("ne", []string{"ne2"}); err != ErrInvalid {
		t.Fatal("same-platform ambiguity accepted", err)
	}
	if err = m.SetGroup("ne", []string{"qq", "qq"}); err != ErrInvalid {
		t.Fatal(err)
	}
	snap := m.Snapshot()
	snap.AccountGroups["ne"][0] = "ne2"
	if len(m.ResolveSources(u)) != 2 || m.Snapshot().AccountGroups["ne"][0] != "qq" {
		t.Fatal("snapshot mutated grants")
	}
	reloaded, err := New(path, build)
	if err != nil || len(reloaded.ResolveSources(u)) != 2 {
		t.Fatal(err)
	}
	oldScope := m.ResolveSources(u)[1].Scope
	if err = m.RotateLoginToken("qq"); err != nil {
		t.Fatal(err)
	}
	if m.ResolveSources(u)[1].Scope == oldScope {
		t.Fatal("group session cache did not rotate")
	}
	if err = m.SetGroup("ne", nil); err != nil {
		t.Fatal(err)
	}
	if len(m.ResolveSources(u)) != 1 {
		t.Fatal("revoked group still accessible")
	}
	if err = m.SetGroup("ne", []string{"qq"}); err != nil {
		t.Fatal(err)
	}
	if err = m.Remove("qq"); err != nil {
		t.Fatal(err)
	}
	if len(m.ResolveSources(u)) != 1 {
		t.Fatal("deleted account still accessible")
	}
}

func TestGroupedUsernameSourcesRequireCurrentUID(t *testing.T) {
	m, err := New(filepath.Join(t.TempDir(), "private", "accounts.json"), build)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []Account{{ID: "ne", Name: "NE", BridgeURL: "native://netease/ne"}, {ID: "qq", Name: "QQ", BridgeURL: "native://qqmusic/qq"}} {
		if err = m.Add(a); err != nil {
			t.Fatal(err)
		}
	}
	currentUID := "1000"
	m.lookupUser = func(name string) (userdir.User, bool) { return userdir.User{Username: name, UID: currentUID}, true }
	if err = m.BindUsername("alice", "ne"); err != nil {
		t.Fatal(err)
	}
	if err = m.SetGroup("ne", []string{"qq"}); err != nil {
		t.Fatal(err)
	}
	u := provider.User{FnOSGUID: "music-alice", Verified: true}
	if len(m.ResolveSources(u)) != 0 {
		t.Fatal("unconfirmed username gained access")
	}
	if err = m.ObserveMusicIdentity("music-alice", "alice", "1000"); err != nil {
		t.Fatal(err)
	}
	if len(m.ResolveSources(u)) != 2 {
		t.Fatal("verified group not resolved")
	}
	currentUID = "1001"
	if len(m.ResolveSources(u)) != 0 {
		t.Fatal("reused username inherited group")
	}
}
