//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/chenpingonline/fnmusic-go/internal/admin"
	"github.com/chenpingonline/fnmusic-go/internal/gateway"
	"github.com/chenpingonline/fnmusic-go/internal/takeover"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fnmusic-go:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: fnmusic-go serve|status|restore|version [flags]")
	}
	if args[0] == "version" {
		fmt.Println("fnmusic-go v0.1.0")
		return nil
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	target := f.String("target", "/var/run/trim_music.socket", "official socket path")
	upstream := f.String("upstream", "/var/run/trim_music_upstream.socket", "original service socket path")
	stateDir := f.String("state-dir", "/run/fnmusic-go", "owned journal and private staging directory")
	officialExe := f.String("official-exe", "trim-music", "trusted official process executable name")
	adminSocket := f.String("admin-socket", "", "fnOS desktop gateway socket (serve only)")
	gatewayUID := f.Int("gateway-uid", 0, "trusted fnOS gateway process UID; default 0")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	cfg := takeover.Config{Target: *target, Upstream: *upstream, StateDir: *stateDir, OfficialExe: *officialExe}
	switch args[0] {
	case "status":
		b, _ := json.MarshalIndent(takeover.Inspect(cfg), "", "  ")
		fmt.Println(string(b))
		return nil
	case "restore":
		return takeover.Restore(cfg)
	case "serve":
		return serve(cfg, *adminSocket, *gatewayUID)
	default:
		return fmt.Errorf("unknown command %s", strconv.Quote(args[0]))
	}
}
func serve(cfg takeover.Config, adminPath string, trustedUID int) error {
	if adminPath == "" {
		return errors.New("--admin-socket must be provided, expected within app target directory")
	}
	if err := os.MkdirAll(cfg.StateDir, 0700); err != nil {
		return err
	}
	tempDir, err := os.MkdirTemp(cfg.StateDir, "stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	stage := filepath.Join(tempDir, "music.sock")
	ln, err := net.Listen("unix", stage)
	if err != nil {
		return err
	}
	uds := ln.(*net.UnixListener)
	uds.SetUnlinkOnClose(false)
	defer ln.Close()
	_ = os.Chmod(stage, 0660)
	// The public official socket must retain the same accessibility as the original one;
	// socket mode is promoted after the official socket has been safely staged.
	gw := gateway.New(cfg.Upstream)
	defer gw.Close()
	musicSrv := &http.Server{Handler: gw, ReadHeaderTimeout: 10 * time.Second}
	musicDone := make(chan error, 1)
	go func() { musicDone <- musicSrv.Serve(ln) }()
	if e := probe(stage); e != nil {
		return fmt.Errorf("staged proxy failed health probe: %w", e)
	}
	if e := removeStaleAdminSocket(adminPath); e != nil {
		return e
	}
	al, err := net.Listen("unix", adminPath)
	if err != nil {
		return err
	}
	admUds := al.(*net.UnixListener)
	admUds.SetUnlinkOnClose(false)
	defer func() { _ = al.Close(); _ = os.Remove(adminPath) }()
	// Socket mode alone is not authentication: each accepted peer is checked via SO_PEERCRED.
	if err = os.Chmod(adminPath, 0666); err != nil {
		return err
	}
	admSrv := &http.Server{Handler: admin.New(admin.Config{Socket: cfg}), ReadHeaderTimeout: 10 * time.Second}
	admDone := make(chan error, 1)
	go func() { admDone <- admSrv.Serve(onlyUIDListener{Listener: al, uid: uint32(trustedUID)}) }()
	// Before publish the proxy is live on a private staging pathname.
	// chmod 0666 matches the official fnOS gateway accessibility; this is safe only
	// after ownership and identity checks and while the root-only supervisor controls it.
	_ = os.Chmod(stage, 0666)
	if err = takeover.Publish(cfg, stage); err != nil {
		return fmt.Errorf("safe socket takeover failed: %w", err)
	}
	fmt.Fprintln(os.Stderr, "fnmusic-go: official socket safely replaced; ctrl-c / SIGTERM restores it")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var serviceErr error
	select {
	case <-ctx.Done():
	case e := <-musicDone:
		if !errors.Is(e, http.ErrServerClosed) {
			serviceErr = e
		}
	case e := <-admDone:
		if !errors.Is(e, http.ErrServerClosed) {
			serviceErr = e
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = musicSrv.Shutdown(shutdownCtx)
	_ = admSrv.Shutdown(shutdownCtx)
	cancel()
	if err = takeover.Restore(cfg); err != nil {
		return fmt.Errorf("CRITICAL: socket restoration failed: %w", err)
	}
	return serviceErr
}
func probe(socket string) error {
	t := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	client := &http.Client{Timeout: 2 * time.Second, Transport: t}
	defer t.CloseIdleConnections()
	resp, e := client.Get("http://local/_ext/livez")
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("liveness returned %s", resp.Status)
	}
	return nil
}

// onlyUIDListener rejects forged admin headers from non-gateway local processes.
type onlyUIDListener struct {
	net.Listener
	uid uint32
}

func (l onlyUIDListener) Accept() (net.Conn, error) {
	for {
		c, e := l.Listener.Accept()
		if e != nil {
			return nil, e
		}
		u, ok := c.(*net.UnixConn)
		if !ok {
			_ = c.Close()
			continue
		}
		raw, e := u.SyscallConn()
		if e != nil {
			_ = c.Close()
			continue
		}
		var got *syscall.Ucred
		var credErr error
		e = raw.Control(func(fd uintptr) {
			got, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		})
		if e != nil || credErr != nil || got == nil || got.Uid != l.uid {
			_ = c.Close()
			continue
		}
		return c, nil
	}
}

// removeStaleAdminSocket only removes an owned socket which no longer has a
// listener. It never deletes an active listener, symlink or ordinary file.
func removeStaleAdminSocket(path string) error {
	st, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if st.Mode()&os.ModeSocket == 0 || st.Mode()&os.ModeSymlink != 0 {
		return errors.New("admin path is not an owned socket")
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(sys.Uid) != os.Geteuid() {
		return errors.New("admin socket owner mismatch")
	}
	c, e := net.DialTimeout("unix", path, 300*time.Millisecond)
	if e == nil {
		_ = c.Close()
		return errors.New("another admin socket listener is active")
	}
	if !errors.Is(e, syscall.ECONNREFUSED) {
		return fmt.Errorf("cannot verify stale admin socket: %w", e)
	}
	return os.Remove(path)
}
