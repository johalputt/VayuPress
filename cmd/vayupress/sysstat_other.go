//go:build !linux

// SPDX-License-Identifier: Apache-2.0

package main

// sysstat_other.go — non-Linux stubs for the VayuOS Storage & System readings
// in sysstat.go (which needs /proc and statfs). The supported deployment target
// is Linux; elsewhere every reading degrades to zero so the page still renders,
// exactly like the Linux implementation's own best-effort fallbacks. Shared,
// platform-neutral helpers (humanBytes, dirSize) keep their real behaviour.

import (
	"os"
	"path/filepath"
	"strconv"
)

// sysStats is a point-in-time snapshot of resource usage.
type sysStats struct {
	ProcRSS      uint64
	GoHeapInUse  uint64
	MemTotal     uint64
	MemUsed      uint64
	MemAvailable uint64
	Goroutines   int

	DiskPath  string
	DiskTotal uint64
	DiskUsed  uint64
	DiskFree  uint64

	DBSize      int64
	CacheSize   int64
	MediaSize   int64
	BackupsSize int64
}

func (s sysStats) memPct() int  { return pctOf(s.MemUsed, s.MemTotal) }
func (s sysStats) diskPct() int { return pctOf(s.DiskUsed, s.DiskTotal) }

func pctOf(used, total uint64) int {
	if total == 0 {
		return 0
	}
	return int((used * 100) / total)
}

func collectSysStats(dbPath, cacheDir, mediaDir, backupsDir string) sysStats {
	return sysStats{
		DiskPath:    dbPath,
		DBSize:      fileSize(dbPath),
		CacheSize:   dirSize(cacheDir),
		MediaSize:   dirSize(mediaDir),
		BackupsSize: dirSize(backupsDir),
	}
}

func startFootprintRefresher(cacheDir, mediaDir, backupsDir string) {}

func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil // best-effort: unreadable entries contribute nothing
		}
		if !fi.IsDir() {
			total += fi.Size()
		}
		return nil
	})
	return total
}

// humanBytes formats a byte count as a compact human-readable string.
func humanBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	val := float64(n) / float64(div)
	return strconv.FormatFloat(val, 'f', 1, 64) + " " + string("KMGTPE"[exp]) + "iB"
}
