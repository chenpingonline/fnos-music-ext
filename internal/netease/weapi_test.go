package netease

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestWEAPIReferenceVector(t *testing.T) {
	form, err := weapiFormWithKey(map[string]any{"csrf_token": "", "key": "example", "type": 3}, []byte("0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("params") != "echuH06dDs7OxMA2egejtmBNEmotNKPl0SWrYUrbyE9JTLbRSAOOIyFNNdLJyOmJxKE1ncdDj2vmdaWBLYtx/S6Spl/Tqw2a4ZBk3EoY6lI=" || form.Get("encSecKey") != "35701388baf89fed412e11269b9c76625d095ecaf17f03fa018abe19ea2d38b949debf242ee39a71ca1f6cda71b1b86a45aa909ee27f7e78e267d34e732f0de948206c3340a788d0003372183e2f753c1f78b66ac23d134ac1fc9b993156520ea826b8aa89a962d4491b4b8d7e08738e1da9b07aa39bf4a7ef0b1c210728cd52" {
		t.Fatal("does not match independent Python reference vector")
	}
}
func TestNativeQRWebSessionAndUnexpectedCode(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprint(nested), func(t *testing.T) {
			var device string
			checks := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				d, err := r.Cookie("deviceId")
				if err != nil {
					t.Error("missing device")
				} else if device == "" {
					device = d.Value
				} else if device != d.Value {
					t.Error("device changed during QR flow")
				}
				switch r.URL.Path {
				case "/weapi/login/qrcode/unikey":
					http.SetCookie(w, &http.Cookie{Name: "__csrf", Value: "challenge-context", Path: "/"})
					fmt.Fprint(w, `{"code":200,"data":{"unikey":"test-key"}}`)
				case "/weapi/login/qrcode/client/login":
					c, err := r.Cookie("__csrf")
					if err != nil || c.Value != "challenge-context" {
						t.Error("challenge cookie lost")
					}
					checks++
					code := 801
					if checks == 2 {
						code = 802
					}
					if checks == 3 {
						code = 803
						http.SetCookie(w, &http.Cookie{Name: "MUSIC_U", Value: "test-session", Path: "/"})
					}
					if checks == 4 {
						code = 200
					}
					if nested {
						fmt.Fprintf(w, `{"code":200,"data":{"code":%d}}`, code)
					} else {
						fmt.Fprintf(w, `{"code":%d}`, code)
					}
				default:
					t.Errorf("wrong auth protocol: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			n, err := newNative("", filepath.Join(t.TempDir(), "private", "session.json"), srv.URL, srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			q, err := n.BeginQRLogin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"pending", "scanned", "success"} {
				state, err := n.CheckQRLogin(context.Background(), q.Key)
				if err != nil || state.State != want {
					t.Fatalf("got %v %v, want %s", state, err, want)
				}
			}
			if _, err := n.CheckQRLogin(context.Background(), q.Key); err == nil || !strings.Contains(err.Error(), "200") {
				t.Fatal("unexpected code silently accepted")
			}
		})
	}
}
func TestNativeWebAccountFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("MUSIC_U"); err != nil || c.Value != "test-session" {
			t.Error("missing login session")
		}
		if r.URL.Path == "/weapi/nuser/account/get" {
			fmt.Fprint(w, `{"code":200,"account":null}`)
			return
		}
		if r.URL.Path != "/weapi/w/nuser/account/get" {
			t.Error("wrong fallback")
		}
		fmt.Fprint(w, `{"code":200,"profile":{"userId":777}}`)
	}))
	defer srv.Close()
	n, err := newNative("", filepath.Join(t.TempDir(), "private", "session.json"), srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	err = n.persistCookie("MUSIC_U=test-session")
	n.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	state, err := n.AuthStatus(context.Background())
	if err != nil || !state.LoggedIn {
		t.Fatalf("%v %v", state, err)
	}
}
func TestQRLoginCode(t *testing.T) {
	for _, raw := range []string{`{"code":803}`, `{"code":200,"data":{"code":803}}`} {
		var obj map[string]json.RawMessage
		json.Unmarshal([]byte(raw), &obj)
		if qrLoginCode(obj) != 803 {
			t.Fatal(raw)
		}
	}
}
