package qqmusic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func hash33(text string, initial uint32) uint32 {
	for _, r := range text {
		initial = initial*33 + uint32(r)
	}
	return initial & 0x7fffffff
}
func (n *Native) loginRequest(ctx context.Context, method, target, body string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://xui.ptlogin2.qq.com/")
	if method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := n.login.Do(req)
	if err != nil {
		return nil, errors.New("QQ 扫码服务连接失败")
	}
	return res, nil
}
func (n *Native) BeginQRLogin(ctx context.Context) (provider.QRLogin, error) {
	n.loginMu.Lock()
	defer n.loginMu.Unlock()
	n.mu.Lock()
	n.challenge = ""
	n.pending = credential{}
	n.mu.Unlock()
	// Discard every previous QR cookie jar; challenges cannot share authorization state.
	jar, _ := cookiejar.New(nil)
	n.login.Jar = jar
	q := url.Values{"appid": {"716027609"}, "e": {"2"}, "l": {"M"}, "s": {"3"}, "d": {"72"}, "v": {"4"}, "daid": {"383"}, "pt_3rd_aid": {"100497308"}, "t": {strconv.FormatInt(time.Now().UnixNano(), 10)}}
	res, err := n.loginRequest(ctx, "GET", "https://ssl.ptlogin2.qq.com/ptqrshow?"+q.Encode(), "")
	if err != nil {
		return provider.QRLogin{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return provider.QRLogin{}, httpFailure("qqmusic-login", res.StatusCode)
	}
	var key string
	for _, c := range res.Cookies() {
		if c.Name == "qrsig" {
			key = c.Value
		}
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (512<<10)+1))
	if err != nil || len(raw) > 512<<10 || len(raw) < 8 || string(raw[:8]) != "\x89PNG\r\n\x1a\n" || key == "" || len(key) > 1024 {
		return provider.QRLogin{}, errors.New("QQ 音乐未返回有效二维码")
	}
	n.mu.Lock()
	n.challenge = key
	n.expires = time.Now().Add(3 * time.Minute)
	n.mu.Unlock()
	return provider.QRLogin{Key: key, QRImage: "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)}, nil
}

var callbackArguments = regexp.MustCompile(`'([^']*)'`)

func (n *Native) CheckQRLogin(ctx context.Context, key string) (provider.LoginState, error) {
	n.loginMu.Lock()
	defer n.loginMu.Unlock()
	n.mu.Lock()
	valid := key != "" && key == n.challenge && time.Now().Before(n.expires)
	pending := n.pending
	n.mu.Unlock()
	if !valid {
		return provider.LoginState{State: "expired"}, nil
	}
	if pending.valid() {
		return n.confirmCredential(ctx, pending)
	}
	q := url.Values{"u1": {"https://graph.qq.com/oauth2.0/login_jump"}, "ptqrtoken": {strconv.FormatUint(uint64(hash33(key, 0)), 10)}, "ptredirect": {"0"}, "h": {"1"}, "t": {"1"}, "g": {"1"}, "from_ui": {"1"}, "ptlang": {"2052"}, "action": {"0-0-" + strconv.FormatInt(time.Now().UnixMilli(), 10)}, "js_ver": {"20102616"}, "js_type": {"1"}, "pt_uistyle": {"40"}, "aid": {"716027609"}, "daid": {"383"}, "pt_3rd_aid": {"100497308"}, "has_onekey": {"1"}}
	res, err := n.loginRequest(ctx, "GET", "https://ssl.ptlogin2.qq.com/ptqrlogin?"+q.Encode(), "")
	if err != nil {
		return provider.LoginState{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 32769))
	res.Body.Close()
	if err != nil || len(raw) > 32768 {
		return provider.LoginState{}, errors.New("invalid QQ login response")
	}
	if res.StatusCode != 200 {
		return provider.LoginState{}, httpFailure("qqmusic-login", res.StatusCode)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(raw)), "ptuiCB(") {
		return provider.LoginState{}, errors.New("invalid QQ login callback")
	}
	args := callbackArguments.FindAllStringSubmatch(string(raw), -1)
	if len(args) < 3 {
		return provider.LoginState{}, errors.New("invalid QQ login status")
	}
	switch args[0][1] {
	case "66":
		return provider.LoginState{State: "pending"}, nil
	case "67":
		return provider.LoginState{State: "scanned"}, nil
	case "65", "68":
		return provider.LoginState{State: "expired"}, nil
	case "0":
	default:
		return provider.LoginState{}, errors.New("QQ 登录未完成，请重新生成二维码")
	}
	callback, err := url.Parse(args[2][1])
	if err != nil || callback.Scheme != "https" || callback.Host != "ssl.ptlogin2.graph.qq.com" || callback.Path != "/check_sig" {
		return provider.LoginState{}, errors.New("invalid QQ authorization callback")
	}
	uin, sig := callback.Query().Get("uin"), callback.Query().Get("ptsigx")
	if !numberPattern.MatchString(uin) || sig == "" || len(sig) > 4096 {
		return provider.LoginState{}, errors.New("QQ 登录缺少授权参数")
	}
	// The callback contains upstream-issued login context. Keep it intact after
	// validating its exact HTTPS host/path; reconstructing it loses required fields.
	res, err = n.loginRequest(ctx, "GET", callback.String(), "")
	if err != nil {
		return provider.LoginState{}, err
	}
	res.Body.Close()
	var pSkey string
	authOrigin, _ := url.Parse("https://graph.qq.com/")
	for _, c := range n.login.Jar.Cookies(authOrigin) {
		if c.Name == "p_skey" && c.Value != "" {
			pSkey = c.Value
			break
		}
	}
	if pSkey == "" {
		return provider.LoginState{}, errors.New("QQ 登录未返回授权 Cookie")
	}
	auth := url.Values{"response_type": {"code"}, "client_id": {"100497308"}, "redirect_uri": {"https://y.qq.com/portal/wx_redirect.html?login_type=1&surl=https://y.qq.com/"}, "scope": {"get_user_info,get_app_friends"}, "state": {"state"}, "from_ptlogin": {"1"}, "src": {"1"}, "update_auth": {"1"}, "openapi": {"1010_1030"}, "g_tk": {strconv.FormatUint(uint64(hash33(pSkey, 5381)), 10)}, "auth_time": {strconv.FormatInt(time.Now().UnixMilli(), 10)}, "ui": {n.guid}}
	res, err = n.loginRequest(ctx, "POST", "https://graph.qq.com/oauth2.0/authorize", auth.Encode())
	if err != nil {
		return provider.LoginState{}, err
	}
	res.Body.Close()
	location, err := url.Parse(res.Header.Get("Location"))
	if err != nil || res.StatusCode < 300 || res.StatusCode >= 400 || location.Scheme != "https" || location.Host != "y.qq.com" || location.Query().Get("code") == "" {
		return provider.LoginState{}, errors.New("QQ 登录未取得授权码")
	}
	data, err := n.call(ctx, "QQConnectLogin.LoginServer", "QQLogin", map[string]any{"code": location.Query().Get("code")}, credential{LoginType: 2})
	if err != nil {
		return provider.LoginState{}, err
	}
	var row map[string]json.RawMessage
	if json.Unmarshal(data, &row) != nil {
		return provider.LoginState{}, errors.New("invalid QQ credentials")
	}
	c := credential{MusicID: text(row, "str_musicid", "musicid"), MusicKey: text(row, "musickey"), RefreshKey: text(row, "refresh_key"), RefreshToken: text(row, "refresh_token"), AccessToken: text(row, "access_token"), OpenID: text(row, "openid"), LoginType: 2}
	if !c.valid() {
		return provider.LoginState{}, errors.New("QQ 音乐未返回完整登录凭证")
	}
	n.mu.Lock()
	n.pending = c
	n.mu.Unlock()
	return n.confirmCredential(ctx, c)
}
func (n *Native) confirmCredential(ctx context.Context, c credential) (provider.LoginState, error) {
	status, err := n.call(ctx, "music.UserInfo.userInfoServer", "GetLoginUserInfo", map[string]any{}, c)
	if err != nil {
		return provider.LoginState{}, err
	}
	verified, err := loginStatus(status)
	if err != nil {
		return provider.LoginState{}, err
	}
	if !verified {
		return provider.LoginState{}, errors.New("QQ 音乐登录凭证尚未验证")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if err = n.persistLocked(c); err != nil {
		return provider.LoginState{}, err
	}
	n.challenge = ""
	n.pending = credential{}
	return provider.LoginState{State: "success", LoggedIn: true}, nil
}
