// SPDX-License-Identifier: Apache-2.0

// Package veilaudit produces an honest report of a VayuPress install's endpoint
// observation posture (ADR-0150 §4), modelled on internal/anonaudit.
//
// The single rule this package exists to enforce on itself: GREEN MEANS VERIFIED
// ENFORCING, not "switched on". ADR-0150 is at P0 — the registry and the
// inventory — and P0 enforces nothing. So this report emits NO passing row, by
// construction, and says why on the page.
//
// That is not a limitation to be worked around. A posture report where
// everything eventually goes green teaches the person to stop reading it, and a
// privacy subsystem that reports a pass it has not verified is worse than no
// subsystem, because it is believed. anonaudit refuses to claim "100% anonymous"
// for the same reason; the equivalent lie here would be "screenshot-proof".
package veilaudit

import (
	"sort"

	"github.com/johalputt/vayupress/internal/vayuveil"
)

// Status is a single row's verdict.
type Status int

const (
	// Pass — VERIFIED ENFORCING. Reserved. Nothing at P0 may use it.
	Pass Status = iota
	// Warn — a residual exposure the operator should understand.
	Warn
	// Info — context, or an inherent limitation that is not a fault.
	Info
	// Fail — something that should not be reachable is reachable right now.
	Fail
	// Unverified — could not be established. Never a pass: absent evidence is not
	// evidence of absence, and a row that quietly rounds "unknown" up to "fine" is
	// how a report stops being worth reading.
	Unverified
)

// Check is one line of the report.
type Check struct {
	Title  string
	Status Status
	Detail string
	// Permanent marks a row that NO configuration clears. ADR-0150 §8 lists what
	// this subsystem will never claim, and each of those appears here forever.
	// They are what makes the rest of the report worth believing.
	Permanent bool
}

// Inputs is the live state the report is computed from, passed in so the report
// is pure and testable rather than a function of the machine the test runs on.
type Inputs struct {
	// Enabled is the operator's activate/deactivate switch.
	Enabled bool
	// Channels is the registry.
	Channels []vayuveil.Channel
	// Observations is what the probes found, keyed by channel. A channel with no
	// entry is reported Unverified, not clean.
	Observations map[vayuveil.ChannelID]vayuveil.Observation
	// EnforcingPhases names the ADR-0150 phases that are actually shipped and
	// verified. At P0 this is empty, and the report is built to say so plainly
	// rather than to look busy.
	EnforcingPhases map[vayuveil.Phase]bool
}

// Run computes the report.
func Run(in Inputs) []Check {
	if !in.Enabled {
		return []Check{{
			Title:  "VayuVeil is not active",
			Status: Info,
			Detail: "No observation channels are being inventoried and nothing is being reported. " +
				"This is a switch on the reporting, not on any protection: turning it on does not " +
				"defend the screen, and turning it off does not expose anything that was not already " +
				"exposed.",
		}}
	}

	checks := []Check{{
		Title:  "Phase 0 — declared, not enforced",
		Status: Info,
		Detail: "Every observation channel below is REGISTERED: its policy, grant model, indicator " +
			"and audit level are decided and pinned by test. None of it is ENFORCED by this install. " +
			"Enforcement is ADR-0150 P1 and later, and lives in a compositor, a sandbox and a " +
			"mandatory-access-control policy — not in this binary. No row here can go green until " +
			"the thing it describes has been verified enforcing.",
	}}

	// Per-channel rows, computed from what was observed.
	chans := append([]vayuveil.Channel(nil), in.Channels...)
	sort.Slice(chans, func(i, j int) bool { return chans[i].ID < chans[j].ID })
	for _, c := range chans {
		checks = append(checks, channelCheck(c, in))
	}

	// §8 — what this subsystem will never claim. Permanent, in every report,
	// cleared by nothing.
	checks = append(checks, permanentLimits()...)
	return checks
}

