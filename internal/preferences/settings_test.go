package preferences

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsPersistAndRejectInvalid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	s, e := New(p)
	if e != nil {
		t.Fatal(e)
	}
	c := s.Snapshot()
	c.Quality = "smooth"
	c.SearchTimeout = 5
	if e = s.Update(c); e != nil {
		t.Fatal(e)
	}
	s, e = New(p)
	if e != nil || s.Snapshot().Quality != "smooth" {
		t.Fatal("reload", e)
	}
	c.CacheDir = "/vol/music"
	c.DownloadDir = "/vol/music/download"
	if s.Update(c) == nil {
		t.Fatal("nested paths accepted")
	}
	c = s.Snapshot()
	c.ScanPath = "/../../etc/passwd"
	if s.Update(c) == nil {
		t.Fatal("invalid scan accepted")
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0600 {
		t.Fatal("settings not private")
	}
}
func TestSnapshotsDoNotShareSelections(t *testing.T) {
	s, _ := New("")
	c := s.Snapshot()
	c.Public = []Selection{{ID: "1", Title: "test"}}
	s.Update(c)
	c.Public[0].Title = "changed"
	snap := s.Snapshot()
	snap.Public[0].Title = "also changed"
	if s.Snapshot().Public[0].Title != "test" {
		t.Fatal("shared settings slice")
	}
}

func TestPersonalPlaylistsLegacyDefaultAndPersistedChoice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"quality":"high"}`), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if !store.Snapshot().PersonalPlaylists {
		t.Fatal("legacy settings lost existing personal playlists")
	}
	config := store.Snapshot()
	config.PersonalPlaylists = false
	if err := store.Update(config); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(path)
	if err != nil || reloaded.Snapshot().PersonalPlaylists {
		t.Fatal("explicit disabled choice did not persist", err)
	}
}
