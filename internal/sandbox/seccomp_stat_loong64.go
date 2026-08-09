// SPDX-License-Identifier: Apache-2.0

//go:build linux && loong64

package sandbox

import "syscall"

// statSyscalls on LoongArch. The ABI has no fstat(2) — it was defined after
// statx(2) and carries only that.
//
// This entry is UNEXERCISED and is here so the package compiles, not because it
// has been shown to work. loong64 has no AUDIT_ARCH_* entry in
// seccompAuditArch, so ApplySeccompFilter refuses on it and this list is never
// turned into a filter. Anyone adding loong64 to that table must verify this
// list against a real LoongArch kernel first — a plugin whose stat call is
// missing from the allowlist takes EPERM on every file it opens, and no test
// here can see that.
var statSyscalls = []uint32{
	syscall.SYS_STATX,
}
