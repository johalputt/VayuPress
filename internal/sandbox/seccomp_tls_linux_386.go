// SPDX-License-Identifier: Apache-2.0

//go:build linux && 386

package sandbox

import "syscall"

// tlsSyscalls on 386: the Go runtime sets its thread-local storage with
// set_thread_area. UNEXERCISED: no 386 kernel runs these tests; it is read
// from the runtime's own settls, not seen to work.
var tlsSyscalls = []uint32{syscall.SYS_SET_THREAD_AREA}
