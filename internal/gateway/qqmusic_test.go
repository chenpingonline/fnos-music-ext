package gateway

import (
	"context"
	"encoding/json"
	"github.com/chenpingonline/fnos-music-ext/internal/fnmusic"
	"github.com/chenpingonline/fnos-music-ext/internal/preferences"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type qqSource struct {
	stubMusic
	calls int
}

func (s *qqSource) ID() string { return "qqmusic" }
func (s *qqSource) GetTrack(ctx context.Context, u provider.User, id string) (provider.Track, error) {
	s.calls++
	return provider.Track{ID: id, Source: "qqmusic", Title: "QQ Song", Playable: true}, nil
}
func (s *qqSource) Search(context.Context, provider.User, provider.SearchRequest) (provider.SearchPage, error) {
	s.calls++
	return provider.SearchPage{Tracks: []provider.Track{{ID: "0039MnYb0qxYhV", Source: "qqmusic", Title: "QQ Song", Playable: true}}, Total: 1}, nil
}
func TestQQGatewayRoutesAndRejectsOtherSourceIDs(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "music.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/music/api/v1/user/me" {
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"guid": "alice"}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"list": []any{}, "total": 0}})
	})}
	go srv.Serve(ln)
	defer srv.Close()
	g := New(sock)
	source := &qqSource{}
	g.SetOnline(source, "alice")
	defer g.Close()
	prefs, _ := preferences.New(filepath.Join(t.TempDir(), "settings.json"))
	if err := g.Configure(t.TempDir(), prefs); err != nil {
		t.Fatal(err)
	}
	guid, _ := fnmusic.EncodeTrackID("qqmusic", "0039MnYb0qxYhV")
	wrong, _ := fnmusic.EncodeNetEaseID("123")
	call := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://localhost"+path, nil)
		r.Header.Set("Authorization", "alice")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	if w := call("/music/api/v1/search/track?q=QQ"); w.Code != 200 || !strings.Contains(w.Body.String(), "[QQ音乐] QQ Song") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/music/api/v1/track/metadata?guid=", "/music/api/v1/track/stream?guid=", "/music/api/v1/track/lyrics?guid=", "/music/api/v1/track/transcode?guid=", "/music/api/v1/download/track/transcode/prepare?trackGUID="} {
		before := source.calls
		if w := call(path + wrong); w.Code != 403 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		if source.calls != before {
			t.Fatal("cross-source request reached adapter", path)
		}
	}
	if w := call("/music/api/v1/track/metadata?guid=" + guid); w.Code != 200 || !strings.Contains(w.Body.String(), "qqmusic") {
		t.Fatal(w.Code, w.Body.String())
	}
}
