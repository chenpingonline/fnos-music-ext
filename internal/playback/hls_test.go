package playback

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample(t *testing.T) string {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("FFmpeg required for playback integration tests")
	}
	p := filepath.Join(t.TempDir(), "input.flac")
	if raw, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=9", "-ac", "2", "-c:a", "flac", p).CombinedOutput(); err != nil {
		t.Fatal(err, string(raw))
	}
	return p
}
func TestRealAACFMP4AndLifecycle(t *testing.T) {
	input := sample(t)
	binary, _ := exec.LookPath("ffmpeg")
	m, err := New(context.Background(), binary)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	open := func(context.Context) (io.ReadCloser, error) { return os.Open(input) }
	key := Key("alice", "account", "guid", "high")
	s, err := m.Ensure(key, open)
	if err != nil {
		t.Fatal(err)
	}
	if same, err := m.Ensure(key, open); err != nil || same != s {
		t.Fatal("session not reused")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f, err := m.File(ctx, s, "preset.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(f)
	f.Close()
	if !strings.Contains(string(raw), "#EXT-X-MAP:") {
		t.Fatal("not fragmented MP4 HLS", string(raw))
	}
	select {
	case <-s.done:
	case <-ctx.Done():
		t.Fatal("transcode did not finish")
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(probe, "-v", "error", "-show_entries", "stream=codec_name,sample_rate,channels", "-of", "json", filepath.Join(s.dir, "preset.m3u8")).CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var info struct {
		Streams []struct {
			Codec    string `json:"codec_name"`
			Rate     string `json:"sample_rate"`
			Channels int    `json:"channels"`
		}
	}
	if json.Unmarshal(out, &info) != nil || len(info.Streams) != 1 || info.Streams[0].Codec != "aac" || info.Streams[0].Rate != "44100" || info.Streams[0].Channels != 2 {
		t.Fatal(string(out))
	}
	rewritten := string(Playlist(raw, "fakeguid"))
	if !strings.Contains(rewritten, `URI="/music/api/v1/track/hls/fakeguid/init.mp4"`) || !strings.Contains(rewritten, "/music/api/v1/track/hls/fakeguid/seg-") {
		t.Fatal(rewritten)
	}
	if Key("bob", "account", "guid", "high") == key || Key("alice", "other", "guid", "high") == key {
		t.Fatal("session isolation failed")
	}
	if _, err := m.File(ctx, s, "../input.flac"); err == nil {
		t.Fatal("traversal allowed")
	}
	if !m.Touch(key) {
		t.Fatal("heartbeat failed")
	}
	m.Quit(key)
	if m.Touch(key) {
		t.Fatal("quit retained session")
	}
	if _, err := os.Stat(s.dir); !os.IsNotExist(err) {
		t.Fatal("quit retained files")
	}
	s, err = m.Ensure(key, open)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	s.last = time.Now().Add(-2 * m.ttl)
	m.mu.Unlock()
	m.sweep()
	if m.Touch(key) {
		t.Fatal("expired session retained")
	}
}
func TestSessionCapacityCancellationAndFailure(t *testing.T) {
	binary, _ := exec.LookPath("ffmpeg")
	if binary == "" {
		t.Fatal("FFmpeg required")
	}
	m, err := New(context.Background(), binary)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	blocker := func(ctx context.Context) (io.ReadCloser, error) { <-ctx.Done(); return nil, ctx.Err() }
	for _, key := range []string{"a", "b"} {
		if _, err := m.Ensure(key, blocker); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Ensure("c", blocker); err != ErrCapacity {
		t.Fatal("concurrency limit missing", err)
	}
	m.Quit("a")
	m.Quit("b")
	bad, _ := m.Ensure("bad", func(context.Context) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("not audio")), nil })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := m.File(ctx, bad, "preset.m3u8"); err == nil {
		t.Fatal("invalid input reported ready")
	}
	m.Close()
	if _, err := os.Stat(m.root); !os.IsNotExist(err) {
		t.Fatal("shutdown retained directory")
	}
}

func TestSeekRequiredMP4AndDiskBudget(t *testing.T) {
	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "tail-moov.m4a")
	if out, err := exec.Command(binary, "-v", "error", "-f", "lavfi", "-i", "sine=duration=5", "-c:a", "alac", input).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	m, err := New(context.Background(), binary)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s, err := m.Ensure("mp4", func(context.Context) (io.ReadCloser, error) { return os.Open(input) })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f, err := m.File(ctx, s, "preset.m3u8")
	if err != nil {
		t.Fatal("nonseekable MP4 conversion failed", err)
	}
	f.Close()
	<-s.done
	m.mu.Lock()
	m.maxBytes = 1
	m.mu.Unlock()
	m.sweep()
	if m.Touch("mp4") {
		t.Fatal("disk budget did not evict session")
	}
}
