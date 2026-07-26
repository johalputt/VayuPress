// SPDX-License-Identifier: Apache-2.0

package vayutor

import (
	"testing"
	"time"
)

// setLive is a test helper that sets the engine's live state fields (normally
// maintained by reconcile) so computeHealth/evalHealth can be exercised directly.
func (e *Engine) setLive(connected bool, boot, wantN int, onions ...string) {
	e.mu.Lock()
	e.connected = connected
	e.bootPct = boot
	e.wantCount = wantN
	e.onionByHost = map[string]string{}
	for _, h := range onions {
		e.onionByHost[h] = h + ".onion"
	}
	e.mu.Unlock()
}

func TestComputeHealthStates(t *testing.T) {
	active := true
	e := NewEngine(Config{Enabled: true, Active: func() bool { return active }})

	active = false
	e.setLive(false, 0, 0)
	if s, _ := e.computeHealth(); s != HealthOff {
		t.Errorf("inactive: got %q want off", s)
	}

	active = true
	e.setLive(false, 0, 2)
	if s, _ := e.computeHealth(); s != HealthDown {
		t.Errorf("not connected: got %q want down", s)
	}
	e.setLive(true, 50, 2)
	if s, _ := e.computeHealth(); s != HealthStarting {
		t.Errorf("bootstrapping: got %q want starting", s)
	}
	e.setLive(true, 100, 2, "a.in")
	if s, _ := e.computeHealth(); s != HealthDegraded {
		t.Errorf("partial onions: got %q want degraded", s)
	}
	e.setLive(true, 100, 2, "a.in", "b.in")
	if s, _ := e.computeHealth(); s != HealthHealthy {
		t.Errorf("all up: got %q want healthy", s)
	}
}

func TestHealthAlertsDownThenRecover(t *testing.T) {
	active := true
	var events []string
	e := NewEngine(Config{
		Enabled: true,
		Active:  func() bool { return active },
		Notify:  func(ev string, _ map[string]any) { events = append(events, ev) },
	})

	// Reach healthy (benign → commits immediately, no alert).
	e.setLive(true, 100, 1, "a.in")
	e.evalHealth()
	if got := e.Snapshot().Health; got != HealthHealthy {
		t.Fatalf("expected healthy, got %q", got)
	}
	if len(events) != 0 {
		t.Fatalf("no alert expected reaching healthy, got %v", events)
	}

	// Connection drops: first eval only arms the debounce — no commit, no alert.
	e.setLive(false, 0, 1)
	e.evalHealth()
	if got := e.Snapshot().Health; got != HealthHealthy {
		t.Fatalf("down should be debounced, still healthy; got %q", got)
	}
	if len(events) != 0 {
		t.Fatalf("no alert during debounce, got %v", events)
	}

	// Rewind the pending timer past the debounce, then eval → commit + alert.
	e.mu.Lock()
	e.healthPendingSince = time.Now().Add(-2 * healthAlertDebounce)
	e.mu.Unlock()
	e.evalHealth()
	if got := e.Snapshot().Health; got != HealthDown {
		t.Fatalf("expected down after debounce, got %q", got)
	}
	if len(events) != 1 || events[0] != "tor.onion_down" {
		t.Fatalf("expected one tor.onion_down, got %v", events)
	}

	// Recovery is benign → commits immediately and fires exactly one recovered.
	e.setLive(true, 100, 1, "a.in")
	e.evalHealth()
	if got := e.Snapshot().Health; got != HealthHealthy {
		t.Fatalf("expected healthy after recovery, got %q", got)
	}
	if len(events) != 2 || events[1] != "tor.onion_recovered" {
		t.Fatalf("expected tor.onion_recovered, got %v", events)
	}

	// Re-eval while stable must not re-fire.
	e.evalHealth()
	if len(events) != 2 {
		t.Fatalf("stable re-eval should not alert, got %v", events)
	}
}

func TestHealthRecoveryLatchSurvivesStarting(t *testing.T) {
	// down → starting → healthy must still produce exactly one recovered alert
	// (the down→starting hop is benign and must not clear the latch).
	active := true
	var events []string
	e := NewEngine(Config{
		Enabled: true,
		Active:  func() bool { return active },
		Notify:  func(ev string, _ map[string]any) { events = append(events, ev) },
	})
	e.setLive(true, 100, 1, "a.in")
	e.evalHealth() // healthy
	e.setLive(false, 0, 1)
	e.evalHealth()
	e.mu.Lock()
	e.healthPendingSince = time.Now().Add(-2 * healthAlertDebounce)
	e.mu.Unlock()
	e.evalHealth() // down + alert
	e.setLive(true, 40, 1)
	e.evalHealth() // starting (benign)
	e.setLive(true, 100, 1, "a.in")
	e.evalHealth() // healthy → recovered
	if len(events) != 2 || events[0] != "tor.onion_down" || events[1] != "tor.onion_recovered" {
		t.Fatalf("want [down, recovered], got %v", events)
	}
}

func TestHealthLogBounded(t *testing.T) {
	active := true
	e := NewEngine(Config{Enabled: true, Active: func() bool { return active }})
	// Flip between healthy and starting many times; each commit appends a log row.
	for i := 0; i < healthLogMax*2; i++ {
		if i%2 == 0 {
			e.setLive(true, 100, 1, "a.in")
		} else {
			e.setLive(true, 30, 1)
		}
		e.evalHealth()
	}
	if got := len(e.Snapshot().HealthLog); got > healthLogMax {
		t.Fatalf("health log unbounded: %d > %d", got, healthLogMax)
	}
}
