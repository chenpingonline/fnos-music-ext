package netease

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// QRLogin is an ephemeral NetEase QR challenge. It is never persisted.
// The challenge key must remain server-side and is used only for polling.
type QRLogin struct {
	Key     string `json:"-"`
	QRURL   string `json:"qr_url"`
	QRASCII string `json:"qr_ascii,omitempty"`
}
type LoginState struct {
	State    string `json:"state"` // pending|scanned|success|expired|unknown
	LoggedIn bool   `json:"logged_in"`
}

type LoginAdapter interface {
	BeginQRLogin(context.Context) (QRLogin, error)
	CheckQRLogin(context.Context, string) (LoginState, error)
	AuthStatus(context.Context) (LoginState, error)
}

func unwrapObject(raw json.RawMessage) map[string]json.RawMessage {
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(raw, &obj)
	return obj
}

func stringField(obj map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		var s string
		if json.Unmarshal(obj[k], &s) == nil && s != "" {
			return s
		}
	}
	return ""
}

func intField(obj map[string]json.RawMessage, keys ...string) int64 {
	for _, k := range keys {
		if len(obj[k]) == 0 {
			continue
		}
		var n json.Number
		d := json.NewDecoder(strings.NewReader(string(obj[k])))
		d.UseNumber()
		if d.Decode(&n) == nil {
			if i, e := n.Int64(); e == nil {
				return i
			}
		}
		var s string
		if json.Unmarshal(obj[k], &s) == nil {
			if i, e := strconv.ParseInt(s, 10, 64); e == nil {
				return i
			}
		}
	}
	return 0
}

func stateFromCode(code int64) LoginState {
	switch code {
	case 800:
		return LoginState{State: "expired"}
	case 801:
		return LoginState{State: "pending"}
	case 802:
		return LoginState{State: "scanned"}
	case 803:
		return LoginState{State: "success", LoggedIn: true}
	default:
		return LoginState{State: "unknown"}
	}
}
