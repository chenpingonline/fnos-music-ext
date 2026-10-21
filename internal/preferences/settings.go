// Package preferences owns the hot-reloaded music policy. Credentials are never
// part of these settings; account sessions stay in the accounts manager.
package preferences

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type Selection struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	CoverURL string `json:"cover_url,omitempty"`
	Kind     string `json:"kind"`
}
type Config struct {
	PersonalSelections map[string][]string `json:"personal_selections,omitempty"`
	Quality            string              `json:"quality"`
	DownloadQuality    string              `json:"download_quality"`
	SearchTimeout      int                 `json:"search_timeout"`
	SearchProbe        bool                `json:"search_probe"`
	DeepPage           bool                `json:"deep_page"`
	MaxPages           int                 `json:"max_pages"`
	PersonalPlaylists  bool                `json:"personal_playlists"`
	Hot                bool                `json:"hot"`
	Daily              bool                `json:"daily"`
	PublicEnabled      bool                `json:"public_enabled"`
	Public             []Selection         `json:"public"`
	SaveEnabled        bool                `json:"save_enabled"`
	AutoCover          bool                `json:"auto_cover"`
	AutoLyrics         bool                `json:"auto_lyrics"`
	CacheMax           int                 `json:"cache_max"`
	CacheDir           string              `json:"cache_dir"`
	CoverCacheDir      string              `json:"cover_cache_dir"`
	DownloadDir        string              `json:"download_dir"`
	AutoBind           bool                `json:"auto_bind"`
	BindTimeout        int                 `json:"bind_timeout"`
	HandoffMax         int                 `json:"handoff_max"`
	ScanPath           string              `json:"scan_path"`
	Filename           string              `json:"filename"`
}

func Defaults() Config {
	return Config{PersonalPlaylists: true, Quality: "high", DownloadQuality: "app", SearchTimeout: 15, DeepPage: true, MaxPages: 10, AutoCover: true, CacheMax: 2, BindTimeout: 120, HandoffMax: 3, Filename: "{artist} - {title}", Public: []Selection{}}
}

type Store struct {
	mu       sync.RWMutex
	path     string
	config   Config
	revision uint64
}

