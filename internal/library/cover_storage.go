package library

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const coverMagic = "FNMCOV2\n"

func writeCover(path string, record coverRecord) error {
	if info, e := os.Lstat(path); e == nil && !info.Mode().IsRegular() {
		return errors.New("refuse non-regular cover cache file")
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".cover-write-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	header := make([]byte, 16)
	copy(header, coverMagic)
	binary.BigEndian.PutUint64(header[8:], uint64(record.Saved.UnixNano()))
	if _, e = f.Write(header); e == nil {
		_, e = f.Write(record.Data)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	if dir, e := os.Open(filepath.Dir(path)); e == nil {
		defer dir.Close()
		return dir.Sync()
	}
	return nil
}
func (s *Service) recordCoverWrite(alias, path string, delta int64, existed bool) error {
	if s.coverUsagePath != path || time.Since(s.coverUsageScan) > time.Minute {
		if e := s.refreshCoverUsage(alias, path); e != nil {
			return e
		}
	} else {
		// The caller supplies the exact size difference including the small header.
		s.coverUsageBytes += delta
		if !existed {
			s.coverUsageFiles++
		}
	}
	if s.coverUsageBytes > coverLimit || s.coverUsageFiles > 2000 {
		if e := trimCovers(alias); e != nil {
			return e
		}
		return s.refreshCoverUsage(alias, path)
	}
	return nil
}
func (s *Service) refreshCoverUsage(alias, path string) error {
	files, size, e := coverFiles(alias)
	if e != nil {
		return e
	}
	s.coverUsagePath = path
	s.coverUsageScan = time.Now()
	s.coverUsageBytes = size
	s.coverUsageFiles = len(files)
	return nil
}
