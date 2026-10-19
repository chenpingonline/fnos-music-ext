// Package library implements bounded, account-scoped audio tasks. No provider
// credentials or fnOS request headers are persisted in task records.
package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"github.com/chenpingonline/fnos-music-ext/internal/requestgroup"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxAudioBytes int64 = 512 << 20
const maxCacheBytes int64 = 2 << 30

type Job struct {
	ID              string         `json:"id"`
	User            string         `json:"user"`
	Scope           string         `json:"scope"`
	Track           provider.Track `json:"track"`
	Purpose         string         `json:"purpose"`
	Validator       string         `json:"validator,omitempty"`
	ResolvedQuality string         `json:"resolved_quality,omitempty"`
	Mode            string         `json:"mode"`
	State           string         `json:"state"`
	Percent         int            `json:"percent"`
	Bytes           int64          `json:"bytes"`
	Error           string         `json:"error,omitempty"`
	Warning         string         `json:"warning,omitempty"`
	Path            string         `json:"path,omitempty"`
	CacheRoot       string         `json:"cache_root"`
	DownloadRoot    string         `json:"download_root,omitempty"`
	ContentType     string         `json:"content_type,omitempty"`
	Created         time.Time      `json:"created_at"`
	Updated         time.Time      `json:"updated_at"`
}
type Resolver func(user, scope string) (provider.Adapter, bool)
type Service struct {
	mu                 sync.Mutex
	updateMu           sync.Mutex
	coverMu            sync.Mutex
	coverEpoch         uint64
	coverFlights       requestgroup.Group[[]byte]
	coverDownloads     chan struct{}
	coverUsagePath     string
	coverUsageBytes    int64
	coverUsageFiles    int
	coverUsageScan     time.Time
	persistenceError   string
	lastPersistAttempt time.Time
	root               string
	settings           *preferences.Store
	resolve            Resolver
	jobs               map[string]Job
	running            map[string]context.CancelFunc
	clients            map[string]context.Context
	ctx                context.Context
	cancel             context.CancelFunc
	wg                 sync.WaitGroup
	wake               chan struct{}
}

