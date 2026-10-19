package library

import (
	"context"
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func coverFixture(t *testing.T, root string, settings *preferences.Store) *Service {
	t.Helper()
	s, e := New(root, settings, func(string, string) (provider.Adapter, bool) { return nil, false })
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return s
}
func pngCover(context.Context) (provider.Stream, error) {
	data := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 24)
	return provider.Stream{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data)), ContentLength: int64(len(data))}, nil
}
func TestCoverPersistsScopesAndStaleFallback(t *testing.T) {
	root := t.TempDir()
	settings, _ := preferences.New("")
	s := coverFixture(t, root, settings)
	stream, e := s.Cover(context.Background(), "alice:account:track", pngCover)
	if e != nil {
		t.Fatal(e)
	}
	stream.Body.Close()
	second := coverFixture(t, root, settings)
	unavailable := func(context.Context) (provider.Stream, error) { return provider.Stream{}, errors.New("offline") }
	stream, e = second.Cover(context.Background(), "alice:account:track", unavailable)
	if e != nil {
		t.Fatal("restart lost cache", e)
	}
	stream.Body.Close()
	if _, e = second.Cover(context.Background(), "bob:account:track", unavailable); e == nil {
		t.Fatal("cache crossed user scope")
	}
	stats, e := s.CoverCache(false)
	if e != nil || stats.Files != 1 {
		t.Fatal(stats, e)
	}
	path := filepath.Join(stats.Directory, coverFileName("alice:account:track"))
	record, ok := readCover(path)
	if !ok {
		t.Fatal("missing record")
	}
	record.Saved = time.Now().Add(-8 * 24 * time.Hour)
	if e = preferences.WriteJSON(path, record); e != nil {
		t.Fatal(e)
	}
	stream, e = second.Cover(context.Background(), "alice:account:track", unavailable)
	if e != nil {
		t.Fatal("stale art should survive outage", e)
	}
	stream.Body.Close()
}
func TestCoverClearPreservesUserFilesAndRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	settings, _ := preferences.New("")
	s := coverFixture(t, root, settings)
	stream, e := s.Cover(context.Background(), "a", pngCover)
	if e != nil {
		t.Fatal(e)
	}
	stream.Body.Close()
	stats, _ := s.CoverCache(false)
	userFile := filepath.Join(stats.Directory, "user.jpg")
	os.WriteFile(userFile, []byte("keep"), 0600)
	target := filepath.Join(root, "important.json")
	os.WriteFile(target, []byte("keep target"), 0600)
	link := filepath.Join(stats.Directory, coverFileName("link"))
	if e = os.Symlink(target, link); e != nil {
		t.Fatal(e)
	}
	cleared, e := s.CoverCache(true)
	if e != nil || cleared.Files != 1 || cleared.Bytes == 0 {
		t.Fatal(cleared, e)
	}
	for _, path := range []string{userFile, target, link} {
		if _, e = os.Lstat(path); e != nil {
			t.Fatal("cleaner removed unrelated/symlink file", path, e)
		}
	}
	if _, valid := readCover(link); valid {
		t.Fatal("read followed symlink")
	}
	again, _ := s.CoverCache(false)
	if again.Files != 0 {
		t.Fatal(again)
	}
}
func TestCoverClearPreventsInflightRepopulationAndDirectoryChange(t *testing.T) {
	root := t.TempDir()
	settings, _ := preferences.New("")
	s := coverFixture(t, root, settings)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		image, e := s.Cover(context.Background(), "inflight", func(ctx context.Context) (provider.Stream, error) { close(entered); <-release; return pngCover(ctx) })
		if e == nil {
			image.Body.Close()
		}
		done <- e
	}()
	<-entered
	if _, e := s.CoverCache(true); e != nil {
		t.Fatal(e)
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	stats, _ := s.CoverCache(false)
	if stats.Files != 0 {
		t.Fatal("inflight repopulated after clear")
	}
	cfg := settings.Snapshot()
	cfg.CoverCacheDir = filepath.Join(root, "custom")
	if e := s.Apply(cfg); e != nil {
		t.Fatal(e)
	}
	stats, e := s.CoverCache(false)
	expectedParent, pathErr := filepath.EvalSymlinks(cfg.CoverCacheDir)
	if e != nil || pathErr != nil || stats.Directory != filepath.Join(expectedParent, ".fnmusic-covers") {
		t.Fatal(stats, e)
	}
	cfg.CoverCacheDir = "/etc/cache"
	if e = s.Apply(cfg); e == nil {
		t.Fatal("system folder accepted")
	}
}
func TestCoverRejectsNonImagesAndEvictsOldest(t *testing.T) {
	settings, _ := preferences.New("")
	s := coverFixture(t, t.TempDir(), settings)
	_, e := s.Cover(context.Background(), "bad", func(context.Context) (provider.Stream, error) {
		return provider.Stream{StatusCode: 200, ContentType: "image/jpeg", Body: io.NopCloser(strings.NewReader("<html>error</html>"))}, nil
	})
	if e == nil {
		t.Fatal("cached HTML")
	}
	stats, _ := s.CoverCache(false)
	if stats.Files != 0 {
		t.Fatal(stats)
	}
	path := stats.Directory
	old := filepath.Join(path, coverFileName("old"))
	fresh := filepath.Join(path, coverFileName("new"))
	f, e := os.Create(old)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(coverLimit); e != nil {
		t.Fatal(e)
	}
	f.Close()
	os.Chtimes(old, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	os.WriteFile(fresh, []byte("new"), 0600)
	if e = trimCovers(path); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(old); !os.IsNotExist(e) {
		t.Fatal("oldest not evicted", e)
	}
	if _, e = os.Stat(fresh); e != nil {
		t.Fatal(e)
	}
}

func TestConcurrentCoversDownloadOnce(t *testing.T) {
	settings, _ := preferences.New("")
	s := coverFixture(t, t.TempDir(), settings)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	fetch := func(ctx context.Context) (provider.Stream, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return pngCover(ctx)
		case <-ctx.Done():
			return provider.Stream{}, ctx.Err()
		}
	}
	const requests = 16
	done := make(chan error, requests)
	for i := 0; i < requests; i++ {
		go func() {
			stream, err := s.Cover(context.Background(), "same", fetch)
			if err == nil {
				_, err = io.ReadAll(stream.Body)
				stream.Body.Close()
			}
			done <- err
		}()
	}
	<-entered
	time.Sleep(30 * time.Millisecond)
	close(release)
	for i := 0; i < requests; i++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate image downloads", calls.Load())
	}
}

