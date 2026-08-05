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
func veilBootLine(self vayuveil.SelfHardening, sb vayuveil.SandboxState) string {
	var b strings.Builder
	b.WriteString("VayuVeil (ADR-0150 server track): observation channels registered=" +
		strconv.Itoa(len(vayuveil.Channels())) + ", enforced host-wide=0")

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
	// The service sandbox. Worth a place in the boot line because it is the one
	// control here that denies a CAPTURE channel rather than a memory one, and
	// because an install running from a unit that predates the hardening block
	// gets no other warning — the operator would have to open the page to find
	// out, and the whole reason this line exists is the operator who does not.
	switch {
	case !sb.Supported, !sb.PrivateDevKnown:
		b.WriteString("; service sandbox: unverified")
	case sb.PrivateDev:
		b.WriteString("; service sandbox: private /dev (verified) — framebuffer, input and DRM " +
			"nodes unreachable from this process")
	default:
		b.WriteString("; service sandbox: SHARED /dev — if this host has a framebuffer, input " +
			"devices or DRM nodes, this process can reach them (unit is missing PrivateDevices=yes)")
	}
	if sb.Supported && sb.NoNewPrivsKnown && !sb.NoNewPrivs {
		b.WriteString(", NoNewPrivileges NOT set")
	}

	// The boundary, in the log as well as on the page. An operator reading only
	// this line must not come away thinking a screen is defended.
	//
	// Worded to stay true in BOTH sandbox states, which the previous version was
	// not: "enforces none of it" became an understatement the moment a verified
	// private /dev started denying the capture nodes. The invariant that does
	// hold either way is the scope — nothing here reaches past this process.
	b.WriteString(". No observation channel is enforced host-wide; every control above covers " +
		"this process only, and the rest of the machine is unaffected.")
	return b.String()
}

// veilPostureIsWarning decides whether the boot line is worth waking someone
// for. Two states qualify, and both are quiet misconfigurations that an info
// line lets scroll past: a process that can be dumped, and a unit that never
// applied the service sandbox.
//
// Split out from logVeilPosture so it can be asserted on directly. The first
// version of that test compared a fixture against its own branch condition,
// which would have passed against any implementation at all — a tautology
// wearing a test's name.
//
// UNKNOWN IS NOT A WARNING, deliberately. A platform that cannot answer is not
// a misconfiguration, and warning on it would train an operator to ignore the
// level on exactly the machines where a real warning still means something.
func veilPostureIsWarning(self vayuveil.SelfHardening, sb vayuveil.SandboxState) bool {
	dumpable := self.Supported && self.Known && !self.Undumpable
	sharedDev := sb.Supported && sb.PrivateDevKnown && !sb.PrivateDev
	return dumpable || sharedDev
}

// logVeilPosture writes the boot line.
func logVeilPosture(self vayuveil.SelfHardening, sb vayuveil.SandboxState) {
	line := veilBootLine(self, sb)
	if veilPostureIsWarning(self, sb) {
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
