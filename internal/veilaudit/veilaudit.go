// SPDX-License-Identifier: Apache-2.0

// Package veilaudit produces an honest report of a VayuPress install's endpoint
// observation posture (ADR-0150 §4), modelled on internal/anonaudit.
//
// The single rule this package exists to enforce on itself: GREEN MEANS VERIFIED
// ENFORCING, not "switched on".
//
// A green row therefore requires two things, and the second is what most reports
// skip: something has to be doing the work, AND this process has to have read
// back from the kernel that it is still doing it at the moment the report ran.
// Everything that clears that bar is process-scoped — the dumpability controls
// and the service sandbox — and every one of those rows carries its scope in its
// own text, because a reader who takes a process-scoped pass for a host-wide one
// has been misled by the report whatever the words said elsewhere.
//
// Nothing about the OBSERVATION CHANNELS themselves can go green here. Enforcing
// those needs a compositor, a sandbox policy and mandatory access control, which
// ADR-0150 §0 records as the endpoint track and not this binary's work.
//
// That is not a limitation to be worked around. A posture report where
// everything eventually goes green teaches the person to stop reading it, and a
// privacy subsystem that reports a pass it has not verified is worse than no
// subsystem, because it is believed. anonaudit refuses to claim "100% anonymous"
// for the same reason; the equivalent lie here would be "screenshot-proof".
package veilaudit

import (
	"sort"
	"time"

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
	// Enforced names the requirements this install actually satisfies. It is
	// empty on a server and the report says so plainly rather than looking busy:
	// none of these can be satisfied by a web server, which is the point ADR-0150
	// §0 records.
	//
	// It stays an input rather than a constant so a host that DID satisfy one —
	// a future desktop build linking the same registry — reports differently
	// because the world changed, not because the page was re-worded.
	Enforced map[vayuveil.Needs]bool
	// SelfHardening is the one control this binary genuinely enforces, read back
	// from the kernel rather than remembered from the call that set it.
	SelfHardening vayuveil.SelfHardening
	// RedTeam is the capture suite's last run (ADR-0150 §6).
	RedTeam []vayuveil.AttackResult
	// Sandbox is the service-unit state read back from the kernel (§5 S6). It is
	// what lets the capture rows tell "this host has no framebuffer" apart from
	// "a control is refusing the framebuffer to this process" — two facts that
	// produce identical glob results and mean opposite things.
	Sandbox vayuveil.SandboxState
	// Host is what the machine itself offers (§5 S5). Reported, never claimed:
	// VayuVeil neither provides Secure Boot nor uses the TPM, and both rows say
	// so, because "Secure Boot: enabled" on a security page reads as a guarantee
	// this subsystem has not earned.
	Host vayuveil.HostPosture
	// Harden is what the root-side worker last did (§5 S6), and ProcessStart is
	// when this process began.
	//
	// The second is not decoration. systemd applies unit directives at exec, so
	// a drop-in written after this process started cannot affect it — and the
	// only way to tell "hardening was requested a moment ago and needs a
	// restart" from "hardening was written and did not take" is to compare those
	// two times. Without ProcessStart the report would have to guess, and both
	// guesses are wrong in a way the operator cannot check.
	Harden       vayuveil.HardenState
	ProcessStart time.Time
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
		Title:  "Observation channels — declared here, enforced elsewhere",
		Status: Info,
		Detail: "Every observation channel below is REGISTERED: its policy, grant model, indicator " +
			"and audit level are decided and pinned by test. No channel is ENFORCED host-wide by " +
			"this install, and none can be — enforcing an observation channel needs a compositor, a " +
			"sandbox policy and mandatory access control, which ADR-0150 §0 records as the endpoint " +
			"track and not this binary's work. The green rows above and below are process-scoped " +
			"controls, each read back from the kernel, and each says so in its own text.",
	}}

	// The one row that is allowed to be green, because it is the one thing this
	// binary both does and can verify. Placed above the channel rows so its
	// SCOPE is read before its verdict: it protects the VayuPress process, and a
	// reader who takes it for the machine has been misled by placement alone.
	checks = append(checks, selfHardeningCheck(in.SelfHardening), coreLimitCheck(in.SelfHardening))

	// The service sandbox, read back from the kernel. These rows are about
	// controls the INIT SYSTEM applied and this binary only verifies, which each
	// one says — crediting VayuVeil for systemd's work would be the same
	// misattribution §8 forbids, pointed inward.
	checks = append(checks, sandboxChecks(in.Sandbox)...)

	// The request-and-verify chain, placed immediately after the rows it is about
	// so a reader meets "this is not in force" and "here is what was asked for"
	// together rather than three screens apart.
	checks = append(checks, hardenCheck(in.Harden, in.Sandbox, in.ProcessStart))

	// What the host already has. Placed after the process rows so a reader meets
	// what this binary DID before what the machine happens to offer, and never
	// green — a fact about the firmware is not this subsystem enforcing anything.
	checks = append(checks, hostChecks(in.Host)...)

	// The capture suite. Findings first, then the honest gap.
	checks = append(checks, redTeamChecks(in.RedTeam, in.Sandbox)...)

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

