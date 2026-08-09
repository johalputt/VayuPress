// SPDX-License-Identifier: Apache-2.0

//go:build linux && !amd64 && !386 && !arm && !ppc64le && !s390x && !arm64 && !riscv64

package sandbox

// legacyPollSyscalls is empty on architectures this package does not carry a
// verified AUDIT_ARCH_* value for. auditArch() returns 0 there and
// ApplySeccompFilter refuses, so the allowlist is never consulted — but the
// package still has to compile, or adding one architecture to the table would
// break the build for every architecture left out of it.
var legacyPollSyscalls []uint32
