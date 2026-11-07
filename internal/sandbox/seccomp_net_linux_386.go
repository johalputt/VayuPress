// SPDX-License-Identifier: Apache-2.0

//go:build linux && 386

package sandbox

import "syscall"

// networkSyscalls on 386, which multiplexes every socket call through
// socketcall(2). UNEXERCISED, as tlsSyscalls on 386.
var networkSyscalls = []uint32{syscall.SYS_SOCKETCALL}
