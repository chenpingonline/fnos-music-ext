//go:build !linux

package library

import (
	"os"
	"path/filepath"
	"syscall"
)

func pinDirectory(root string) (string, func(), error) {
	real, e := filepath.EvalSymlinks(root)
	if e != nil {
		return "", nil, e
	}
	return real, func() {}, nil
}
func noFollowOpen(path string, flags int, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, mode)
}
