//go:build linux

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestGatewayTrust(t *testing.T) {
	if !trustedGatewayPeer(&syscall.Ucred{Uid: 0}, -1) {
		t.Fatal("root rejected")
	}
	if trustedGatewayPeer(&syscall.Ucred{Pid: 2147483647, Uid: 1000}, -1) {
		t.Fatal("unknown peer accepted")
	}
	if !trustedGatewayPeer(&syscall.Ucred{Uid: 1000}, 1000) || trustedGatewayPeer(&syscall.Ucred{Uid: 1001}, 1000) {
		t.Fatal("UID override ignored")
	}
}
func TestNginxWorkerRequiresRootMaster(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned executable fixture requires root")
	}
	proc := t.TempDir()
	exe := filepath.Join(proc, "nginx")
	if err := os.WriteFile(exe, []byte("fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []string{"100", "101"} {
		dir := filepath.Join(proc, pid)
		os.Mkdir(dir, 0700)
		os.Symlink(exe, filepath.Join(dir, "exe"))
	}
	os.WriteFile(filepath.Join(proc, "100/status"), []byte("PPid:\t1\nUid:\t0\t0\t0\t0\n"), 0600)
	worker := filepath.Join(proc, "101/status")
	os.WriteFile(worker, []byte("PPid:\t100\nUid:\t999\t999\t999\t999\n"), 0600)
	if !nginxWorker(proc, 101, 999) {
		t.Fatal("valid worker rejected")
	}
	if nginxWorker(proc, 101, 1000) {
		t.Fatal("wrong worker UID accepted")
	}
	os.WriteFile(filepath.Join(proc, "100/status"), []byte("PPid:\t1\nUid:\t999\t999\t999\t999\n"), 0600)
	if nginxWorker(proc, 101, 999) {
		t.Fatal("unprivileged nginx master accepted")
	}
	os.WriteFile(filepath.Join(proc, "100/status"), []byte("PPid:\t1\nUid:\t0\t0\t0\t0\n"), 0600)
	os.Chmod(exe, 0777)
	if nginxWorker(proc, 101, 999) {
		t.Fatal("writable executable accepted")
	}
}