// selfHardeningCheck is the only row in this report that may be a Pass, and it
// earns it by being read back from the kernel at the moment the report runs.
func selfHardeningCheck(h vayuveil.SelfHardening) Check {
	const title = "The VayuPress process refuses to be dumped"
	switch {
	case !h.Supported, !h.Known:
		return Check{Title: title, Status: Unverified, Detail: h.Describe()}
	case h.Undumpable:
		return Check{Title: title, Status: Pass, Detail: h.Describe()}
	default:
		return Check{Title: title, Status: Fail, Detail: h.Describe()}
	}
}

// coreLimitCheck is the second control on the same channel, reported on its own
// row so an operator can see WHICH mechanism is holding. Averaging two
// independent controls into one verdict hides either of them failing.
func coreLimitCheck(h vayuveil.SelfHardening) Check {
	const title = "No core file is written for the VayuPress process"
	switch {
	case !h.Supported, !h.CoreLimitKnown:
		return Check{Title: title, Status: Unverified, Detail: h.DescribeCoreLimit()}
	case h.CoreLimitZero:
		return Check{Title: title, Status: Pass, Detail: h.DescribeCoreLimit()}
	default:
		return Check{Title: title, Status: Fail, Detail: h.DescribeCoreLimit()}
	}
}

// redTeamChecks renders the capture suite.
//
// A technique that was NOT ATTEMPTED is Unverified, never Info and never a pass.
// §6 is explicit: a technique that is not in the suite is not defended, and the
// report must not imply otherwise. Folding those into a comfortable-looking
// bucket is exactly the implication it forbids.
func redTeamChecks(rs []vayuveil.AttackResult, sb vayuveil.SandboxState) []Check {
	if len(rs) == 0 {
		return []Check{{Title: "Capture suite", Status: Unverified,
			Detail: "The red-team capture suite has not run, so no technique has been tried against " +
				"this host. Nothing here has been demonstrated either way."}}
	}
	out := make([]Check, 0, len(rs))
	for _, r := range rs {
		title := "Attack: " + r.Technique
		switch r.Outcome {
		case vayuveil.AttackCapturedContent:
			out = append(out, Check{Title: title, Status: Fail, Detail: r.Detail})
		case vayuveil.AttackRefused:
			// NOT a pass. The technique came away empty on THIS host, from THIS
			// process, and nothing in P0 is what stopped it — the host's own
			// permissions were. Calling that a VayuVeil pass would credit this
			// subsystem with somebody else's work.
			out = append(out, Check{Title: title, Status: Warn,
				Detail: r.Detail + " Nothing in Phase 0 is what refused it; the host's own " +
					"permissions did, and they hold for this process rather than for every process."})
		case vayuveil.AttackNothingPresent:
			// The row that used to understate. A device-node technique finding
			// nothing on a service with a VERIFIED private /dev has not found an
			// empty machine — it has run into a control that is in force. Saying
			// "the absence of a device, not the presence of a control" there is
			// wrong in the direction of hiding real protection, which is a milder
			// defect than the §8 lie but a defect all the same.
			//
			// It is a Pass because the CONTROL is what was verified: this process
			// cannot reach a capture device node. Whether the host has one is
			// unknowable from inside the namespace, and does not need to be known
			// — the guarantee is about what this process can reach.
			if r.ViaDeviceNode && sb.DeniedDeviceCapture() {
				out = append(out, Check{Title: title, Status: Pass,
					Detail: "No device node was reachable to attack, and the reason is a control " +
						"rather than an empty machine: this service has a verified private /dev, so " +
						"the node is unreachable from this process whether or not the host has one. " +
						vayuveil.SandboxScope})
				continue
			}
			out = append(out, Check{Title: title, Status: Info, Detail: r.Detail})
		default:
			out = append(out, Check{Title: title, Status: Unverified, Detail: r.Detail})
		}
	}
	return out
}

