package library

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

type testSource struct {
	provider.Adapter
	play  func(provider.PlaybackRequest) (provider.Stream, error)
	cover []byte
}

func (s *testSource) Play(_ context.Context, _ provider.User, r provider.PlaybackRequest) (provider.Stream, error) {
	return s.play(r)
}
func (s *testSource) GetLyrics(context.Context, provider.User, string) (provider.Lyrics, error) {
	return provider.Lyrics{LRC: "[00:00.00]hello"}, nil
}
func (s *testSource) OpenCover(context.Context, provider.User, string) (provider.Stream, error) {
	return provider.Stream{Body: io.NopCloser(bytes.NewReader(s.cover)), StatusCode: 200}, nil
}
func fixture(t *testing.T, s *testSource) (*Service, *preferences.Store) {
	t.Helper()
	root := t.TempDir()
	settings, e := preferences.New(filepath.Join(root, "settings.json"))
	if e != nil {
		t.Fatal(e)
	}
	media, e := New(root, settings, func(u, scope string) (provider.Adapter, bool) { return s, u == "alice" && scope == "account-a" })
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(media.Close)
	return media, settings
}

var alice = provider.User{FnOSGUID: "alice", Verified: true}

func begin(t *testing.T, s *Service, id, purpose, mode string) Job {
	t.Helper()
	j, e := s.Start(context.Background(), alice, "account-a", provider.Track{ID: id, Title: "测试歌曲", Artists: []string{"测试歌手"}, Playable: true}, purpose, mode)
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func await(t *testing.T, s *Service, id, state string) Job {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := s.Lookup(id, "alice", "account-a")
		if j.State == state {
			return j
		}
		if j.State == "failed" && state != "failed" {
			t.Fatalf("task failed: %s", j.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("did not reach %s: %+v", state, s.Snapshot())
	return Job{}
}
func audio() []byte {
	b := make([]byte, 1644)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 8000)
	binary.LittleEndian.PutUint32(b[28:], 16000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], 1600)
	return b
}
func full(b []byte) provider.Stream {
	return provider.Stream{StatusCode: 200, ContentLength: int64(len(b)), Body: io.NopCloser(bytes.NewReader(b))}
}
func TestResumePinsQualityAndRejectsPartialRange(t *testing.T) {
	b := audio()
	var mu sync.Mutex
	calls := []provider.PlaybackRequest{}
	s, _ := fixture(t, &testSource{play: func(r provider.PlaybackRequest) (provider.Stream, error) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r)
		if len(calls) == 1 {
			v := full(b[:20])
			v.ETag = `"version-1"`
			v.ContentLength = int64(len(b))
			return v, nil
		}
		if r.Range != "bytes=20-" || r.Quality != "jymaster" || r.IfRange != `"version-1"` {
			return provider.Stream{}, errors.New("bad resume")
		}
		return provider.Stream{StatusCode: 206, ContentLength: int64(len(b) - 20), ContentRange: fmt.Sprintf("bytes 20-%d/%d", len(b)-1, len(b)), Body: io.NopCloser(bytes.NewReader(b[20:]))}, nil
	}})
	j := begin(t, s, "1", "cache", "high")
	await(t, s, j.ID, "failed")
	if e := s.Retry(j.ID); e != nil {
		t.Fatal(e)
	}
	j = await(t, s, j.ID, "completed")
	got, e := os.ReadFile(j.Path)
	if e != nil || !bytes.Equal(got, b) {
		t.Fatal("resumed file differs", e)
	}
	if _, ok := s.Lookup(j.ID, "bob", "account-a"); ok {
		t.Fatal("cross-user task visible")
	}
}
func TestIncomplete206CannotPublish(t *testing.T) {
	s, _ := fixture(t, &testSource{play: func(provider.PlaybackRequest) (provider.Stream, error) {
		return provider.Stream{StatusCode: 206, ContentLength: 20, ContentRange: "bytes 0-19/103", Body: io.NopCloser(bytes.NewReader(audio()[:20]))}, nil
	}})
	j := begin(t, s, "1", "cache", "high")
	j = await(t, s, j.ID, "failed")
	if !strings.Contains(j.Error, "范围") || j.Path != "" {
		t.Fatalf("published incomplete range: %+v", j)
	}
}
func TestCacheEvictionAndSourceIsolation(t *testing.T) {
	s, c := fixture(t, &testSource{play: func(provider.PlaybackRequest) (provider.Stream, error) { return full(audio()), nil }})
	cfg := c.Snapshot()
	cfg.CacheMax = 1
	if e := c.Update(cfg); e != nil {
		t.Fatal(e)
	}
	first := await(t, s, begin(t, s, "1", "cache", "high").ID, "completed")
	time.Sleep(20 * time.Millisecond)
	await(t, s, begin(t, s, "2", "cache", "high").ID, "completed")
	if _, e := os.Stat(first.Path); !os.IsNotExist(e) {
		t.Fatal("old cache retained", e)
	}
	if _, e := s.Start(context.Background(), provider.User{FnOSGUID: "bob", Verified: true}, "account-a", provider.Track{ID: "3"}, "cache", "high"); e == nil {
		t.Fatal("cross-user source accepted")
	}
}
func TestNoOverwriteAndUnsafePaths(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source")
	dst := filepath.Join(root, "target")
	os.WriteFile(src, []byte("new"), 0600)
	os.WriteFile(dst, []byte("user music"), 0600)
	if copyExclusive(src, dst) == nil {
		t.Fatal("overwrote user file")
	}
	os.Symlink(dst, filepath.Join(root, "alias"))
	if copyExclusive(src, filepath.Join(root, "alias")) == nil {
		t.Fatal("followed link")
	}
	if _, e := directory("/etc/music-test", true); e == nil {
		t.Fatal("system path allowed")
	}
	if len(safeName("{title}", provider.Track{Title: strings.Repeat("歌", 150)})) > 200 {
		t.Fatal("filename too long")
	}
}
func TestLedgerRemoveReaddAndPersistence(t *testing.T) {
	root := t.TempDir()
	l, e := NewLedger(root)
	if e != nil {
		t.Fatal(e)
	}
	track := provider.Track{ID: "1"}
	if e = l.Set("alice", "scope", "favorites", track, true, true); e != nil {
		t.Fatal(e)
	}
	old, _ := l.Get("alice", "scope", "favorites", "1")
	l.Set("alice", "scope", "favorites", track, false, false)
	l.Set("alice", "scope", "favorites", track, true, true)
	if l.CommitBinding("alice", "scope", "favorites", "1", old.Revision, "official") {
		t.Fatal("stale bind committed")
	}
	current, _ := l.Get("alice", "scope", "favorites", "1")
	if !l.CommitBinding("alice", "scope", "favorites", "1", current.Revision, "official") {
		t.Fatal("current bind rejected")
	}
	restored, e := NewLedger(root)
	if e != nil {
		t.Fatal(e)
	}
	entry, ok := restored.Get("alice", "scope", "favorites", "1")
	if !ok || entry.Bound != "official" || len(restored.List("bob", "scope", "favorites")) != 0 {
		t.Fatal("ledger isolation/persistence failed")
	}
}
func TestSaveMP3CoverAndLyricsWithFFmpeg(t *testing.T) {
	if FFmpeg() == "" {
		t.Skip("ffmpeg unavailable")
	}
	probe, e := exec.LookPath("ffprobe")
	if e != nil {
		t.Skip("ffprobe unavailable")
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "input.flac")
	cmd := exec.Command(FFmpeg(), "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "flac", input)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture: %s %v", out, e)
	}
	b, _ := os.ReadFile(input)
	broken := filepath.Join(dir, "broken.flac")
	os.WriteFile(broken, b[:len(b)/2], 0600)
	if validateMedia(context.Background(), broken) == nil {
		t.Fatal("truncated FLAC passed decoder check")
	}
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	var cover bytes.Buffer
	png.Encode(&cover, img)
	s, c := fixture(t, &testSource{cover: cover.Bytes(), play: func(provider.PlaybackRequest) (provider.Stream, error) { return full(b), nil }})
	cfg := c.Snapshot()
	cfg.SaveEnabled = true
	cfg.AutoLyrics = true
	cfg.DownloadDir = filepath.Join(dir, "library")
	if e = s.Apply(cfg); e != nil {
		t.Fatal(e)
	}
	j := await(t, s, begin(t, s, "9", "save", "standard").ID, "completed")
	if j.Warning != "" {
		t.Fatal(j.Warning)
	}
	out, e := exec.Command(probe, "-v", "error", "-show_streams", "-show_format", "-of", "json", j.Path).Output()
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		Streams []struct {
			Codec       string `json:"codec_name"`
			BitRate     string `json:"bit_rate"`
			Disposition struct {
				Attached int `json:"attached_pic"`
			} `json:"disposition"`
		}
		Format struct {
			Tags map[string]string `json:"tags"`
		}
	}
	if json.Unmarshal(out, &v) != nil {
		t.Fatal(string(out))
	}
	foundAudio, foundCover := false, false
	for _, st := range v.Streams {
		if st.Codec == "mp3" && st.BitRate == "320000" {
			foundAudio = true
		}
		if st.Disposition.Attached == 1 {
			foundCover = true
		}
	}
	if !foundAudio || !foundCover || v.Format.Tags["title"] != "测试歌曲" {
		t.Fatalf("missing streams/tags: %s", out)
	}
	if _, e = os.Stat(strings.TrimSuffix(j.Path, ".mp3") + ".lrc"); e != nil {
		t.Fatal(e)
	}
	coverPath := filepath.Join(dir, "cover.png")
	os.WriteFile(coverPath, cover.Bytes(), 0600)
	flac := filepath.Join(dir, "tagged.flac")
	if e = embedCover(context.Background(), input, coverPath, flac, provider.Track{Title: "FLAC cover"}); e != nil {
		t.Fatal(e)
	}
	picture, e := exec.Command(probe, "-v", "error", "-select_streams", "v", "-show_entries", "stream_disposition=attached_pic", "-of", "json", flac).Output()
	if e != nil || !strings.Contains(string(picture), `"attached_pic": 1`) {
		t.Fatalf("FLAC cover missing: %s %v", picture, e)
	}
	if entries, _ := os.ReadDir(j.CacheRoot); len(entries) > 1 {
		t.Fatalf("temporary audio leaked: %v", entries)
	}
}

