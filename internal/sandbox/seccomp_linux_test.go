// SPDX-License-Identifier: Apache-2.0

//go:build linux

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// ── Child-process probe ───────────────────────────────────────────────────────
//
// TestSeccompFilterActuallyEnforces re-execs this binary with vpSeccompChildEnv
// set. Everything after ApplySeccompFilter runs on a filtered thread, so the
// probe uses only RawSyscall — no allocation, no fmt, no Go exit path, nothing
// that could itself trip the filter and make a working filter look broken.

const vpSeccompChildEnv = "VP_SECCOMP_CHILD_PROBE"

// Exit codes the child reports through exit_group. Distinct values so a
// failure names which invariant broke rather than just "non-zero".
const (
	probeOK              = 0
	probeFilterRefused   = 3 // ApplySeccompFilter returned an error
	probeAllowedDenied   = 4 // an allowed syscall did not go through
	probeDeniedSucceeded = 5 // a syscall outside the allowlist was not blocked
	probeDeniedWrongErr  = 6 // blocked, but not with EPERM
)

func TestMain(m *testing.M) {
	if os.Getenv(vpSeccompChildEnv) != "" {
		seccompProbeChild()
	}
	os.Exit(m.Run())
}

// seccompProbeChild installs the real filter and probes it. It never returns.
func seccompProbeChild() {
	// prctl(PR_SET_SECCOMP) applies to the calling thread only — this
	// goroutine must not be migrated off it after the filter is installed.
	runtime.LockOSThread()

	if err := ApplySeccompFilter(); err != nil {
		rawExit(probeFilterRefused)
	}

	// getpid is on the allowlist: it must still work.
	if _, _, errno := syscall.RawSyscall(syscall.SYS_GETPID, 0, 0, 0); errno != 0 {
		rawExit(probeAllowedDenied)
	}

	// getuid is not on the allowlist. The filter's default action is
	// SECCOMP_RET_ERRNO|EPERM rather than KILL, so the call must return an
	// error and leave the process alive — if the process dies here the parent
	// sees a signal, which the assertions below treat as a failure.
	_, _, errno := syscall.RawSyscall(syscall.SYS_GETUID, 0, 0, 0)
	switch {
	case errno == 0:
		rawExit(probeDeniedSucceeded)
	case errno != syscall.EPERM:
		rawExit(probeDeniedWrongErr)
	}

	rawExit(probeOK)
}

// rawExit leaves via exit_group directly. Go's os.Exit runs exit hooks that
// would issue syscalls the allowlist does not carry.
func rawExit(code uintptr) {
	for {
		syscall.RawSyscall(syscall.SYS_EXIT_GROUP, code, 0, 0) //nolint:errcheck,dogsled
	}
}

func TestSeccompFilterActuallyEnforces(t *testing.T) {
	// A wrong action can hang rather than fail: SECCOMP_RET_KILL_THREAD kills
	// the offending thread and leaves the Go runtime deadlocked on it, so the
	// child neither exits nor signals. Bound the wait — a gate that stalls CI
	// reports nothing.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestSeccompFilterActuallyEnforces")
	cmd.Env = append(os.Environ(), vpSeccompChildEnv+"=1")
	err := cmd.Run()

	if ctx.Err() != nil {
		t.Fatal("probe child never exited — the filter's action neither allowed, " +
			"denied nor killed the process")
	}

	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running probe child: %v", err)
		}
		status, ok := exitErr.Sys().(syscall.WaitStatus)
		if !ok {
			t.Fatalf("no wait status from probe child: %v", err)
		}
		if status.Signaled() {
			t.Fatalf("probe child died on signal %v — the filter killed the process "+
				"instead of returning EPERM", status.Signal())
		}
		code = status.ExitStatus()
	}

	switch code {
	case probeOK:
	case probeFilterRefused:
		// A sandbox this environment cannot install is worth knowing about,
		// but it is not this package's bug.
		t.Skip("kernel refused the seccomp filter (no CONFIG_SECCOMP_FILTER, or " +
			"a container policy blocks prctl) — cannot exercise enforcement here")
	case probeAllowedDenied:
		t.Fatal("getpid is on the allowlist but the filter blocked it")
	case probeDeniedSucceeded:
		t.Fatal("getuid is not on the allowlist but the filter let it through")
	case probeDeniedWrongErr:
		t.Fatal("getuid was blocked with the wrong errno; the default action must be EPERM")
	default:
		t.Fatalf("probe child exited %d", code)
	}
}