// channelCheck turns one channel and its observation into a row.
//
// The mapping is where the honesty lives, so it is written out rather than
// compressed: an interface that happens to be missing from this host is NOT a
// defence, and an interface the host restricts is not VayuVeil restricting it.
func channelCheck(c vayuveil.Channel, in Inputs) Check {
	obs, seen := in.Observations[c.ID]
	title := c.Name

	if !seen {
		return Check{Title: title, Status: Unverified,
			Detail: "No probe result for this channel, so nothing is known about it on this host. " +
				"Reported as unverified rather than clean."}
	}

	enforcing := in.EnforcingPhases[c.Phase]

	switch obs.Presence {
	case vayuveil.PresenceUnknown:
		return Check{Title: title, Status: Unverified, Detail: obs.Detail}

	case vayuveil.PresenceAbsent:
		return Check{Title: title, Status: Info,
			Detail: obs.Detail + ". Absent from this host, which is not the same as defended — " +
				"there is simply nothing here to observe. A row like this goes green only once " +
				"something is verified to be stopping it."}

	case vayuveil.PresentUnreachable:
		return Check{Title: title, Status: Warn,
			Detail: obs.Detail + ". Restricted by the host's own permissions rather than by " +
				"VayuVeil, so it holds for this process and says nothing about any other."}

	case vayuveil.PresentReachable:
		if c.Default == vayuveil.DispositionGrant {
			return Check{Title: title, Status: Warn,
				Detail: obs.Detail + ". This channel is grantable by design; at P0 the grant, the " +
					"indicator and the audit trail are declared but not implemented, so anything " +
					"holding it today holds it without asking."}
		}
		if enforcing {
			return Check{Title: title, Status: Fail,
				Detail: obs.Detail + ". Policy denies this channel and it is reachable anyway, " +
					"which means the control that should be stopping it is not."}
		}
		return Check{Title: title, Status: Fail,
			Detail: obs.Detail + ". Policy for this channel is Deny, and nothing enforces that yet " +
				"(ADR-0150 " + phaseName(c.Phase) + "). It is open on this host right now."}
	}
	return Check{Title: title, Status: Unverified, Detail: obs.Detail}
}

func phaseName(p vayuveil.Phase) string {
	switch p {
	case vayuveil.PhaseP1Screen:
		return "P1"
	case vayuveil.PhaseP2Input:
		return "P2"
	case vayuveil.PhaseP3Accessibility:
		return "P3"
	case vayuveil.PhaseP4Sandbox:
		return "P4"
	case vayuveil.PhaseP5Hygiene:
		return "P5"
	}
	return "an unshipped phase"
}

// permanentLimits is ADR-0150 §8, verbatim in intent: the things this subsystem
// will never claim. Every one is a Fail that no configuration clears.
func permanentLimits() []Check {
	const p = true
	return []Check{
		{Title: "An attacker with root on this machine", Status: Fail, Permanent: p,
			Detail: "Reads /dev/mem, loads modules, patches the compositor. Measured boot and a " +
				"signed compositor raise the cost of this; they do not close it. No setting here " +
				"changes that, and this row never goes green."},
		{Title: "A kernel or driver-level attacker", Status: Fail, Permanent: p,
			Detail: "Reads GPU memory directly, below everything this subsystem can see. Out of " +
				"scope by construction."},
		{Title: "DMA-capable hardware, firmware, ME/PSP, SMM", Status: Fail, Permanent: p,
			Detail: "Below the operating system entirely. Nothing running as software on this " +
				"machine can observe it, let alone stop it."},
		{Title: "A camera pointed at the screen", Status: Fail, Permanent: p,
			Detail: "Also an HDMI splitter, a hostile monitor, and electromagnetic reconstruction. " +
				"No software boundary exists between a screen and a lens, and any product claiming " +
				"otherwise is selling something."},
		{Title: "A person who grants capture to malware", Status: Fail, Permanent: p,
			Detail: "Policy did its job and the answer was still yes. This is why grants are worded " +
				"to make the consequence legible, scoped to one window, time-boxed and revocable — " +
				"so a mistake is bounded rather than prevented, which is the honest goal."},
		{Title: "A compromised compositor", Status: Fail, Permanent: p,
			Detail: "The compositor is the trusted computing base for all of this. The honest " +
				"response is to keep it small, memory-safe, signed and measured — not to claim it " +
				"cannot fail."},
		{Title: "No Recall-equivalent, ever", Status: Info, Permanent: p,
			Detail: "ADR-0150 §3.3 forbids it as a constitutional constraint: no periodic " +
				"snapshotting, no local screen index, no timeline, not even opt-in. The moment this " +
				"product keeps a searchable history of a screen it IS the threat, whatever the " +
				"encryption. Recorded here so no future copy quietly upgrades it."},
	}
}

// Summary counts the report by status, for the tiles.
func Summary(checks []Check) (pass, warn, fail, unverified int) {
	for _, c := range checks {
		switch c.Status {
		case Pass:
			pass++
		case Warn:
			warn++
		case Fail:
			fail++
		case Unverified:
			unverified++
		}
	}
	return
}