func TestRestartPreservesPartialAndResolvedQuality(t *testing.T) {
	root := t.TempDir()
	settings, _ := preferences.New(filepath.Join(root, "settings.json"))
	b := audio()
	first := &testSource{play: func(provider.PlaybackRequest) (provider.Stream, error) {
		v := full(b[:20])
		v.ETag = `"version-1"`
		v.ContentLength = int64(len(b))
		return v, nil
	}}
	resolve := func(string, string) (provider.Adapter, bool) { return first, true }
	service, e := New(root, settings, resolve)
	if e != nil {
		t.Fatal(e)
	}
	j := begin(t, service, "99", "cache", "high")
	await(t, service, j.ID, "failed")
	service.Close()
	resumed := &testSource{play: func(r provider.PlaybackRequest) (provider.Stream, error) {
		if r.Range != "bytes=20-" || r.Quality != "jymaster" || r.IfRange != `"version-1"` {
			return provider.Stream{}, errors.New("lost resume metadata")
		}
		return provider.Stream{StatusCode: 206, ContentLength: int64(len(b) - 20), ContentRange: fmt.Sprintf("bytes 20-%d/%d", len(b)-1, len(b)), Body: io.NopCloser(bytes.NewReader(b[20:]))}, nil
	}}
	restored, e := New(root, settings, func(string, string) (provider.Adapter, bool) { return resumed, true })
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	if e = restored.Retry(j.ID); e != nil {
		t.Fatal(e)
	}
	saved := await(t, restored, j.ID, "completed")
	got, e := os.ReadFile(saved.Path)
	if e != nil || !bytes.Equal(got, b) {
		t.Fatal("restart corrupted audio", e)
	}
}

