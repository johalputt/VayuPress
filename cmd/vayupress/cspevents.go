package main

import (
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

// cspViolation is one recent Content-Security-Policy violation, kept in a small
// in-memory ring so frontend-governance signals can surface in the Unified
// Operational Timeline alongside mode transitions and faults. It is intentionally
// ephemeral (bounded, process-local): the durable record is the structured log
// line and the vayupress_csp_violations_total metric.
type cspViolation struct {
	When       time.Time
	Directive  string
	BlockedURI string
}

const cspRingMax = 10

var (
	cspRingMu sync.Mutex
	cspRing   []cspViolation
)

// recordCSPViolation appends a violation to the bounded ring (newest last).
func recordCSPViolation(directive, blocked string) {
	cspRingMu.Lock()
	cspRing = append(cspRing, cspViolation{When: time.Now().UTC(), Directive: directive, BlockedURI: blocked})
	if len(cspRing) > cspRingMax {
		cspRing = cspRing[len(cspRing)-cspRingMax:]
	}
	cspRingMu.Unlock()
}

// recentCSPViolations returns a copy of the current ring (oldest first).
func recentCSPViolations() []cspViolation {
	cspRingMu.Lock()
	defer cspRingMu.Unlock()
	out := make([]cspViolation, len(cspRing))
	copy(out, cspRing)
	return out
}

// cspEnforcementMode returns the human-readable CSP enforcement posture. The
// enforcement posture is operational state, so it is surfaced in the timeline,
// the governance dashboard, and the stats/health JSON — not hidden in an env var.
func cspEnforcementMode() string {
	if config.Cfg.CSPReportOnly {
		return "report-only"
	}
	return "enforcing"
}
