package admin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
)

type csrfKey struct{}
type csrfGuard struct {
	secret [32]byte
	err    error
}

func newCSRFGuard() *csrfGuard { g := &csrfGuard{}; _, g.err = rand.Read(g.secret[:]); return g }
func (g *csrfGuard) token(user string) string {
	mac := hmac.New(sha256.New, g.secret[:])
	mac.Write([]byte("fnmusic-admin-write\x00" + user))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (g *csrfGuard) authorize(r *http.Request) *http.Request {
	expected := g.token(r.Header.Get("X-Trim-Userid"))
	provided := r.Header.Get("X-FnMusic-CSRF")
	valid := provided != "" && hmac.Equal([]byte(provided), []byte(expected))
	return r.WithContext(context.WithValue(r.Context(), csrfKey{}, valid))
}
func rejectManagement(w http.ResponseWriter) {
	respond(w, 403, map[string]string{"code": "management_request_rejected", "error": "写入请求校验失败，请刷新页面后重试。"})
}