func TestChangedValidatorRestartsInsteadOfSplicing(t *testing.T) {
	b := audio()
	var mu sync.Mutex
	calls := 0
	s, _ := fixture(t, &testSource{play: func(r provider.PlaybackRequest) (provider.Stream, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		switch calls {
		case 1:
			v := full(b[:20])
			v.ContentLength = int64(len(b))
			v.ETag = `"old"`
			return v, nil
		case 2:
			if r.IfRange != `"old"` {
				return provider.Stream{}, errors.New("missing If-Range")
			}
			v := full(b[20:])
			v.ETag = `"new"`
			v.StatusCode = 206
			v.ContentRange = fmt.Sprintf("bytes 20-%d/%d", len(b)-1, len(b))
			return v, nil
		default:
			if r.Range != "" || r.IfRange != "" {
				return provider.Stream{}, errors.New("did not restart")
			}
			v := full(b)
			v.ETag = `"new"`
			return v, nil
		}
	}})
	j := begin(t, s, "8", "cache", "high")
	await(t, s, j.ID, "failed")
	s.Retry(j.ID)
	await(t, s, j.ID, "completed")
	mu.Lock()
	defer mu.Unlock()
	if calls != 3 {
		t.Fatal("changed audio was spliced", calls)
	}
}

func TestTaskPersistenceFailureWarnsAndRetries(t *testing.T) {
	s, _ := fixture(t, &testSource{play: func(provider.PlaybackRequest) (provider.Stream, error) { return full(audio()), nil }})
	j := begin(t, s, "persist-retry", "cache", "high")
	j = await(t, s, j.ID, "completed")
	state := filepath.Join(s.root, "audio-jobs.json")
	backup := state + ".backup"
	if e := os.Rename(state, backup); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(state, 0700); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	err := s.persistLocked()
	s.mu.Unlock()
	if err == nil {
		t.Fatal("persistence failure ignored")
	}
	rows := s.Snapshot()
	if len(rows) == 0 || !strings.Contains(rows[0].Warning, "尚未写入磁盘") {
		t.Fatal("missing durability warning", rows)
	}
	if e := os.Remove(state); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	s.lastPersistAttempt = time.Now().Add(-time.Minute)
	s.mu.Unlock()
	s.signal()
	deadline := time.Now().Add(2 * time.Second)
	recovered := false
	for time.Now().Before(deadline) {
		s.mu.Lock()
		recovered = s.persistenceError == ""
		s.mu.Unlock()
		if recovered {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !recovered {
		t.Fatal("automatic retry failed")
	}
	if _, e := os.Stat(state); e != nil {
		t.Fatal("state not recovered", e)
	}
	if strings.Contains(s.Snapshot()[0].Warning, "尚未写入磁盘") {
		t.Fatal("warning not cleared")
	}
}
