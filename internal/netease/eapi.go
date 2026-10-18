package netease

import (
	"crypto/aes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// eapiForm encodes the EAPI wire format using only Go standard-library cryptography.
// These are undocumented, changeable upstream protocols, not an authorization bypass.
func eapiForm(apiPath string, params any) (url.Values, error) {
	if !strings.HasPrefix(apiPath, "/api/") || strings.ContainsAny(apiPath, "?#") {
		return nil, errors.New("invalid EAPI path")
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	sign := md5.Sum([]byte("nobody" + apiPath + "use" + string(body) + "md5forencrypt"))
	plain := []byte(fmt.Sprintf("%s-36cd479b6b5-%s-36cd479b6b5-%x", apiPath, body, sign))
	block, err := aes.NewCipher([]byte("e82ckenh8dichen8"))
	if err != nil {
		return nil, err
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	for i := 0; i < pad; i++ {
		plain = append(plain, byte(pad))
	}
	encrypted := make([]byte, len(plain))
	for i := 0; i < len(plain); i += aes.BlockSize {
		block.Encrypt(encrypted[i:i+aes.BlockSize], plain[i:i+aes.BlockSize])
	}
	return url.Values{"params": {strings.ToUpper(hex.EncodeToString(encrypted))}}, nil
}

func decodeEAPIResponse(body []byte) ([]byte, error) {
	// Some endpoints reply JSON directly; others reply encrypted binary.
	trim := strings.TrimSpace(string(body))
	if strings.HasPrefix(trim, "{") || strings.HasPrefix(trim, "[") {
		return []byte(trim), nil
	}
	if len(body) == 0 || len(body)%aes.BlockSize != 0 {
		return nil, errors.New("invalid EAPI response")
	}
	b, err := aes.NewCipher([]byte("e82ckenh8dichen8"))
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(body))
	for i := 0; i < len(body); i += aes.BlockSize {
		b.Decrypt(plain[i:i+aes.BlockSize], body[i:i+aes.BlockSize])
	}
	pad := int(plain[len(plain)-1])
	if pad < 1 || pad > aes.BlockSize {
		return nil, errors.New("bad EAPI padding")
	}
	for _, v := range plain[len(plain)-pad:] {
		if int(v) != pad {
			return nil, errors.New("bad EAPI padding")
		}
	}
	plain = plain[:len(plain)-pad]
	if !json.Valid(plain) {
		return nil, errors.New("invalid EAPI JSON")
	}
	return plain, nil
}
