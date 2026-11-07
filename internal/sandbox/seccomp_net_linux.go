// SPDX-License-Identifier: Apache-2.0

//go:build linux && !386

package sandbox

import "syscall"

// networkSyscalls are added for a plugin whose manifest allows the network;
// any other plugin is also in a network namespace with no interfaces.
var networkSyscalls = []uint32{
	syscall.SYS_SOCKET,
	syscall.SYS_CONNECT,
	syscall.SYS_GETSOCKOPT,
	syscall.SYS_SETSOCKOPT,
	syscall.SYS_GETSOCKNAME,
	syscall.SYS_GETPEERNAME,
	syscall.SYS_SENDTO,
	syscall.SYS_RECVFROM,
	syscall.SYS_SENDMSG,
	syscall.SYS_RECVMSG,
	syscall.SYS_SHUTDOWN,
}
