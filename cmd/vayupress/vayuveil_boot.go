// SPDX-License-Identifier: Apache-2.0

package main

// vayuveil_boot.go — what VayuVeil says at boot, and what it writes down.
//
// Two things ADR-0150 asks for that a panel page alone cannot provide.
//
// The BOOT LINE follows anonaudit's precedent: an operator should be able to see
// the posture in the log without opening a browser, and a subsystem that is only
// visible in a panel is invisible on the day the panel will not load.
//
// The AUDIT TRAIL is L8, narrowed to what this phase can honestly record. §3.2
// asks for every attempt allowed or refused, which needs the compositor that P1
// builds. What P0 CAN write down is what the capture suite found and what the
// enforcement verification said — and the reason to bother is stated in the ADR:
// "Refusals matter as much as grants: they are how you discover that something
// has been trying for a month." A finding that exists only in a page nobody
// reloaded has not been discovered.

import (
	"strconv"
	"strings"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/vayuveil"
)

// veilBootLine renders the one-line posture summary.
//
// Pure, so the sentence itself can be asserted on. The wording is the whole
// deliverable here — a boot line that overstates is a claim defect that reaches
// every operator's log rather than only the ones who open a page.
func veilBootLine(self vayuveil.SelfHardening) string {
	var b strings.Builder
	b.WriteString("VayuVeil (ADR-0150 P0): observation channels registered=" +
		strconv.Itoa(len(vayuveil.Channels())) + ", enforced on this host=0")

	switch {
	case !self.Supported:
		b.WriteString("; this process: dumpability not controllable on this platform")
	case self.Known && self.Undumpable:
		b.WriteString("; this process: undumpable (verified)")
	case self.Known:
		b.WriteString("; this process: DUMPABLE — a core file or a same-user /proc read would expose " +
			"session tokens and the keystore key")
	default:
		b.WriteString("; this process: dumpability unverified")
	}
	switch {
	case self.Supported && self.CoreLimitKnown && self.CoreLimitZero:
		b.WriteString(", core limit 0 (verified)")
	case self.Supported && self.CoreLimitKnown:
		b.WriteString(", CORE LIMIT NOT ZERO")
	}
	// The boundary, in the log as well as on the page. An operator reading only
	// this line must not come away thinking a screen is defended.
	b.WriteString(". Phase 0 registers policy and enforces none of it on this host; " +
		"the process controls above cover this process only.")
	return b.String()
}

// logVeilPosture writes the boot line.
func logVeilPosture(self vayuveil.SelfHardening) {
	line := veilBootLine(self)
	if self.Supported && self.Known && !self.Undumpable {
		logging.LogWarn("vayuveil", line)
		return
	}
	logging.LogInfo("vayuveil", line)
}

// recordVeilFindings writes the capture suite's findings to the audit trail.
//
// Only findings and gaps are recorded, never a clean run: an append-only log
// filled with "nothing happened" on every page load is a log nobody reads, and
// the entries that matter — something was capturable, or a technique was never
// tried — would be buried in it.
func recordVeilFindings(actor string, red []vayuveil.AttackResult) {
	for _, r := range red {
		if r.Outcome != vayuveil.AttackCapturedContent {
			continue
		}
		dbpkg.AuditLog("vayuveil.capture", actor, r.Technique,
			"CAPTURED "+strconv.Itoa(r.Bytes)+" bytes — "+r.Detail)
	}
	if n := len(vayuveil.TechniquesNotAttempted(red)); n > 0 {
		dbpkg.AuditLog("vayuveil.sweep", actor, "techniques-not-attempted",
			strconv.Itoa(n)+" technique(s) in ADR-0150 §6 were not attempted by this binary and are "+
				"therefore untested and undefended")
	}
}
