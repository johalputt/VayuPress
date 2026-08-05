// SPDX-License-Identifier: Apache-2.0

//go:build linux

package vayuveil

// sandbox_linux.go — reading the service sandbox back from the kernel.
//
// Everything here READS. Nothing applies. See sandbox.go for why applying
// PR_SET_NO_NEW_PRIVS from inside this process would be the wrong move.

import (
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// readSandbox gathers the verifiable service-unit state.
//
// procRead is injected so a test can drive every branch — including the ones a
// test host cannot produce, like a machine that has NOT got a private /dev.
// A suite that could only ever observe the state of the machine it happens to
// run on proves nothing about the other state.
func readSandbox(procRead func(path string) (string, error)) SandboxState {
	s := SandboxState{Supported: true}

	if v, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0); err == nil {
		s.NoNewPrivs, s.NoNewPrivsKnown = v == 1, true
	}

	if status, err := procRead("/proc/self/status"); err == nil {
		if v, ok := parseStatusField(status, "Seccomp"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				s.SeccompMode, s.SeccompModeKnown = n, true
			}
		}
		// VmSwap is how much of THIS process is on disk right now. An outcome
		// rather than a control, and the only direct evidence available that
		// decrypted mail or the keystore key has left RAM.
		if v, ok := parseStatusField(status, "VmSwap"); ok {
			if n, err := strconv.Atoi(strings.Fields(strings.TrimSpace(v))[0]); err == nil {
				s.SwapKiB, s.SwapKiBKnown = n, true
			}
		}
		s.CapEff, s.CapEffKnown = parseCapMask(status, "CapEff")
		s.CapBnd, s.CapBndKnown = parseCapMask(status, "CapBnd")
		// NoNewPrivs also appears here. Used only as a fallback: the prctl is the
		// authoritative answer, and preferring the parsed text over the syscall
		// would be choosing the less direct evidence for no reason.
		if !s.NoNewPrivsKnown {
			if v, ok := parseStatusField(status, "NoNewPrivs"); ok {
				s.NoNewPrivs, s.NoNewPrivsKnown = strings.TrimSpace(v) == "1", true
			}
		}
	}

	if mi, err := procRead("/proc/self/mountinfo"); err == nil {
		// systemd's PrivateDevices=, PrivateTmp= and ProtectHome= each install a
		// mount at exactly that path in this process's namespace. Their presence
		// is the evidence; the unit file is not, because a unit file on disk says
		// what someone intended, not what the process got.
		s.PrivateDev, s.PrivateDevKnown = mountedOn(mi, "/dev"), true
		s.PrivateTmp, s.PrivateTmpKnown = mountedOn(mi, "/tmp"), true
		s.ProtectedHome, s.ProtectedHomeKnown = mountedOn(mi, "/home"), true
	}

	// cgroup v2 delegates this file into the service's own namespace, so an
	// unprivileged read of the relative path is the service's OWN limit rather
	// than the machine's. A unit carrying MemorySwapMax=0 shows up here as 0,
	// which is the one control on this channel a unit can actually assert.
	if v, err := procRead("/sys/fs/cgroup/memory.swap.max"); err == nil {
		t := strings.TrimSpace(v)
		// "max" means unlimited. Anything else is a byte count.
		if t == "max" {
			s.SwapMaxZero, s.SwapMaxKnown = false, true
		} else if n, err := strconv.ParseInt(t, 10, 64); err == nil {
			s.SwapMaxZero, s.SwapMaxKnown = n == 0, true
		}
	}

	if v, err := procRead("/proc/sys/kernel/yama/ptrace_scope"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			s.PtraceScope, s.PtraceScopeKnown = n, true
		}
	}

	return s
}

// ReadSandbox reads the live state.
func ReadSandbox() SandboxState { return readSandbox(readFileString) }

func readFileString(path string) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // fixed procfs paths, not operator input
	if err != nil {
		return "", err
	}
	return string(b), nil
}
