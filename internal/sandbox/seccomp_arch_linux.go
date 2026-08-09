// SPDX-License-Identifier: Apache-2.0

//go:build linux

package sandbox

import "runtime"

// A seccomp filter compares raw syscall numbers, and those numbers mean
// different things on different ABIs — write(2) is 1 on x86-64 and 64 on
// arm64. So every filter must first check seccomp_data.arch and refuse to
// interpret the number at all if it does not recognise it. The value is the
// kernel's AUDIT_ARCH_* constant: the ELF machine type OR'd with
// __AUDIT_ARCH_64BIT (0x80000000) for 64-bit ABIs and __AUDIT_ARCH_LE
// (0x40000000) for little-endian ones.
//
// This is a GOARCH-keyed table rather than a per-file build-tagged constant
// so the whole table can be asserted from a test on one host. Getting an
// entry wrong is silent in the worst direction — the guard stops matching and
// every syscall is killed — and a table that only one architecture can see is
// a table nobody checks.
var seccompAuditArch = map[string]uint32{
	"amd64":   0xC000003E, // AUDIT_ARCH_X86_64   — EM_X86_64 (62) | 64BIT | LE
	"arm64":   0xC00000B7, // AUDIT_ARCH_AARCH64  — EM_AARCH64 (183) | 64BIT | LE
	"riscv64": 0xC00000F3, // AUDIT_ARCH_RISCV64  — EM_RISCV (243) | 64BIT | LE
	"ppc64le": 0xC0000015, // AUDIT_ARCH_PPC64LE  — EM_PPC64 (21) | 64BIT | LE
	"s390x":   0x80000016, // AUDIT_ARCH_S390X    — EM_S390 (22) | 64BIT, big-endian
	"arm":     0x40000028, // AUDIT_ARCH_ARM      — EM_ARM (40) | LE
	"386":     0x40000003, // AUDIT_ARCH_I386     — EM_386 (3) | LE
}

// auditArch returns the AUDIT_ARCH_* value for the architecture this binary
// was built for, or 0 if seccomp is not supported here. Callers must treat 0
// as "refuse to install a filter" — never as "install one anyway". A filter
// carrying the wrong arch value does not degrade to no filtering; its guard
// fails on the first syscall and the kernel kills the process.
func auditArch() uint32 { return seccompAuditArch[runtime.GOARCH] }
