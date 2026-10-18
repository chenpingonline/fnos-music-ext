//go:build linux

package takeover

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const renameNoReplace = 1

var ErrUnsafe = errors.New("socket takeover refused: unsafe or unverified state")

type Config struct{ Target, Upstream, StateDir, OfficialExe string }
type Identity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	PID    int    `json:"pid"`
	Exe    string `json:"exe"`
}
type Journal struct {
	Target   string    `json:"target"`
	Upstream string    `json:"upstream"`
	Official Identity  `json:"official"`
	Proxy    Identity  `json:"proxy"`
	Started  time.Time `json:"started"`
}

func (c Config) normalize() Config {
	if c.Target == "" {
		c.Target = "/var/run/trim_music.socket"
	}
	if c.Upstream == "" {
		c.Upstream = "/var/run/trim_music_upstream.socket"
	}
	if c.StateDir == "" {
		c.StateDir = "/run/fnmusic-go"
	}
	if c.OfficialExe == "" {
		c.OfficialExe = "trim-music"
	}
	return c
}
func renameNoClobber(src, dst string) error {
	if renameat2Syscall == 0 {
		return fmt.Errorf("renameat2 unavailable on architecture %s", runtime.GOARCH)
	}
	a, err := syscall.BytePtrFromString(src)
	if err != nil {
		return err
	}
	b, err := syscall.BytePtrFromString(dst)
	if err != nil {
		return err
	}
	const atFDCWD = ^uintptr(99)
	_, _, errno := syscall.RawSyscall6(renameat2Syscall, atFDCWD, uintptr(unsafe.Pointer(a)), atFDCWD, uintptr(unsafe.Pointer(b)), renameNoReplace, 0)
	runtime.KeepAlive(a)
	runtime.KeepAlive(b)
	if errno != 0 {
		return errno
	}
	return nil
}
func socketStat(path string) (Identity, error) {
	s, e := os.Lstat(path)
	if e != nil {
		return Identity{}, e
	}
	if s.Mode()&os.ModeSocket == 0 || s.Mode()&os.ModeSymlink != 0 {
		return Identity{}, fmt.Errorf("%w: not a socket %s", ErrUnsafe, path)
	}
	st, ok := s.Sys().(*syscall.Stat_t)
	if !ok {
		return Identity{}, ErrUnsafe
	}
	return Identity{Device: uint64(st.Dev), Inode: st.Ino}, nil
}
func same(a, b Identity) bool { return a.Device == b.Device && a.Inode == b.Inode }
func peerIdentity(path string) (Identity, error) {
	id, e := socketStat(path)
	if e != nil {
		return id, e
	}
	c, e := net.DialTimeout("unix", path, 900*time.Millisecond)
	if e != nil {
		return id, e
	}
	defer c.Close()
	conn, ok := c.(*net.UnixConn)
	if !ok {
		return id, ErrUnsafe
	}
	raw, e := conn.SyscallConn()
	if e != nil {
		return id, e
	}
	var pid int
	var credErr error
	e = raw.Control(func(fd uintptr) {
		var cr *syscall.Ucred
		cr, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if credErr == nil {
			pid = int(cr.Pid)
		}
	})
	if e != nil {
		return id, e
	}
	if credErr != nil {
		return id, credErr
	}
	exe, e := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if e != nil {
		return id, e
	}
	id.PID = pid
	id.Exe = exe
	fresh, e := socketStat(path)
	if e != nil || !same(fresh, id) {
		return Identity{}, fmt.Errorf("%w: socket changed during peer inspection", ErrUnsafe)
	}
	return id, nil
}
func lock(c Config) (func(), error) {
	if e := os.MkdirAll(c.StateDir, 0700); e != nil {
		return nil, e
	}
	st, e := os.Lstat(c.StateDir)
	if e != nil {
		return nil, e
	}
	statInfo, ok := st.Sys().(*syscall.Stat_t)
	if !st.IsDir() || st.Mode().Perm()&0022 != 0 || st.Mode()&os.ModeSymlink != 0 || !ok || int(statInfo.Uid) != os.Geteuid() {
		return nil, fmt.Errorf("%w: unsafe state directory", ErrUnsafe)
	}
	f, e := os.OpenFile(filepath.Join(c.StateDir, "operation.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); e != nil {
		f.Close()
		return nil, e
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
func journalPath(c Config) string { return filepath.Join(c.StateDir, "ownership.json") }
func readJournal(c Config) (Journal, error) {
	var j Journal
	p := journalPath(c)
	info, e := os.Lstat(p)
	if e != nil {
		return j, e
	}
	statInfo, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || int(statInfo.Uid) != os.Geteuid() || info.Mode().Perm()&0077 != 0 || statInfo.Nlink != 1 {
		return j, ErrUnsafe
	}
	fd, e := syscall.Open(p, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if e != nil {
		return j, e
	}
	f := os.NewFile(uintptr(fd), p)
	b, e := io.ReadAll(io.LimitReader(f, 65537))
	_ = f.Close()
	if len(b) > 65536 {
		return j, ErrUnsafe
	}
	if e != nil {
		return j, e
	}
	e = json.Unmarshal(b, &j)
	return j, e
}
func writeJournal(c Config, j Journal) error {
	b, e := json.MarshalIndent(j, "", "  ")
	if e != nil {
		return e
	}
	p := journalPath(c)
	f, e := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	return f.Close()
}
func allowedOfficial(id Identity, c Config) bool {
	return id.PID > 0 && filepath.Base(strings.TrimSuffix(id.Exe, " (deleted)")) == c.OfficialExe
}

// Publish requires an already listening private unix socket. It never removes an unknown socket.
func Publish(cfg Config, stage string) error {
	c := cfg.normalize()
	unlock, e := lock(c)
	if e != nil {
		return e
	}
	defer unlock()
	if _, e = os.Lstat(journalPath(c)); e == nil {
		return fmt.Errorf("%w: ownership journal already exists", ErrUnsafe)
	} else if !os.IsNotExist(e) {
		return e
	}
	if _, e = os.Lstat(c.Upstream); e == nil {
		return fmt.Errorf("%w: upstream path already exists", ErrUnsafe)
	} else if !os.IsNotExist(e) {
		return e
	}
	official, e := peerIdentity(c.Target)
	if e != nil {
		return fmt.Errorf("%w: cannot verify official: %v", ErrUnsafe, e)
	}
	if !allowedOfficial(official, c) {
		return fmt.Errorf("%w: expected %s, got %s", ErrUnsafe, c.OfficialExe, official.Exe)
	}
	proxy, e := peerIdentity(stage)
	if e != nil {
		return fmt.Errorf("%w: staged proxy unverified: %v", ErrUnsafe, e)
	}
	if proxy.PID != os.Getpid() {
		return fmt.Errorf("%w: staging socket is not owned by current process", ErrUnsafe)
	}
	j := Journal{Target: c.Target, Upstream: c.Upstream, Official: official, Proxy: proxy, Started: time.Now().UTC()}
	if e = writeJournal(c, j); e != nil {
		return e
	}
	if e = renameNoClobber(c.Target, c.Upstream); e != nil {
		_ = os.Remove(journalPath(c))
		return fmt.Errorf("move official: %w", e)
	}
	if e = renameNoClobber(stage, c.Target); e != nil {
		rb := renameNoClobber(c.Upstream, c.Target)
		if rb == nil {
			_ = os.Remove(journalPath(c))
		}
		return fmt.Errorf("publish proxy: %v (rollback: %v)", e, rb)
	}
	return nil
}

// Restore is conservative. A socket owned by an unknown service is never deleted.
func Restore(cfg Config) error {
	c := cfg.normalize()
	unlock, e := lock(c)
	if e != nil {
		return e
	}
	defer unlock()
	j, e := readJournal(c)
	if e != nil {
		return fmt.Errorf("%w: missing or corrupt journal: %v", ErrUnsafe, e)
	}
	if j.Target != c.Target || j.Upstream != c.Upstream {
		return fmt.Errorf("%w: journal path mismatch", ErrUnsafe)
	}
	upstream, e := peerIdentity(c.Upstream)
	if e != nil {
		return fmt.Errorf("%w: official unavailable: %v", ErrUnsafe, e)
	}
	if !allowedOfficial(upstream, c) {
		return fmt.Errorf("%w: upstream process not official", ErrUnsafe)
	}
	// A legitimate official process restart might change the inode; require a separate operator recovery.
	if !same(upstream, j.Official) {
		return fmt.Errorf("%w: official socket identity changed; manual recovery needed", ErrUnsafe)
	}
	target, e := socketStat(c.Target)
	if e == nil {
		if !same(target, j.Proxy) {
			return fmt.Errorf("%w: target socket not ours", ErrUnsafe)
		}
		active, probeErr := peerIdentity(c.Target)
		if probeErr == nil && active.PID == j.Proxy.PID && active.PID != os.Getpid() {
			return fmt.Errorf("%w: proxy is running; stop the service before manual restore", ErrUnsafe)
		}
		if probeErr == nil && active.PID != j.Proxy.PID {
			return fmt.Errorf("%w: live unknown service at target", ErrUnsafe)
		}
		if probeErr != nil && !errors.Is(probeErr, syscall.ECONNREFUSED) {
			return fmt.Errorf("%w: cannot verify proxy vacancy: %v", ErrUnsafe, probeErr)
		}
		if e = os.Remove(c.Target); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if e = renameNoClobber(c.Upstream, c.Target); e != nil {
		return fmt.Errorf("%w: restore rename failed: %v", ErrUnsafe, e)
	}
	restored, e := peerIdentity(c.Target)
	if e != nil || !same(restored, j.Official) {
		return fmt.Errorf("%w: restoration verification failed: %v", ErrUnsafe, e)
	}
	return os.Remove(journalPath(c))
}
func Inspect(cfg Config) map[string]any {
	c := cfg.normalize()
	out := map[string]any{"target": c.Target, "upstream": c.Upstream, "journal": false}
	if _, e := os.Stat(journalPath(c)); e == nil {
		out["journal"] = true
	}
	for name, path := range map[string]string{"target_state": c.Target, "upstream_state": c.Upstream} {
		if v, e := peerIdentity(path); e == nil {
			out[name] = map[string]any{"connected": true, "pid": v.PID, "exe": filepath.Base(v.Exe)}
		} else if os.IsNotExist(e) {
			out[name] = "absent"
		} else {
			out[name] = "unknown_or_stale"
		}
	}
	return out
}