func TestCoverBinaryStorageAndLegacyMigration(t *testing.T) {
	settings, _ := preferences.New("")
	s := coverFixture(t, t.TempDir(), settings)
	stats, _ := s.CoverCache(false)
	stream, _ := pngCover(context.Background())
	data, _ := io.ReadAll(stream.Body)
	stream.Body.Close()
	key := "legacy"
	legacy := filepath.Join(stats.Directory, coverFileName(key)+".json")
	if e := preferences.WriteJSON(legacy, coverRecord{Saved: time.Now(), Data: data}); e != nil {
		t.Fatal(e)
	}
	image, e := s.Cover(context.Background(), key, func(context.Context) (provider.Stream, error) {
		t.Error("legacy cache was discarded")
		return provider.Stream{}, errors.New("offline")
	})
	if e != nil {
		t.Fatal(e)
	}
	image.Body.Close()
	raw, e := os.ReadFile(filepath.Join(stats.Directory, coverFileName(key)))
	if e != nil || string(raw[:8]) != coverMagic || len(raw) != len(data)+16 {
		t.Fatal("not a compact image record", len(raw), e)
	}
	if _, e = os.Stat(legacy); !os.IsNotExist(e) {
		t.Fatal("legacy record not migrated", e)
	}
	image, e = s.Cover(context.Background(), "second", pngCover)
	if e != nil {
		t.Fatal(e)
	}
	image.Body.Close()
	actual, _ := s.CoverCache(false)
	s.coverMu.Lock()
	bytes, files := s.coverUsageBytes, s.coverUsageFiles
	s.coverMu.Unlock()
	if bytes != actual.Bytes || files != actual.Files {
		t.Fatal("incremental accounting mismatch", bytes, files, actual)
	}
}
