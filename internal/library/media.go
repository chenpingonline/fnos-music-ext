package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func FFmpeg() string {
	if p, e := exec.LookPath("ffmpeg"); e == nil {
		return p
	}
	for _, p := range []string{"/usr/bin/ffmpeg", "/usr/trim/bin/ffmpeg", "/var/apps/trim.music/target/bin/ffmpeg"} {
		if i, e := os.Stat(p); e == nil && i.Mode().IsRegular() && i.Mode().Perm()&0111 != 0 {
			return p
		}
	}
	return ""
}
func audioType(path string) (string, string, error) {
	f, e := noFollowOpen(path, os.O_RDONLY, 0)
	if e != nil {
		return "", "", e
	}
	defer f.Close()
	b := make([]byte, 16)
	n, _ := io.ReadFull(f, b)
	b = b[:n]
	switch {
	case bytes.HasPrefix(b, []byte("fLaC")):
		return ".flac", "audio/flac", nil
	case bytes.HasPrefix(b, []byte("ID3")) || len(b) > 1 && b[0] == 0xff && b[1]&0xe0 == 0xe0:
		return ".mp3", "audio/mpeg", nil
	case len(b) > 8 && string(b[4:8]) == "ftyp":
		return ".m4a", "audio/mp4", nil
	case bytes.HasPrefix(b, []byte("OggS")):
		return ".ogg", "audio/ogg", nil
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WAVE":
		return ".wav", "audio/wav", nil
	}
	return "", "", errors.New("音源返回内容不是可识别的音频文件")
}
func command(ctx context.Context, args ...string) error {
	binary := FFmpeg()
	if binary == "" {
		return errors.New("缺少 ffmpeg，无法完成转码或封面处理")
	}
	cmd := exec.CommandContext(ctx, binary, append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}, args...)...)
	if e := cmd.Run(); e != nil {
		return errors.New("ffmpeg 处理失败，请检查音频格式与系统编解码器")
	}
	return nil
}
func transcode(ctx context.Context, input, output string) error {
	return transcodeLimit(ctx, input, output, maxAudioBytes)
}
func transcodeLimit(ctx context.Context, input, output string, limit int64) error {
	return command(ctx, "-protocol_whitelist", "file,pipe", "-i", input, "-map", "0:a:0", "-vn", "-c:a", "libmp3lame", "-b:a", "320k", "-id3v2_version", "3", "-fs", fmt.Sprint(limit), output)
}
func embedCover(ctx context.Context, input, cover, output string, t provider.Track) error {
	return embedCoverLimit(ctx, input, cover, output, t, maxAudioBytes)
}
func embedCoverLimit(ctx context.Context, input, cover, output string, t provider.Track, limit int64) error {
	return command(ctx, "-protocol_whitelist", "file,pipe", "-i", input, "-protocol_whitelist", "file,pipe", "-i", cover, "-map", "0:a:0", "-map", "1:v:0", "-c:a", "copy", "-c:v", "mjpeg", "-disposition:v:0", "attached_pic", "-metadata", "title="+t.Title, "-metadata", "artist="+strings.Join(t.Artists, " / "), "-metadata", "album="+t.Album, "-fs", fmt.Sprint(limit), output)
}
func safeName(template string, t provider.Track) string {
	name := strings.NewReplacer("{artist}", strings.Join(t.Artists, " & "), "{title}", t.Title, "{source}", t.Source).Replace(template)
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune("/\\:*?\"<>|", r) {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	runes := []rune(name)
	if len(runes) > 100 {
		runes = runes[:100]
	}
	name = string(runes)
	for len(name) > 200 {
		runes = runes[:len(runes)-1]
		name = string(runes)
	}
	if name == "" {
		name = "track"
	}
	return name
}
func writeSmall(path string, r io.Reader, limit int64) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	n, e := io.Copy(f, io.LimitReader(r, limit+1))
	f.Close()
	if n > limit {
		return errors.New("附件过大")
	}
	return e
}
func fileHash(path string) ([32]byte, error) {
	f, e := noFollowOpen(path, os.O_RDONLY, 0)
	if e != nil {
		return [32]byte{}, e
	}
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, f)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, e
}
func copyExclusive(src, dst string) error {
	alias, closeDir, e := pinDirectory(filepath.Dir(dst))
	if e != nil {
		return e
	}
	defer closeDir()
	dst = filepath.Join(alias, filepath.Base(dst))
	if info, e := os.Lstat(dst); e == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("保存目标不是普通文件")
		}
		a, e := fileHash(src)
		if e != nil {
			return e
		}
		b, e := fileHash(dst)
		if e == nil && a == b {
			return nil
		}
		return errors.New("同名文件已存在，未覆盖用户文件")
	}
	in, e := noFollowOpen(src, os.O_RDONLY, 0)
	if e != nil {
		return e
	}
	defer in.Close()
	f, e := os.CreateTemp(filepath.Dir(dst), ".fnmusic-save-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0644); e == nil {
		_, e = io.Copy(f, in)
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
	// A hard link publishes the fully written file without replacing an existing
	// destination. Both paths are in the destination directory's filesystem.
	if e = os.Link(f.Name(), dst); e != nil {
		if os.IsExist(e) {
			a, ae := fileHash(src)
			b, be := fileHash(dst)
			if ae == nil && be == nil && a == b {
				return nil
			}
		}
		return fmt.Errorf("无法发布歌曲文件：%w", e)
	}
	return nil
}
func writeExclusive(path string, b []byte) error {
	alias, closeDir, e := pinDirectory(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer closeDir()
	path = filepath.Join(alias, filepath.Base(path))
	if len(b) > 1<<20 {
		return errors.New("歌词过大")
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if os.IsExist(e) {
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() {
			return errors.New("歌词目标不是普通文件")
		}
		return nil
	}
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}

func tagAudio(ctx context.Context, input, output string, t provider.Track) error {
	return tagAudioLimit(ctx, input, output, t, maxAudioBytes)
}
func tagAudioLimit(ctx context.Context, input, output string, t provider.Track, limit int64) error {
	return command(ctx, "-protocol_whitelist", "file,pipe", "-i", input, "-map", "0:a:0", "-c:a", "copy", "-metadata", "title="+t.Title, "-metadata", "artist="+strings.Join(t.Artists, " / "), "-metadata", "album="+t.Album, "-fs", fmt.Sprint(limit), output)
}

// pcmBytes counts decoded mono 16-bit samples without retaining audio in memory.
type pcmBytes struct{ n uint64 }

func (c *pcmBytes) Write(p []byte) (int, error) { c.n += uint64(len(p)); return len(p), nil }

func validateMedia(ctx context.Context, path string) error {
	ffmpeg := FFmpeg()
	if ffmpeg == "" {
		return nil
	}
	invalid := errors.New("音频解码完整性检查失败，未发布文件")
	f, err := os.Open(path)
	if err != nil {
		return invalid
	}
	var header [42]byte
	n, _ := io.ReadFull(f, header[:])
	f.Close()
	var expected uint64
	if n >= 4 && string(header[:4]) == "fLaC" {
		// RFC 9639 §8.2: first block is 34-byte STREAMINFO; its packed
		// 64-bit rate/channel/depth/sample field starts at file offset 18.
		if n < len(header) || header[4]&0x7f != 0 || !bytes.Equal(header[5:8], []byte{0, 0, 34}) {
			return invalid
		}
		expected = binary.BigEndian.Uint64(header[18:26]) & ((1 << 36) - 1)
	}
	// FFmpeg 6 may exit zero after decoding only complete frames from a
	// truncated FLAC. Decode once, count samples, and compare to STREAMINFO.
	// Preserve input sample rate; mono/s16le gives exactly two bytes/sample.
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-abort_on", "empty_output+empty_output_stream", "-xerror", "-err_detect", "explode", "-protocol_whitelist", "file,pipe", "-i", path, "-map", "0:a:0", "-vn", "-ac", "1", "-c:a", "pcm_s16le", "-f", "s16le", "-")
	var decoded pcmBytes
	cmd.Stdout = &decoded
	cmd.Stderr = io.Discard
	cmd.Env = append(os.Environ(), "GCONV_PATH=")
	if cmd.Run() != nil || decoded.n == 0 || decoded.n%2 != 0 || expected > 0 && decoded.n/2 != expected {
		return invalid
	}
	return nil
}