func New(root string, settings *preferences.Store, resolve Resolver) (*Service, error) {
	if settings == nil || resolve == nil {
		return nil, errors.New("media service dependencies missing")
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{root: root, settings: settings, resolve: resolve, jobs: map[string]Job{}, running: map[string]context.CancelFunc{}, clients: map[string]context.Context{}, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), coverDownloads: make(chan struct{}, 4)}
	b, e := preferences.ReadPrivate(filepath.Join(root, "audio-jobs.json"))
	if e != nil && !os.IsNotExist(e) {
		cancel()
		return nil, e
	}
	if e == nil {
		if len(b) > 16<<20 {
			cancel()
			return nil, errors.New("task registry too large")
		}
		if e = json.Unmarshal(b, &s.jobs); e != nil {
			cancel()
			return nil, e
		}
		for id, j := range s.jobs {
			if _, err := hex.DecodeString(id); err != nil || j.ID != id || len(id) != 64 {
				cancel()
				return nil, errors.New("invalid task registry")
			}
			if j.State == "running" {
				j.State = "queued"
				s.jobs[id] = j
			}
		}
	}
	s.wg.Add(1)
	go s.dispatch()
	return s, nil
}
func (s *Service) Close() { s.cancel(); s.wg.Wait() }
func hash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
func (s *Service) persistLocked() error {
	err := preferences.WriteJSON(filepath.Join(s.root, "audio-jobs.json"), s.jobs)
	diagnostics.Record("task-state-save", s.root, err)
	s.lastPersistAttempt = time.Now()
	s.persistenceError = ""
	if err != nil {
		s.persistenceError = diagnostics.Code(err)
	}
	return err
}
func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) Snapshot() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		if s.persistenceError != "" {
			warning := "任务状态尚未写入磁盘（" + s.persistenceError + "），服务正在重试"
			if j.Warning != "" {
				warning = j.Warning + "；" + warning
			}
			j.Warning = warning
		}
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}
func (s *Service) Lookup(id, user, scope string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	return j, ok && j.User == user && j.Scope == scope
}
func (s *Service) Apply(c preferences.Config) error {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	if e := preferences.Validate(c); e != nil {
		return e
	}
	coverRoot, e := s.coverDirectory(c)
	if e != nil {
		return e
	}
	if _, e := directory(coverRoot, true); e != nil {
		return e
	}
	old := s.settings.Snapshot()
	s.mu.Lock()
	active := len(s.running)
	s.mu.Unlock()
	if (active > 0 || s.hasQueued()) && (old.CacheDir != c.CacheDir || old.DownloadDir != c.DownloadDir) {
		return errors.New("请等待下载完成或取消任务后再修改目录")
	}
	root, _, e := s.directories(c, c.SaveEnabled || c.AutoBind)
	if e != nil {
		return e
	}
	if c.DownloadQuality == "standard" || (c.SaveEnabled || c.AutoBind) && c.AutoCover {
		if FFmpeg() == "" {
			return errors.New("当前设备缺少 ffmpeg，无法启用转码或封面嵌入")
		}
	}
	// The registry stays in app data; only deduplication references are copied to
	// the new cache root. Old audio files are deliberately not migrated.
	markers := map[string]string{}
	s.mu.Lock()
	for id, j := range s.jobs {
		if j.State == "completed" && j.Purpose != "cache" {
			markers[id] = j.Path
		}
	}
	s.mu.Unlock()
	if e = preferences.WriteJSON(filepath.Join(root, "saved-index.json"), markers); e != nil {
		return e
	}
	return s.settings.Update(c)
}
func (s *Service) CheckDirectory(path string) (string, error) { return directory(path, true) }
func (s *Service) Start(ctx context.Context, u provider.User, scope string, track provider.Track, purpose, mode string) (Job, error) {
	if e := u.Validate(); e != nil {
		return Job{}, e
	}
	if _, ok := s.resolve(u.FnOSGUID, scope); !ok {
		return Job{}, provider.ErrUnauthenticated
	}
	if purpose != "cache" && purpose != "save" && purpose != "bind" && purpose != "download" {
		return Job{}, errors.New("invalid media task")
	}
	c := s.settings.Snapshot()
	if purpose == "save" && !c.SaveEnabled || purpose == "bind" && !c.AutoBind {
		return Job{}, errors.New("自动保存未开启")
	}
	cache, dl, e := s.directories(c, purpose == "save" || purpose == "bind")
	if e != nil {
		return Job{}, e
	}
	id := hash(u.FnOSGUID, scope, track.ID, purpose, mode)
	s.mu.Lock()
	defer s.mu.Unlock()
	if purpose == "save" || purpose == "bind" {
		for _, saved := range s.jobs {
			if saved.User == u.FnOSGUID && saved.Scope == scope && saved.Track.ID == track.ID && (saved.Purpose == "save" || saved.Purpose == "bind") && saved.State == "completed" && saved.DownloadRoot == dl && s.validFile(saved) {
				return saved, nil
			}
		}
	}
	if old, ok := s.jobs[id]; ok && (old.State == "running" || old.State == "queued" || old.State == "completed" && s.validFile(old)) {
		return old, nil
	}
	if len(s.jobs) >= 1000 {
		for key, j := range s.jobs {
			if j.State != "running" && j.State != "queued" {
				s.cleanAudio(j)
				delete(s.jobs, key)
				break
			}
		}
		if len(s.jobs) >= 1000 {
			return Job{}, errors.New("任务队列已满")
		}
	}
	j := Job{ID: id, User: u.FnOSGUID, Scope: scope, Track: track, Purpose: purpose, Mode: mode, State: "queued", CacheRoot: cache, DownloadRoot: dl, Created: time.Now(), Updated: time.Now()}
	old, exists := s.jobs[id]
	s.jobs[id] = j
	if e = s.persistLocked(); e != nil {
		if exists {
			s.jobs[id] = old
		} else {
			delete(s.jobs, id)
		}
		return Job{}, e
	}
	if c.HandoffMax == 0 && (purpose == "save" || purpose == "cache") {
		s.clients[id] = ctx
	}
	s.signal()
	return j, nil
}
func (s *Service) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return errors.New("任务不存在")
	}
	if cancel := s.running[id]; cancel != nil {
		cancel()
	}
	if j.State != "running" {
		s.cleanAudio(j)
	}
	j.Bytes = 0
	j.State = "cancelled"
	j.Updated = time.Now()
	s.jobs[id] = j
	return s.persistLocked()
}
func (s *Service) Retry(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return errors.New("任务不存在")
	}
	if j.State == "running" || j.State == "queued" {
		return nil
	}
	if j.State == "completed" && s.validFile(j) {
		return nil
	}
	if _, ok = s.resolve(j.User, j.Scope); !ok {
		return provider.ErrUnauthenticated
	}
	j.State = "queued"
	j.Error = ""
	j.Updated = time.Now()
	s.jobs[id] = j
	delete(s.clients, id)
	if e := s.persistLocked(); e != nil {
		return e
	}
	s.signal()
	return nil
}
func (s *Service) validFile(j Job) bool {
	root := j.CacheRoot
	if j.Purpose == "save" || j.Purpose == "bind" {
		root = j.DownloadRoot
	}
	return root != "" && owned(j.Path, root)
}
func (s *Service) File(j Job) (*os.File, error) {
	if j.State != "completed" || !s.validFile(j) {
		return nil, os.ErrNotExist
	}
	root := j.CacheRoot
	if j.Purpose == "save" || j.Purpose == "bind" {
		root = j.DownloadRoot
	}
	alias, closeDir, e := pinDirectory(root)
	if e != nil {
		return nil, e
	}
	defer closeDir()
	f, e := noFollowOpen(filepath.Join(alias, filepath.Base(j.Path)), os.O_RDONLY, 0)
	if e != nil {
		return nil, e
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("音频文件无效")
	}
	return f, nil
}
func (s *Service) Cached(user, scope, track, mode string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.User == user && j.Scope == scope && j.Track.ID == track && j.Mode == mode && j.State == "completed" && s.validFile(j) {
			return j, true
		}
	}
	return Job{}, false
}
func (s *Service) dispatch() {
	defer s.wg.Done()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
		s.mu.Lock()
		limit := s.settings.Snapshot().HandoffMax
		if limit < 1 {
			limit = 1
		}
		list := []Job{}
		for _, j := range s.jobs {
			if j.State == "queued" {
				list = append(list, j)
			}
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Created.Before(list[j].Created) })
		dirty := false
		for _, j := range list {
			if len(s.running) >= limit {
				break
			}
			ctx, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
			s.running[j.ID] = cancel
			j.State = "running"
			j.Updated = time.Now()
			s.jobs[j.ID] = j
			client := s.clients[j.ID]
			s.wg.Add(1)
			go s.run(ctx, cancel, j, client)
			dirty = true
		}
		if dirty || s.persistenceError != "" && time.Since(s.lastPersistAttempt) >= 5*time.Second {
			_ = s.persistLocked()
		}
		s.mu.Unlock()
	}
}
func (s *Service) run(ctx context.Context, cancel context.CancelFunc, j Job, client context.Context) {
	defer s.wg.Done()
	defer cancel()
	stop := func() bool { return false }
	if client != nil {
		stop = context.AfterFunc(client, cancel)
	}
	defer stop()
	path, contentType, warning, err := s.produce(ctx, j)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, j.ID)
	delete(s.clients, j.ID)
	current := s.jobs[j.ID]
	if current.State == "cancelled" {
		s.cleanAudio(j)
		_ = s.persistLocked()
		return
	}
	current.Updated = time.Now()
	current.Warning = warning
	if err != nil {
		if ctx.Err() != nil {
			current.State = "paused"
			current.Error = "下载中断，可恢复续传"
		} else {
			current.State = "failed"
			current.Error = diagnostics.Message(err)
			diagnostics.Record("audio-task", j.ID, err)
		}
	} else {
		current.State = "completed"
		current.Percent = 100
		current.Path = path
		current.ContentType = contentType
	}
	s.jobs[j.ID] = current
	_ = s.persistLocked()
	s.pruneLocked(j.User, j.Scope)
	s.signal()
}
func (s *Service) update(id string, bytes, total int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[id]
	j.Bytes = bytes
	if total > 0 {
		j.Percent = int(bytes * 100 / total)
		if j.Percent > 99 {
			j.Percent = 99
		}
	}
	s.jobs[id] = j
}
func (s *Service) pruneLocked(user, scope string) {
	list := []Job{}
	var size int64
	for _, j := range s.jobs {
		if (j.Purpose == "cache" || j.Purpose == "download") && (j.State == "completed" || j.State == "failed" || j.State == "paused") {
			list = append(list, j)
			size += s.artifactSize(j)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Created.Before(list[j].Created) })
	count := 0
	for _, j := range list {
		if j.User == user && j.Scope == scope {
			count++
		}
	}
	for _, j := range list {
		same := j.User == user && j.Scope == scope
		if (!same || count <= s.settings.Snapshot().CacheMax) && size <= maxCacheBytes {
			continue
		}
		size -= s.artifactSize(j)
		s.cleanAudio(j)
		delete(s.jobs, j.ID)
		if same {
			count--
		}
	}
	_ = s.persistLocked()
}
func (s *Service) produce(ctx context.Context, j Job) (string, string, string, error) {
	source, ok := s.resolve(j.User, j.Scope)
	if !ok {
		return "", "", "", provider.ErrUnauthenticated
	}
	c := s.settings.Snapshot()
	if j.Purpose == "save" && !c.SaveEnabled || j.Purpose == "bind" && !c.AutoBind {
		return "", "", "", errors.New("自动保存已关闭")
	}
	if info, e := os.Lstat(j.CacheRoot); e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", "", errors.New("缓存工作目录无效")
	}
	originalCache := j.CacheRoot
	cache, closeCache, e := pinDirectory(j.CacheRoot)
	if e != nil {
		return "", "", "", e
	}
	defer closeCache()
	j.CacheRoot = cache
	part := filepath.Join(j.CacheRoot, j.ID+".part")
	offset := int64(0)
	if info, e := os.Lstat(part); e == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", "", "", errors.New("缓存文件无效")
		}
		offset = info.Size()
	}
	var stream provider.Stream
	var err error
	mode := j.Mode
	if mode == "original" || mode == "standard" {
		mode = "high"
	}
	qualities := preferences.QualityOrder(mode)
	if offset > 0 {
		if j.ResolvedQuality != "" {
			qualities = []string{j.ResolvedQuality}
		} else {
			offset = 0
		}
	}
	for _, quality := range qualities {
		r := ""
		if offset > 0 {
			r = fmt.Sprintf("bytes=%d-", offset)
		}
		stream, err = source.Play(ctx, provider.User{FnOSGUID: j.User, Verified: true}, provider.PlaybackRequest{TrackID: j.Track.ID, Quality: quality, Range: r, IfRange: j.Validator})
		if err == nil && offset > 0 && (stream.StatusCode == 416 || stream.StatusCode == 206 && j.Validator != "" && streamValidator(stream) != "" && streamValidator(stream) != j.Validator) {
			if stream.Body != nil {
				stream.Body.Close()
			}
			offset = 0
			stream, err = source.Play(ctx, provider.User{FnOSGUID: j.User, Verified: true}, provider.PlaybackRequest{TrackID: j.Track.ID, Quality: quality})
		}
		if err == nil && stream.Body != nil && (stream.StatusCode == 200 || stream.StatusCode == 206) {
			s.mu.Lock()
			current := s.jobs[j.ID]
			current.ResolvedQuality = quality
			current.Validator = streamValidator(stream)
			s.jobs[j.ID] = current
			_ = s.persistLocked()
			s.mu.Unlock()
			break
		}
		if stream.Body != nil {
			stream.Body.Close()
		}
		if ctx.Err() != nil {
			return "", "", "", ctx.Err()
		}
	}
	if err != nil || stream.Body == nil {
		return "", "", "", errors.New("音频不可用或账号没有完整播放权限")
	}
	defer stream.Body.Close()
	if stream.StatusCode == 200 {
		offset = 0
	} else if stream.StatusCode == 206 {
		var begin, end, total int64
		if _, err = fmt.Sscanf(stream.ContentRange, "bytes %d-%d/%d", &begin, &end, &total); err != nil || begin != offset || end < begin || total != end+1 || stream.ContentLength != end-begin+1 {
			return "", "", "", diagnostics.New("invalid-range")
		}
	} else {
		return "", "", "", errors.New("音源状态异常")
	}
	if stream.ContentLength <= 0 || stream.ContentLength > maxAudioBytes || offset+stream.ContentLength > maxAudioBytes {
		return "", "", "", errors.New("音频长度未知或超过 512 MiB 上限")
	}
	f, e := noFollowOpen(part, os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		return "", "", "", e
	}
	if offset == 0 {
		e = f.Truncate(0)
	}
	if e == nil {
		_, e = f.Seek(offset, io.SeekStart)
	}
	if e != nil {
		f.Close()
		return "", "", "", e
	}
	total := offset + stream.ContentLength
	written := offset
	buf := make([]byte, 128<<10)
	for {
		if ctx.Err() != nil {
			e = ctx.Err()
			break
		}
		n, readErr := stream.Body.Read(buf)
		if n > 0 {
			if written+int64(n) > maxAudioBytes || !s.capacity(j.ID, written+int64(n)) {
				e = errors.New("音频或缓存空间超过上限，请清理或取消旧任务")
				break
			}
			m, we := f.Write(buf[:n])
			written += int64(m)
			s.update(j.ID, written, total)
			if we != nil {
				e = we
				break
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			e = readErr
			break
		}
	}
	if e == nil && written != total {
		e = io.ErrUnexpectedEOF
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return "", "", "", e
	}
	if _, ok = s.resolve(j.User, j.Scope); !ok {
		return "", "", "", provider.ErrUnauthenticated
	}
	ext, kind, e := audioType(part)
	if e != nil {
		return "", "", "", e
	}
	final := filepath.Join(j.CacheRoot, j.ID+ext)
	if e = os.Rename(part, final); e != nil {
		return "", "", "", e
	}
	if e = validateMedia(ctx, final); e != nil {
		return "", "", "", e
	}
	warning := ""
	if j.Mode == "standard" {
		converted := filepath.Join(j.CacheRoot, j.ID+"-320.mp3")
		if e = transcode(ctx, final, converted); e != nil {
			return "", "", "", e
		}
		final = converted
		ext = ".mp3"
		kind = "audio/mpeg"
	}
	if j.Purpose == "save" || j.Purpose == "bind" {
		latest := s.settings.Snapshot()
		_, dl, e := s.directories(latest, true)
		if e != nil || dl != j.DownloadRoot || j.Purpose == "save" && !latest.SaveEnabled || j.Purpose == "bind" && !latest.AutoBind {
			return "", "", "", errors.New("保存目录或策略已变化，已保留缓存")
		}
		if c.AutoCover {
			if adapter, ok := source.(interface {
				OpenCover(context.Context, provider.User, string) (provider.Stream, error)
			}); ok {
				cover, e := adapter.OpenCover(ctx, provider.User{FnOSGUID: j.User, Verified: true}, j.Track.ID)
				if e == nil && cover.Body != nil && cover.StatusCode == 200 {
					coverPath := filepath.Join(j.CacheRoot, j.ID+".cover")
					if owned(coverPath, j.CacheRoot) {
						_ = os.Remove(coverPath)
					}
					e = writeSmall(coverPath, cover.Body, 10<<20)
					cover.Body.Close()
					if e == nil {
						tagged := filepath.Join(j.CacheRoot, j.ID+"-tagged"+ext)
						if e = embedCover(ctx, final, coverPath, tagged, j.Track); e == nil {
							final = tagged
						} else {
							warning = "歌曲已保存，但封面嵌入失败"
						}
					}
					os.Remove(coverPath)
				} else {
					if cover.Body != nil {
						cover.Body.Close()
					}
					warning = "歌曲已保存，但封面获取失败"
				}
			}
		}
		if FFmpeg() != "" && !strings.Contains(final, "-tagged") {
			tagged := filepath.Join(j.CacheRoot, j.ID+"-tagged"+ext)
			if tagAudio(ctx, final, tagged, j.Track) == nil {
				final = tagged
			} else {
				warning = "歌曲已保存，但标签写入失败"
			}
		}
		qualityKey := j.Mode
		if qualityKey == "original" {
			qualityKey = "high"
		}
		fileID := hash(j.User, j.Scope, j.Track.ID, qualityKey)
		filename := safeName(c.Filename, j.Track) + " [" + fileID[:10] + "]" + ext
		destination := filepath.Join(dl, filename)
		if e = copyExclusive(final, destination); e != nil {
			return "", "", "", e
		}
		cleanupJob := j
		cleanupJob.CacheRoot = originalCache
		s.cleanAudio(cleanupJob)
		final = destination
		if c.AutoLyrics {
			lyrics, e := source.GetLyrics(ctx, provider.User{FnOSGUID: j.User, Verified: true}, j.Track.ID)
			if e == nil && strings.TrimSpace(lyrics.LRC) != "" {
				e = writeExclusive(strings.TrimSuffix(destination, ext)+".lrc", []byte(lyrics.LRC))
			}
			if e != nil {
				warning = "歌曲已保存，但歌词保存失败"
			}
		}
	}
	if j.Purpose == "cache" || j.Purpose == "download" {
		final = filepath.Join(originalCache, filepath.Base(final))
	}
	return final, kind, warning, nil
}

