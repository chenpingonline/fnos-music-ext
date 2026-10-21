package playback

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

// Failure contains only allowlisted diagnostic fields. Never expose stderr,
// source response bodies, paths, URLs or account credentials.
type Failure struct {
	Stage    string `json:"stage"`
	Reason   string `json:"reason"`
	ExitCode int    `json:"exit_code,omitempty"`
}

func (e *Failure) Error() string { return fmt.Sprintf("%s: %s", e.Stage, e.Reason) }
func failure(stage string, err error) *Failure {
	reason := diagnostics.Code(err)
	if errors.Is(err, provider.ErrNotEntitled) {
		reason = "not-entitled"
	}
	return &Failure{Stage: stage, Reason: reason}
}
func Message(err error) string {
	if errors.Is(err, ErrCapacity) || errors.Is(err, ErrUnavailable) {
		return err.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "等待完整播放清单超过 30 秒，音源下载或转码仍未完成，请稍后重试"
	}
	var f *Failure
	if !errors.As(err, &f) {
		return "播放文件不可用，请重新准备播放"
	}
	switch f.Reason {
	case "netease-api-http-403", "netease-api-code-403":
		return "网易云播放接口拒绝请求（403），尚未取得音频，请检查登录状态及音源连接"
	case "media-cdn-http-403":
		return "已取得播放地址，但音频 CDN 拒绝访问（403），请检查音源地址有效性及 NAS 出口网络"
	case "not-entitled":
		return "当前音源账号没有此歌曲的完整播放权限"
	case "unrecognized-audio":
		return "无法识别音源内容，可能是错误页面、不支持的格式或加密数据"
	case "encrypted-audio":
		return "FFmpeg 报告音源含加密内容，当前转码流程无法解码"
	case "empty-audio":
		return "音源返回空内容，无法转码播放"
	case "disk-full":
		return "转码临时存储空间不足，请清理后重试"
	case "permission":
		return "转码文件或程序无访问权限，请检查系统权限"
	case "missing-codec":
		return "FFmpeg 缺少所需编解码器或 HLS/fMP4 支持"
	case "invalid-audio":
		return "音频解码失败，音源可能损坏、截断或格式不受支持"
	case "input-too-large":
		return "音源超过播放转码大小限制（512 MiB）"
	}
	switch f.Stage {
	case "source":
		return "音源获取失败（" + f.Reason + "），请检查账号及音源连接"
	case "read":
		return "音源读取失败（" + f.Reason + "），请检查音源连接"
	case "start":
		return "FFmpeg 启动失败，请检查程序路径及执行权限"
	case "transcode":
		return "FFmpeg 转码失败，请查看播放诊断中的错误类别和退出码"
	default:
		return "播放准备失败（" + f.Reason + "），请查看播放诊断"
	}
}

// boundedStderr retains a small prefix solely for classification; it is never logged.
type boundedStderr struct{ data []byte }

func (b *boundedStderr) Write(p []byte) (int, error) {
	n := len(p)
	if left := 8192 - len(b.data); left > 0 {
		if len(p) > left {
			p = p[:left]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}
func (b *boundedStderr) reason() string {
	s := strings.ToLower(string(b.data))
	for _, word := range []string{"encrypted", "decryption", "decrypt"} {
		if strings.Contains(s, word) {
			return "encrypted-audio"
		}
	}
	for _, word := range []string{"unknown encoder", "unknown decoder", "encoder not found", "decoder not found", "unknown output format", "requested output format", "muxer not found"} {
		if strings.Contains(s, word) {
			return "missing-codec"
		}
	}
	if strings.Contains(s, "no space left on device") {
		return "disk-full"
	}
	if strings.Contains(s, "permission denied") {
		return "permission"
	}
	for _, word := range []string{"invalid data", "error while decoding", "invalid audio", "end of file", "partial file", "corrupt", "error decoding", "stream ends prematurely", "invalid residual", "decode_frame()", "input buffer exhausted"} {
		if strings.Contains(s, word) {
			return "invalid-audio"
		}
	}
	return "ffmpeg-failed"
}