// ── Audit-arch table ──────────────────────────────────────────────────────────

// TestAuditArchTableMatchesTheKernelDerivation re-derives every entry from the
// ELF machine number and the kernel's two flag bits, which is how
// <linux/audit.h> defines AUDIT_ARCH_*. A hand-typed hex constant that is
// wrong by one bit stops the filter's guard matching and the kernel then kills
// the process on its first syscall — on an architecture no test here can run.
// Re-deriving is the only check that reaches those architectures.
func TestAuditArchTableMatchesTheKernelDerivation(t *testing.T) {
	const (
		bits64 = 0x80000000 // __AUDIT_ARCH_64BIT
		little = 0x40000000 // __AUDIT_ARCH_LE
	)
	// ELF e_machine values.
	const (
		emX86_64  = 62
		emAArch64 = 183
		emRISCV   = 243
		emPPC64   = 21
		emS390    = 22
		emARM     = 40
		em386     = 3
	)

	want := map[string]uint32{
		"amd64":   emX86_64 | bits64 | little,
		"arm64":   emAArch64 | bits64 | little,
		"riscv64": emRISCV | bits64 | little,
		"ppc64le": emPPC64 | bits64 | little,
		"s390x":   emS390 | bits64, // s390x is big-endian
		"arm":     emARM | little,
		"386":     em386 | little,
	}

	for arch, expect := range want {
		got, ok := seccompAuditArch[arch]
		if !ok {
			t.Errorf("linux/%s has no audit arch; ApplySeccompFilter would refuse there", arch)
			continue
		}
		if got != expect {
			t.Errorf("linux/%s audit arch = %#08x, kernel derivation gives %#08x", arch, got, expect)
		}
	}
	if len(seccompAuditArch) != len(want) {
		t.Errorf("audit arch table has %d entries, the derivation covers %d — a new "+
			"entry must be derived here too", len(seccompAuditArch), len(want))
	}
}

// TestFilterGuardsTheArchItWasBuiltFor pins the guard to this build's own
// architecture. The bug this replaces was a filter that compared against a
// hardcoded AUDIT_ARCH_X86_64 everywhere, so on any other architecture the
// guard fell through to SECCOMP_RET_KILL_PROCESS on the first syscall.
func TestFilterGuardsTheArchItWasBuiltFor(t *testing.T) {
	insns, err := buildSeccompFilter()
	if err != nil {
		t.Fatalf("buildSeccompFilter on linux/%s: %v", runtime.GOARCH, err)
	}
	if len(insns) < 4 {
		t.Fatalf("filter is %d instructions, too short to carry an arch guard", len(insns))
	}
	if got, want := insns[1].k, auditArch(); got != want {
		t.Errorf("arch guard compares against %#08x, this build is linux/%s (%#08x)",
			got, runtime.GOARCH, want)
	}
	if insns[2].k != seccompActKill {
		t.Errorf("arch mismatch action = %#08x, want SECCOMP_RET_KILL_PROCESS (%#08x)",
			insns[2].k, seccompActKill)
	}
}

