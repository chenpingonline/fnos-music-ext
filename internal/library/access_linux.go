//go:build linux

package library

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Pin each canonical directory component with openat/O_NOFOLLOW. Shared NAS
// directories can be renamed by other users while a root download is running.
func pinDirectory(root string) (string, func(), error) {
	fd, e := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if e != nil {
		return "", nil, e
	}
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(root), "/"), "/") {
		if part == "" {
			continue
		}
		next, e := syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		syscall.Close(fd)
		if e != nil {
			return "", nil, e
		}
		fd = next
	}
	return fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), fd), func() { _ = syscall.Close(fd) }, nil
}
func noFollowOpen(path string, flags int, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, mode)
}
