package netease

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoginDraftCredentialsStayInMemoryUntilCommit(t *testing.T) {
	n, err := NewLoginDraft("native://netease/draft")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "netease-sessions", "draft.json")
	if err := n.persistCookie("MUSIC_U=test-only"); err != nil {
		t.Fatal(err)
	}
	if n.session != "" {
		t.Fatal("draft has a persistence path")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("credentials written before commit")
	}
	if err := n.CommitLogin(dir, "draft"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credentials not private", err)
	}
	n.RollbackLogin()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("rollback retained credentials")
	}
	if n.session != "" {
		t.Fatal("rollback retained persistence target")
	}
	if err := n.CommitLogin(dir, "draft"); err != nil {
		t.Fatal("retry after rollback failed", err)
	}
	reloaded, err := NewNative("native://netease/draft", dir)
	if err != nil || reloaded.cookie != "MUSIC_U=test-only" {
		t.Fatal("committed credentials not reloadable", err)
	}
}
