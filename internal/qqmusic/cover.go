package qqmusic

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"io"
	"regexp"

	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

// Some QQ tracks have no album MID but expose artwork via a PMID such as
// 0030lak94GN5Ad_0. Accept only the identifier, never an upstream URL or path.
var coverPMIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{14}(_[0-9]+)?$`)

func albumCoverURL(album map[string]json.RawMessage) string {
	id := text(album, "mid")
	if !midPattern.MatchString(id) {
		id = text(album, "pmid")
		if len(id) > 32 || !coverPMIDPattern.MatchString(id) {
			return ""
		}
	}
	return "https://y.gtimg.cn/music/photo_new/T002R500x500M000" + id + ".jpg"
}

//go:embed assets/default-cover.png
var defaultCoverPNG []byte

func defaultCover() provider.Stream {
	return provider.Stream{
		Body: io.NopCloser(bytes.NewReader(defaultCoverPNG)), StatusCode: 200,
		ContentType: "image/png", ContentLength: int64(len(defaultCoverPNG)),
	}
}
