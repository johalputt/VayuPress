//go:build linux

package sandbox

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/johalputt/vayupress/internal/logging"
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

// applyRunAs parses a "uid:gid" string and sets SysProcAttr.Credential.
// It is a no-op if runas is empty or malformed (logs a warning on malformed input).
func applyRunAs(cmd *exec.Cmd, runas string) {
	if runas == "" {
		return
	}
	parts := strings.SplitN(runas, ":", 2)
	if len(parts) != 2 {
		logging.LogJSON(logging.LogFields{Level: "warn", Component: "sandbox", Msg: "RunAs: invalid format (expected uid:gid): " + runas})
		return
	}
	uid, errU := strconv.ParseUint(parts[0], 10, 32)
	gid, errG := strconv.ParseUint(parts[1], 10, 32)
	if errU != nil || errG != nil {
		logging.LogJSON(logging.LogFields{Level: "warn", Component: "sandbox", Msg: "RunAs: non-numeric uid/gid: " + runas})
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Credential = &syscall.Credential{
		Uid: uint32(uid),
		Gid: uint32(gid),
	}
}