func New(path string) (*Store, error) {
	s := &Store{path: path, config: Defaults()}
	if path != "" {
		b, e := ReadPrivate(path)
		if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
		if e == nil {
			if e = json.Unmarshal(b, &s.config); e != nil {
				return nil, e
			}
		}
	}
	if e := Validate(s.config); e != nil {
		return nil, e
	}
	return s, nil
}
func (s *Store) Snapshot() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.config
	c.Public = append([]Selection{}, c.Public...)
	c.PersonalSelections = clonePersonalSelections(c.PersonalSelections)
	return c
}
func (s *Store) Revision() uint64 { s.mu.RLock(); defer s.mu.RUnlock(); return s.revision }
func (s *Store) Update(c Config) error {
	if e := Validate(c); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path != "" {
		if e := WriteJSON(s.path, c); e != nil {
			return e
		}
	}
	s.config = c
	s.config.Public = append([]Selection{}, c.Public...)
	s.config.PersonalSelections = clonePersonalSelections(c.PersonalSelections)
	s.revision++
	return nil
}
func clonePersonalSelections(source map[string][]string) map[string][]string {
	if source == nil {
		return nil
	}
	result := make(map[string][]string, len(source))
	for account, ids := range source {
		result[account] = append([]string{}, ids...)
	}
	return result
}
func Validate(c Config) error {
	if len(c.PersonalSelections) > 100 {
		return errors.New("个人歌单筛选最多支持 100 个账号")
	}
	for account, ids := range c.PersonalSelections {
		if len(account) == 0 || len(account) > 48 || strings.Trim(account, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") != "" || len(ids) > 1000 {
			return errors.New("个人歌单筛选无效")
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if id == "" || len(id) > 128 || strings.ContainsAny(id, "\x00\r\n") || seen[id] {
				return errors.New("个人歌单选择无效或重复")
			}
			seen[id] = true
		}
	}

	if c.Quality != "high" && c.Quality != "balanced" && c.Quality != "smooth" {
		return errors.New("音质偏好无效")
	}
	if c.DownloadQuality != "app" && c.DownloadQuality != "original" && c.DownloadQuality != "standard" {
		return errors.New("下载音质无效")
	}
	if c.SearchTimeout < 1 || c.SearchTimeout > 60 || c.MaxPages < 1 || c.MaxPages > 50 || c.CacheMax < 1 || c.CacheMax > 100 || c.HandoffMax < 0 || c.HandoffMax > 20 || c.BindTimeout < 10 || c.BindTimeout > 3600 {
		return errors.New("超时、分页或并行数超出允许范围")
	}
	if len(c.Public) > 100 {
		return errors.New("最多选择 100 个公共歌单")
	}
	seen := map[string]bool{}
	for _, p := range c.Public {
		n, numberErr := strconv.ParseUint(p.ID, 10, 64)
		if numberErr != nil || n == 0 || len(p.CoverURL) > 2048 || p.ID == "" || len(p.ID) > 20 || strings.Trim(p.ID, "0123456789") != "" || p.Title == "" || len(p.Title) > 300 || seen[p.ID] {
			return errors.New("公共歌单选择无效或重复")
		}
		seen[p.ID] = true
	}
	if c.ScanPath != "" && (!strings.HasPrefix(c.ScanPath, "/music/api/v1/") || strings.ContainsAny(c.ScanPath, "?#\r\n") || strings.Contains(c.ScanPath, "..")) {
		return errors.New("曲库重扫必须是 /music/api/v1/ 下的接口路径")
	}
	name := c.Filename
	for _, token := range []string{"{artist}", "{title}", "{source}"} {
		name = strings.ReplaceAll(name, token, "")
	}
	if len(c.Filename) > 160 || strings.TrimSpace(c.Filename) == "" || strings.ContainsAny(c.Filename, "/\\\x00\r\n") || strings.ContainsAny(name, "{}") {
		return errors.New("文件名模板仅支持 artist、title、source，且不能包含目录")
	}
	for _, p := range []string{c.CacheDir, c.DownloadDir, c.CoverCacheDir} {
		if p != "" && (!filepath.IsAbs(p) || filepath.Clean(p) == "/" || strings.ContainsAny(p, "\x00\r\n")) {
			return errors.New("目录必须是有效的绝对路径")
		}
	}
	if c.CacheDir != "" && c.DownloadDir != "" && Overlap(c.CacheDir, c.DownloadDir) {
		return errors.New("缓存目录与下载目录不能相同或互为父子目录")
	}
	return nil
}
func Overlap(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	return a == b || strings.HasPrefix(a, b+string(os.PathSeparator)) || strings.HasPrefix(b, a+string(os.PathSeparator))
}
func WriteJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return fmt.Errorf("refuse non-regular state file")
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if len(b) > 16<<20 {
		return errors.New("state file too large")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	if d, e := os.Open(filepath.Dir(path)); e == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// QualityOrder is a policy over server-authorized variants, never a rights bypass.
func QualityOrder(mode string) []string {
	switch mode {
	case "smooth":
		return []string{"standard", "higher", "exhigh", "lossless", "hires", "jymaster"}
	case "balanced":
		return []string{"lossless", "exhigh", "higher", "standard", "hires", "jymaster"}
	default:
		return []string{"jymaster", "hires", "lossless", "exhigh", "higher", "standard"}
	}
}

// ReadPrivate bounds state reads and refuses links or shared writable files.
func ReadPrivate(path string) ([]byte, error) {
	info, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return nil, errors.New("invalid private state file")
	}
	if info.Size() > 16<<20 {
		return nil, errors.New("state file too large")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 16<<20))
}
