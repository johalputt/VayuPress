package safefetch

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSafeTransportEgressKillSwitch verifies the Tor-Space anti-leak chokepoint
// (audit H3/M7): when clearnet egress is blocked, every non-allowlisted host is
// refused with ErrBlockedAddress BEFORE any DNS/dial, while allowlisted loopback
// services still connect (so a local Meili/Ollama keeps working).
func TestSafeTransportEgressKillSwitch(t *testing.T) {
	SetBlockClearnetEgress(true)
	defer SetBlockClearnetEgress(false)
	tr := SafeTransport(TransportOptions{AllowHosts: []string{"127.0.0.1"}})

	// A public host must be refused with the block error, and no network touched.
	if _, err := tr.DialContext(context.Background(), "tcp", "example.com:443"); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("blocked egress: want ErrBlockedAddress for public host, got %v", err)
	}
	// An allowlisted loopback host bypasses the kill-switch (it will fail to
	// connect to a closed port, but NOT with the block error).
	if _, err := tr.DialContext(context.Background(), "tcp", "127.0.0.1:1"); errors.Is(err, ErrBlockedAddress) {
		t.Fatal("allowlisted loopback must bypass the egress kill-switch")
	}
}

func TestIsPrivateOrReservedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1", "10.0.0.1", "192.168.1.1", "172.16.0.1",
		"169.254.169.254", "100.100.100.200", "0.0.0.0", "224.0.0.1",
		"fe80::1", "fc00::1", "fd12:3456::1",
		"100.64.0.1", "192.0.0.1", "198.18.0.1", "240.0.0.1",
	}
	for _, s := range blocked {
		if !isPrivateOrReservedIP(net.ParseIP(s)) {
			t.Errorf("expected %s to be blocked", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:2800:220:1::1"}
	for _, s := range allowed {
		if isPrivateOrReservedIP(net.ParseIP(s)) {
			t.Errorf("expected %s to be allowed", s)
		}
	}
	if !isPrivateOrReservedIP(nil) {
		t.Error("nil IP must be treated as blocked")
	}
}

func TestGetAllowsPublicServer(t *testing.T) {
	// httptest binds to loopback; to exercise the happy path we point the
	// client's dialer at the test server directly, bypassing the IP guard, so
	// we can still verify body/size/Content-Type handling end to end.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	c := New(Options{})
	c.httpc.Transport = srv.Client().Transport // loopback transport for the test
	c.hostGuard = nil                          // bypass the host barrier for the loopback server

	res, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(res.Body) != "hello world" {
		t.Errorf("body = %q", res.Body)
	}
	if !strings.HasPrefix(res.ContentType, "text/plain") {
		t.Errorf("content-type = %q", res.ContentType)
	}
}

func TestGetRejectsPrivateHost(t *testing.T) {
	// The real guarded transport must refuse a loopback target.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("secret"))
	}))
	defer srv.Close()

	c := New(Options{})
	_, err := c.Get(context.Background(), srv.URL) // 127.0.0.1 — must be blocked
	if err == nil {
		t.Fatal("expected SSRF block for loopback host, got nil")
	}
}

func TestGetRejectsDisallowedScheme(t *testing.T) {
	c := New(Options{})
	_, err := c.Get(context.Background(), "file:///etc/passwd")
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("expected ErrBlockedAddress, got %v", err)
	}
	_, err = c.Get(context.Background(), "gopher://example.com")
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("expected ErrBlockedAddress for gopher, got %v", err)
	}
}

func TestGetEnforcesSizeCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 2048))
	}))
	defer srv.Close()

	c := New(Options{MaxBytes: 1024})
	c.httpc.Transport = srv.Client().Transport // loopback for the test
	c.hostGuard = nil                          // bypass the host barrier for the loopback server

	_, err := c.Get(context.Background(), srv.URL)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
}

func TestGetSizeCapBoundary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 1024))
	}))
	defer srv.Close()

	c := New(Options{MaxBytes: 1024})
	c.httpc.Transport = srv.Client().Transport
	c.hostGuard = nil // bypass the host barrier for the loopback server

	res, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("exactly-at-cap should succeed, got %v", err)
	}
	if len(res.Body) != 1024 {
		t.Errorf("body len = %d, want 1024", len(res.Body))
	}
}
