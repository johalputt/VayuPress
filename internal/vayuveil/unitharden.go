// SPDX-License-Identifier: Apache-2.0

package vayuveil

// unitharden.go — ADR-0150 §5 S6. What the panel may ask root to write, and the
// far more interesting question of what it may not.
//
// sandbox.go reads the service unit's controls back. This file is the other
// half: when a read-back says a control is NOT in force, the operator needs a
// way to fix it that is not "open a terminal and edit a unit file". So the panel
// requests, a root-side worker writes a drop-in, and the result is verified the
// same way everything else here is — by reading the kernel afterwards.
//
// THE RULE THAT DECIDES THE CONTENTS OF THE DROP-IN
// ADR-0150 §5 says a step earns its place only if this process can VERIFY the
// result by reading it back. Applied to a hardening drop-in that rule is sharper
// than it first looks: a directive this process cannot re-read would be written,
// reported as "applied", and never checked again. That is a configuration
// reported as a control — the exact defect §8 exists to prevent, arriving
// through a button labelled "harden".
//
// So the baseline below contains EXACTLY the directives whose effect shows up in
// /proc/self/status, /proc/self/mountinfo or the service's own cgroup. Five of
// them. Every other hardening directive systemd offers — and there are dozens,
// several of which would look impressive in a release note — is refused, with
// the refusal written down beside it in HardenRefusals so nobody has to guess
// whether it was considered.
//
// The second rule, learned from the unit file this repository already ships: a
// directive that can take a live install down at the next restart must not be
// written by a button. ProtectSystem=strict is the worked example. It is in the
// shipped unit, it is correct there, and it is correct there only because the
// unit also carries a ReadWritePaths= list matched to this install's data
// directory. A drop-in written from a panel cannot know that list, and a wrong
// one means the service comes back unable to write its own database.

import (
	"strings"
	"time"
)

// UnitDirective is one systemd directive the baseline asks for, together with
// the two things that make it eligible: what it denies, and where its effect is
// read back from.
type UnitDirective struct {
	// Directive is written verbatim into the drop-in.
	Directive string
	// Denies is what an operator gets for it, in one sentence.
	Denies string
	// ReadBack names the file or syscall the verification comes from. It is a
	// field rather than a comment because it is rendered: a row claiming a
	// control should be able to say how it knows.
	ReadBack string

	// inForce answers from a live SandboxState. Unexported so the only way to
	// ask is InForce, which keeps the "known" bit from being dropped by a caller
	// that only wanted the bool.
	inForce func(SandboxState) (on bool, known bool)
}

// InForce reports whether this directive's effect is verified present.
//
// The two return values are never collapsed. "The kernel says no" and "the
// kernel would not say" are different facts, and a caller that treats the second
// as the first has written the report that rounds unknown up to fine.
//
// When the answer is not known, `on` is forced to false rather than passed
// through. SandboxState's paired fields can hold a stale value beside a false
// Known bit — a read that failed leaves whatever was there — and a caller that
// took the first return and dropped the second would then be handed a `true`
// that nothing verified. Making the unknown case return the safe value costs
// nothing and removes the only way to misuse this method.
func (d UnitDirective) InForce(s SandboxState) (on bool, known bool) {
	if d.inForce == nil || !s.Supported {
		return false, false
	}
	on, known = d.inForce(s)
	if !known {
		return false, false
	}
	return on, true
}

// HardenBaseline is the set a request writes. Five directives, each verifiable.
func HardenBaseline() []UnitDirective {
	return []UnitDirective{
		{
			Directive: "NoNewPrivileges=yes",
			Denies: "no program this process executes can gain privileges through a setuid bit or a " +
				"file capability, so a compromise of the web server cannot escalate by running one",
			ReadBack: "PR_GET_NO_NEW_PRIVS",
			inForce:  func(s SandboxState) (bool, bool) { return s.NoNewPrivs, s.NoNewPrivsKnown },
		},
		{
			Directive: "PrivateDevices=yes",
			Denies: "the framebuffer, the input event devices and the DRM card nodes are removed from " +
				"this process's view of /dev — the single most direct capture channel on a Linux host",
			ReadBack: "/proc/self/mountinfo",
			inForce:  func(s SandboxState) (bool, bool) { return s.PrivateDev, s.PrivateDevKnown },
		},
		{
			Directive: "PrivateTmp=yes",
			Denies: "a private /tmp, which takes away /tmp/.X11-unix and any other socket or scratch " +
				"file another local process left where this one could read it",
			ReadBack: "/proc/self/mountinfo",
			inForce:  func(s SandboxState) (bool, bool) { return s.PrivateTmp, s.PrivateTmpKnown },
		},
		{
			Directive: "ProtectHome=yes",
			Denies: "home directories are unreadable to this process, including the thumbnail caches " +
				"that hold rendered copies of documents and images",
			ReadBack: "/proc/self/mountinfo",
			inForce:  func(s SandboxState) (bool, bool) { return s.ProtectedHome, s.ProtectedHomeKnown },
		},
		{
			Directive: "MemorySwapMax=0",
			Denies: "the kernel may not page this service's anonymous memory to disk at all, so " +
				"decrypted mail, session tokens and the keystore key cannot reach a swap file",
			ReadBack: "the service's own cgroup (memory.swap.max)",
			inForce:  func(s SandboxState) (bool, bool) { return s.SwapMaxZero, s.SwapMaxKnown },
		},
	}
}

