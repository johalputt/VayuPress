// SPDX-License-Identifier: Apache-2.0

//go:build linux && (amd64 || 386 || arm || ppc64le || s390x)

package sandbox

import "syscall"

// legacyPollSyscalls are the pre-sigmask polling calls. The arm64 and riscv64
// ABIs were defined after ppoll(2) and epoll_pwait(2) existed and simply do
// not carry epoll_wait(2) or poll(2) — referencing syscall.SYS_EPOLL_WAIT in
// architecture-neutral code is what stopped this package compiling for them.
//
// They stay allowed where the ABI has them because a plugin need not be a Go
// binary: glibc's poll() still issues SYS_poll on x86-64, and narrowing the
// list to what the Go runtime happens to use would take that away.
var legacyPollSyscalls = []uint32{
	syscall.SYS_EPOLL_WAIT,
	syscall.SYS_POLL,
}
