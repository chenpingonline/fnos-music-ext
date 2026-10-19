package diagnostics

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

func TestCodesAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{{context.DeadlineExceeded, "timeout"}, {&net.DNSError{Err: "secret", Name: "private.invalid"}, "dns"}, {x509.UnknownAuthorityError{}, "tls"}, {&HTTPError{Status: 403}, "http-403"}, {syscall.ENOSPC, "disk-full"}, {syscall.EACCES, "permission"}, {New("non-public-address"), "non-public-address"}} {
		if got := Code(tc.err); got != tc.code {
			t.Fatalf("got=%s want=%s", got, tc.code)
		}
	}
	var output bytes.Buffer
	before := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(before)
	secret := "https://private.example/image?token=do-not-log"
	err := &url.Error{Op: "Get", URL: secret, Err: errors.New("Cookie=super-secret")}
	Record("redaction-test", secret, err)
	Observe("cover-cache-test", "hit")
	raw, _ := json.Marshal(Snapshot())
	for _, s := range []string{output.String(), string(raw)} {
		if strings.Contains(s, "do-not-log") || strings.Contains(s, "super-secret") || strings.Contains(s, "private.example") {
			t.Fatal("credentials or URLs escaped redaction")
		}
	}
	if !strings.Contains(output.String(), "network") {
		t.Fatal(output.String())
	}
}