// UnverifiedHardening returns the baseline directives that are NOT verified in
// force right now — which is what a request would be for.
//
// A directive whose state could not be READ is included. That is deliberate and
// it is the direction this whole subsystem leans: unverified is never treated as
// fine, and re-writing a directive that turns out to have been present already
// costs nothing, while omitting one because the answer was unreadable leaves the
// operator with a gap the page told them was closed.
func UnverifiedHardening(s SandboxState) []UnitDirective {
	var out []UnitDirective
	for _, d := range HardenBaseline() {
		on, known := d.InForce(s)
		if !known || !on {
			out = append(out, d)
		}
	}
	return out
}

// UnitRefusal is a hardening directive deliberately kept OUT of the baseline.
type UnitRefusal struct{ Directive, Reason string }

// HardenRefusals is the list, rendered on the page.
//
// It is here because a security control that only advertises what it does is
// half a document. Every one of these would read well in a release note, and
// each is excluded for a reason that survives being said out loud.
func HardenRefusals() []UnitRefusal {
	return []UnitRefusal{
		{
			Directive: "ProtectSystem=strict",
			Reason: "It is only safe alongside a ReadWritePaths= list matched to THIS install's data " +
				"directory, and a drop-in written from a panel cannot know that list. Get it wrong and " +
				"the service returns from its next restart unable to write its own database. The " +
				"shipped unit sets it because the unit and the paths were written together.",
		},
		{
			Directive: "SystemCallFilter=",
			Reason: "The seccomp MODE is readable from /proc/self/status; which calls are actually " +
				"filtered is not. A row built on that would say a filter exists, never that the right " +
				"filter exists — and a syscall filter written blind kills the process that trips it.",
		},
		{
			Directive: "CapabilityBoundingSet=",
			Reason: "Narrowing it is verifiable, and narrowing it wrongly takes away " +
				"CAP_NET_BIND_SERVICE, at which point the mail listeners cannot bind :25 and :143 and " +
				"inbound mail stops. What the bounding set should contain depends on how this install " +
				"runs its listeners, which this process cannot determine from inside.",
		},
		{
			Directive: "ProtectKernelTunables=, ProtectKernelModules=, LockPersonality=, RestrictRealtime=",
			Reason: "All genuinely useful and none of them observable from inside the process they " +
				"confine. Writing them would produce an entry that says applied and can never say " +
				"verified, which is the configuration-reported-as-control defect this ADR is about.",
		},
		{
			Directive: "Restarting into an unverifiable state",
			Reason: "The worker restarts the service so the directives take effect, then checks the " +
				"unit came back. If it did not, the drop-in is REMOVED and the service restarted " +
				"again, because a hardening button that can lock an operator out of their own panel is " +
				"worse than the exposure it closes.",
		},
	}
}

// HardenState is what the root-side worker last did, as the unprivileged side
// can observe it.
type HardenState struct {
	// Installed is whether the root-side worker and its watcher exist. Without
	// them a request would sit unconsumed forever and the button must say so
	// rather than appear to work.
	Installed bool
	// Pending is a request written and not yet consumed.
	Pending bool
	// HaveResult is whether the worker has ever reported.
	HaveResult bool
	// AppliedAt is when it last finished.
	//
	// Taken from the result FILE's modification time rather than from a
	// timestamp inside it. Both are written by the same machine, but only the
	// mtime is on the same clock as this process's own start time — and the
	// entire verdict below is a comparison between those two moments, so they
	// have to be commensurable. A parsed string that arrived in an unexpected
	// format would also silently become the zero time, which compares as "before
	// this process started" and turns "not restarted yet" into "written and did
	// not take".
	AppliedAt time.Time
	// Wrote and Skipped are what the worker put in the drop-in and what it left
	// out, each skip carrying its reason.
	Wrote   []string
	Skipped []string
	// Reverted records that the worker put the service back because it did not
	// come up. It is the most important field here and it is reported loudly.
	Reverted bool
	// Failed is whether the run reported a problem.
	Failed bool
	// Detail is the worker's own sentence.
	Detail string
}

