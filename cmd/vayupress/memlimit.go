// SPDX-License-Identifier: Apache-2.0

package main

// memlimit.go — give the Go garbage collector a memory ceiling to aim at.
//
// A soft memory limit (runtime/debug.SetMemoryLimit / GOMEMLIMIT) keeps steady
// RSS bounded and makes the runtime return memory to the OS more eagerly under
// pressure, without the hard failure mode of a fixed heap cap. On a single
// binary deployed to a VPS or container this is the difference between a GC that
// targets the box's real memory and one that overshoots toward an OOM-kill.
//
// This is part of the 2026-07 RAM reduction work (L4). It is intentionally
// conservative: it never overrides an operator's explicit GOMEMLIMIT, and on a
// bare host with no cgroup limit it changes nothing.

import (
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/logging"
)

// memLimitHeadroom leaves 10% below the detected cgroup hard limit so the GC's
// target sits under the limit and a transient overshoot during a collection
// cycle does not trip the kernel OOM-killer.
const memLimitHeadroom = 0.90

// cgroupUnlimited is the threshold above which a reported cgroup limit is treated
// as "no limit". cgroup v1 reports a near-int64-max sentinel for unlimited; any
// value ≥ 1 PiB is implausible as a real container limit and means unbounded.
const cgroupUnlimited = int64(1) << 50

// applyMemorySoftLimit sets a soft memory ceiling for the process.
//
// Precedence:
//  1. If GOMEMLIMIT is set in the environment, the Go runtime already honours it
//     verbatim — respect the operator's explicit choice and do nothing.
//  2. Otherwise, if the process runs under a cgroup memory limit (containers,
//     systemd MemoryMax=), derive a soft limit at memLimitHeadroom of that hard
//     limit. This is the common VPS/container deployment.
//  3. Otherwise leave the runtime default (no limit) — unchanged on bare hosts.
func applyMemorySoftLimit() {
	if v := strings.TrimSpace(os.Getenv("GOMEMLIMIT")); v != "" {
		logging.LogInfo("mem", "GOMEMLIMIT set in environment ("+v+") — honouring operator value")
		return
	}
	hard, ok := cgroupMemoryLimitBytes()
	if !ok {
		return
	}
	soft := int64(float64(hard) * memLimitHeadroom)
	if soft <= 0 {
		return
	}
	debug.SetMemoryLimit(soft)
	logging.LogInfo("mem", fmt.Sprintf("soft memory limit %d MiB (%.0f%% of cgroup limit %d MiB)",
		soft>>20, memLimitHeadroom*100, hard>>20))
}

// cgroupMemoryLimitBytes reports the process's cgroup memory hard limit, trying
// cgroup v2 then v1. ok is false when there is no file or no finite limit.
func cgroupMemoryLimitBytes() (int64, bool) {
	if b, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil { // cgroup v2
		if n, ok := parseCgroupLimit(string(b)); ok {
			return n, true
		}
	}
	if b, err := os.ReadFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil { // cgroup v1
		if n, ok := parseCgroupLimit(string(b)); ok {
			return n, true
		}
	}
	return 0, false
}

// parseCgroupLimit parses a cgroup memory limit file's contents. It returns
// ok=false for the empty string, the literal "max" (v2 unlimited), unparseable
// input, non-positive values, and implausibly large sentinels (v1 unlimited).
func parseCgroupLimit(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "max" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 || n >= cgroupUnlimited {
		return 0, false
	}
	return n, true
}
