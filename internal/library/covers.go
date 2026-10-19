package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

const coverLimit = 256 << 20
const coverTTL = 7 * 24 * time.Hour
const coverImageLimit = 6 << 20

type CoverStats struct {
	Directory string `json:"directory"`
	Files     int    `json:"files"`
	Bytes     int64  `json:"bytes"`
	MaxBytes  int64  `json:"max_bytes"`
	TTLDays   int    `json:"ttl_days"`
}
type coverRecord struct {
	Saved time.Time `json:"saved"`
	Data  []byte    `json:"data"`
}

// A dedicated, private subdirectory keeps cleaning separate from user files.
func (s *Service) coverDirectory(c preferences.Config) (string, error) {
	parent := c.CoverCacheDir
	if parent == "" {
		parent = s.root
	}
	_, statErr := os.Stat(parent)
	// A cache hit must not require a test write in its parent directory.
	parent, err := directory(parent, os.IsNotExist(statErr))
	if err != nil {
		return "", err
	}
	path := filepath.Join(parent, ".fnmusic-covers")
	if err = os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ok || int(stat.Uid) != os.Geteuid() {
		return "", errors.New("封面缓存工作目录无效或不属于当前服务用户")
	}
	alias, closeDir, err := pinDirectory(path)
	if err != nil {
		return "", err
	}
	defer closeDir()
	f, err := os.Open(alias)
	if err != nil {
		return "", err
	}
	defer f.Close()
	pinned, err := f.Stat()
	if err != nil || !os.SameFile(info, pinned) {
		return "", errors.New("封面缓存目录在检查期间发生变化")
	}
	if pinned.Mode().Perm() != 0700 {
		if err = f.Chmod(0700); err != nil {
			return "", err
		}
	}
	return path, nil
}
func coverFileName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:]) + ".cover"
}
func managedCover(name string) bool {
	raw := strings.TrimSuffix(strings.TrimSuffix(name, ".json"), ".cover")
	if name != raw+".cover" && name != raw+".cover.json" {
		return false
	}
	b, err := hex.DecodeString(raw)
	return err == nil && len(b) == 32 && strings.ToLower(raw) == raw
}
func coverStream(data []byte) provider.Stream {
	return provider.Stream{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), ContentType: http.DetectContentType(data), ContentLength: int64(len(data))}
}
func readCover(path string) (coverRecord, bool) {
	f, err := noFollowOpen(path, os.O_RDONLY, 0)
	if err != nil {
		return coverRecord{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 9<<20 {
		return coverRecord{}, false
	}
	var entry coverRecord
	header := make([]byte, 16)
	if _, err = io.ReadFull(f, header); err == nil && string(header[:8]) == coverMagic {
		entry.Saved = time.Unix(0, int64(binary.BigEndian.Uint64(header[8:])))
		entry.Data, err = io.ReadAll(io.LimitReader(f, coverImageLimit+1))
	} else {
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			return coverRecord{}, false
		}
		err = json.NewDecoder(io.LimitReader(f, 9<<20)).Decode(&entry)
	}
	valid := err == nil && !entry.Saved.IsZero() && len(entry.Data) > 0 && len(entry.Data) <= coverImageLimit && strings.HasPrefix(http.DetectContentType(entry.Data), "image/")
	return entry, valid
}

// Caller authenticates and verifies the current source/playlist visibility first.
// Failed refreshes can use validated stale art. Failed responses never enter disk.
func (s *Service) Cover(ctx context.Context, key string, fetch func(context.Context) (provider.Stream, error)) (provider.Stream, error) {
	s.coverMu.Lock()
	epoch := s.coverEpoch
	config := s.settings.Snapshot()
	s.coverMu.Unlock()
	flightKey := fmt.Sprintf("%s\x00%d\x00%s", config.CoverCacheDir, epoch, key)
	data, err := s.coverFlights.Do(ctx, flightKey, 20*time.Second, func(shared context.Context) ([]byte, error) {
		image, err := s.coverOnce(shared, key, fetch, epoch, config)
		if err != nil {
			return nil, err
		}
		defer image.Body.Close()
		return io.ReadAll(io.LimitReader(image.Body, coverImageLimit+1))
	})
	if err != nil {
		return provider.Stream{}, err
	}
	return coverStream(data), nil
}
func (s *Service) coverOnce(ctx context.Context, key string, fetch func(context.Context) (provider.Stream, error), epoch uint64, config preferences.Config) (provider.Stream, error) {
	s.coverMu.Lock()
	path, err := s.coverDirectory(config)
	diagnostics.Record("cover-cache-directory", key, err)
	var stale coverRecord
	var valid bool
	if err == nil {
		alias, closeDir, e := pinDirectory(path)
		if e == nil {
			filename := filepath.Join(alias, coverFileName(key))
			stale, valid = readCover(filename)
			if !valid {
				legacy := filename + ".json"
				stale, valid = readCover(legacy)
				if valid && epoch == s.coverEpoch {
					if migrateErr := writeCover(filename, stale); migrateErr == nil {
						_ = os.Remove(legacy)
						s.coverUsagePath = ""
					} else {
						diagnostics.Record("cover-cache-migrate", key, migrateErr)
					}
				}
			}
			closeDir()
		}
	}
	s.coverMu.Unlock()
	if valid && time.Since(stale.Saved) < coverTTL {
		diagnostics.Observe("cover-cache", "hit")
		return coverStream(stale.Data), nil
	}
	select {
	case s.coverDownloads <- struct{}{}:
		defer func() { <-s.coverDownloads }()
	case <-ctx.Done():
		return provider.Stream{}, ctx.Err()
	}
	diagnostics.Observe("cover-cache", "miss")
	image, err := fetch(ctx)
	diagnostics.Record("cover-fetch", key, err)
	if err != nil {
		if valid {
			diagnostics.Observe("cover-cache", "stale")
			return coverStream(stale.Data), nil
		}
		return provider.Stream{}, err
	}
	if image.Body == nil {
		if valid {
			diagnostics.Observe("cover-cache", "stale")
			return coverStream(stale.Data), nil
		}
		return provider.Stream{}, errors.New("封面响应没有图片内容")
	}
	data, readErr := io.ReadAll(io.LimitReader(image.Body, coverImageLimit+1))
	image.Body.Close()
	if readErr != nil || image.StatusCode != 200 || len(data) == 0 || len(data) > coverImageLimit || !strings.HasPrefix(http.DetectContentType(data), "image/") {
		diagnostics.Record("cover-validation", key, diagnostics.New("invalid-image-response"))
		if valid {
			diagnostics.Observe("cover-cache", "stale")
			return coverStream(stale.Data), nil
		}
		return provider.Stream{}, errors.New("封面响应不是有效图片或超过 6 MiB")
	}
	s.coverMu.Lock()
	defer s.coverMu.Unlock()
	current, e := s.coverDirectory(s.settings.Snapshot())
	if e == nil && current == path && epoch == s.coverEpoch {
		alias, closeDir, e := pinDirectory(path)
		if e == nil {
			defer closeDir()
			// Cache failure must not prevent delivery of the downloaded picture.
			filename := filepath.Join(alias, coverFileName(key))
			previous := int64(0)
			existed := false
			if info, e := os.Lstat(filename); e == nil && info.Mode().IsRegular() {
				previous = info.Size()
				existed = true
			}
			writeErr := writeCover(filename, coverRecord{Saved: time.Now(), Data: data})
			diagnostics.Record("cover-cache-write", key, writeErr)
			if writeErr == nil {
				diagnostics.Record("cover-cache-trim", path, s.recordCoverWrite(alias, path, int64(16+len(data))-previous, existed))
			}
		}
	}
	return coverStream(data), nil
}
func coverFiles(path string) ([]os.FileInfo, int64, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, 0, err
	}
	var files []os.FileInfo
	var size int64
	for _, entry := range entries {
		if !managedCover(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, 0, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		files = append(files, info)
		size += info.Size()
	}
	return files, size, nil
}
func trimCovers(path string) error {
	files, size, err := coverFiles(path)
	if err != nil {
		return err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime().Before(files[j].ModTime()) })
	for i, f := range files {
		if size <= coverLimit && len(files)-i <= 2000 {
			break
		}
		if err = os.Remove(filepath.Join(path, f.Name())); err != nil {
			return err
		}
		size -= f.Size()
	}
	return nil
}

// CoverCache never accepts a client-supplied deletion path. It operates only on
// generated regular files in the saved directory, with no recursive removal.
func (s *Service) CoverCache(clear bool) (CoverStats, error) {
	s.coverMu.Lock()
	defer s.coverMu.Unlock()
	path, err := s.coverDirectory(s.settings.Snapshot())
	if err != nil {
		return CoverStats{}, err
	}
	alias, closeDir, err := pinDirectory(path)
	if err != nil {
		return CoverStats{}, err
	}
	defer closeDir()
	files, size, err := coverFiles(alias)
	if err != nil {
		return CoverStats{}, err
	}
	result := CoverStats{Directory: path, Files: len(files), Bytes: size, MaxBytes: coverLimit, TTLDays: 7}
	if clear {
		s.coverUsagePath = ""
		s.coverEpoch++ // In-flight downloads may finish, but cannot repopulate after clear.
		result.Files = 0
		result.Bytes = 0
		for _, file := range files {
			if err = os.Remove(filepath.Join(alias, file.Name())); err != nil {
				return result, err
			}
			result.Files++
			result.Bytes += file.Size()
		}
	}
	return result, nil
}
