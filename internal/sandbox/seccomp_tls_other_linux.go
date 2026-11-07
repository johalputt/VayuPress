// SPDX-License-Identifier: Apache-2.0

//go:build linux && !amd64 && !386

package sandbox

// tlsSyscalls is empty where the Go runtime keeps its goroutine pointer in a
// register (arm64, riscv64, ppc64le, s390x, arm) and sets no thread-local
// storage through the kernel.
var tlsSyscalls []uint32
