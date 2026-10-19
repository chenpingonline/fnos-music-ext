package netease

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestMediaDialTriesNextValidatedAddress(t *testing.T) {
	ips := []net.IPAddr{{IP: net.ParseIP("2606:4700:4700::1111")}, {IP: net.ParseIP("1.1.1.1")}}
	first, peer := net.Pipe()
	defer first.Close()
	defer peer.Close()
	calls := []string{}
	conn, err := dialMediaAddresses(context.Background(), "tcp", "443", ips, func(ctx context.Context, network, address string) (net.Conn, error) {
		calls = append(calls, address)
		if len(calls) == 1 {
			return nil, errors.New("IPv6 network unreachable")
		}
		return first, nil
	})
	if err != nil || conn != first || len(calls) != 2 || calls[1] != "1.1.1.1:443" {
		t.Fatalf("fallback failed: calls=%v err=%v", calls, err)
	}
}
func TestMediaDialStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := dialMediaAddresses(ctx, "tcp", "443", []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("8.8.8.8")}}, func(context.Context, string, string) (net.Conn, error) {
		calls++
		cancel()
		return nil, context.Canceled
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
