//go:build !linux

package sandbox

import "os/exec"

func applyProcAttr(cmd *exec.Cmd) {}

func applyRunAs(cmd *exec.Cmd, runas string) {}
