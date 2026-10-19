package playback

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

func TestPlaybackFailureStagesAndNoCredentialLeak(t *testing.T) {
	script := filepath.Join(t.TempDir(), "ffmpeg")
	// Deliberately include access-bearing data in stderr: only category/exit may escape.
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' 'Unknown encoder aac https://secret.example/?token=private MUSIC_U=private' >&2\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	m, err := New(context.Background(), script)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	cases := []struct {
		name, stage, reason, raw string
		source                   error
		exit                     int
	}{
		{name: "entitlement", stage: "source", reason: "not-entitled", source: provider.ErrNotEntitled},
		{name: "http", stage: "source", reason: "http-403", source: &diagnostics.HTTPError{Status: 403}},
		{name: "network", stage: "source", reason: "timeout", source: context.DeadlineExceeded},
		{name: "empty", stage: "read", reason: "empty-audio"},
		{name: "unknown", stage: "probe", reason: "unrecognized-audio", raw: "<html>private error</html>"},
		{name: "ffmpeg", stage: "transcode", reason: "missing-codec", raw: "fLaC1234567890", exit: 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := m.Ensure(tc.name, func(context.Context) (io.ReadCloser, error) {
				if tc.source != nil {
					return nil, tc.source
				}
				return io.NopCloser(strings.NewReader(tc.raw)), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			f, err := m.File(ctx, s, "preset.m3u8")
			if f != nil {
				f.Close()
				t.Fatal("failure was ready")
			}
			var detail *Failure
			if !errors.As(err, &detail) || detail.Stage != tc.stage || detail.Reason != tc.reason || detail.ExitCode != tc.exit {
				t.Fatalf("unexpected detail: %#v %v", detail, err)
			}
			raw, _ := json.Marshal(detail)
			for _, v := range []string{string(raw), Message(err)} {
				if strings.Contains(v, "private") || strings.Contains(v, "secret.example") {
					t.Fatal("credential leak", v)
				}
			}
		})
	}
}

func TestManifestWaitsForSuccessfulCompletion(t *testing.T) {
	dir := t.TempDir()
	s := &Session{key: "one", dir: dir, done: make(chan struct{})}
	m := &Manager{sessions: map[string]*Session{"one": s}, maxBytes: 1 << 20}
	path := filepath.Join(dir, "preset.m3u8")
	partial := "#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXTINF:4,\nseg-000000.m4s\n"
	if err := os.WriteFile(path, []byte(partial), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if f, err := m.File(ctx, s, "preset.m3u8"); !errors.Is(err, context.DeadlineExceeded) {
		if f != nil {
			f.Close()
		}
		t.Fatal("partial manifest published", err)
	}
	complete := partial + "#EXTINF:3,\nseg-000001.m4s\n#EXT-X-ENDLIST\n"
	if err := os.WriteFile(path, []byte(complete), 0600); err != nil {
		t.Fatal(err)
	}
	close(s.done)
	f, err := m.File(context.Background(), s, "preset.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(f)
	f.Close()
	out := string(Playlist(raw, "guid"))
	if !strings.Contains(out, "#EXT-X-PLAYLIST-TYPE:VOD") || !strings.Contains(out, "seg-000001.m4s") || !strings.Contains(out, "#EXT-X-ENDLIST") {
		t.Fatal(out)
	}
	s.err = &Failure{Stage: "transcode", Reason: "invalid-audio"}
	if f, err := m.File(context.Background(), s, "preset.m3u8"); err == nil {
		f.Close()
		t.Fatal("failed session published manifest")
	}
}

func TestStderrCategoriesAndBound(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"Error: encrypted audio", "encrypted-audio"},
		{"Invalid data found when processing input", "invalid-audio"},
		{"No space left on device", "disk-full"},
		{"unrecognized failure", "ffmpeg-failed"},
	} {
		b := &boundedStderr{}
		b.Write([]byte(tc.raw))
		if b.reason() != tc.want {
			t.Fatal(b.reason(), tc.want)
		}
	}
	b := &boundedStderr{}
	if n, err := b.Write([]byte(strings.Repeat("x", 20000))); err != nil || n != 20000 || len(b.data) != 8192 {
		t.Fatal(n, err, len(b.data))
	}
}

// FFmpeg can exit zero after emitting an error and writing a partial playlist.
func TestDecoderErrorsNeverPublishReadyEvenWithOutput(t *testing.T) {
	for _, tc := range []struct{ name, stderr, reason, exit string }{
		{"encrypted", "decryption key missing https://secret.example/?token=private", "encrypted-audio", "3"},
		{"truncated-zero-exit", "Stream ends prematurely at byte 42", "invalid-audio", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := filepath.Join(t.TempDir(), "ffmpeg")
			body := "#!/bin/sh\ncat >/dev/null\nprintf '#EXTM3U\\n#EXT-X-ENDLIST\\n' > preset.m3u8\nprintf '%s\\n' '" + tc.stderr + "' >&2\nexit " + tc.exit + "\n"
			if err := os.WriteFile(script, []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			m, err := New(context.Background(), script)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			s, err := m.Ensure("key", func(context.Context) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("fLaC1234567890")), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			f, err := m.File(ctx, s, "preset.m3u8")
			if f != nil {
				f.Close()
				t.Fatal("decoder error was ready")
			}
			var detail *Failure
			if !errors.As(err, &detail) || detail.Reason != tc.reason {
				t.Fatalf("reason lost: %v", err)
			}
		})
	}
}
