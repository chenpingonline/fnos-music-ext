package netease

import (
	"encoding/json"
	"github.com/chenpingonline/fnos-music-ext/internal/provider"
	"strconv"
	"strings"
)

type QRLogin = provider.QRLogin
type LoginState = provider.LoginState
type LoginAdapter = provider.LoginAdapter

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
