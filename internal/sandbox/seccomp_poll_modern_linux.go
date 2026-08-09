// SPDX-License-Identifier: Apache-2.0

//go:build linux && (arm64 || riscv64)

package sandbox

// legacyPollSyscalls is empty on the architectures whose ABI never defined
// epoll_wait(2) or poll(2). ppoll(2) and epoll_pwait(2) are the only spellings
// here, and both are in the architecture-neutral allowlist.
var legacyPollSyscalls []uint32
