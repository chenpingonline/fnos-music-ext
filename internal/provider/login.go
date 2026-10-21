package provider

import "context"

// QRLogin is an ephemeral provider QR challenge. It is never persisted.
// The challenge key must remain server-side and is used only for polling.
type QRLogin struct {
	Key     string `json:"-"`
	QRURL   string `json:"qr_url,omitempty"`
	QRImage string `json:"qr_image,omitempty"`
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
