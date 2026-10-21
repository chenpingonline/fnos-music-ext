// Package qqmusic implements the native QQ Music protocol. Upstream endpoints
// are undocumented; successful protocol responses do not imply media entitlement.
package qqmusic

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/diagnostics"
	"github.com/chenpingonline/fnos-music-ext/internal/mediahttp"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

var accountID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,48}$`)
var midPattern = regexp.MustCompile(`^[A-Za-z0-9]{14}$`)
var numberPattern = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

const apiOrigin = "https://u.y.qq.com"
const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

type credential struct {
	MusicID      string `json:"musicid"`
	MusicKey     string `json:"musickey"`
	RefreshKey   string `json:"refresh_key,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	OpenID       string `json:"openid,omitempty"`
	LoginType    int    `json:"login_type"`
}

func (c credential) valid() bool {
	return numberPattern.MatchString(c.MusicID) && len(c.MusicKey) > 0 && len(c.MusicKey) <= 4096 && !strings.ContainsAny(c.MusicKey, "\r\n\x00;")
}

type Native struct {
	mu                     sync.Mutex
	loginMu                sync.Mutex
	client, media          *http.Client
	login                  *http.Client
	session, account, guid string
	pending                credential
	cred                   credential
	challenge              string
	expires                time.Time
}

func New(address, directory string) (*Native, error) { return newAccount(address, directory, false) }
func NewLoginDraft(address string) (*Native, error)  { return newAccount(address, "", true) }
func newAccount(address, directory string, draft bool) (*Native, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "native" || u.Host != "qqmusic" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !accountID.MatchString(strings.TrimPrefix(u.Path, "/")) {
		return nil, errors.New("invalid native QQ Music account")
	}
	jar, _ := cookiejar.New(nil)
	n := &Native{account: strings.TrimPrefix(u.Path, "/"), client: &http.Client{Jar: jar, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, media: mediahttp.NewClient()}
	b := make([]byte, 16)
	if _, err = rand.Read(b); err != nil {
		return nil, err
	}
	n.guid = hex.EncodeToString(b)
	n.login = &http.Client{Jar: jar, Timeout: 12 * time.Second, CheckRedirect: n.client.CheckRedirect}
	n.client.Jar = nil
	if !draft {
		n.session = filepath.Join(directory, "qqmusic-sessions", n.account+".json")
		if err = n.load(); err != nil {
			return nil, err
		}
	}
	return n, nil
}
func (n *Native) ID() string                       { return "qqmusic" }
func (n *Native) snapshot() credential             { n.mu.Lock(); defer n.mu.Unlock(); return n.cred }
func (n *Native) Health(ctx context.Context) error { _, err := n.AuthStatus(ctx); return err }

// CGI auth travels only to the fixed API origin. Cookies never reach media hosts.
func (n *Native) cgi(ctx context.Context, module, method string, param any) (json.RawMessage, error) {
	return n.call(ctx, module, method, param, n.snapshot())
}
func (n *Native) call(ctx context.Context, module, method string, param any, c credential) (json.RawMessage, error) {
	comm := map[string]any{"ct": 19, "cv": 2201, "format": "json", "uin": c.MusicID, "qq": c.MusicID, "authst": c.MusicKey, "tmeLoginType": c.LoginType, "guid": n.guid}
	body, _ := json.Marshal(map[string]any{"comm": comm, "req_0": map[string]any{"module": module, "method": method, "param": param}})
	endpoint := apiOrigin + "/cgi-bin/musicu.fcg"
	if module == "music.search.SearchCgiService" || module == "music.vkey.GetVkey" {
		endpoint = apiOrigin + "/cgi-bin/musics.fcg?sign=" + requestSign(body)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", "https://y.qq.com/")
	req.Header.Set("User-Agent", userAgent)
	if c.valid() {
		for k, v := range map[string]string{"uin": c.MusicID, "qqmusic_uin": c.MusicID, "qqmusic_key": c.MusicKey, "qm_keyst": c.MusicKey} {
			req.AddCookie(&http.Cookie{Name: k, Value: v})
		}
	}
	client := n.client
	if module == "QQConnectLogin.LoginServer" && method == "QQLogin" {
		client = n.login
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("QQ 音乐接口连接失败")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, httpFailure("qqmusic-api", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return nil, errors.New("QQ 音乐响应过大或无法读取")
	}
	var reply struct {
		Code *int `json:"code"`
		Item struct {
			Code *int            `json:"code"`
			Data json.RawMessage `json:"data"`
		} `json:"req_0"`
	}
	if json.Unmarshal(raw, &reply) != nil || reply.Code == nil || reply.Item.Code == nil {
		return nil, errors.New("QQ 音乐响应格式异常")
	}
	for _, code := range []*int{reply.Code, reply.Item.Code} {
		if *code != 0 {
			if *code == 1000 || *code == 104400 || *code == 104401 {
				return nil, provider.ErrUnauthenticated
			}
			return nil, diagnostics.New("qqmusic-api-code-" + strconv.Itoa(*code))
		}
	}
	if len(reply.Item.Data) == 0 || string(reply.Item.Data) == "null" {
		return nil, errors.New("QQ 音乐未返回数据")
	}
	return reply.Item.Data, nil
}
func httpFailure(stage string, status int) error {
	return &diagnostics.Failure{Kind: fmt.Sprintf("%s-http-%d", stage, status), Cause: &diagnostics.HTTPError{Status: status}}
}
func (n *Native) AuthStatus(ctx context.Context) (provider.LoginState, error) {
	c := n.snapshot()
	if !c.valid() {
		return provider.LoginState{State: "pending"}, nil
	}
	raw, err := n.call(ctx, "music.UserInfo.userInfoServer", "GetLoginUserInfo", map[string]any{}, c)
	if errors.Is(err, provider.ErrUnauthenticated) {
		return provider.LoginState{State: "expired"}, nil
	}
	if err != nil {
		return provider.LoginState{}, err
	}
	verified, err := loginStatus(raw)
	if err != nil {
		return provider.LoginState{}, err
	}
	if !verified {
		return provider.LoginState{State: "expired"}, nil
	}
	return provider.LoginState{State: "success", LoggedIn: true}, nil
}
func validRange(v string) bool {
	if v == "" || v == "__HEAD__" {
		return true
	}
	return regexp.MustCompile(`^bytes=([0-9]+-[0-9]*|-[0-9]+)$`).MatchString(v) && len(v) < 73
}

// The CGI envelope always has an explicit code. GetLoginUserInfo may omit the
// redundant data.code on success, but an empty/malformed payload is not proof.
func loginStatus(raw json.RawMessage) (bool, error) {
	obj := object(raw)
	if len(obj) == 0 {
		return false, errors.New("QQ 音乐未确认登录状态")
	}
	if value, exists := obj["code"]; exists {
		var code int
		if json.Unmarshal(value, &code) != nil {
			return false, errors.New("invalid QQ login status")
		}
		return code == 0, nil
	}
	if len(object(obj["info"])) == 0 {
		return false, errors.New("QQ 音乐未确认登录用户")
	}
	return true, nil
}

// requestSign signs the exact UTF-8 request bytes using the QQ desktop zzc format.
// This is a request checksum, separate from account credentials and media keys.
func requestSign(body []byte) string {
	digest := sha1.Sum(body)
	h := strings.ToUpper(hex.EncodeToString(digest[:]))
	var a, b strings.Builder
	for _, i := range []int{23, 14, 6, 36, 16, 7, 19} {
		a.WriteByte(h[i])
	}
	for _, i := range []int{16, 1, 32, 12, 19, 27, 8, 5} {
		b.WriteByte(h[i])
	}
	mask := [20]byte{89, 39, 179, 150, 218, 82, 58, 252, 177, 52, 186, 123, 120, 64, 242, 133, 143, 161, 121, 179}
	for i := range digest {
		digest[i] ^= mask[i]
	}
	mix := strings.NewReplacer("/", "", "+", "", "=", "").Replace(base64.StdEncoding.EncodeToString(digest[:]))
	return strings.ToLower("zzc" + a.String() + mix + b.String())
}
