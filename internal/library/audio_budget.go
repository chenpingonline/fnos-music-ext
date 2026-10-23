package library

import (
	"errors"
	"os"
	"sort"
)

// Audio usage includes every artifact, not just bytes received from the source.
// Reservations share the same counter so concurrent workers cannot oversubscribe it.
func (s *Service) refreshAudioUsageLocked(j Job) {
	if s.audioUsage == nil {
		s.audioUsage = map[string]int64{}
	}
	size := s.artifactSize(j)
	s.audioBytes += size - s.audioUsage[j.ID]
	if size == 0 {
		delete(s.audioUsage, j.ID)
	} else {
		s.audioUsage[j.ID] = size
	}
}
func (s *Service) refreshAudioUsage(j Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// produce pins directories with /proc/fd aliases; keep canonical registry paths.
	if current, ok := s.jobs[j.ID]; ok {
		s.refreshAudioUsageLocked(current)
	}
}
func (s *Service) reserveAudio(id string, extra int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reserveAudioLocked(id, extra)
}
func (s *Service) reserveAudioLocked(id string, extra int64) bool {
	if extra < 0 || extra > maxCacheBytes {
		return false
	}
	if s.audioUsage == nil {
		s.audioUsage = map[string]int64{}
	}
	if s.audioBytes+extra > maxCacheBytes {
		candidates := []Job{}
		for _, j := range s.jobs {
			if j.ID != id && s.running[j.ID] == nil && (j.Purpose == "cache" || j.Purpose == "download") && (j.State == "completed" || j.State == "failed" || j.State == "paused") {
				candidates = append(candidates, j)
			}
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].Created.Before(candidates[j].Created) })
		for _, j := range candidates {
			s.cleanAudio(j)
			s.refreshAudioUsageLocked(j)
			delete(s.jobs, j.ID)
			if s.audioBytes+extra <= maxCacheBytes {
				break
			}
		}
	}
	if s.audioBytes+extra > maxCacheBytes {
		return false
	}
	s.audioUsage[id] += extra
	s.audioBytes += extra
	return true
}

// FFmpeg can exceed -fs by a packet. Keep headroom in the reservation and reject
// output reaching the limit, rather than silently publishing a shortened song.
func (s *Service) audioOutput(j Job, output string, produce func(int64) error) error {
	const headroom int64 = 1 << 20
	s.mu.Lock()
	available := min(maxAudioBytes, maxCacheBytes-s.audioBytes-headroom)
	if available <= 0 || !s.reserveAudioLocked(j.ID, available+headroom) {
		s.mu.Unlock()
		return errors.New("缓存空间不足，无法生成音频文件")
	}
	s.mu.Unlock()
	err := produce(available)
	info, statErr := os.Lstat(output)
	if err == nil && (statErr != nil || !info.Mode().IsRegular() || info.Size() >= available) {
		err = errors.New("生成的音频超过缓存空间上限，未发布文件")
	}
	if err != nil {
		_ = os.Remove(output)
	}
	s.refreshAudioUsage(j)
	return err
}
