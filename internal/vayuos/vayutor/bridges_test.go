package vayutor

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyBridges(t *testing.T) {
	in := []string{
		"Bridge obfs4 1.2.3.4:443 ABCDEF cert=xyz iat-mode=0", // leading keyword + PT
		"  5.6.7.8:9001 0123456789ABCDEF  ",                   // vanilla, trailing space
		"",                                                    // dropped
	}
	out, needsPT := classifyBridges(in)
	if len(out) != 2 {
		t.Fatalf("got %d lines, want 2: %v", len(out), out)
	}
	if !needsPT {
		t.Error("obfs4 line should require a pluggable transport")
	}
	if strings.HasPrefix(out[0], "Bridge ") {
		t.Errorf("leading Bridge keyword not stripped: %q", out[0])
	}
	if v := vanillaOnly(out); len(v) != 1 || strings.Contains(v[0], "obfs4") {
		t.Errorf("vanillaOnly = %v, want the single non-obfs4 line", v)
	}
}

func TestClassifyBridgesVanillaOnly(t *testing.T) {
	out, needsPT := classifyBridges([]string{"1.2.3.4:443 FINGERPRINT"})
	if needsPT {
		t.Error("a bare IP:port bridge needs no PT")
	}
	if len(out) != 1 {
		t.Fatalf("got %v", out)
	}
}

func TestLogHasNoRoute(t *testing.T) {
	if !logHasNoRoute("... (No route to host; NOROUTE; count 2 ...)") {
		t.Error("should detect No route to host")
	}
	if logHasNoRoute("Bootstrapped 30%: Loading networkstatus consensus") {
		t.Error("normal bootstrap is not a NOROUTE")
	}
}

// TestEscalationSkipsToBridgesWithOperatorBridges: when the operator has
// configured bridges, a stall escalates straight to the bridges rung (skipping
// firewall-friendly ports, which can't fix an IP-level block), and the rung is
// terminal; teardown resets the ladder.
func TestEscalationSkipsToBridgesWithOperatorBridges(t *testing.T) {
	e := NewEngine(Config{
		Enabled:    true,
		Managed:    true,
		ManagedDir: t.TempDir(),
		TorBinary:  "/nonexistent/tor",
		Bridges:    []string{"1.2.3.4:443 ABCDEF0123"}, // vanilla → no obfs4proxy needed
	})
	// Simulate a managed bootstrap that has stalled at 30%.
	e.mu.Lock()
	e.usingMgd = true
	e.bootPct = 30
	e.bootBestPct = 30
	e.bootMovedAt = time.Now().Add(-2 * bootStallTimeout)
	e.mu.Unlock()

	e.maybeEscalate()
	if e.esc != escBridges {
		t.Fatalf("esc = %d, want escBridges (operator bridges skip fascist)", e.esc)
	}
	if !e.managed.usingBridges() {
		t.Error("managed tor should have bridges configured")
	}

	// Terminal: a further stall must not escalate past bridges.
	e.mu.Lock()
	e.bootMovedAt = time.Now().Add(-2 * bootStallTimeout)
	e.mu.Unlock()
	e.maybeEscalate()
	if e.esc != escBridges {
		t.Errorf("escBridges must be terminal, got %d", e.esc)
	}

	// Deactivation clears the ladder + bridges.
	e.teardown()
	if e.esc != escDirect {
		t.Errorf("teardown should reset esc to escDirect, got %d", e.esc)
	}
	if e.managed.usingBridges() {
		t.Error("teardown should clear managed bridges")
	}
}

// TestOperatorBridgesPrefersLive: the settings-backed (VayuOS form) source wins
// over the static env value; an empty live source falls back to env.
func TestOperatorBridgesPrefersLive(t *testing.T) {
	e := NewEngine(Config{Bridges: []string{"env-bridge"}, BridgesLive: func() []string { return []string{"live-bridge"} }})
	if got := e.operatorBridges(); len(got) != 1 || got[0] != "live-bridge" {
		t.Fatalf("operatorBridges = %v, want the live settings bridge", got)
	}
	e2 := NewEngine(Config{Bridges: []string{"env-bridge"}, BridgesLive: func() []string { return nil }})
	if got := e2.operatorBridges(); len(got) != 1 || got[0] != "env-bridge" {
		t.Fatalf("operatorBridges = %v, want the env fallback", got)
	}
}

// TestBridgesLiveAppliedAndCleared: applyOperatorBridges configures + selects the
// bridges rung from the live (VayuOS form) source without waiting for a stall,
// and clearing them (no env fallback) drops back to a direct connection.
func TestBridgesLiveAppliedAndCleared(t *testing.T) {
	live := []string{"9.9.9.9:443 LIVEFINGERPRINT"}
	e := NewEngine(Config{
		Enabled:     true,
		Managed:     true,
		ManagedDir:  t.TempDir(),
		TorBinary:   "/nonexistent/tor",
		BridgesLive: func() []string { return live },
	})

	e.applyOperatorBridges()
	if !e.managed.usingBridges() {
		t.Fatal("applyOperatorBridges should configure the managed tor with bridges")
	}
	if e.esc != escBridges {
		t.Fatalf("esc = %d, want escBridges after applying operator bridges", e.esc)
	}
	if !e.managed.bridgesEqual([]string{"9.9.9.9:443 LIVEFINGERPRINT"}, "") {
		t.Error("managed bridges should equal the live set")
	}

	// Clearing the live bridges reverts to a direct connection.
	live = nil
	e.applyOperatorBridges()
	if e.esc != escDirect {
		t.Errorf("clearing bridges should return to escDirect, got %d", e.esc)
	}
	if e.managed.usingBridges() {
		t.Error("managed bridges should be cleared")
	}
}

// TestEscalationDirectToFascist: a generic stall with no NOROUTE and no operator
// bridges takes the firewall-friendly rung first.
func TestEscalationDirectToFascist(t *testing.T) {
	e := NewEngine(Config{
		Enabled:    true,
		Managed:    true,
		ManagedDir: t.TempDir(),
		TorBinary:  "/nonexistent/tor",
	})
	e.mu.Lock()
	e.usingMgd = true
	e.bootPct = 10
	e.bootBestPct = 10
	e.bootMovedAt = time.Now().Add(-2 * bootStallTimeout)
	e.mu.Unlock()

	e.maybeEscalate()
	if e.esc != escFascist {
		t.Fatalf("esc = %d, want escFascist", e.esc)
	}
	if !e.managed.isStrict() {
		t.Error("fascist rung should enable strict (80/443) mode")
	}
}
