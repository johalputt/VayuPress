package vayutor

import (
	"context"
	"strings"
	"testing"
)

func TestManagedTorrcHasOnlyWhatWeNeed(t *testing.T) {
	m := newManagedTor("/usr/bin/tor", "/var/lib/vayupress/tor")
	rc := m.buildTorrc()
	for _, want := range []string{
		"DataDirectory /var/lib/vayupress/tor/data",
		"ControlPort unix:/var/lib/vayupress/tor/control.sock",
		"CookieAuthentication 1",
		"SocksPort 0",
	} {
		if !strings.Contains(rc, want) {
			t.Errorf("torrc missing %q\n---\n%s", want, rc)
		}
	}
	// It must NOT open a relay/exit/dir surface.
	for _, forbidden := range []string{"ORPort", "ExitRelay 1", "DirPort"} {
		if strings.Contains(rc, forbidden) {
			t.Errorf("torrc must not contain %q", forbidden)
		}
	}
}

func TestManagedControlPaths(t *testing.T) {
	m := newManagedTor("tor", "/base/tor")
	if got, want := m.controlAddr(), "unix:/base/tor/control.sock"; got != want {
		t.Errorf("controlAddr = %q, want %q", got, want)
	}
	if got, want := m.cookiePath(), "/base/tor/data/control_auth_cookie"; got != want {
		t.Errorf("cookiePath = %q, want %q", got, want)
	}
}

func TestManagedEnsureNoBinary(t *testing.T) {
	m := newManagedTor("", t.TempDir())
	err := m.ensure(context.Background())
	if err == nil {
		t.Fatal("ensure with empty binary should fail")
	}
	if m.running() {
		t.Error("no process should be running after a failed ensure")
	}
	if le := m.lastError(); !strings.Contains(le, "tor binary not found") {
		t.Errorf("lastError = %q, want it to mention the missing binary", le)
	}
}

// TestManagedEnsureBogusBinary proves a non-existent binary path fails cleanly
// (no panic, no lingering "running" state) rather than hanging.
func TestManagedEnsureBogusBinary(t *testing.T) {
	m := newManagedTor("/nonexistent/definitely/not/tor", t.TempDir())
	if err := m.ensure(context.Background()); err == nil {
		t.Fatal("ensure with a bogus binary path should fail")
	}
	if m.running() {
		t.Error("no process should be running after a failed start")
	}
}

// TestExternalControlPreferred proves that when an external control port is
// reachable, the engine uses it and never touches managed mode.
func TestExternalControlPreferred(t *testing.T) {
	addr := fakeTorServer(t)
	e := NewEngine(Config{
		Enabled:     true,
		ControlAddr: addr,
		Target:      "127.0.0.1:8080",
		Managed:     true,
		TorBinary:   "/nonexistent/tor", // would fail if managed mode were reached
		ManagedDir:  t.TempDir(),
	})
	if err := e.ensureConnected(); err != nil {
		t.Fatalf("ensureConnected via external port: %v", err)
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if !e.connected {
		t.Fatal("engine should be connected")
	}
	if e.usingMgd {
		t.Error("engine should be on the external control port, not managed tor")
	}
}