// hardenCheck reports the gap between a unit directive being WRITTEN and being
// IN FORCE (§5 S6).
//
// It is never a Pass, and the reason is worth stating because it looks like a
// missed opportunity. Each directive in the baseline already has its own row
// above, computed from the kernel — devices, capabilities, no-new-privs, swap.
// A second green row summarising those would inflate the "verified enforcing"
// count with a control that does not exist separately from the ones already
// counted, which is the double-counting this repository has a standing note
// about. This row's job is the *relationship*: whether something was written,
// whether it took, and whether the difference is being reported honestly.
func hardenCheck(h vayuveil.HardenState, s vayuveil.SandboxState, processStart time.Time) Check {
	const title = "Requested unit hardening has actually taken effect"
	v := vayuveil.ReconcileHardening(h, s, processStart)
	detail := vayuveil.DescribeHardenVerdict(v, vayuveil.UnverifiedHardening(s))
	if h.Detail != "" {
		detail += " The worker reported: " + h.Detail
	}
	c := Check{Title: title, Detail: detail}
	switch v {
	case vayuveil.HardenInForce, vayuveil.HardenPending:
		// Info, not Pass. Nothing on this row is a control; the controls are the
		// rows above, and this one says only that there is nothing left to ask for.
		c.Status = Info
	case vayuveil.HardenDidNotTake:
		// The one Fail. A drop-in that predates this process and still is not in
		// force means an operator has been told hardening was applied while the
		// process runs without it — a claim the page made and the kernel denies.
		c.Status = Fail
	case vayuveil.HardenUnknown:
		c.Status = Unverified
	default:
		// Reverted, Failed, AwaitingRestart, NotRequested. Each is a real
		// residual exposure the operator should understand, which is Warn's job.
		// AwaitingRestart in particular is NOT Info: the exposure is live until
		// the service restarts, and toning it down to context would let a page
		// that has changed nothing yet read as a page that has fixed something.
		c.Status = Warn
	}
	return c
}

// hostChecks renders the two host facts.
//
// Neither can ever be Pass. Pass means VERIFIED ENFORCING, and what is verified
// here was enforced by firmware or by a chip — crediting this subsystem for it
// is the same misattribution as crediting it for systemd's sandbox, one layer
// further out.
func hostChecks(h vayuveil.HostPosture) []Check {
	sb := Check{Title: "The firmware checked a signature on what this host booted",
		Detail: h.DescribeSecureBoot()}
	switch {
	case !h.Supported, !h.SecureBootKnown:
		sb.Status = Unverified
	case h.SecureBoot:
		// Info, not Pass. It is true and it is worth knowing and it is not ours.
		sb.Status = Info
	default:
		// A real residual exposure an operator should understand, which is what
		// Warn is for. Not a Fail: nothing is reachable right now because of it.
		sb.Status = Warn
	}

	tpm := Check{Title: "A TPM is available on this host", Detail: h.DescribeTPM()}
	if !h.Supported || !h.TPMKnown {
		tpm.Status = Unverified
	} else {
		// Info either way. Having no TPM is not a fault — nothing in this install
		// requires one — and having one is not protection while nothing is sealed
		// to it.
		tpm.Status = Info
	}
	return []Check{sb, tpm}
}

