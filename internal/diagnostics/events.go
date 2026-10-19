// Package diagnostics records bounded operational counters and redacted errors.
// Raw errors, request headers, URLs and upstream response bodies never enter logs.
package diagnostics

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
)

type Failure struct {
	Kind  string
	Cause error
}

func (e *Failure) Error() string { return e.Kind }
func (e *Failure) Unwrap() error { return e.Cause }
func New(kind string) error      { return &Failure{Kind: kind} }

type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("upstream HTTP %d", e.Status) }
func Code(err error) string {
	if err == nil {
		return "ok"
	}
	var failure *Failure
	if errors.As(err, &failure) {
		return failure.Kind
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "dns"
	}
	var cert *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	if errors.As(err, &cert) || errors.As(err, &unknown) || errors.As(err, &invalid) {
		return "tls"
	}
	var status *HTTPError
	if errors.As(err, &status) {
		return fmt.Sprintf("http-%d", status.Status)
	}
	if errors.Is(err, syscall.ENOSPC) {
		return "disk-full"
	}
	if errors.Is(err, os.ErrPermission) {
		return "permission"
	}
	var network net.Error
	if errors.As(err, &network) {
		if network.Timeout() {
			return "timeout"
		}
		return "network"
	}
	return "source-error"
}

type Event struct {
	Time      time.Time `json:"time"`
	Operation string    `json:"operation"`
	Code      string    `json:"code"`
	Resource  string    `json:"resource,omitempty"`
}
type Report struct {
	Counters map[string]uint64 `json:"counters"`
	Recent   []Event           `json:"recent"`
}

var state = struct {
	sync.Mutex
	counters map[string]uint64
	recent   []Event
	logged   map[string]time.Time
}{counters: map[string]uint64{}, logged: map[string]time.Time{}}

func Observe(operation, outcome string) {
	state.Lock()
	state.counters[operation+":"+outcome]++
	state.Unlock()
}
func Record(operation, resource string, err error) {
	code := Code(err)
	Observe(operation, code)
	if err == nil || code == "cancelled" {
		return
	}
	sum := sha256.Sum256([]byte(resource))
	token := hex.EncodeToString(sum[:8])
	event := Event{Time: time.Now().UTC(), Operation: operation, Code: code, Resource: token}
	key := operation + ":" + code + ":" + token
	state.Lock()
	if last := state.logged[key]; time.Since(last) < 30*time.Second {
		state.Unlock()
		return
	}
	if len(state.logged) >= 512 {
		state.logged = map[string]time.Time{}
	}
	state.logged[key] = event.Time
	state.recent = append(state.recent, event)
	if len(state.recent) > 100 {
		state.recent = append([]Event(nil), state.recent[len(state.recent)-100:]...)
	}
	state.Unlock()
	raw, _ := json.Marshal(event)
	log.Printf("fnmusic-go diagnostic %s", raw)
}
func Snapshot() Report {
	state.Lock()
	defer state.Unlock()
	out := Report{Counters: map[string]uint64{}, Recent: append([]Event{}, state.recent...)}
	for k, v := range state.counters {
		out.Counters[k] = v
	}
	return out
}

func Message(err error) string {
	code := Code(err)
	switch code {
	case "invalid-range":
		return "音源续传范围无效，请重试"
	case "timeout":
		return "连接超时，请检查网络后重试"
	case "dns":
		return "域名解析失败，请检查 DNS 后重试"
	case "tls":
		return "安全连接验证失败，请检查设备时间和证书"
	case "disk-full":
		return "存储空间不足，请清理后重试"
	case "permission":
		return "存储目录不可写，请检查目录权限"
	default:
		return "任务失败（" + code + "），请查看诊断记录后重试"
	}
}
