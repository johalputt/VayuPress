package budget

import (
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/severity"
)

func consumed(st []Status, name string) int {
	for _, s := range st {
		if s.Name == name {
			return s.Consumed
		}
	}
	return -1
}

// TestRecordToChargesOnlyNamed pins the fix for the false "degradation-debt
// exhausted" signal: a VayuShield bot-block must charge ONLY the tolerant,
// bot-specific budget, never the strict general degradation-debt budget (both of
// which track WARN).
func TestRecordToChargesOnlyNamed(t *testing.T) {
	l := NewLedger(DefaultRules())
	now := time.Now()
	for i := 0; i < 8; i++ {
		l.RecordTo("bot-attack-intensity", "vayushield", now)
	}
	st := l.Status(now)
	if got := consumed(st, "bot-attack-intensity"); got != 8 {
		t.Errorf("bot-attack-intensity consumed = %d, want 8", got)
	}
	if got := consumed(st, "degradation-debt"); got != 0 {
		t.Errorf("degradation-debt consumed = %d, want 0 (bot-blocks must not drain it)", got)
	}
}

// TestRecordFromStillChargesBySeverity guards that the broadcast path is
// unchanged: a real WARN still charges every WARN-tracking budget.
func TestRecordFromStillChargesBySeverity(t *testing.T) {
	l := NewLedger(DefaultRules())
	now := time.Now()
	l.RecordFrom(severity.Warn, "health", now)
	st := l.Status(now)
	if got := consumed(st, "degradation-debt"); got != 1 {
		t.Errorf("degradation-debt consumed = %d, want 1 for a genuine WARN", got)
	}
}

// TestRecordToUnknownIsNoOp: an unknown budget name charges nothing.
func TestRecordToUnknownIsNoOp(t *testing.T) {
	l := NewLedger(DefaultRules())
	now := time.Now()
	l.RecordTo("no-such-budget", "x", now)
	for _, s := range l.Status(now) {
		if s.Consumed != 0 {
			t.Errorf("budget %q charged by an unknown-name RecordTo", s.Name)
		}
	}
}
