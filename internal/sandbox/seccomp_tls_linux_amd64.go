// SPDX-License-Identifier: Apache-2.0

//go:build linux && amd64

package sandbox

import "syscall"

// tlsSyscalls on x86-64: the Go runtime sets its thread-local storage with
// arch_prctl(ARCH_SET_FS) as its first act, and stops dead if refused, so
// no Go plugin starts without it (seen with strace on the example plugins).
var tlsSyscalls = []uint32{syscall.SYS_ARCH_PRCTL}
