package library

import (
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

func directory(path string, create bool) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("请选择绝对目录")
	}
	path = filepath.Clean(path)
	// Resolve the existing prefix so a symlink cannot hide a system directory.
	current := path
	tail := []string{}
	for {
		_, e := os.Stat(current)
		if e == nil {
			break
		}
		if !os.IsNotExist(e) || current == filepath.Dir(current) {
			return "", e
		}
		tail = append(tail, filepath.Base(current))
		current = filepath.Dir(current)
	}
	real, e := filepath.EvalSymlinks(current)
	if e != nil {
		return "", e
	}
	for i := len(tail) - 1; i >= 0; i-- {
		real = filepath.Join(real, tail[i])
	}
	for _, blocked := range []string{"/etc", "/proc", "/sys", "/dev", "/run", "/usr", "/bin", "/sbin", "/boot", "/var/run", "/private/etc", "/private/var/run"} {
		if real == blocked || strings.HasPrefix(real, blocked+"/") {
			return "", errors.New("不能将系统目录用于音乐存储")
		}
	}
	if real == "/" {
		return "", errors.New("不能使用根目录")
	}
	if create {
		if e = os.MkdirAll(real, 0750); e != nil {
			return "", e
		}
	}
	info, e := os.Stat(real)
	if e != nil || !info.IsDir() {
		return "", errors.New("目录不存在或不是文件夹")
	}
	if create {
		f, e := os.CreateTemp(real, ".fnmusic-write-check-*")
		if e != nil {
			return "", errors.New("目录不可写")
		}
		f.Close()
		os.Remove(f.Name())
	}
	return real, nil
}
func DetectLibraryDir() (string, error) {
	dirs := map[string]bool{}
	for _, path := range sharedLibraryPaths() {
		dirs[path] = true
	}
	for _, base := range []string{"/var/apps/trim.music/shares", "/var/apps/trim.music/share"} {
		entries, _ := os.ReadDir(base)
		for _, e := range entries {
			p, err := filepath.EvalSymlinks(filepath.Join(base, e.Name()))
			if err != nil {
				continue
			}
			if info, err := os.Stat(p); err == nil && info.IsDir() {
				dirs[p] = true
			}
		}
	}
	all := []string{}
	for p := range dirs {
		all = append(all, p)
	}
	sort.Strings(all)
	if len(all) == 1 {
		return all[0], nil
	}
	return "", errors.New("无法唯一确定官方共享曲库，请选择下载目录")
}
func (s *Service) directories(c preferences.Config, needDownload bool) (string, string, error) {
	cache := c.CacheDir
	if cache == "" {
		cache = filepath.Join(s.root, "audio-cache")
	}
	cache, e := directory(cache, true)
	if e != nil {
		return "", "", e
	}
	dl := c.DownloadDir
	if dl == "" && needDownload {
		dl, e = DetectLibraryDir()
		if e != nil {
			return "", "", e
		}
	}
	if dl != "" {
		dl, e = directory(dl, needDownload)
		if e != nil {
			return "", "", e
		}
		if preferences.Overlap(cache, dl) {
			return "", "", errors.New("缓存与下载目录不能相同或互为父子目录")
		}
	}
	private := filepath.Join(cache, ".fnmusic-go")
	if e = os.MkdirAll(private, 0700); e != nil {
		return "", "", e
	}
	if info, e := os.Lstat(private); e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.New("缓存工作目录无效")
	}
	alias, closePrivate, e := pinDirectory(private)
	if e != nil {
		return "", "", e
	}
	defer closePrivate()
	f, e := os.Open(alias)
	if e != nil {
		return "", "", e
	}
	defer f.Close()
	stat, e := f.Stat()
	if e != nil {
		return "", "", e
	}
	owner, ok := stat.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() {
		return "", "", errors.New("缓存工作目录不属于当前服务用户，请选择其他目录")
	}
	if e = f.Chmod(0700); e != nil {
		return "", "", e
	}
	return private, dl, nil
}
func owned(path, root string) bool {
	relative, e := filepath.Rel(root, path)
	if e != nil || relative == "." || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
		return false
	}
	info, e := os.Lstat(path)
	return e == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}
