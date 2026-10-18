package gateway

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func unixBackend(t *testing.T, handler http.Handler) (string, func()) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "official.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	s := &http.Server{Handler: handler, ReadHeaderTimeout: 2 * time.Second}
	go s.Serve(l)
	return sock, func() { _ = s.Shutdown(context.Background()) }
}
func TestTransparentForwardAndRange(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/music/api/v1/track/stream":
			if r.Header.Get("Range") != "bytes=8-11" {
				t.Errorf("range lost: %q", r.Header.Get("Range"))
			}
			w.Header().Set("Content-Range", "bytes 8-11/20")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("abcd"))
		case "/music/api/v1/playlist/update":
			if r.Method != "POST" || r.URL.Query().Get("guid") != "xyz" {
				t.Errorf("method/query altered: %v", r.URL)
			}
			data, _ := io.ReadAll(r.Body)
			if string(data) != `{"title":"foo"}` {
				t.Errorf("body altered: %s", data)
			}
			w.Header().Set("X-Original", "yes")
			w.WriteHeader(201)
		case "/music/api/v1/user/me":
			w.WriteHeader(401)
		default:
			http.NotFound(w, r)
		}
	})
	socket, closeFn := unixBackend(t, handler)
	defer closeFn()
	g := New(socket)
	defer g.Close()
	server := httptest.NewServer(g)
	defer server.Close()
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/music/api/v1/track/stream", nil)
	req.Header.Set("Range", "bytes=8-11")
	r, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 206 || r.Header.Get("Content-Range") != "bytes 8-11/20" || string(data) != "abcd" {
		t.Fatalf("range streaming changed: %d %v %s", r.StatusCode, r.Header, string(data))
	}
	req, _ = http.NewRequest("POST", server.URL+"/music/api/v1/playlist/update?guid=xyz", strings.NewReader(`{"title":"foo"}`))
	r, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 201 || r.Header.Get("X-Original") != "yes" {
		t.Fatalf("POST changed: %d", r.StatusCode)
	}
	r, e = http.Get(server.URL + "/_ext/healthz")
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
}
func TestUpstreamUnavailable(t *testing.T) {
	g := New(filepath.Join(t.TempDir(), "missing.sock"))
	defer g.Close()
	req := httptest.NewRequest("GET", "/music/api/v1/track/list", nil)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 502 {
		t.Fatalf("got %d", w.Code)
	}
	w = httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest("GET", "/_ext/healthz", nil))
	if w.Code != 503 {
		t.Fatalf("health %d", w.Code)
	}
}
