package main

// sysstat.go — lightweight host + process resource readings for the VayuOS
// Storage & System page: how much RAM and disk (NVMe) the VayuPress system is
// using. Linux-only (matches the supported deployment target); every reading is
// best-effort and degrades to zero rather than erroring, so the page always
// renders.

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// sysStats is a point-in-time snapshot of resource usage.
type sysStats struct {
	// Memory (bytes).
	ProcRSS      uint64 // resident memory of THIS VayuPress process
	GoHeapInUse  uint64 // Go heap actually in use by the process
	MemTotal     uint64 // total system RAM
	MemUsed      uint64 // system RAM in use (total - available)
	MemAvailable uint64
	Goroutines   int

	// Disk (bytes) for the filesystem that holds the database.
	DiskPath  string
	DiskTotal uint64
	DiskUsed  uint64
	DiskFree  uint64

	// VayuPress on-disk footprint (bytes).
	DBSize      int64
	CacheSize   int64
	MediaSize   int64
	BackupsSize int64
}

// memPct returns system RAM used as a 0–100 integer.
func (s sysStats) memPct() int { return pctOf(s.MemUsed, s.MemTotal) }

// diskPct returns disk used as a 0–100 integer.
func (s sysStats) diskPct() int { return pctOf(s.DiskUsed, s.DiskTotal) }

func pctOf(used, total uint64) int {
	if total == 0 {
		return 0
	}
	return int((used * 100) / total)
}

// collectSysStats gathers the current resource snapshot. dataDirs supplies the
// VayuPress directories whose sizes are summed for the footprint figures.
func collectSysStats(dbPath, cacheDir, mediaDir, backupsDir string) sysStats {
	var s sysStats

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	s.GoHeapInUse = ms.HeapInuse
	s.Goroutines = runtime.NumGoroutine()
	s.ProcRSS = readProcRSS()

	total, avail := readMemInfo()
	s.MemTotal = total
	s.MemAvailable = avail
	if total >= avail {
		s.MemUsed = total - avail
	}

	dir := filepath.Dir(dbPath)
	s.DiskPath = dir
	s.DiskTotal, s.DiskFree = diskUsage(dir)
	if s.DiskTotal >= s.DiskFree {
		s.DiskUsed = s.DiskTotal - s.DiskFree
	}

	s.DBSize = fileSize(dbPath) + fileSize(dbPath+"-wal") + fileSize(dbPath+"-shm")
	s.CacheSize = dirSize(cacheDir)
	s.MediaSize = dirSize(mediaDir)
	s.BackupsSize = dirSize(backupsDir)
	return s
}

// readProcRSS reads this process's resident set size (bytes) from
// /proc/self/status (VmRSS, reported in kB). Returns 0 if unavailable.
func readProcRSS() uint64 {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "VmRSS:") {
			return kbFieldToBytes(line)
		}
	}
	return 0
}

// readMemInfo reads total and available system memory (bytes) from
// /proc/meminfo. Returns (0,0) if unavailable.
func readMemInfo() (total, available uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			total = kbFieldToBytes(line)
		case strings.HasPrefix(line, "MemAvailable:"):
			available = kbFieldToBytes(line)
		}
		if total != 0 && available != 0 {
			break
		}
	}
	return total, available
}

// kbFieldToBytes parses a /proc line like "VmRSS:   12345 kB" into bytes.
func kbFieldToBytes(line string) uint64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	kb, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return kb * 1024
}

// diskUsage returns the total and free bytes of the filesystem containing path.
func diskUsage(path string) (total, free uint64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	bsize := uint64(st.Bsize) //nolint:unconvert // Bsize is int64 on some arches
	total = st.Blocks * bsize
	free = st.Bavail * bsize // space available to an unprivileged process
	return total, free
}

// fileSize returns the size of a single file in bytes (0 if missing).
func fileSize(path string) int64 {
	if path == "" {
		return 0
	}
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return 0
	}
	return fi.Size()
}

// dirSize sums the sizes of all regular files under dir (0 if missing).
func dirSize(dir string) int64 {
	if dir == "" {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // best-effort: skip unreadable entries
		}
		if info, e := d.Info(); e == nil {
			total += info.Size()
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
