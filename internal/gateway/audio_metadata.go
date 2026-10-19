package gateway

import (
	"bytes"
	"context"
	"fmt"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"strings"
)

func audioEncoding(raw []byte, ct string) (string, string) {
	switch {
	case bytes.HasPrefix(raw, []byte("fLaC")):
		return "flac", "flac"
	case bytes.HasPrefix(raw, []byte("ID3")):
		return "mp3", "mp3"
	case len(raw) > 1 && raw[0] == 0xff && raw[1]&0xf6 == 0xf0:
		return "aac", "aac"
	case len(raw) > 1 && raw[0] == 0xff && raw[1]&0xe0 == 0xe0:
		return "mp3", "mp3"
	case len(raw) > 8 && string(raw[4:8]) == "ftyp":
		return "m4a", ""
	case bytes.HasPrefix(raw, []byte("OggS")):
		return "ogg", ""
	case len(raw) >= 12 && string(raw[:4]) == "RIFF" && string(raw[8:12]) == "WAVE":
		return "wav", ""
	}
	switch strings.ToLower(strings.Split(ct, ";")[0]) {
	case "audio/flac", "audio/x-flac":
		return "flac", "flac"
	case "audio/mpeg", "audio/mp3":
		return "mp3", "mp3"
	case "audio/aac":
		return "aac", "aac"
	case "audio/mp4", "audio/x-m4a":
		return "m4a", ""
	case "audio/ogg":
		return "ogg", ""
	}
	return "", ""
}
func (g *Gateway) resolvePlaybackMetadata(ctx context.Context, u provider.User, source provider.Adapter, scope string, t provider.Track) provider.Track {
	quality := g.policy().Quality
	if t.Audio != nil && t.Audio.Quality == quality {
		return t
	}
	stream, err := playQuality(ctx, source, u, t.ID, quality, "bytes=0-511")
	if err != nil {
		return t
	}
	defer stream.Body.Close()
	if stream.StatusCode != 200 && stream.StatusCode != 206 {
		return t
	}
	raw, err := io.ReadAll(io.LimitReader(stream.Body, 512))
	if err != nil {
		return t
	}
	format, codec := audioEncoding(raw, stream.ContentType)
	if format == "" {
		return t
	}
	size := stream.ContentLength
	if stream.StatusCode == 206 {
		var begin, end int64
		if _, err := fmt.Sscanf(stream.ContentRange, "bytes %d-%d/%d", &begin, &end, &size); err != nil {
			size = 0
		}
	}
	if size < 0 {
		size = 0
	}
	t.Audio = &provider.AudioInfo{Format: format, Codec: codec, Size: size, Quality: quality}
	g.cache.put(scope+":"+t.ID, t)
	return t
}
