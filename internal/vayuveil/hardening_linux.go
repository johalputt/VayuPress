// SPDX-License-Identifier: Apache-2.0

//go:build linux

package vayuveil

// hardening_linux.go — the part of ADR-0150 this binary can actually enforce.
//
// P0 registers policy and enforces none of it, with one exception that is worth
// making precisely because it is small and real: a process can refuse to be
// dumped. prctl(PR_SET_DUMPABLE, 0) does three things at once —
//
//   - no core dump is written for this process, and a VayuPress core contains
//     session tokens, the keystore DEK, decrypted mail bodies and whatever PGP
//     material was in memory at the time;
//   - /proc/<pid>/ becomes root-owned, so another process running as the SAME
//     user cannot read /proc/<pid>/mem or /proc/<pid>/environ;
//   - PTRACE_ATTACH from a same-uid process is refused, because ptrace requires
//     the target to be dumpable (or the tracer to hold CAP_SYS_PTRACE).
//
// That is the `core-dumps` and `proc-pid-mem` channels, enforced, for this
// process. THE SCOPE IS THIS PROCESS AND NOTHING ELSE, and every surface that
// reports it says so — "VayuPress cannot be dumped" is true and useful; "this
// machine is protected from memory capture" would be the §8 lie.
//
// It is verifiable, which is why it is allowed to turn a row green: the report
// asks the kernel with PR_GET_DUMPABLE rather than trusting that the setter was
// called. A control that reports itself is not evidence.

import "golang.org/x/sys/unix"

// hardeningSupported reports whether this platform can refuse to be dumped.
const hardeningSupported = true

// setUndumpable asks the kernel to make this process undumpable.
func setUndumpable() error {
	return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
}

// dumpableState reads the kernel's answer back.
//
// Read rather than remembered. The whole reason this row is permitted to be
// green is that it is checked against the kernel at the moment the report is
// computed — a flag set at boot and trusted forever is configuration, and
// configuration is what ADR-0150 §4 refuses to accept as evidence.
func dumpableState() (undumpable bool, known bool) {
	var v int
	v, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0)
	if err != nil {
		return false, false
	}
	return v == 0, true
}

// nonblockFlag is O_NONBLOCK, and the capture suite depends on it.
//
// The red-team techniques do not merely open device nodes, they READ from them —
// that is the whole point, since the question is what an attacker ends up
// holding. But a read from an evdev node with no pending events BLOCKS until
// somebody presses a key. Rendering the console on a machine with a real
// keyboard would have hung the request until the operator happened to type.
const nonblockFlag = unix.O_NONBLOCK
