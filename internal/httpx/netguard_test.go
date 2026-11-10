package httpx

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsPublicIPSpecialPurposeRanges(t *testing.T) {
	blocked := []string{
		"0.1.2.3", "10.0.0.8", "100.64.0.1", "100.127.255.254", "127.0.0.1",
		"169.254.169.254", "172.16.5.5", "192.0.0.8", "192.0.2.1", "192.88.99.1",
		"192.168.1.1", "198.18.0.1", "198.19.255.255", "198.51.100.7", "203.0.113.9",
		"224.0.0.1", "240.0.0.1", "255.255.255.255",
		"::", "::1", "::127.0.0.1", "::ffff:127.0.0.1", "::ffff:10.0.0.1",
		"::ffff:0:a00:1", "64:ff9b::a00:1", "64:ff9b:1::1", "100::1", "2001::1",
		"2001:db8::1", "2002:a00:1::1", "3fff::1", "5f00::1", "fc00::1", "fd12::1",
		"fe80::1", "fec0::1", "ff02::1",
	}
	for _, s := range blocked {
		if IsPublicIP(net.ParseIP(s)) {
			t.Errorf("IsPublicIP(%s) = true, want false", s)
		}
	}
	allowed := []string{"93.184.216.34", "1.1.1.1", "100.128.0.1", "198.20.0.1", "::ffff:1.1.1.1", "2606:4700:4700::1111", "2a00:1450:4001::200e"}
	for _, s := range allowed {
		if !IsPublicIP(net.ParseIP(s)) {
			t.Errorf("IsPublicIP(%s) = false, want true", s)
		}
	}
	if IsPublicIP(nil) {
		t.Error("IsPublicIP(nil) = true")
	}
}

func TestGuardedDialRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	if conn, err := GuardedDialContext(context.Background(), "tcp", addr); err == nil {
		conn.Close()
		t.Fatalf("dialed %s", addr)
	}
	_, port, _ := net.SplitHostPort(addr)
	if conn, err := GuardedDialContext(context.Background(), "tcp", net.JoinHostPort("localhost", port)); err == nil {
		conn.Close()
		t.Fatal("dialed localhost")
	}
}
