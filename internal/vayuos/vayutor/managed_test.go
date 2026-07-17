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

func TestParseBootstrap(t *testing.T) {
	cases := []struct {
		line    string
		wantPct int
		wantSum string
	}{
		{`NOTICE BOOTSTRAP PROGRESS=100 TAG=done SUMMARY="Done"`, 100, "Done"},
		{`NOTICE BOOTSTRAP PROGRESS=45 TAG=requesting_descriptors SUMMARY="Asking for relay descriptors"`, 45, "Asking for relay descriptors"},
		{`NOTICE BOOTSTRAP PROGRESS=0 TAG=starting SUMMARY="Starting"`, 0, "Starting"},
		{"garbage", 0, ""},
	}
	for _, c := range cases {
		pct, sum := parseBootstrap(c.line)
		if pct != c.wantPct || sum != c.wantSum {
			t.Errorf("parseBootstrap(%q) = (%d,%q), want (%d,%q)", c.line, pct, sum, c.wantPct, c.wantSum)
		}
	}
}

func TestManagedTorrcBridges(t *testing.T) {
	m := newManagedTor("/usr/bin/tor", "/var/lib/vayupress/tor")

	// No bridges set → no bridge directives (keeps the onion-only shape).
	if rc := m.buildTorrc(); strings.Contains(rc, "UseBridges") || strings.Contains(rc, "Bridge ") {
		t.Errorf("torrc should have no bridge lines when unset:\n%s", rc)
	}

	// obfs4 bridges + a PT path → full block.
	m.setBridges([]string{"obfs4 1.2.3.4:443 ABCDEF cert=zzz iat-mode=0"}, "/usr/bin/obfs4proxy")
	rc := m.buildTorrc()
	for _, want := range []string{
		"ClientTransportPlugin obfs4 exec /usr/bin/obfs4proxy",
		"UseBridges 1",
		"Bridge obfs4 1.2.3.4:443 ABCDEF cert=zzz iat-mode=0",
	} {
		if !strings.Contains(rc, want) {
			t.Errorf("torrc missing %q\n%s", want, rc)
		}
	}

	// Vanilla bridge (no PT) → UseBridges but NO ClientTransportPlugin.
	m.setBridges([]string{"5.6.7.8:9001 0123456789"}, "")
	rc = m.buildTorrc()
	if strings.Contains(rc, "ClientTransportPlugin") {
		t.Errorf("vanilla bridge should not emit ClientTransportPlugin:\n%s", rc)
	}
	if !strings.Contains(rc, "UseBridges 1") || !strings.Contains(rc, "Bridge 5.6.7.8:9001 0123456789") {
		t.Errorf("vanilla bridge lines missing:\n%s", rc)
	}
}

func TestResolvePTEmbeddedFallback(t *testing.T) {
	m := newManagedTor("tor", t.TempDir())
	SetEmbeddedObfs4("")
	if m.resolvePT() != "" {
		t.Skip("a system obfs4proxy is present in the test environment")
	}
	SetEmbeddedObfs4("/opt/vayupress __run-obfs4")
	defer SetEmbeddedObfs4("")
	if got := m.resolvePT(); got != "/opt/vayupress __run-obfs4" {
		t.Errorf("resolvePT = %q, want the embedded obfs4 fallback", got)
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
	if le := m.lastError(); !strings.Contains(le, "not installed") {
		t.Errorf("lastError = %q, want it to mention tor is not installed", le)
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
