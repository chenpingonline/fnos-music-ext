package qqmusic

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(r *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}
func fixture(t *testing.T) *Native {
	t.Helper()
	n, err := NewLoginDraft("native://qqmusic/test")
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func user() provider.User { return provider.User{FnOSGUID: "alice", Verified: true} }

const mid = "0039MnYb0qxYhV"
const trackJSON = `{"mid":"0039MnYb0qxYhV","title":"晴天","interval":269,"singer":[{"name":"周杰伦"}],"album":{"mid":"000f01724fd7TH","name":"叶惠美"},"file":{"media_mid":"0039MnYb0qxYhV"}}`

func envelope(data string) string { return `{"code":0,"req_0":{"code":0,"data":` + data + `}}` }
func requestCall(t *testing.T, r *http.Request) (string, map[string]json.RawMessage) {
	t.Helper()
	if r.URL.Host != "u.y.qq.com" || (r.URL.Path != "/cgi-bin/musicu.fcg" && r.URL.Path != "/cgi-bin/musics.fcg") || r.Method != "POST" {
		t.Fatalf("wrong API target %s", r.URL)
	}
	var body struct {
		Comm map[string]json.RawMessage `json:"comm"`
		Call struct {
			Module, Method string
			Param          map[string]json.RawMessage
		} `json:"req_0"`
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if r.URL.Path == "/cgi-bin/musics.fcg" && r.URL.Query().Get("sign") != requestSign(raw) {
		t.Fatal("signature does not match transmitted bytes")
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body.Call.Module + "/" + body.Call.Method, body.Call.Param
}
func TestQQSearchAndDetailProtocol(t *testing.T) {
	n := fixture(t)
	n.client.Transport = transport(func(r *http.Request) (*http.Response, error) {
		method, param := requestCall(t, r)
		switch method {
		case "music.search.SearchCgiService/DoSearchForQQMusicDesktop":
			if text(param, "query") != "晴天" || integer(param, "page_num") != 1 || integer(param, "num_per_page") != 20 || r.URL.Path != "/cgi-bin/musics.fcg" {
				t.Fatal(param)
			}
			return response(r, envelope(`{"meta":{"sum":1},"body":{"song":{"list":[`+trackJSON+`]}}}`)), nil
		case "music.pf_song_detail_svr/get_song_detail_yqq":
			return response(r, envelope(`{"track_info":`+trackJSON+`}`)), nil
		default:
			t.Fatal(method)
			return nil, nil
		}
	})
	p, err := n.Search(context.Background(), user(), provider.SearchRequest{Keyword: "晴天", Limit: 20})
	if err != nil || len(p.Tracks) != 1 || p.Total != 1 || p.Tracks[0].Source != "qqmusic" {
		t.Fatal(p, err)
	}
	got, err := n.GetTrack(context.Background(), user(), mid)
	if err != nil || got.MediaID != mid || got.DurationMS != 269000 || got.Artists[0] != "周杰伦" {
		t.Fatal(got, err)
	}
	if _, err = n.Search(context.Background(), provider.User{}, provider.SearchRequest{Keyword: "晴天", Limit: 20}); err != provider.ErrUnauthenticated {
		t.Fatal(err)
	}
}
func TestQQPlaybackOnlyDeliversAuthorizedRequestedFile(t *testing.T) {
	for _, mode := range []string{"ok", "denied", "trial", "different", "internal"} {
		t.Run(mode, func(t *testing.T) {
			n := fixture(t)
			n.cred = credential{MusicID: "123", MusicKey: "private", LoginType: 2}
			calls := 0
			n.client.Transport = transport(func(r *http.Request) (*http.Response, error) {
				method, param := requestCall(t, r)
				if method == "music.pf_song_detail_svr/get_song_detail_yqq" {
					return response(r, envelope(`{"track_info":`+trackJSON+`}`)), nil
				}
				if method != "music.vkey.GetVkey/UrlGetVkey" {
					t.Fatal(method)
				}
				if c, err := r.Cookie("qqmusic_key"); err != nil || c.Value != "private" {
					t.Fatal("missing per-account credentials")
				}
				var files []string
				_ = json.Unmarshal(param["filename"], &files)
				if len(files) != 1 || files[0] != "M800"+mid+".mp3" {
					t.Fatal(files)
				}
				result := 0
				file := files[0]
				purl := "song.mp3?vkey=private"
				domain := "https://cdn.example/"
				switch mode {
				case "denied":
					result = 104003
				case "trial":
					file = "RS02" + mid + ".mp3"
				case "different":
					file = "M500" + mid + ".mp3"
				case "internal":
					domain = "http://127.0.0.1/"
					purl = "http://127.0.0.1/private"
				}
				raw := fmt.Sprintf(`{"sip":[%q],"midurlinfo":[{"songmid":%q,"filename":%q,"result":%d,"purl":%q}]}`, domain, mid, file, result, purl)
				return response(r, envelope(raw)), nil
			})
			n.media.Transport = transport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
					t.Fatal("credentials leaked to CDN")
				}
				if r.Header.Get("Range") != "bytes=0-3" || r.Header.Get("If-Range") != "etag" {
					t.Fatal("resume headers lost")
				}
				res := response(r, "demo")
				res.StatusCode = 206
				res.Header.Set("Content-Range", "bytes 0-3/4")
				res.ContentLength = 4
				return res, nil
			})
			stream, err := n.Play(context.Background(), user(), provider.PlaybackRequest{TrackID: mid, Quality: "exhigh", Range: "bytes=0-3", IfRange: "etag"})
			if mode == "ok" {
				if err != nil || calls != 1 || stream.StatusCode != 206 {
					t.Fatal(stream, err, calls)
				}
				stream.Body.Close()
			} else if err == nil || calls != 0 {
				t.Fatal("unusable media delivered", mode, err, calls)
			}
		})
	}
}
func TestQQQRExchangeRequiresBusinessSuccessAndValidCredential(t *testing.T) {
	for _, mode := range []string{"ok", "business", "missing-key", "unverified", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			n := fixture(t)
			var stages []string
			n.login.Transport = transport(func(r *http.Request) (*http.Response, error) {
				stages = append(stages, r.URL.Host+r.URL.Path)
				switch r.URL.Path {
				case "/ptqrshow":
					if r.URL.Query().Get("t") == "" {
						t.Fatal("missing cache-busting QR nonce")
					}
					res := response(r, "\x89PNG\r\n\x1a\nfixture")
					res.Header.Add("Set-Cookie", "qrsig=test-only; Path=/; Secure")
					return res, nil
				case "/ptqrlogin":
					if cookie, err := r.Cookie("qrsig"); err != nil || cookie.Value != "test-only" {
						t.Fatal("QR session missing")
					}
					return response(r, `ptuiCB('0','0','https://ssl.ptlogin2.graph.qq.com/check_sig?uin=123&ptsigx=one-time&pt_login_type=3&pt_aaid=16','0','OK');`), nil
				case "/check_sig":
					if r.URL.Query().Get("pt_login_type") != "3" || r.URL.Query().Get("pt_aaid") != "16" {
						t.Fatal("upstream-issued callback context lost")
					}
					res := response(r, "")
					res.StatusCode = 302
					res.Header.Add("Set-Cookie", "p_skey=auth-cookie; Domain=.graph.qq.com; Path=/; Secure")
					res.Header.Add("Set-Cookie", "p_skey=; Domain=.qq.com; Path=/; Max-Age=0; Secure")
					res.Header.Set("Location", "http://127.0.0.1/never-follow")
					return res, nil
				case "/oauth2.0/authorize":
					if c, err := r.Cookie("p_skey"); err != nil || c.Value != "auth-cookie" {
						t.Fatal("OAuth session missing")
					}
					res := response(r, "")
					res.StatusCode = 302
					res.Header.Set("Location", "https://y.qq.com/portal/wx_redirect.html?code=one-time&state=state")
					if mode == "redirect" {
						res.Header.Set("Location", "https://evil.example/?code=private")
					}
					return res, nil
				case "/cgi-bin/musicu.fcg":
					method, _ := requestCall(t, r)
					if method != "QQConnectLogin.LoginServer/QQLogin" {
						t.Fatal(method)
					}
					if mode == "business" {
						return response(r, `{"code":0,"req_0":{"code":403,"data":{"musicid":123,"musickey":"private"}}}`), nil
					}
					if mode == "missing-key" {
						return response(r, envelope(`{"musicid":123}`)), nil
					}
					return response(r, envelope(`{"musicid":123,"musickey":"private"}`)), nil
				default:
					t.Fatal("unexpected login request", r.URL)
					return nil, nil
				}
			})
			n.client.Transport = transport(func(r *http.Request) (*http.Response, error) {
				method, _ := requestCall(t, r)
				if method != "music.UserInfo.userInfoServer/GetLoginUserInfo" {
					t.Fatal(method)
				}
				code := 0
				if mode == "unverified" {
					code = 1000
				}
				return response(r, envelope(fmt.Sprintf(`{"code":%d}`, code))), nil
			})
			qr, err := n.BeginQRLogin(context.Background())
			if err != nil || qr.QRImage == "" || qr.Key == "" {
				t.Fatal(qr, err)
			}
			raw, _ := json.Marshal(qr)
			if strings.Contains(string(raw), "test-only") {
				t.Fatal("QR key exposed")
			}
			state, err := n.CheckQRLogin(context.Background(), qr.Key)
			if mode != "ok" {
				if err == nil || n.snapshot().valid() {
					t.Fatal("invalid login accepted", state, err)
				}
				return
			}
			if err != nil || !state.LoggedIn {
				t.Fatal(state, err, stages)
			}
			dir := t.TempDir()
			if err = n.CommitLogin(dir, "test"); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "qqmusic-sessions", "test.json")
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal(info, err)
			}
			loaded, err := New("native://qqmusic/test", dir)
			if err != nil || loaded.snapshot().MusicKey != "private" {
				t.Fatal(err)
			}
			if err = os.Chmod(path, 0644); err != nil {
				t.Fatal(err)
			}
			if _, err = New("native://qqmusic/test", dir); err == nil {
				t.Fatal("public credential file accepted")
			}
			n.RollbackLogin()
			if _, err = os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("rollback left session")
			}
		})
	}
}
func TestQQOwnedPlaylistsOnly(t *testing.T) {
	n := fixture(t)
	n.cred = credential{MusicID: "123", MusicKey: "private", LoginType: 2}
	details := 0
	n.client.Transport = transport(func(r *http.Request) (*http.Response, error) {
		method, _ := requestCall(t, r)
		switch method {
		case "music.UserInfo.userInfoServer/GetLoginUserInfo":
			return response(r, envelope(`{"code":0}`)), nil
		case "music.musicasset.PlaylistBaseRead/GetPlaylistByUin":
			return response(r, envelope(`{"v_playlist":[{"tid":11,"title":"Owned","uin":"123"},{"tid":12,"title":"Foreign","uin":"456"}]}`)), nil
		case "music.srfDissInfo.DissInfo/CgiGetDiss":
			details++
			return response(r, envelope(`{"songlist":[`+trackJSON+`],"hasmore":0}`)), nil
		default:
			t.Fatal(method)
			return nil, nil
		}
	})
	if _, err := n.PlaylistTracks(context.Background(), user(), "12"); err != provider.ErrUnauthenticated || details != 0 {
		t.Fatal("foreign playlist fetched", err)
	}
	tracks, err := n.PlaylistTracks(context.Background(), user(), "11")
	if err != nil || len(tracks) != 1 || details != 1 {
		t.Fatal(tracks, err)
	}
}

