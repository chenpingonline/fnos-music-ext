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

func TestPersonalSelectionsPersistenceAndIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store, _ := New(path)
	cfg := store.Snapshot()
	cfg.PersonalSelections = map[string][]string{"alice": {"901"}, "bob": {}}
	if err := store.Update(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.PersonalSelections["alice"][0] = "changed"
	snap := store.Snapshot()
	if snap.PersonalSelections["alice"][0] != "901" {
		t.Fatal("update retained caller data")
	}
	snap.PersonalSelections["alice"][0] = "changed-again"
	if store.Snapshot().PersonalSelections["alice"][0] != "901" {
		t.Fatal("snapshot exposed mutable data")
	}
	reloaded, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if ids, ok := reloaded.Snapshot().PersonalSelections["bob"]; !ok || len(ids) != 0 {
		t.Fatal("empty selection was lost")
	}
	cfg = reloaded.Snapshot()
	cfg.PersonalSelections["alice"] = []string{"901", "901"}
	if err := store.Update(cfg); err == nil {
		t.Fatal("duplicate IDs accepted")
	}
}

func TestSourceSelectionPersistsEmptyAndCopiesSlices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sources.json")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Snapshot()
	c.SearchSources = []string{}
	c.PlaylistSources = []string{"qqmusic"}
	if err = s.Update(c); err != nil {
		t.Fatal(err)
	}
	c.PlaylistSources[0] = "netease"
	if s.Snapshot().PlaylistSources[0] != "qqmusic" {
		t.Fatal("mutable policy alias")
	}
	s, err = New(path)
	if err != nil || s.Snapshot().SearchSources == nil || len(s.Snapshot().SearchSources) != 0 {
		t.Fatal("empty source selection lost", err)
	}
	snap := s.Snapshot()
	snap.PlaylistSources[0] = "netease"
	if s.Snapshot().PlaylistSources[0] != "qqmusic" {
		t.Fatal("mutable snapshot")
	}
	for _, bad := range [][]string{{"unknown"}, {"qqmusic", "qqmusic"}} {
		c = s.Snapshot()
		c.SearchSources = bad
		if s.Update(c) == nil {
			t.Fatal("invalid source accepted")
		}
	}
}