// HardenVerdict is the relationship between what was written and what is in
// force. The two are not the same thing and this type exists so no caller can
// accidentally treat them as one.
type HardenVerdict int

const (
	// HardenUnknown — the sandbox could not be read, so nothing is claimed.
	HardenUnknown HardenVerdict = iota
	// HardenInForce — every baseline directive is verified present.
	HardenInForce
	// HardenPending — a request is in flight.
	HardenPending
	// HardenReverted — the worker applied a drop-in, the service did not come
	// back, and the drop-in was removed.
	HardenReverted
	// HardenFailed — the worker reported a problem.
	HardenFailed
	// HardenAwaitingRestart — the drop-in was written AFTER this process
	// started, so this process cannot have it and no amount of reading will show
	// it. Not a fault, and emphatically not "applied".
	HardenAwaitingRestart
	// HardenDidNotTake — the drop-in was written BEFORE this process started and
	// the control is still not in force. This is the serious one: something is
	// writing to a unit this service does not run from, or a later drop-in is
	// overriding it. A configuration exists and a control does not.
	HardenDidNotTake
	// HardenNotRequested — controls are missing and nobody has asked for them.
	HardenNotRequested
)

// ReconcileHardening compares what was written with what the kernel says.
//
// processStart is when THIS process began. It is the whole hinge: systemd
// applies these directives at exec, so a drop-in written one second after the
// service started has no effect on the running process and will not until it
// restarts. Reporting that as "applied" would be true about the file and false
// about the machine, and an operator reading the page would believe a control
// was holding that was not.
func ReconcileHardening(h HardenState, s SandboxState, processStart time.Time) HardenVerdict {
	if !s.Supported {
		return HardenUnknown
	}
	missing := UnverifiedHardening(s)
	switch {
	case h.Reverted:
		// Before "in force", because a revert is news even on an install that
		// turns out to be fully hardened anyway: something about this unit made
		// the service fail to start, and that is worth an operator's attention
		// whatever the current posture is.
		return HardenReverted
	case h.Pending:
		return HardenPending
	case len(missing) == 0:
		return HardenInForce
	case h.Failed:
		return HardenFailed
	case !h.HaveResult:
		return HardenNotRequested
	case h.AppliedAt.After(processStart):
		return HardenAwaitingRestart
	default:
		return HardenDidNotTake
	}
}

// DescribeHardenVerdict renders it. Every branch names what is true about the
// PROCESS, not about the file, because the file is not what protects anything.
func DescribeHardenVerdict(v HardenVerdict, missing []UnitDirective) string {
	names := make([]string, 0, len(missing))
	for _, d := range missing {
		names = append(names, strings.SplitN(d.Directive, "=", 2)[0])
	}
	list := strings.Join(names, ", ")

	switch v {
	case HardenInForce:
		return "Every directive in the baseline is verified in force for this process, read back from " +
			"the kernel rather than from the unit file. There is nothing to request."
	case HardenPending:
		return "A hardening request is waiting for the root-side worker. Nothing has changed yet, and " +
			"nothing about this process changes until it restarts."
	case HardenReverted:
		return "The worker wrote the drop-in, the service did not come back, and the drop-in was " +
			"REMOVED and the service restarted without it. Nothing is hardened and nothing is broken. " +
			"The unit or the host rejected one of these directives, and the worker's detail below says " +
			"which step failed."
	case HardenFailed:
		return "The last hardening run reported a problem and " + list + " " +
			"is still not in force for this process. Its detail is below."
	case HardenAwaitingRestart:
		return "The drop-in was written AFTER this process started, so this process does not have it " +
			"and cannot: systemd applies these at exec. " + list + " will become a control at the next " +
			"restart, and this page will say so only once it has read it back from the kernel. Until " +
			"then it is a configuration."
	case HardenDidNotTake:
		return "A drop-in was written before this process started and " + list + " is STILL not in " +
			"force. That means it was written somewhere this service does not read from, or a later " +
			"drop-in overrides it. This is a configuration that looks like a control, which is the one " +
			"outcome this page exists to make impossible to miss."
	case HardenNotRequested:
		return list + " is not in force for this process. The panel can ask the root-side worker to " +
			"write a drop-in containing it; it cannot write one itself, and it does not instruct you " +
			"to write one either."
	default:
		return "The service sandbox could not be read, so it is not known which of these directives " +
			"are in force. Reported as unverified rather than as absent."
	}
}