// TestKillActionEndsTheProcessNotJustTheThread pins the one seccomp action
// whose two spellings differ by a single bit and by everything else.
// SECCOMP_RET_KILL_THREAD (0x00000000) reaps the offending thread and leaves
// the process running — for a Go program that means a deadlock, not a
// termination, so a breach of the arch guard would hang instead of stopping.
func TestKillActionEndsTheProcessNotJustTheThread(t *testing.T) {
	const (
		killThread  = 0x00000000 // SECCOMP_RET_KILL_THREAD
		killProcess = 0x80000000 // SECCOMP_RET_KILL_PROCESS
	)
	if seccompActKill == killThread {
		t.Fatal("kill action is SECCOMP_RET_KILL_THREAD; a filter breach must end the process")
	}
	if seccompActKill != killProcess {
		t.Errorf("kill action = %#08x, want SECCOMP_RET_KILL_PROCESS (%#08x)",
			uint32(seccompActKill), uint32(killProcess))
	}
}

// TestUnsupportedArchRefusesInsteadOfFiltering covers the architectures with no
// table entry. Installing a filter there is strictly worse than installing
// none: the guard cannot match, so every syscall is killed. Refusing hands the
// caller an error and the plugin does not start.
func TestUnsupportedArchRefusesInsteadOfFiltering(t *testing.T) {
	saved, had := seccompAuditArch[runtime.GOARCH]
	delete(seccompAuditArch, runtime.GOARCH)
	defer func() {
		if had {
			seccompAuditArch[runtime.GOARCH] = saved
		}
	}()

	insns, err := buildSeccompFilter()
	if err == nil {
		t.Fatalf("built a %d-instruction filter for an architecture with no audit arch; "+
			"it would kill on the first syscall", len(insns))
	}
	if insns != nil {
		t.Error("returned instructions alongside the error; a caller ignoring err would install them")
	}
}

// ── Allowlist contents ────────────────────────────────────────────────────────

// TestNetpollSyscallIsAllowed is a regression test. The Go runtime's netpoller
// issues epoll_pwait on every Linux architecture, x86-64 included — the
// allowlist carried only epoll_wait, so a sandboxed Go plugin took EPERM on
// every poll of its own IPC pipe.
func TestNetpollSyscallIsAllowed(t *testing.T) {
	if !allowsSyscall(syscall.SYS_EPOLL_PWAIT) {
		t.Error("epoll_pwait is not allowed; the Go runtime's netpoller cannot run under this filter")
	}
	if !allowsSyscall(syscall.SYS_EPOLL_CTL) || !allowsSyscall(syscall.SYS_EPOLL_CREATE1) {
		t.Error("epoll_ctl and epoll_create1 must accompany epoll_pwait")
	}
}

// TestAllowlistStaysMinimal guards the direction of travel: the list exists to
// be small, and a syscall that opens files, opens sockets or starts processes
// defeats the confinement whatever else the sandbox does.
func TestAllowlistStaysMinimal(t *testing.T) {
	forbidden := map[string]uintptr{
		"execve": syscall.SYS_EXECVE,
		"openat": syscall.SYS_OPENAT,
		"socket": syscall.SYS_SOCKET,
		"ptrace": syscall.SYS_PTRACE,
		"mount":  syscall.SYS_MOUNT,
		"prctl":  syscall.SYS_PRCTL,
	}
	for name, nr := range forbidden {
		if allowsSyscall(nr) {
			t.Errorf("%s is on the allowlist; it defeats the sandbox", name)
		}
	}
}

// TestAllowlistHasNoDuplicates keeps the emitted program honest — a repeated
// number is two comparisons that can never both matter, and usually means an
// architecture-specific entry was also added to the portable list.
func TestAllowlistHasNoDuplicates(t *testing.T) {
	seen := map[uint32]bool{}
	for _, nr := range allowedSyscalls() {
		if seen[nr] {
			t.Errorf("syscall %d appears twice in the allowlist", nr)
		}
		seen[nr] = true
	}
}

func allowsSyscall(nr uintptr) bool {
	for _, allowed := range allowedSyscalls() {
		if allowed == uint32(nr) {
			return true
		}
	}
	return false
}
