// SPDX-License-Identifier: Apache-2.0

// Package hoststat reads the host's resource figures from /proc: memory, the
// kernel's pressure-stall information and the load average. The console's
// Storage page and the pacer (internal/pace) both read them, from here.
//
// Every reading reports whether it could be taken. Off Linux, or on a kernel
// without PSI, the answer is "not available", never a zero that reads as idle.
package hoststat

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
)

// procRoot is where the readers look; a test points it at fixture files.
var procRoot = "/proc"

// MemInfo returns total and available memory in bytes.
func MemInfo() (total, available uint64, ok bool) {
	f, err := os.Open(procRoot + "/meminfo")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	total, available = parseMemInfo(f)
	return total, available, total > 0
}

func parseMemInfo(r io.Reader) (total, available uint64) {
	sc := bufio.NewScanner(r)
	for sc.Scan() && (total == 0 || available == 0) {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			total = kbField(line)
		case strings.HasPrefix(line, "MemAvailable:"):
			available = kbField(line)
		}
	}
	return total, available
}

// ProcRSS returns this process's resident set size in bytes, or 0.
func ProcRSS() uint64 {
	f, err := os.Open(procRoot + "/self/status")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := sc.Text(); strings.HasPrefix(line, "VmRSS:") {
			return kbField(line)
		}
	}
	return 0
}

// kbField parses a /proc line like "VmRSS:   12345 kB" into bytes.
func kbField(line string) uint64 {
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

// Pressure returns the "some avg10" figure of /proc/pressure/<resource> (cpu,
// io or memory): the percentage of the last ten seconds in which at least one
// task was stalled waiting for that resource.
func Pressure(resource string) (float64, bool) {
	b, err := os.ReadFile(procRoot + "/pressure/" + resource)
	if err != nil {
		return 0, false
	}
	return parsePressure(string(b))
}

func parsePressure(s string) (float64, bool) {
	for _, line := range strings.Split(s, "\n") {
		if !strings.HasPrefix(line, "some ") {
			continue
		}
		for _, f := range strings.Fields(line) {
			if v, found := strings.CutPrefix(f, "avg10="); found {
				n, err := strconv.ParseFloat(v, 64)
				return n, err == nil
			}
		}
	}
	return 0, false
}

// Load1 returns the one-minute load average.
func Load1() (float64, bool) {
	b, err := os.ReadFile(procRoot + "/loadavg")
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, false
	}
	n, err := strconv.ParseFloat(f[0], 64)
	return n, err == nil
}