// Explicit opt-in only. No account credentials or QR challenge keys are logged.
func TestLiveAnonymousProtocol(t *testing.T) {
	if os.Getenv("FNMUSIC_QQ_LIVE") != "1" {
		t.Skip("opt-in live QQ protocol check")
	}
	n := fixture(t)
	qr, err := n.BeginQRLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state, err := n.CheckQRLogin(context.Background(), qr.Key)
	if err != nil {
		t.Fatal(err)
	}
	if state.State != "pending" {
		t.Fatalf("unexpected new QR state %s", state.State)
	}
	t.Log("QQ QR generation and pending-status polling succeeded")
	track, err := n.GetTrack(context.Background(), user(), mid)
	if err != nil {
		t.Fatal(err)
	}
	if track.ID != mid || track.MediaID == "" {
		t.Fatal("missing real track identity")
	}
	t.Log("QQ song detail and media MID resolved")
	page, err := n.Search(context.Background(), user(), provider.SearchRequest{Keyword: "晴天", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tracks) != 2 || page.Total < 2 {
		t.Fatal("real search returned no usable songs")
	}
	t.Logf("signed desktop search: %d returned tracks", len(page.Tracks))
}

func TestDesktopRequestSignature(t *testing.T) {
	body := []byte(`{"comm":{"ct":19,"cv":2201},"req_0":{"method":"search","param":{"query":"test"}}}`)
	if got := requestSign(body); got != "zzc64136f0gllwidr8qllxvmjtr7gr3vumtry6bd90103" {
		t.Fatal(got)
	}
}
func TestLoginStatusRequiresUserConfirmation(t *testing.T) {
	for _, tc := range []struct {
		raw            string
		logged, failed bool
	}{
		{`{"info":{"musicid":123}}`, true, false},
		{`{"code":0}`, true, false}, {`{"code":1000}`, false, false},
		{`{}`, false, true}, {`{"other":true}`, false, true}, {`{"code":"0"}`, false, true},
	} {
		got, err := loginStatus(json.RawMessage(tc.raw))
		if got != tc.logged || (err != nil) != tc.failed {
			t.Fatalf("status %s: logged=%v error=%v", tc.raw, got, err)
		}
	}
}
func TestSearchPaginationCrossesSmallUpstreamPages(t *testing.T) {
	n := fixture(t)
	pages := []int{}
	n.client.Transport = transport(func(r *http.Request) (*http.Response, error) {
		_, param := requestCall(t, r)
		page := integer(param, "page_num")
		pages = append(pages, page)
		if integer(param, "num_per_page") != 20 {
			t.Fatal("upstream page limit exceeded")
		}
		rows := []string{}
		for i := 0; i < 20; i++ {
			rows = append(rows, trackJSON)
		}
		return response(r, envelope(`{"meta":{"sum":45},"body":{"song":{"list":[`+strings.Join(rows, ",")+`]}}}`)), nil
	})
	got, err := n.Search(context.Background(), user(), provider.SearchRequest{Keyword: "晴天", Offset: 19, Limit: 3})
	if err != nil || len(got.Tracks) != 3 || !got.HasMore || fmt.Sprint(pages) != "[1 2]" {
		t.Fatal(got, err, pages)
	}
}
