//go:build linux

package library

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedDirectorySurvivesRenameAndRejectsSymlink(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(root, "audio"), []byte("original"), 0600)
	alias, closeDir, e := pinDirectory(root)
	if e != nil {
		t.Fatal(e)
	}
	defer closeDir()
	old := root + "-old"
	os.Rename(root, old)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "audio"), []byte("secret"), 0600)
	os.Symlink(outside, root)
	b, e := os.ReadFile(filepath.Join(alias, "audio"))
	if e != nil || string(b) != "original" {
		t.Fatal("directory pin escaped", e)
	}
	if _, closer, e := pinDirectory(root); e == nil {
		closer()
		t.Fatal("directory symlink accepted")
	}
	os.Symlink(filepath.Join(outside, "audio"), filepath.Join(old, "link"))
	if f, e := noFollowOpen(filepath.Join(alias, "link"), os.O_RDONLY, 0); e == nil {
		f.Close()
		t.Fatal("file symlink accepted")
	}
}
