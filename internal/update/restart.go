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
	"syscall"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
)

// Relaunch re-execs the currently running executable, replacing this process
// image. On success it does not return. beforeExec, if non-nil, runs
// immediately before the re-exec (use it to checkpoint and close the database).
func Relaunch(beforeExec func()) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Resolve any symlink so we exec the real, freshly-written binary.
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	if beforeExec != nil {
		beforeExec()
	}
	logging.LogInfo("update", "re-executing "+exe+" to activate update")
	return syscall.Exec(exe, os.Args, os.Environ()) //nosec G702 -- re-exec of our own resolved binary (os.Executable); arguments are this process's own, not external input
}

// ScheduleRestart performs Relaunch after delay, off the calling goroutine, so
// an HTTP handler can return its response to the operator first. If the re-exec
// fails, the process exits 0 so a supervisor can restart it.
func ScheduleRestart(delay time.Duration, beforeExec func()) {
	go func() {
		time.Sleep(delay)
		if err := Relaunch(beforeExec); err != nil {
			logging.LogError("update", "self-restart failed — exiting for supervisor restart", err.Error())
			os.Exit(0)
		}
	}()
}
