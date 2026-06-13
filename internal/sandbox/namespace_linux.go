//go:build linux

package sandbox

import "syscall"

// namespaceCloneflags returns Cloneflags based on the manifest isolation settings.
// CLONE_NEWPID: own PID namespace — plugin cannot see/signal host processes.
// CLONE_NEWIPC: own IPC namespace — no shared memory or semaphores with host.
// CLONE_NEWNET: own network namespace — no network (AllowNetwork=false only).
func namespaceCloneflags(m Manifest) uintptr {
	var flags uintptr
	if m.IsolatePID {
		flags |= syscall.CLONE_NEWPID
	}
	if m.IsolateIPC {
		flags |= syscall.CLONE_NEWIPC
	}
	if !m.AllowNetwork {
		// Isolated network namespace: no interfaces, no outbound/inbound.
		flags |= syscall.CLONE_NEWNET
	}
	return flags
}
