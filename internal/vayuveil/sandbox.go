// SPDX-License-Identifier: Apache-2.0

package vayuveil

// sandbox.go — ADR-0150 §5 S6: what the service unit actually got, read back.
//
// This is the finding that changed the shape of the server track, so it is
// written down rather than left in a commit message.
//
// The shipped unit (deploy/vayupress.service) carries PrivateDevices=yes,
// ProtectHome=yes and PrivateTmp=yes. Those are not incidental hygiene — they
// are, precisely, denial of the capture channels this ADR enumerates.
// PrivateDevices replaces /dev with a minimal tmpfs holding null, zero, random
// and a few others: no /dev/fb*, no /dev/input/event*, no /dev/dri/card*.
// ProtectHome puts the thumbnail caches out of reach. PrivateTmp takes away
// /tmp/.X11-unix.
//
// Which means the capture suite's "nothing matches /dev/fb*" result has TWO
// completely different causes that it could not previously tell apart:
//
//   - the host genuinely has no framebuffer (a headless VPS), which is the
//     absence of a target and is not protection; or
//   - the host has one and the service sandbox is REFUSING IT TO THIS PROCESS,
//     which is a control, verifiably in force, doing exactly what L2 asks for.
//
// Reporting the second as the first understates real protection. Reporting the
// first as the second would be the §8 lie. So the distinction is drawn from
// /proc/self/mountinfo — evidence, read at report time — and never assumed.
//
// NOTHING HERE IS APPLIED BY THIS BINARY, and that is deliberate. The controls
// belong to the unit, which is root's to write. Applying PR_SET_NO_NEW_PRIVS
// from inside would also be the wrong move for a subtler reason: this binary
// re-execs itself for the in-app update and for the Tor Space child, and an
// install that relies on file capabilities rather than systemd's ambient ones
// would lose its ability to bind :25 and :443 on the next restart. Verifying is
// useful and safe; applying is neither.

import (
	"strconv"
	"strings"
)

// SandboxState is what the kernel says this process actually got. Every field
// carries its own "known" bit: an answer that could not be read is reported as
// unverified, never as fine.
type SandboxState struct {
	// Supported is whether this platform exposes any of it.
	Supported bool

	// NoNewPrivs — the process cannot gain privileges through execve. Read from
	// PR_GET_NO_NEW_PRIVS, cross-checked against /proc/self/status.
	NoNewPrivs, NoNewPrivsKnown bool

	// SeccompMode is /proc/self/status Seccomp: 0 disabled, 1 strict, 2 filter.
	// systemd's SystemCallFilter= shows up as 2.
	SeccompMode      int
	SeccompModeKnown bool

	// CapEff is the effective capability mask as the kernel reports it. Zero is
	// a real and good answer, which is why Known is separate: an unreadable mask
	// and a genuinely empty one are not the same fact.
	CapEff, CapBnd           uint64
	CapEffKnown, CapBndKnown bool

	// PrivateDev, ProtectedHome and PrivateTmp are read from the mount table.
	PrivateDev, PrivateDevKnown       bool
	ProtectedHome, ProtectedHomeKnown bool
	PrivateTmp, PrivateTmpKnown       bool

	// PtraceScope is the Yama tunable: 0 classic, 1 restricted, 2 admin-only,
	// 3 no attach at all. Host-wide, so it is reported as context about the host
	// rather than as something this install enforces.
	PtraceScope      int
	PtraceScopeKnown bool

	// SwapKiB is how much of THIS process's anonymous memory the kernel has
	// written to disk, read from VmSwap in /proc/self/status.
	//
	// It is the one number here that is neither a control nor a configuration —
	// it is an outcome, and it is the only evidence available that decrypted
	// mail, session tokens or the keystore key have left RAM. Zero is a real and
	// good answer, which is why Known is separate: a process with no VmSwap line
	// and a process genuinely holding nothing on disk are different facts.
	SwapKiB      int
	SwapKiBKnown bool

	// SwapMaxZero is whether this service's cgroup forbids swapping at all
	// (memory.swap.max = 0). Unlike VmSwap it is a CONTROL, and it is the only
	// one on this channel a service unit can actually assert.
	SwapMaxZero, SwapMaxKnown bool
}

// DeniedDeviceCapture reports whether the service sandbox — not the absence of
// hardware — is what stands between this process and the capture device nodes.
//
// Only true when it was actually READ. An unknown mount table gives false here,
// so the capture suite falls back to describing absence as absence, which is the
// conservative direction: understating a control is a smaller sin than claiming
// one that may not exist.
func (s SandboxState) DeniedDeviceCapture() bool {
	return s.PrivateDevKnown && s.PrivateDev
}

// SandboxScope is the sentence that keeps these rows honest. The controls are
// the unit's, applied by systemd at exec; this binary only verifies them.
const SandboxScope = "These are service-unit controls applied by the init system at start, " +
	"verified here by reading the kernel back rather than by reading the unit file. " +
	"They cover the VayuPress process; the rest of the machine is unaffected."

// parseStatusField pulls one "Name:\tvalue" line out of /proc/<pid>/status.
func parseStatusField(status, name string) (string, bool) {
	for _, line := range strings.Split(status, "\n") {
		rest, ok := strings.CutPrefix(line, name+":")
		if !ok {
			continue
		}
		return strings.TrimSpace(rest), true
	}
	return "", false
}

