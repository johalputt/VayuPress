//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/johalputt/vayupress/internal/logging"
)

const cgroupRoot = "/sys/fs/cgroup/vayupress/plugins"

// setupCgroup creates a per-plugin cgroup v2 slice, applies resource limits,
// places the subprocess PID in it, and returns a cleanup function.
// If cgroup v2 is unavailable or permission is denied, it logs a warning and
// returns a no-op cleanup — cgroup is best-effort, not a hard requirement.
func setupCgroup(m Manifest, pid int) func() {
	cgPath := filepath.Join(cgroupRoot, fmt.Sprintf("%s-%d", m.Name, pid))

	if err := os.MkdirAll(cgPath, 0755); err != nil {
		logging.LogJSON(logging.LogFields{
			Level:     "warn",
			Component: "sandbox",
			Msg:       fmt.Sprintf("cgroup: cannot create %s (cgroup v2 unavailable or no permission): %v", cgPath, err),
		})
		return func() {}
	}

	// Enable controllers in parent path — ignore errors (may already be set).
	enableControllersUpward(cgroupRoot)

	if m.ResourceLimits.MemoryMaxBytes > 0 {
		writeControl(cgPath, "memory.max", strconv.FormatInt(m.ResourceLimits.MemoryMaxBytes, 10)) //nolint:errcheck
	}
	if m.ResourceLimits.CPUQuotaPercent > 0 {
		// cpu.max format: "$QUOTA $PERIOD" — period is 100000 µs (100ms)
		quota := m.ResourceLimits.CPUQuotaPercent * 1000
		writeControl(cgPath, "cpu.max", fmt.Sprintf("%d 100000", quota)) //nolint:errcheck
	}
	if m.ResourceLimits.MaxPIDs > 0 {
		writeControl(cgPath, "pids.max", strconv.Itoa(m.ResourceLimits.MaxPIDs)) //nolint:errcheck
	}

	// Place subprocess in this cgroup.
	if err := writeControl(cgPath, "cgroup.procs", strconv.Itoa(pid)); err != nil {
		logging.LogJSON(logging.LogFields{
			Level:     "warn",
			Component: "sandbox",
			Msg:       fmt.Sprintf("cgroup: cannot assign pid %d to %s: %v", pid, cgPath, err),
		})
	} else {
		logging.LogJSON(logging.LogFields{
			Level:     "info",
			Component: "sandbox",
			Msg:       fmt.Sprintf("cgroup: plugin=%s pid=%d memory_max=%d cpu_pct=%d pids_max=%d", m.Name, pid, m.ResourceLimits.MemoryMaxBytes, m.ResourceLimits.CPUQuotaPercent, m.ResourceLimits.MaxPIDs),
		})
	}

	return func() {
		// Remove the cgroup directory (succeeds only after all procs exit).
		if err := os.Remove(cgPath); err != nil && !os.IsNotExist(err) {
			logging.LogJSON(logging.LogFields{
				Level:     "warn",
				Component: "sandbox",
				Msg:       fmt.Sprintf("cgroup cleanup: cannot remove %s: %v", cgPath, err),
			})
		}
	}
}

// writeControl writes value to a cgroup control file.
func writeControl(cgPath, file, value string) error {
	p := filepath.Join(cgPath, file)
	return os.WriteFile(p, []byte(value), 0)
}

// enableControllersUpward writes "+memory +cpu +pids" to cgroup.subtree_control
// for each directory from cgroupRoot upward to ensure controllers are delegated.
func enableControllersUpward(dir string) {
	// Walk from /sys/fs/cgroup down to dir, enabling subtree_control at each level.
	// Ignore errors — the system may already have controllers enabled.
	for _, path := range []string{
		"/sys/fs/cgroup",
		"/sys/fs/cgroup/vayupress",
		dir,
	} {
		_ = os.WriteFile(filepath.Join(path, "cgroup.subtree_control"), []byte("+memory +cpu +pids"), 0)
	}
}
