package update

// restart.go — in-process self-restart, so a verified binary update or a
// database restore can be activated from the admin UI with no shell access.
//
// After ApplyVerified atomically replaces the running binary at os.Executable()
// (or StageRestore stages a new database), the operator still needs the process
// to re-exec so the new code/data is loaded. Relaunch performs a hard re-exec
// of the same path with the same argv and environment via execve(2): the kernel
// replaces the current process image in place, so the PID, listening sockets'
// ownership under a supervisor, and service identity are all preserved. If the
// re-exec fails for any reason, we exit cleanly so a process supervisor
// (systemd Restart=always, Docker restart policy, etc.) brings us back.
//
// This is intentionally a Unix-only mechanism; VayuPress ships and is supported
// on Linux (see Dockerfile / deploy/). The beforeExec hook lets the caller
// flush and close the database (WAL checkpoint) so no writes are lost.

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
)

// Relaunch re-execs the currently running executable, replacing this process
// image. On success it does not return. beforeExec, if non-nil, runs
// immediately before the re-exec (use it to checkpoint and close the database).
//
// It derives the path from os.Executable(); use RelaunchExec when a specific
// (freshly-written) binary path is known — see the comment on cleanExePath for
// why os.Executable() alone is unsafe immediately after a self-update.
func Relaunch(beforeExec func()) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return RelaunchExec(exe, beforeExec)
}

// RelaunchExec re-execs execPath, replacing this process image (execve(2)): the
// PID and any supervisor's service identity are preserved, so the new binary
// takes over in place. On success it does not return. beforeExec, if non-nil,
// runs immediately before the re-exec (checkpoint + close the database there).
//
// The path is normalised with cleanExePath so a "(deleted)" marker left by a
// just-completed binary swap is stripped and symlinks are resolved — this is the
// fix for "update installed but still the old version after restart": the bare
// os.Executable() value points at the now-unlinked OLD inode, so re-execing it
// either fails (and we fall back to a supervisor) or re-runs the old code.
func RelaunchExec(execPath string, beforeExec func()) error {
	exe := cleanExePath(execPath)
	if exe == "" {
		return os.ErrInvalid
	}
	if beforeExec != nil {
		beforeExec()
	}
	logging.LogInfo("update", "re-executing "+exe+" to activate update")
	return syscall.Exec(exe, os.Args, os.Environ()) //nosec G702 -- re-exec of our own resolved binary; arguments are this process's own, not external input
}

// cleanExePath normalises a binary path for re-exec. After a self-update the
// kernel reports the running binary's path as "/path/to/bin (deleted)" (the old
// inode was unlinked by the atomic swap); execing that literal path fails with
// ENOENT and would re-run the stale image. We strip the " (deleted)" suffix so
// we exec the path that now holds the NEW binary, then resolve symlinks so the
// real file is launched.
func cleanExePath(p string) string {
	const deleted = " (deleted)"
	p = strings.TrimSuffix(p, deleted)
	if p == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// ScheduleRestart performs Relaunch after delay, off the calling goroutine, so
// an HTTP handler can return its response to the operator first. If the re-exec
// fails, the process exits 0 so a supervisor can restart it.
func ScheduleRestart(delay time.Duration, beforeExec func()) {
	ScheduleRestartExec("", delay, beforeExec)
}

// ScheduleRestartExec is ScheduleRestart targeting a specific binary path (the
// one a self-update just wrote). An empty execPath falls back to os.Executable()
// via Relaunch. If the re-exec fails, the process exits 0 so a supervisor
// (systemd Restart=always, Docker restart policy, …) restarts it on the new
// binary.
func ScheduleRestartExec(execPath string, delay time.Duration, beforeExec func()) {
	go func() {
		time.Sleep(delay)
		var err error
		if strings.TrimSpace(execPath) == "" {
			err = Relaunch(beforeExec)
		} else {
			err = RelaunchExec(execPath, beforeExec)
		}
		if err != nil {
			logging.LogError("update", "self-restart failed — exiting for supervisor restart", err.Error())
			os.Exit(0)
		}
	}()
}
