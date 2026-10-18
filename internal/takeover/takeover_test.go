//go:build linux

package takeover

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func testConfig(t *testing.T) (Config, *net.UnixListener, *net.UnixListener, string) {
	t.Helper()
	root := t.TempDir()
	target := filepath.Join(root, "trim_music.socket")
	upstream := filepath.Join(root, "trim_music_upstream.socket")
	a, e := net.Listen("unix", target)
	if e != nil {
		t.Fatal(e)
	}
	stage := filepath.Join(root, "staged.sock")
	b, e := net.Listen("unix", stage)
	if e != nil {
		t.Fatal(e)
	}
	for _, l := range []net.Listener{a, b} {
		l.(*net.UnixListener).SetUnlinkOnClose(false)
	}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	return Config{Target: target, Upstream: upstream, StateDir: filepath.Join(root, "state"), OfficialExe: filepath.Base(exe)}, a.(*net.UnixListener), b.(*net.UnixListener), stage
}
func TestPublishAndRestore(t *testing.T) {
	c, official, proxy, stage := testConfig(t)
	defer official.Close()
	defer proxy.Close()
	if e := Publish(c, stage); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Lstat(c.Upstream); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Lstat(c.Target); e != nil {
		t.Fatal(e)
	}
	if e := Restore(c); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(c.Upstream); !os.IsNotExist(e) {
		t.Fatalf("upstream not cleared: %v", e)
	}
	if _, e := os.Stat(c.Target); e != nil {
		t.Fatalf("official not restored: %v", e)
	}
	if _, e := os.Stat(journalPath(c)); !os.IsNotExist(e) {
		t.Fatalf("journal not cleared: %v", e)
	}
}
func TestRefusesUnknownOfficial(t *testing.T) {
	c, official, proxy, stage := testConfig(t)
	defer official.Close()
	defer proxy.Close()
	c.OfficialExe = "intentionally-wrong-name"
	e := Publish(c, stage)
	if !errors.Is(e, ErrUnsafe) {
		t.Fatalf("expected unsafe, got %v", e)
	}
	if _, e := os.Stat(c.Target); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(c.Upstream); !os.IsNotExist(e) {
		t.Fatalf("must not move official, got %v", e)
	}
}
func TestRefusesOverwriteOrStrangerOnRestore(t *testing.T) {
	c, official, proxy, stage := testConfig(t)
	defer official.Close()
	defer proxy.Close()
	extra, e := net.Listen("unix", c.Upstream)
	if e != nil {
		t.Fatal(e)
	}
	if e := Publish(c, stage); !errors.Is(e, ErrUnsafe) {
		t.Fatalf("must not overwrite existing upstream: %v", e)
	}
	extra.Close()
	if e := Publish(c, stage); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(c.Target); e != nil {
		t.Fatal(e)
	}
	stranger, e := net.Listen("unix", c.Target)
	if e != nil {
		t.Fatal(e)
	}
	stranger.(*net.UnixListener).SetUnlinkOnClose(false)
	if e := Restore(c); !errors.Is(e, ErrUnsafe) {
		t.Fatalf("must protect unknown target: %v", e)
	}
	stranger.Close()
	_ = os.Remove(c.Target)
	if e := Restore(c); e != nil {
		t.Fatal(e)
	}
}
