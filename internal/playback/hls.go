// Package playback owns private, bounded AAC/fMP4 playback sessions.
package playback

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
)

var ErrUnavailable = errors.New("播放转码不可用，请检查 FFmpeg 和音源")
var ErrCapacity = errors.New("转码播放会话已满，请稍后重试")
var segment = regexp.MustCompile(`^seg-[0-9]{6}\.m4s$`)

func ValidFile(name string) bool {
	return name == "preset.m3u8" || name == "init.mp4" || segment.MatchString(name)
}

type Session struct {
	key, dir string
	cancel   context.CancelFunc
	done     chan struct{}
	last     time.Time
	err      error
}
type Manager struct {
	mu           sync.Mutex
	root, binary string
	ctx          context.Context
	cancel       context.CancelFunc
	sessions     map[string]*Session
	wg           sync.WaitGroup
	once         sync.Once
	ttl          time.Duration
	maxActive    int
	maxBytes     int64
}

func New(parent context.Context, binary string) (*Manager, error) {
	if binary == "" {
		return nil, ErrUnavailable
	}
	root, err := os.MkdirTemp("", "fnmusic-hls-")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	m := &Manager{root: root, binary: binary, ctx: ctx, cancel: cancel, sessions: map[string]*Session{}, ttl: 90 * time.Second, maxActive: 2, maxBytes: 512 << 20}
	m.wg.Add(1)
	go m.sweepLoop()
	return m, nil
}
func Key(user, scope, guid, quality string) string {
	sum := sha256.Sum256([]byte(user + "\x00" + scope + "\x00" + guid + "\x00" + quality))
	return hex.EncodeToString(sum[:])
}
func (m *Manager) Ensure(key string, open func(context.Context) (io.ReadCloser, error)) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	if s := m.sessions[key]; s != nil {
		s.last = time.Now()
		if s.err != nil {
			return nil, s.err
		}
		return s, nil
	}
	active := 0
	for _, s := range m.sessions {
		select {
		case <-s.done:
		default:
			active++
		}
	}
	if active >= m.maxActive {
		return nil, ErrCapacity
	}
	dir, err := os.MkdirTemp(m.root, "session-")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.ctx, 2*time.Hour)
	s := &Session{key: key, dir: dir, cancel: cancel, done: make(chan struct{}), last: time.Now()}
	m.sessions[key] = s
	m.wg.Add(1)
	go m.run(ctx, s, open)
	return s, nil
}
func (m *Manager) run(ctx context.Context, s *Session, open func(context.Context) (io.ReadCloser, error)) {
	defer m.wg.Done()
	defer close(s.done)
	defer s.cancel()
	err := func() error {
		input, err := open(ctx)
		if err != nil {
			return failure("source", err)
		}
		defer input.Close()
		stopClose := context.AfterFunc(ctx, func() { input.Close() })
		defer stopClose()
		buffered := bufio.NewReader(input)
		prefix, peekErr := buffered.Peek(12)
		if peekErr != nil && !errors.Is(peekErr, io.EOF) {
			return failure("read", peekErr)
		}
		if len(prefix) == 0 {
			return &Failure{Stage: "read", Reason: "empty-audio"}
		}
		// Force a recognized audio demuxer. Never let an untrusted upstream
		// payload become a playlist that can reference arbitrary local files.
		demux := ""
		switch {
		case bytes.HasPrefix(prefix, []byte("fLaC")):
			demux = "flac"
		case bytes.HasPrefix(prefix, []byte("ID3")):
			demux = "mp3"
		case len(prefix) > 1 && prefix[0] == 0xff && prefix[1]&0xf6 == 0xf0:
			demux = "aac"
		case len(prefix) > 1 && prefix[0] == 0xff && prefix[1]&0xe0 == 0xe0:
			demux = "mp3"
		case len(prefix) >= 8 && string(prefix[4:8]) == "ftyp":
			demux = "mov"
		case bytes.HasPrefix(prefix, []byte("OggS")):
			demux = "ogg"
		case len(prefix) >= 12 && string(prefix[:4]) == "RIFF" && string(prefix[8:12]) == "WAVE":
			demux = "wav"
		default:
			return &Failure{Stage: "probe", Reason: "unrecognized-audio"}
		}
		inputPath := "pipe:0"
		// MP4 files with moov at the end require seeking. Spool those into the
		// private bounded session directory; MP3/FLAC still transcode as they arrive.
		if len(prefix) >= 8 && string(prefix[4:8]) == "ftyp" {
			inputPath = "input.m4a"
			spool, e := os.OpenFile(filepath.Join(s.dir, inputPath), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return e
			}
			n, e := io.Copy(spool, io.LimitReader(buffered, (512<<20)+1))
			ce := spool.Close()
			if e != nil {
				return failure("read", e)
			}
			if ce != nil {
				return ce
			}
			if n > 512<<20 {
				return &Failure{Stage: "spool", Reason: "input-too-large"}
			}
		}
		cmd := exec.CommandContext(ctx, m.binary, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-protocol_whitelist", "file,pipe", "-f", demux, "-i", inputPath, "-map", "0:a:0", "-vn", "-c:a", "aac", "-b:a", "128k", "-ac", "2", "-ar", "44100", "-f", "hls", "-hls_time", "4", "-hls_list_size", "0", "-hls_playlist_type", "event", "-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4", "-hls_segment_filename", "seg-%06d.m4s", "-hls_flags", "temp_file+independent_segments", "preset.m3u8")
		cmd.Dir = s.dir
		cmd.Env = append(os.Environ(), "GCONV_PATH=")
		cmd.Stdout = io.Discard
		stderr := &boundedStderr{}
		cmd.Stderr = stderr
		runError := func(err error) error {
			if err == nil {
				// Some demuxers log decode/truncation errors but still exit zero.
				if len(stderr.data) > 0 && stderr.reason() != "ffmpeg-failed" {
					return &Failure{Stage: "transcode", Reason: stderr.reason()}
				}
				return nil
			}
			if ctx.Err() != nil {
				return failure("transcode", ctx.Err())
			}
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return &Failure{Stage: "transcode", Reason: stderr.reason(), ExitCode: exit.ExitCode()}
			}
			return failure("start", err)
		}
		if inputPath != "pipe:0" {
			return runError(cmd.Run())
		}
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return failure("start", err)
		}
		if err = cmd.Start(); err != nil {
			stdin.Close()
			return failure("start", err)
		}
		copied := make(chan error, 1)
		go func() {
			n, e := io.Copy(stdin, io.LimitReader(buffered, (512<<20)+1))
			if n > 512<<20 {
				e = &Failure{Stage: "read", Reason: "input-too-large"}
			}
			stdin.Close()
			copied <- e
		}()
		waitErr := cmd.Wait()
		input.Close()
		copyErr := <-copied
		if waitErr != nil {
			// A source read failure is more useful than a secondary decoder error.
			if copyErr != nil && !errors.Is(copyErr, os.ErrClosed) && !errors.Is(copyErr, io.ErrClosedPipe) && !errors.Is(copyErr, syscall.EPIPE) && !errors.Is(copyErr, net.ErrClosed) {
				var f *Failure
				if errors.As(copyErr, &f) {
					return f
				}
				return failure("read", copyErr)
			}
			return runError(waitErr)
		}
		if copyErr != nil {
			var f *Failure
			if errors.As(copyErr, &f) {
				return f
			}
			return failure("read", copyErr)
		}
		return runError(nil)
	}()
	if err != nil {
		var f *Failure
		if !errors.As(err, &f) {
			f = failure("storage", err)
		}
		m.mu.Lock()
		s.err = f
		m.mu.Unlock()
		code := f.Reason
		if f.Stage == "transcode" && f.ExitCode != 0 {
			code += fmt.Sprintf("-exit-%d", f.ExitCode)
		}
		diagnostics.Record("playback-"+f.Stage, s.key, diagnostics.New(code))
	}
}
func (m *Manager) Touch(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[key]; s != nil {
		s.last = time.Now()
		return true
	}
	return false
}
func (m *Manager) Quit(key string) {
	m.mu.Lock()
	s := m.sessions[key]
	delete(m.sessions, key)
	if s != nil {
		s.cancel()
	}
	m.mu.Unlock()
	if s != nil {
		<-s.done
		os.RemoveAll(s.dir)
	}
}
func (m *Manager) File(ctx context.Context, s *Session, name string) (*os.File, error) {
	if !ValidFile(name) {
		return nil, os.ErrNotExist
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		alive := m.sessions[s.key] == s
		failure := s.err
		m.mu.Unlock()
		if !alive {
			return nil, os.ErrNotExist
		}
		if failure != nil {
			return nil, failure
		}
		f, err := os.Open(filepath.Join(s.dir, name))
		if err == nil {
			complete := true
			if name == "preset.m3u8" {
				select {
				case <-s.done:
					m.mu.Lock()
					failure = s.err
					m.mu.Unlock()
					if failure != nil {
						f.Close()
						return nil, failure
					}
				default:
					complete = false
				}
			}
			info, e := f.Stat()
			if complete && e == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() <= m.maxBytes {
				return f, nil
			}
			f.Close()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.done:
			m.mu.Lock()
			failure := s.err
			m.mu.Unlock()
			if failure != nil {
				return nil, failure
			}
			// Completion may race the initial open; retry once after FFmpeg has
			// atomically published its last files.
			f, e := os.Open(filepath.Join(s.dir, name))
			if e == nil {
				if info, e := f.Stat(); e == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() <= m.maxBytes {
					return f, nil
				}
				f.Close()
			}
			return nil, os.ErrNotExist
		case <-ticker.C:
		}
	}
}
func (m *Manager) sweepLoop() {
	defer m.wg.Done()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-tick.C:
			m.sweep()
		}
	}
}
func (m *Manager) sweep() {
	m.mu.Lock()
	var total int64
	var oldest *Session
	keys := []string{}
	for key, s := range m.sessions {
		var size int64
		filepath.WalkDir(s.dir, func(p string, d os.DirEntry, e error) error {
			if e == nil && !d.IsDir() {
				if i, e := d.Info(); e == nil {
					size += i.Size()
				}
			}
			return nil
		})
		total += size
		if s.err != nil || time.Since(s.last) > m.ttl || size > m.maxBytes {
			keys = append(keys, key)
		}
		if oldest == nil || s.last.Before(oldest.last) {
			oldest = s
		}
	}
	if total > m.maxBytes && oldest != nil {
		keys = append(keys, oldest.key)
	}
	m.mu.Unlock()
	for _, key := range keys {
		m.Quit(key)
	}
}
func (m *Manager) Close() {
	m.once.Do(func() {
		m.cancel()
		m.mu.Lock()
		for _, s := range m.sessions {
			s.cancel()
		}
		m.mu.Unlock()
		m.wg.Wait()
		os.RemoveAll(m.root)
	})
}

// Rewrite absolute client-facing URLs so no filesystem paths or account IDs leak.
func Playlist(raw []byte, guid string) []byte {
	// A completed EVENT playlist is immutable and safe for clients that fetch once.
	raw = bytes.ReplaceAll(raw, []byte("#EXT-X-PLAYLIST-TYPE:EVENT"), []byte("#EXT-X-PLAYLIST-TYPE:VOD"))
	lines := strings.Split(string(raw), "\n")
	prefix := "/music/api/v1/track/hls/" + guid + "/"
	for i, line := range lines {
		if ValidFile(line) && line != "preset.m3u8" {
			lines[i] = prefix + line
		}
		if strings.HasPrefix(line, "#EXT-X-MAP:") {
			lines[i] = strings.ReplaceAll(line, `URI="init.mp4"`, `URI="`+prefix+`init.mp4"`)
		}
	}
	return []byte(strings.Join(lines, "\n"))
}
