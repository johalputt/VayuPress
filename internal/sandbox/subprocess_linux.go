// SPDX-License-Identifier: Apache-2.0

//go:build linux

package sandbox

import (
	"os/exec"
	"syscall"
)

func applyProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL, // kill child if parent dies
		Setpgid:   true,            // own process group — kill group on timeout
	}
}

// applyNamespaceFlags OR's namespace clone flags into the existing SysProcAttr.
func applyNamespaceFlags(cmd *exec.Cmd, flags uintptr) {
	if flags == 0 {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Cloneflags |= flags
}

// pluginCommand starts m's plugin through the helper (helper_linux.go),
// which confines the child and then execs the plugin. RunAs is applied
// there too, after the mounts that need the host's privilege. ownMounts
// says the child has its own mount namespace, the only place the helper may
// mount.
func pluginCommand(m Manifest, c *PluginConfinement, ownMounts bool) *exec.Cmd {
	spec := helperSpec{
		confine:  m.ConfineMounts && ownMounts,
		scratch:  c.ScratchDir(),
		runAs:    m.RunAs,
		syscalls: syscallClassOf(m),
	}
	return exec.Command(helperSelf, helperArgv(spec, m.Executable, m.Args)...)
}