func (s *Service) Warn(id, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if ok {
		j.Warning = message
		s.jobs[id] = j
		_ = s.persistLocked()
	}
}

func (s *Service) hasQueued() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.State == "queued" {
			return true
		}
	}
	return false
}

var audioSuffixes = []string{".part", ".mp3", ".flac", ".m4a", ".ogg", ".wav", "-320.mp3", "-tagged.mp3", "-tagged.flac", "-tagged.m4a", "-tagged.ogg", "-tagged.wav", ".cover"}

func (s *Service) cleanAudio(j Job) {
	alias, closeDir, e := pinDirectory(j.CacheRoot)
	if e != nil {
		return
	}
	defer closeDir()
	for _, suffix := range audioSuffixes {
		p := filepath.Join(alias, j.ID+suffix)
		if owned(p, alias) {
			_ = os.Remove(p)
		}
	}
}
func (s *Service) artifactSize(j Job) int64 {
	alias, closeDir, e := pinDirectory(j.CacheRoot)
	if e != nil {
		return 0
	}
	defer closeDir()
	var size int64
	for _, suffix := range audioSuffixes {
		p := filepath.Join(alias, j.ID+suffix)
		if owned(p, alias) {
			if info, e := os.Lstat(p); e == nil {
				size += info.Size()
			}
		}
	}
	return size
}

func (s *Service) capacity(id string, next int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := next
	for key, j := range s.jobs {
		if key == id {
			continue
		}
		if j.State == "completed" && (j.Purpose == "save" || j.Purpose == "bind") {
			continue
		}
		total += j.Bytes
	}
	return total <= maxCacheBytes
}

func (s *Service) Known(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.jobs[id]
	return ok
}

func (s *Service) Saved(user, scope, track string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.User == user && j.Scope == scope && j.Track.ID == track && (j.Purpose == "save" || j.Purpose == "bind") && j.State == "completed" && s.validFile(j) {
			return j, true
		}
	}
	return Job{}, false
}

func streamValidator(stream provider.Stream) string {
	if stream.ETag != "" && !strings.HasPrefix(stream.ETag, "W/") {
		return stream.ETag
	}
	return stream.LastModified
}
