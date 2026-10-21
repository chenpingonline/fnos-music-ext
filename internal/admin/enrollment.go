package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/accounts"
	"github.com/chenpingonline/fnos-music-ext/internal/netease"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

type enrollment struct {
	mu                sync.Mutex
	owner             string
	account           accounts.Account
	adapter           provider.Adapter
	key               string
	expires, checked  time.Time
	authorized, saved bool
	ctx               context.Context
	cancel            context.CancelFunc
}
type enrollmentManager struct {
	mu       sync.Mutex
	entries  map[string]*enrollment
	accounts *accounts.Manager
}

func newEnrollmentManager(m *accounts.Manager) *enrollmentManager {
	return &enrollmentManager{entries: map[string]*enrollment{}, accounts: m}
}
func randomEnrollmentID() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func (m *enrollmentManager) remove(id string, d *enrollment) {
	m.mu.Lock()
	if m.entries[id] == d {
		delete(m.entries, id)
	}
	m.mu.Unlock()
	d.cancel()
}
func enrollmentOwner(r *http.Request) string {
	return r.Header.Get("X-Trim-Userid") + ":" + r.Header.Get("X-Trim-Username") + ":" + r.Header.Get("X-FnMusic-CSRF")
}

func (m *enrollmentManager) handle(w http.ResponseWriter, r *http.Request) bool {
	const prefix = "/api/account-enrollments"
	if r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
		return false
	}
	if !managementRequest(r) {
		rejectManagement(w)
		return true
	}
	if m.accounts == nil {
		respond(w, 503, map[string]string{"error": "账号管理服务不可用"})
		return true
	}
	owner := enrollmentOwner(r)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if r.URL.Path == prefix && r.Method == http.MethodPost {
		var req struct {
			Name     string `json:"name"`
			Provider string `json:"provider"`
		}
		if err := decode(r, &req); err != nil {
			fail(w, err)
			return true
		}
		id, err := randomEnrollmentID()
		if err != nil {
			respond(w, 500, map[string]string{"error": "无法创建登录会话"})
			return true
		}
		a := accounts.Account{ID: "account_" + id, Name: strings.TrimSpace(req.Name)}
		switch req.Provider {
		case "", "native", "netease":
			a.BridgeURL = "native://netease/" + a.ID
		case "qqmusic":
			a.BridgeURL = "native://qqmusic/" + a.ID
		default:
			fail(w, accounts.ErrInvalid)
			return true
		}
		// Reserve the bounded slot before any upstream request.
		draftCtx, draftCancel := context.WithCancel(context.Background())
		d := &enrollment{owner: owner, account: a, ctx: draftCtx, cancel: draftCancel}
		m.mu.Lock()
		if len(m.entries) >= 32 {
			m.mu.Unlock()
			draftCancel()
			respond(w, 429, map[string]string{"error": "正在进行的登录过多，请稍后重试"})
			return true
		}
		m.entries[id] = d
		m.mu.Unlock()
		time.AfterFunc(5*time.Minute, func() { m.remove(id, d) })
		d.mu.Lock()
		defer d.mu.Unlock()
		stop := context.AfterFunc(d.ctx, cancel)
		defer stop()
		adapter, err := m.accounts.PrepareLogin(a)
		if err != nil {
			m.remove(id, d)
			fail(w, err)
			return true
		}
		d.adapter = adapter
		qr, err := d.begin(ctx)
		if err != nil {
			m.remove(id, d)
			enrollmentError(w, err)
			return true
		}
		respond(w, 201, map[string]string{"id": id, "qr_url": qr.QRURL, "qr_image": qr.QRImage})
		return true
	}
	bits := strings.Split(strings.TrimPrefix(r.URL.Path, prefix+"/"), "/")
	if len(bits) > 2 || bits[0] == "" {
		http.NotFound(w, r)
		return true
	}
	m.mu.Lock()
	d := m.entries[bits[0]]
	m.mu.Unlock()
	if d == nil || d.owner != owner {
		respond(w, 410, map[string]string{"error": "登录会话已结束，请重新扫码"})
		return true
	}
	if len(bits) == 1 && r.Method == http.MethodDelete {
		m.remove(bits[0], d)
		respond(w, 200, map[string]bool{"ok": true})
		return true
	}
	if len(bits) != 2 || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	stop := context.AfterFunc(d.ctx, cancel)
	defer stop()
	if d.ctx.Err() != nil {
		enrollmentError(w, errNoChallenge)
		return true
	}
	switch bits[1] {
	case "qr":
		if d.saved {
			respond(w, 409, map[string]string{"error": "账号已保存"})
			return true
		}
		qr, err := d.begin(ctx)
		if err != nil {
			enrollmentError(w, err)
			return true
		}
		respond(w, 200, map[string]string{"qr_url": qr.QRURL, "qr_image": qr.QRImage})
	case "check":
		if d.saved {
			respond(w, 200, map[string]any{"state": "success", "logged_in": true, "saved": true})
			return true
		}
		if time.Now().After(d.expires) {
			enrollmentError(w, errNoChallenge)
			return true
		}
		if time.Since(d.checked) < 2*time.Second {
			enrollmentError(w, errPollFast)
			return true
		}
		d.checked = time.Now()
		login := d.adapter.(provider.LoginAdapter)
		if !d.authorized {
			state, err := login.CheckQRLogin(ctx, d.key)
			if err != nil {
				enrollmentError(w, err)
				return true
			}
			if !state.LoggedIn || state.State != "success" {
				respond(w, 200, state)
				return true
			}
			d.authorized = true
		}
		state, err := login.AuthStatus(ctx)
		if err != nil {
			enrollmentError(w, err)
			return true
		}
		if !state.LoggedIn {
			respond(w, 502, map[string]string{"error": "尚未验证登录成功，请稍后重试或重新扫码"})
			return true
		}
		if ctx.Err() != nil || d.ctx.Err() != nil {
			enrollmentError(w, errNoChallenge)
			return true
		}
		if err = m.accounts.CommitLogin(ctx, d.account, d.adapter); err != nil {
			respond(w, 500, map[string]string{"error": "登录已成功，但保存账号失败，请重试"})
			return true
		}
		d.saved = true
		d.key = ""
		respond(w, 200, map[string]any{"state": "success", "logged_in": true, "saved": true})
	default:
		http.NotFound(w, r)
	}
	return true
}
func (d *enrollment) begin(ctx context.Context) (provider.QRLogin, error) {
	login, ok := d.adapter.(provider.LoginAdapter)
	if !ok {
		return provider.QRLogin{}, errNoLoginAdapter
	}
	qr, err := login.BeginQRLogin(ctx)
	if err != nil {
		return qr, err
	}
	if ctx.Err() != nil || d.ctx.Err() != nil {
		return provider.QRLogin{}, errNoChallenge
	}
	d.key = qr.Key
	d.expires = time.Now().Add(3 * time.Minute)
	d.checked = time.Time{}
	d.authorized = false
	qr.Key = ""
	return qr, nil
}
func enrollmentError(w http.ResponseWriter, err error) {
	status := 502
	message := "扫码服务暂时不可用，请重试"
	switch {
	case errors.Is(err, errNoChallenge):
		status = 410
		message = "登录会话已过期，请重新扫码"
	case errors.Is(err, errPollFast):
		status = 429
		message = "检查过于频繁，请稍候"
	}
	var qrError *netease.QRStateError
	if errors.As(err, &qrError) {
		respond(w, 502, map[string]any{"code": "qr_state_unexpected", "upstream_code": qrError.Code})
		return
	}
	respond(w, status, map[string]string{"error": message})
}