// parseCapMask reads one of the hex capability masks from /proc/self/status.
func parseCapMask(status, name string) (uint64, bool) {
	v, ok := parseStatusField(status, name)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(v), 16, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// mountedOn reports whether the mount table contains a mount whose mount point
// is exactly `point`.
//
// Exactly, not by prefix. A prefix match on "/dev" would be satisfied by
// /dev/pts or /dev/shm, which are present on EVERY Linux system including one
// with no sandbox at all — so the check would report a control that is not
// there, on every host, forever. That is the failure this repository has a
// standing note about: an assertion that cannot say which thing it matched.
func mountedOn(mountinfo, point string) bool {
	for _, line := range strings.Split(mountinfo, "\n") {
		// mountinfo format: ID PARENT MAJ:MIN ROOT MOUNTPOINT OPTIONS...
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		// Field 5 is the mount point, with spaces escaped as \040.
		if strings.ReplaceAll(f[4], `\040`, " ") == point {
			return true
		}
	}
	return false
}

// CapNetBindService is the one capability the shipped unit grants, so a report
// can name it rather than printing a hex mask at an operator.
const CapNetBindService = 10

// DescribeCaps renders the effective capability set in words.
func (s SandboxState) DescribeCaps() string {
	if !s.CapEffKnown {
		return "The kernel did not report this process's capability set, so nothing is known about " +
			"what privileges it holds. Reported as unverified rather than assumed empty."
	}
	switch {
	case s.CapEff == 0:
		return "This process holds NO capabilities at all: it cannot bind a privileged port, load a " +
			"module, or read another user's memory even if something tricked it into trying. " +
			SandboxScope
	case s.CapEff == 1<<CapNetBindService:
		return "This process holds exactly one capability — CAP_NET_BIND_SERVICE, which lets the mail " +
			"listeners take :25 and :143 — and nothing else. That is what the shipped unit grants. " +
			SandboxScope
	default:
		return "This process holds capabilities beyond the single CAP_NET_BIND_SERVICE the shipped " +
			"unit grants (effective mask 0x" + strconv.FormatUint(s.CapEff, 16) + "). Running with " +
			"more privilege than the service needs widens what a compromise of it reaches. " +
			SandboxScope
	}
}

// DescribeDevices renders the /dev sandbox, which is the row that matters most
// to this ADR: it is the difference between a machine with no camera and a
// machine whose camera has been taken away.
func (s SandboxState) DescribeDevices() string {
	switch {
	case !s.PrivateDevKnown:
		return "The mount table could not be read, so it is not known whether this process has a " +
			"private /dev. Capture-device results below are therefore reported as absence of a " +
			"target rather than as a control."
	case s.PrivateDev:
		return "Verified from the mount table: this process has a PRIVATE /dev containing only the " +
			"pseudo-devices a server needs. The framebuffer, the input event devices and the DRM " +
			"card nodes are not merely missing from this host — they are unreachable from this " +
			"process even if the host has them. This is the closest thing in the server track to " +
			"what §3.2 L2 asks for, and it is the init system doing it, not this binary. " +
			SandboxScope
	default:
		return "This process shares the host's /dev. If this machine has a framebuffer, input devices " +
			"or DRM card nodes, this process can reach them subject only to file permissions — and " +
			"so can anything that compromises it. The shipped unit sets PrivateDevices=yes; an " +
			"install reporting this row is running from a unit that does not."
	}
}

// DescribeSwap renders what is known about this process's anonymous memory
// reaching disk.
//
// Two facts, deliberately not averaged. VmSwap is an OUTCOME — bytes already
// written — and memory.swap.max is a CONTROL. A row that folded them together
// would let "nothing swapped yet" read as "cannot swap", which is the difference
// between luck and a guarantee.
func (s SandboxState) DescribeSwap() string {
	if !s.Supported {
		return "This platform does not report whether this process's memory has been written to disk."
	}
	var b strings.Builder
	switch {
	case !s.SwapKiBKnown:
		b.WriteString("The kernel did not report VmSwap for this process, so whether any of its " +
			"memory has been written to disk is not known. Reported as unverified rather than as none.")
	case s.SwapKiB == 0:
		b.WriteString("VmSwap is 0: none of this process's anonymous memory is currently on disk.")
	default:
		b.WriteString("VmSwap is " + strconv.Itoa(s.SwapKiB) + " kB — that much of this process's " +
			"anonymous memory has been written to swap, and anything that was in it when the kernel " +
			"paged it out is in that file: decrypted mail, session tokens, the keystore key. " +
			"An encrypted data directory does not cover swap.")
	}
	switch {
	case !s.SwapMaxKnown:
		b.WriteString(" Whether this service is permitted to swap at all could not be read, so the " +
			"zero above is an observation and not a guarantee.")
	case s.SwapMaxZero:
		b.WriteString(" This service's cgroup sets memory.swap.max=0, so the kernel cannot page it " +
			"out at all — the reading above is a consequence of a control rather than of luck. " +
			SandboxScope)
	default:
		b.WriteString(" This service's cgroup permits swapping, so a memory-pressure event can put " +
			"its anonymous pages on disk at any time. A unit setting MemorySwapMax=0 forbids it.")
	}
	return b.String()
}
