package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/mode"
)

func TestModeStateClass(t *testing.T) {
	cases := map[mode.Mode]string{
		mode.ModeNormal:      "tool-status--on",
		mode.ModeDegraded:    "tool-status--idle",
		mode.ModeRecovery:    "tool-status--idle",
		mode.ModeMaintenance: "tool-status--idle",
		mode.ModeReadOnly:    "tool-status--off",
		mode.ModeQuarantined: "tool-status--off",
	}
	for m, want := range cases {
		if got := modeStateClass(m); got != want {
			t.Errorf("modeStateClass(%q): want %q, got %q", m, want, got)
		}
	}
}

func TestBudgetStateClass(t *testing.T) {
	cases := map[string]string{
		"healthy":   "tool-status--on",
		"at-risk":   "tool-status--idle",
		"exhausted": "tool-status--off",
		"":          "tool-status--off",
	}
	for state, want := range cases {
		if got := budgetStateClass(state); got != want {
			t.Errorf("budgetStateClass(%q): want %q, got %q", state, want, got)
		}
	}
}

// TestMonStatCSPSafeAndEscapes checks the stat-card renderer is CSP-clean and
// escapes hostile input.
func TestMonStatCSPSafeAndEscapes(t *testing.T) {
	out := monStat("HTTP p95", "12 ms", "request latency")
	assertCSPSafe(t, "monStat", out)
	if !strings.Contains(out, "HTTP p95") || !strings.Contains(out, "12 ms") {
		t.Error("monStat dropped its label/value")
	}
	hostile := monStat(`<script>alert(1)</script>`, `"><img onerror=x>`, "x")
	if strings.Contains(hostile, "<script>alert(1)") || strings.Contains(hostile, "<img onerror") {
		t.Error("monStat did not escape hostile input")
	}
}
