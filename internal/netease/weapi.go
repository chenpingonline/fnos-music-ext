package netease

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chenpingonline/fnos-music-ext/internal/provider"
)

// The MusicBox QR flow uses WEAPI on music.163.com, rather than the EAPI
// transport used for playback. Each account keeps its own browser cookie jar.
func (n *Native) initWebSession() error {
	base := *n.base
	if base.Scheme == "https" && base.Host == "interface.music.163.com" {
		base.Host = "music.163.com"
	}
	n.webBase = &base
	client := *n.client
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	client.Jar = jar
	n.webClient = &client
	device := make([]byte, 26)
	if _, err = rand.Read(device); err != nil {
		return err
	}
	nuid := make([]byte, 16)
	if _, err = rand.Read(nuid); err != nil {
		return err
	}
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)
	defaults := map[string]string{"deviceId": strings.ToUpper(hex.EncodeToString(device)), "os": "pc", "appver": "3.1.17.204416", "osver": "Microsoft-Windows-10-Professional-build-19045-64bit", "channel": "netease", "__remember_me": "true", "_ntes_nuid": hex.EncodeToString(nuid), "_ntes_nnid": hex.EncodeToString(nuid) + "," + now, "WNMCID": hex.EncodeToString(nuid[:3]) + "." + now + ".01.0", "WEVNSM": "1.0.0"}
	for name, value := range defaults {
		jar.SetCookies(n.webBase, []*http.Cookie{{Name: name, Value: value, Path: "/"}})
	}
	n.seedWebCookies(n.cookie)
	return nil
}
func (n *Native) seedWebCookies(raw string) {
	req := http.Request{Header: make(http.Header)}
	req.Header.Set("Cookie", raw)
	for _, cookie := range req.Cookies() {
		cookie.Path = "/"
		n.webClient.Jar.SetCookies(n.webBase, []*http.Cookie{cookie})
	}
}
func weapiForm(params any) (url.Values, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	return weapiFormWithKey(params, []byte(hex.EncodeToString(random)))
}
func weapiFormWithKey(params any, key []byte) (url.Values, error) {
	if len(key) != 16 {
		return nil, errors.New("invalid WEAPI key")
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	encrypt := func(raw, key []byte) []byte {
		pad := aes.BlockSize - len(raw)%aes.BlockSize
		plain := append([]byte(nil), raw...)
		for i := 0; i < pad; i++ {
			plain = append(plain, byte(pad))
		}
		block, _ := aes.NewCipher(key)
		out := make([]byte, len(plain))
		cipher.NewCBCEncrypter(block, []byte("0102030405060708")).CryptBlocks(out, plain)
		return []byte(base64.StdEncoding.EncodeToString(out))
	}
	encrypted := encrypt(encrypt(raw, []byte("0CoJUm6Qyw8W8jud")), key)
	reversed := append([]byte(nil), key...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	modulus, _ := new(big.Int).SetString("00e0b509f6259df8642dbc35662901477df22677ec152b5ff68ace615bb7b725152b3ab17a876aea8a5aa76d2e417629ec4ee341f56135fccf695280104e0312ecbda92557c93870114af6c9d05c4f7f0c3685b7a46bee255932575cce10b424d813cfe4875d3e82047b97ddef52741d546b8e289dc6935b3ece0462db0a22b8e7", 16)
	rsa := new(big.Int).Exp(new(big.Int).SetBytes(reversed), big.NewInt(65537), modulus)
	return url.Values{"params": {string(encrypted)}, "encSecKey": {fmt.Sprintf("%0256x", rsa)}}, nil
}
func (n *Native) webPost(ctx context.Context, path string, params map[string]any) (map[string]json.RawMessage, error) {
	if !strings.HasPrefix(path, "/weapi/") || strings.ContainsAny(path, "?#") {
		return nil, errors.New("invalid WEAPI path")
	}
	if params == nil {
		params = map[string]any{}
	}
	csrf := ""
	for _, c := range n.webClient.Jar.Cookies(n.webBase) {
		if c.Name == "__csrf" {
			csrf = c.Value
		}
	}
	params["csrf_token"] = csrf
	form, err := weapiForm(params)
	if err != nil {
		return nil, err
	}
	target := *n.webBase
	target.Path = path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", "https://music.163.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 11_1_0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/88.0.4324.87 Safari/537.36")
	res, err := n.webClient.Do(req)
	if err != nil {
		return nil, errors.New("网易云登录接口连接失败，请稍后重试。")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("网易云登录接口 HTTP %d，请稍后重试。", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, errors.New("网易云登录响应读取失败。")
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil, errors.New("网易云登录响应格式异常，请稍后重试。")
	}
	code := intField(obj, "code")
	if code == 301 || code == 401 {
		return nil, provider.ErrUnauthenticated
	}
	// Only successful authorization may expose a session to CheckQRLogin.
	if qrLoginCode(obj) == 803 {
		var cookies []string
		for _, c := range res.Cookies() {
			if c.Name == "MUSIC_U" || c.Name == "NMTID" || c.Name == "__csrf" {
				cookies = append(cookies, c.Name+"="+c.Value)
			}
		}
		if cookie := strings.Join(cookies, "; "); validSessionCookie(cookie) {
			obj["cookie"], _ = json.Marshal(cookie)
		}
	}
	return obj, nil
}
func qrLoginCode(obj map[string]json.RawMessage) int64 {
	code := intField(obj, "code")
	if code == 0 || code == 200 {
		if nested := intField(unwrapObject(obj["data"]), "code"); nested != 0 {
			return nested
		}
	}
	return code
}

// QRStateError contains only a numeric upstream code, never provider content.
type QRStateError struct{ Code int64 }

func (e *QRStateError) Error() string {
	return fmt.Sprintf("网易云扫码接口返回异常状态码 %d，请重新生成二维码后重试。", e.Code)
}
