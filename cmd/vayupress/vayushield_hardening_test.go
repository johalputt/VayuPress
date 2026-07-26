// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestShieldTierIntentFlags proves the panel's only action is writing/removing an
// empty intent flag — and that it is idempotent.
func TestShieldTierIntentFlags(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYUSHIELD_CONTROL_DIR", dir)

	if shieldTierWanted(2) {
		t.Fatal("tier2 should not be wanted initially")
	}
	if err := shieldSetTierWant(2, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !shieldTierWanted(2) {
		t.Fatal("tier2 should be wanted after enable")
	}
	if _, err := os.Stat(filepath.Join(dir, "tier2.want")); err != nil {
		t.Fatalf("flag file missing: %v", err)
	}
	if err := shieldSetTierWant(2, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if shieldTierWanted(2) {
		t.Fatal("tier2 should not be wanted after disable")
	}
	// Disabling again (flag already gone) must be a no-op, not an error.
	if err := shieldSetTierWant(2, false); err != nil {
		t.Fatalf("idempotent disable: %v", err)
	}
}

// TestShieldAgentAliveHeartbeat proves the panel detects the root agent only via
// a fresh heartbeat.
func TestShieldAgentAliveHeartbeat(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYUSHIELD_CONTROL_DIR", dir)
	if shieldAgentAlive() {
		t.Fatal("agent should read dead with no heartbeat")
	}
	beat := filepath.Join(dir, "agent.alive")
	if err := os.WriteFile(beat, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !shieldAgentAlive() {
		t.Fatal("fresh heartbeat should read alive")
	}
	old := time.Now().Add(-2 * time.Minute)
	_ = os.Chtimes(beat, old, old)
	if shieldAgentAlive() {
		t.Fatal("stale heartbeat should read dead")
	}
}

// TestShieldTierState reads the agent-reported state string.
func TestShieldTierState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYUSHIELD_CONTROL_DIR", dir)
	if shieldTierState(3) != "" {
		t.Fatal("no state file should be empty")
	}
	if err := os.WriteFile(filepath.Join(dir, "tier3.state"), []byte("active\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := shieldTierState(3); got != "active" {
		t.Fatalf("state = %q, want active", got)
	}
}
