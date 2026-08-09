// SPDX-License-Identifier: Apache-2.0

//go:build linux && !loong64

package sandbox

import "syscall"

// statSyscalls is how a plugin asks about an open file descriptor. Every Linux
// ABI here spells it fstat(2) except LoongArch, which was defined after statx(2)
// and carries no fstat at all — naming syscall.SYS_FSTAT in
// architecture-neutral code is what stopped this package compiling for it.
var statSyscalls = []uint32{
	syscall.SYS_FSTAT,
}
