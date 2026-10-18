package netease

import (
	"bytes"
	"context"
	"crypto/aes"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

func unpackEAPI(t *testing.T, req *http.Request) (string, map[string]any) {
	t.Helper()
	if e := req.ParseForm(); e != nil {
		t.Fatal(e)
	}
	src, e := hex.DecodeString(req.PostForm.Get("params"))
	if e != nil {
		t.Fatal(e)
	}
	block, e := aes.NewCipher([]byte("e82ckenh8dichen8"))
	if e != nil {
		t.Fatal(e)
	}
	plaintext := make([]byte, len(src))
	for i := 0; i < len(src); i += aes.BlockSize {
		block.Decrypt(plaintext[i:i+16], src[i:i+16])
	}
	pad := int(plaintext[len(plaintext)-1])
	plaintext = plaintext[:len(plaintext)-pad]
	chunks := bytes.SplitN(plaintext, []byte("-36cd479b6b5-"), 3)
	if len(chunks) != 3 {
		t.Fatalf("bad EAPI: %s", plaintext)
	}
	var args map[string]any
	if e := json.Unmarshal(chunks[1], &args); e != nil {
		t.Fatal(e)
	}
	return string(chunks[0]), args
}
func TestEAPIWireFormat(t *testing.T) {
	v, e := eapiForm("/api/cloudsearch/pc", map[string]any{"s": "音乐", "type": 1})
	if e != nil {
		t.Fatal(e)
	}
	h := http.Header{}
	req := httptest.NewRequest("POST", "/", strings.NewReader(v.Encode()))
	req.Header = h
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	path, args := unpackEAPI(t, req)
	if path != "/api/cloudsearch/pc" || args["s"] != "音乐" {
		t.Fatalf("%s %v", path, args)
	}
	if _, e = eapiForm("http://host/other", map[string]any{}); e == nil {
		t.Fatal("accepted external path")
	}
}
func TestNativeProviderFunctionalAndPerAccountSession(t *testing.T) {
	uidCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(200)
			return
		}
		path, args := unpackEAPI(t, r)
		if path == "/api/login/qrcode/client/login" {
			if args["key"] != "fake-key" {
				t.Errorf("wrong QR key: %v", args)
			}
			_, _ = io.WriteString(w, `{"code":803,"cookie":"MUSIC_U=test-private-session; __csrf=token"}`)
			return
		}
		if path == "/api/nuser/account/get" {
			if !strings.Contains(r.Header.Get("Cookie"), "MUSIC_U=test-private-session") {
				t.Errorf("missing persisted credential")
			}
			uidCalls++
			_, _ = io.WriteString(w, `{"code":200,"account":{"id":777}}`)
			return
		}
		switch path {
		case "/api/login/qrcode/unikey":
			_, _ = io.WriteString(w, `{"code":200,"data":{"unikey":"fake-key"}}`)
		case "/api/cloudsearch/pc":
			_, _ = io.WriteString(w, `{"code":200,"result":{"songCount":1,"songs":[{"id":123,"name":"歌曲","ar":[{"name":"作者"}],"al":{"name":"专辑","picUrl":"https://music.163.com/a.jpg"},"dt":270000}]}}`)
		case "/api/v3/song/detail":
			_, _ = io.WriteString(w, `{"code":200,"songs":[{"id":123,"name":"歌曲","ar":[{"name":"作者"}],"al":{"name":"专辑"},"dt":270000}]}`)
		case "/api/song/lyric":
			_, _ = io.WriteString(w, `{"code":200,"lrc":{"lyric":"[00:01]歌"},"tlyric":{"lyric":"translated"}}`)
		case "/api/user/playlist":
			_, _ = io.WriteString(w, `{"code":200,"playlist":[{"id":901,"name":"我的歌单","userId":777,"trackCount":1},{"id":902,"name":"其他人歌单","userId":555,"trackCount":1}]}`)
		case "/api/v6/playlist/detail":
			_, _ = io.WriteString(w, `{"code":200,"playlist":{"trackIds":[{"id":123}]}}`)
		case "/api/song/enhance/player/url/v1":
			_, _ = io.WriteString(w, `{"code":200,"data":[{"code":200,"url":"","freeTrialInfo":{"start":0}}]}`)
		default:
			t.Errorf("unexpected path %s", path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	session := filepath.Join(dir, "state", "session.json")
	n, e := newNative("", session, srv.URL, srv.Client())
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	u := provider.User{FnOSGUID: "alice", Verified: true}
	if s, e := n.AuthStatus(ctx); e != nil || s.LoggedIn {
		t.Fatalf("unexpected initial login status: %+v %v", s, e)
	}
	q, e := n.BeginQRLogin(ctx)
	if e != nil || q.Key != "fake-key" || !strings.Contains(q.QRURL, "codekey=fake-key") {
		t.Fatalf("QR: %+v %v", q, e)
	}
	s, e := n.CheckQRLogin(ctx, q.Key)
	if e != nil || !s.LoggedIn {
		t.Fatalf("QR confirmation: %+v %v", s, e)
	}
	st, e := os.Stat(session)
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("session permission: %v %v", st, e)
	}
	n2, e := newNative("", session, srv.URL, srv.Client())
	if e != nil {
		t.Fatal(e)
	}
	s, e = n2.AuthStatus(ctx)
	if e != nil || !s.LoggedIn || uidCalls == 0 {
		t.Fatalf("reloaded session: %+v %v", s, e)
	}
	p, e := n2.Search(ctx, u, provider.SearchRequest{Keyword: "歌曲", Limit: 10})
	if e != nil || len(p.Tracks) != 1 || p.Tracks[0].Artists[0] != "作者" {
		t.Fatalf("search: %+v %v", p, e)
	}
	track, e := n2.GetTrack(ctx, u, "123")
	if e != nil || track.ID != "123" {
		t.Fatalf("track: %+v %v", track, e)
	}
	lyric, e := n2.GetLyrics(ctx, u, "123")
	if e != nil || lyric.LRC == "" {
		t.Fatalf("lyrics %+v %v", lyric, e)
	}
	pls, e := n2.ListPlaylists(ctx, u)
	if e != nil || len(pls) != 1 || pls[0].ID != "901" {
		t.Fatalf("own playlists %+v %v", pls, e)
	}
	if _, e = n2.PlaylistTracks(ctx, u, "902"); e == nil {
		t.Fatal("read someone else's playlist")
	}
	tracks, e := n2.PlaylistTracks(ctx, u, "901")
	if e != nil || len(tracks) != 1 {
		t.Fatalf("playlist tracks %+v %v", tracks, e)
	}
	if _, e = n2.Play(ctx, u, provider.PlaybackRequest{TrackID: "123"}); e != provider.ErrNotEntitled {
		t.Fatalf("playback entitlement: %v", e)
	}
	if _, e = n2.Search(ctx, provider.User{}, provider.SearchRequest{Keyword: "hi", Limit: 1}); e != provider.ErrUnauthenticated {
		t.Fatalf("unauthenticated: %v", e)
	}
}
func TestNativeStrictConfigurationAndSessionPermissions(t *testing.T) {
	dir := t.TempDir()
	for _, raw := range []string{"native://netease", "native://netease/../oops", "native://other/acct", "native://netease/account?token=x", "https://netease/account", "native://netease/a/b"} {
		if _, e := NewNative(raw, dir); e == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if _, e := NewNative("native://netease/home", dir); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "netease-sessions", "home.json")
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte(`{"cookie":"MUSIC_U=test"}`), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := NewNative("native://netease/home", dir); e == nil {
		t.Fatal("read world-readable token")
	}
}
func TestNativeNoRedirectAndURLParsing(t *testing.T) {
	u, _ := url.Parse("https://music.163.com/song?id=123")
	if !safeMediaURL(u) {
		t.Fatal("media safety")
	}
	if normalizeCookies("MUSIC_U=one; Path=/; HttpOnly; __csrf=two") != "MUSIC_U=one; __csrf=two" {
		t.Fatal("cookie normalization")
	}
}
