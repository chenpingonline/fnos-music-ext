package gateway

import (
	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHLSPrepareLazyFetchRangeAndUserIsolation(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("FFmpeg required")
	}
	audio := filepath.Join(t.TempDir(), "test.flac")
	if out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "sine=duration=9", "-c:a", "flac", audio).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	socket := filepath.Join(t.TempDir(), "official.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/music/api/v1/user/me" {
			who := strings.TrimPrefix(r.Header.Get("Authorization"), "Token ")
			io.WriteString(w, `{"code":0,"data":{"guid":"`+who+`"}}`)
			return
		}
		http.NotFound(w, r)
	})}
	go srv.Serve(l)
	defer srv.Close()
	g := New(socket)
	defer g.Close()
	source := &policySource{play: func(provider.PlaybackRequest) (provider.Stream, error) {
		f, err := os.Open(audio)
		if err != nil {
			return provider.Stream{}, err
		}
		info, _ := f.Stat()
		return provider.Stream{Body: f, StatusCode: 200, ContentType: "audio/flac", ContentLength: info.Size()}, nil
	}}
	g.SetOnline(source, "alice")
	guid, _ := fnmusic.EncodeNetEaseID("123")
	query := func(method, path, body, who, byteRange string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Token "+who)
		if byteRange != "" {
			r.Header.Set("Range", byteRange)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	base := "/music/api/v1/track/"
	prepared := query("POST", base+"transcode", `{"trackGUID":"`+guid+`"}`, "alice", "")
	if prepared.Code != 200 || !strings.Contains(prepared.Body.String(), `"status":"ready"`) {
		t.Fatal(prepared.Code, prepared.Body.String())
	}
	for _, action := range []string{"heartbeat", "quit"} {
		w := query("POST", base+"transcode/"+action, `{"guid":"`+guid+`"}`, "alice", "")
		if w.Code != 200 {
			t.Fatal(action, w.Code)
		}
	}
	manifest := query("GET", base+"hls/"+guid+"/preset.m3u8", "", "alice", "")
	if manifest.Code != 200 || !strings.Contains(manifest.Body.String(), "#EXT-X-MAP:") || !strings.Contains(manifest.Body.String(), "#EXT-X-ENDLIST") || !strings.Contains(manifest.Body.String(), "#EXT-X-PLAYLIST-TYPE:VOD") {
		t.Fatal("lazy HLS failed", manifest.Code, manifest.Body.String())
	}
	for _, file := range []string{"init.mp4", "seg-000000.m4s"} {
		w := query("GET", base+"hls/"+guid+"/"+file, "", "alice", "bytes=0-15")
		if w.Code != 206 || w.Body.Len() != 16 {
			t.Fatal("segment range failed", file, w.Code, w.Body.String())
		}
		head := query("HEAD", base+"hls/"+guid+"/"+file, "", "alice", "")
		if head.Code != 200 || head.Body.Len() != 0 {
			t.Fatal("HEAD failed", head.Code)
		}
	}
	denied := query("GET", base+"hls/"+guid+"/preset.m3u8", "", "bob", "")
	if denied.Code != 403 {
		t.Fatal("cross user access", denied.Code)
	}
	traversal := query("GET", base+"hls/"+guid+"/input.m4a", "", "alice", "")
	if traversal.Code != 404 {
		t.Fatal("private file accessible", traversal.Code)
	}
	failedGUID, _ := fnmusic.EncodeNetEaseID("456")
	source.play = func(provider.PlaybackRequest) (provider.Stream, error) {
		return provider.Stream{}, provider.ErrNotEntitled
	}
	for i := 0; i < 2; i++ {
		failed := query("POST", base+"transcode", `{"guid":"`+failedGUID+`","output":{"codec":"flac"}}`, "alice", "")
		if failed.Code < 400 || !strings.Contains(failed.Body.String(), `"reason":"not-entitled"`) || !strings.Contains(failed.Body.String(), `"stage":"source"`) || strings.Contains(failed.Body.String(), `"ready"`) {
			t.Fatal("source failure lost", failed.Code, failed.Body.String())
		}
	}
	source.play = func(provider.PlaybackRequest) (provider.Stream, error) {
		f, err := os.Open(audio)
		if err != nil {
			return provider.Stream{}, err
		}
		info, _ := f.Stat()
		return provider.Stream{Body: f, StatusCode: 200, ContentType: "audio/flac", ContentLength: info.Size()}, nil
	}
	metadata := query("GET", base+"metadata?guid="+guid, "", "alice", "")
	if metadata.Code != 200 || !strings.Contains(metadata.Body.String(), `"format":"flac"`) {
		t.Fatal("actual format missing", metadata.Code, metadata.Body.String())
	}
}
func TestMutationMetadataProxySafety(t *testing.T) {
	r := httptest.NewRequest("POST", "http://internal/music/api/v1/track/transcode", nil)
	r.Header.Set("Origin", "https://nas.example")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if !userMutation(r) {
		t.Fatal("proxy rewrite rejected")
	}
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if userMutation(r) {
		t.Fatal("cross site accepted")
	}
	for _, tc := range []struct{ raw, ct, want string }{{"fLaC", "application/octet-stream", "flac"}, {"ID3", "audio/flac", "mp3"}, {"garbage", "application/octet-stream", ""}} {
		format, _ := audioEncoding([]byte(tc.raw), tc.ct)
		if format != tc.want {
			t.Fatal(format, tc.want)
		}
	}
}
