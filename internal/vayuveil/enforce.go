// SPDX-License-Identifier: Apache-2.0

package vayuveil

// enforce.go — what VayuVeil actually enforces, and the exact size of it.
//
// Scoping this correctly matters more than the mechanism. Making THIS process
// undumpable does not enforce the `core-dumps` or `proc-pid-mem` channels, which
// describe the whole host: every other process on the machine is exactly as
// dumpable as it was. So those channel rows do not go green, and the protection
// is reported as its own row with its own scope written into it.
//
// The temptation to credit the host channel is precisely the defect ADR-0150 §8
// exists to prevent — right mechanism, wrong subject, and no way for the reader
// to tell.

import "strings"

// SelfHardening is what this process has verifiably done to itself.
type SelfHardening struct {
	// Supported is whether the platform offers the control at all.
	Supported bool
	// Known is whether the kernel could be asked. False means unverified, which
	// is never reported as protection.
	Known bool
	// Undumpable is the kernel's answer, read back at report time.
	Undumpable bool
	// ApplyErr is what happened when it was applied, if anything.
	ApplyErr string
}

// Scope is the sentence that keeps this row honest wherever it is rendered.
const SelfHardeningScope = "This covers the VayuPress process and nothing else on the machine. " +
	"Every other process is exactly as dumpable as it was."

// ApplyProcessHardening refuses core dumps and same-uid memory reads of this
// process. Safe to call more than once.
func ApplyProcessHardening() error {
	if !hardeningSupported {
		return nil
	}
	return setUndumpable()
}

// VerifyProcessHardening asks the kernel what is actually true now.
func VerifyProcessHardening() SelfHardening {
	s := SelfHardening{Supported: hardeningSupported}
	s.Undumpable, s.Known = dumpableState()
	return s
}

// Describe renders the verified state for a report row.
func (s SelfHardening) Describe() string {
	var b strings.Builder
	switch {
	case !s.Supported:
		b.WriteString("This platform does not offer a way to refuse being dumped, and this binary " +
			"cannot read the setting back either.")
	case !s.Known:
		b.WriteString("The kernel did not answer PR_GET_DUMPABLE, so nothing is known about whether " +
			"this process can be dumped. Reported as unverified rather than assumed.")
	case s.Undumpable:
		b.WriteString("Verified with PR_GET_DUMPABLE: no core dump is written for this process, " +
			"/proc/<pid> is root-owned so a same-user process cannot read its memory or environment, " +
			"and PTRACE_ATTACH from a same-user process is refused. " + SelfHardeningScope)
	default:
		b.WriteString("This process CAN be dumped. A core file would contain session tokens, the " +
			"keystore key, decrypted mail and any PGP material in memory, and a process running as " +
			"the same user can read /proc/<pid>/mem directly.")
	}
	if s.ApplyErr != "" {
		b.WriteString(" Applying it reported: " + s.ApplyErr + ".")
	}
	return b.String()
}
