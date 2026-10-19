//go:build linux

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The gateway worker UID varies between fnOS installations. In automatic mode,
// require a root-owned nginx executable and a live root nginx master parent.
// Merely running the system nginx binary as a local user is not sufficient.
func trustedGatewayPeer(peer *syscall.Ucred, override int) bool {
	if peer.Uid == 0 {
		return true
	}
	if override >= 0 {
		return peer.Uid == uint32(override)
	}
	return nginxWorker("/proc", int(peer.Pid), peer.Uid)
}

func nginxWorker(proc string, pid int, uid uint32) bool {
	dir := filepath.Join(proc, strconv.Itoa(pid))
	worker, err := os.Stat(filepath.Join(dir, "exe"))
	if err != nil || !rootExecutable(worker) {
		return false
	}
	exe, err := os.Readlink(filepath.Join(dir, "exe"))
	if err != nil || filepath.Base(exe) != "nginx" {
		return false
	}
	status, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		return false
	}
	parent, effectiveUID := processIdentity(string(status))
	if parent <= 1 || effectiveUID != int64(uid) {
		return false
	}
	masterDir := filepath.Join(proc, strconv.Itoa(parent))
	master, err := os.Stat(filepath.Join(masterDir, "exe"))
	if err != nil || !os.SameFile(worker, master) {
		return false
	}
	status, err = os.ReadFile(filepath.Join(masterDir, "status"))
	if err != nil {
		return false
	}
	_, effectiveUID = processIdentity(string(status))
	return effectiveUID == 0
}

func rootExecutable(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0 && info.Mode().IsRegular() && info.Mode().Perm()&0022 == 0
}

func processIdentity(status string) (parent int, uid int64) {
	parent, uid = -1, -1
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "PPid:" {
			parent, _ = strconv.Atoi(fields[1])
		}
		if len(fields) >= 3 && fields[0] == "Uid:" {
			value, err := strconv.ParseInt(fields[2], 10, 64)
			if err == nil {
				uid = value
			}
		}
	}
	return
}

func adminHealth(socket string) error {
	if socket == "" {
		return fmt.Errorf("--admin-socket is required")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, "http://local/app/fnmusic-go/api/ready", nil)
	req.Header.Set("X-Trim-Isadmin", "true")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness returned %s", resp.Status)
	}
	return nil
}