// sandboxChecks renders the service-unit state.
//
// Every row here is Unverified unless it was actually read. The unit file on
// disk is deliberately never consulted: it records what somebody intended, and
// this report is built on what the process got.
func sandboxChecks(s vayuveil.SandboxState) []Check {
	if !s.Supported {
		return []Check{{Title: "Service sandbox", Status: Unverified,
			Detail: "This platform does not expose the service's sandbox state to the process itself, " +
				"so nothing is known about what confinement it is running under."}}
	}

	devices := Check{Title: "Capture device nodes are unreachable from this process",
		Detail: s.DescribeDevices()}
	switch {
	case !s.PrivateDevKnown:
		devices.Status = Unverified
	case s.PrivateDev:
		devices.Status = Pass
	default:
		devices.Status = Warn
	}

	caps := Check{Title: "Privileges this process actually holds", Detail: s.DescribeCaps()}
	switch {
	case !s.CapEffKnown:
		caps.Status = Unverified
	case s.CapEff == 0 || s.CapEff == 1<<vayuveil.CapNetBindService:
		caps.Status = Pass
	default:
		caps.Status = Warn
	}

	swap := Check{Title: "This process's memory has not been written to disk", Detail: s.DescribeSwap()}
	switch {
	case !s.SwapKiBKnown:
		swap.Status = Unverified
	case s.SwapKiB > 0:
		// A Fail, not a warning. Bytes of decrypted mail and the keystore key
		// are on a disk this report cannot tell you is encrypted, and that has
		// already happened — it is not a risk, it is an event.
		swap.Status = Fail
	case s.SwapMaxKnown && s.SwapMaxZero:
		// The only green case: nothing swapped AND the cgroup forbids it. Zero
		// bytes without that control is luck, and luck is not a pass.
		swap.Status = Pass
	default:
		swap.Status = Warn
	}

	nnp := Check{Title: "This process cannot gain privileges by running another program"}
	switch {
	case !s.NoNewPrivsKnown:
		nnp.Status = Unverified
		nnp.Detail = "The kernel did not answer PR_GET_NO_NEW_PRIVS, so it is not known whether a " +
			"setuid binary could raise this process's privileges. Reported as unverified."
	case s.NoNewPrivs:
		nnp.Status = Pass
		nnp.Detail = "Verified with PR_GET_NO_NEW_PRIVS: no execve from this process can gain " +
			"privileges through a setuid binary or a file capability. It is inherited, so the " +
			"in-app updater's re-exec and the Tor Space child keep it too. " + vayuveil.SandboxScope
	default:
		nnp.Status = Warn
		nnp.Detail = "This process CAN gain privileges by executing a setuid binary or one carrying " +
			"file capabilities, which is one of the routes a compromise uses to become a bigger " +
			"one. The shipped unit sets NoNewPrivileges=yes; an install reporting this row is " +
			"running from a unit that does not."
	}

	return []Check{devices, caps, nnp, swap}
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

	enforcing := in.Enforced[c.Needs]

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
			Detail: obs.Detail + ". Policy for this channel is Deny and nothing here enforces it: " +
				"doing so needs " + c.Needs.String() + ", which this install does not have. It is " +
				"open on this host right now."}
	}
	return Check{Title: title, Status: Unverified, Detail: obs.Detail}
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
