// SPDX-License-Identifier: Apache-2.0

package db

// stall.go — noticing that the single write connection is jammed, and saying so.
//
// # Why this exists
//
// SQLite has one writer, so this pool has one connection. That is correct and
// not negotiable. The consequence is that anything holding it for a while makes
// every other would-be writer queue, and until now NOTHING in this product could
// see that queue. An operator watching their site 502 had a process that was
// running, a database that was fine, no restart, no OOM kill and nothing in the
// log — and the only honest answer available was a hypothesis.
//
// The queue was always measurable. database/sql has counted it since Go 1.11:
//
//	DBStats.WaitCount    — how many times a caller had to wait for a connection
//	DBStats.WaitDuration — how long callers have spent waiting, in total
//
// Both are cumulative and both are lock-free to read. Sampling them on a tick
// turns "the site felt slow at some point" into "the write connection was
// continuously contended for 42 seconds starting 14:03:11, and callers spent 6
// minutes queued for it".
//
// # What counts as a stall
//
// Between two samples one second apart, WaitDuration can grow by at most one
// second per waiting goroutine. If it grew by nearly a full second or more, then
// something was queued for essentially the whole interval — the connection was
// not merely busy, it was unavailable. That is the signal, and it is deliberately
// a floor rather than a threshold on depth: one caller blocked for the entire
// window is already the failure, and waiting for a crowd to form would miss the
// case where the crowd is what the stall creates.
//
// # What this does NOT claim
//
// DBStats has no gauge for "goroutines waiting right now", so this file does not
// report one. It reports what it can actually measure: how long the contention
// lasted, and the total time callers spent queued during it. A panel row that
// invented a live waiter count would be the same defect as a posture verdict for
// a control nobody verified.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

// Stall watchdog tuning.
const (
	// stallSample is how often DBStats is read. Cheap: it takes the pool's
	// mutex briefly and copies a struct.
	stallSample = time.Second

	// stallRatio is the fraction of a sample interval that must be spent
	// waiting for the interval to count as stalled. Below 1.0 because timer
	// jitter means a fully-blocked second measures slightly under a second.
	stallRatio = 0.8

	// stallHistory is how many recent stalls are kept for the panel.
	stallHistory = 20
)

// StallEvent is one period during which the write connection was continuously
// contended.
type StallEvent struct {
	Start    time.Time     // when contention was first observed
	Duration time.Duration // how long it lasted (still growing if Ongoing)
	Ongoing  bool          // true while it is still happening
	// Blocked is the total time callers spent queued during the event, summed
	// across callers. It exceeds Duration whenever more than one caller was
	// waiting, which is exactly what makes it worth reporting: 42 seconds of
	// contention costing 6 minutes of queued callers is a different incident
	// from 42 seconds costing 45.
	Blocked time.Duration
	// Waits is how many callers had to queue during the event.
	Waits int64
	// Dump is the path to a goroutine snapshot taken during the event, or "".
	Dump string
}

type stallWatch struct {
	mu       sync.Mutex
	current  *StallEvent
	recent   []StallEvent // newest last, capped at stallHistory
	total    int64
	longest  time.Duration
	lastSeen time.Time
	started  bool

	// dumper is called once per event when it crosses dumpAfter. Swappable so
	// the test can observe it without writing files.
	dumper    func() string
	dumpAfter time.Duration
}

var writeStall = &stallWatch{dumpAfter: 5 * time.Second}

// StartStallWatch begins sampling the write pool. Safe to call more than once;
// only the first call starts a sampler.
func StartStallWatch(stop <-chan struct{}) {
	writeStall.mu.Lock()
	if writeStall.started || DB == nil {
		writeStall.mu.Unlock()
		return
	}
	writeStall.started = true
	if writeStall.dumper == nil {
		writeStall.dumper = writeGoroutineDump
	}
	writeStall.mu.Unlock()

	go func() {
		t := time.NewTicker(stallSample)
		defer t.Stop()
		var prevWait time.Duration
		var prevCount int64
		first := true
		for {
			select {
			case <-stop:
				return
			case now := <-t.C:
				if DB == nil {
					continue
				}
				st := DB.Stats()
				if first {
					prevWait, prevCount, first = st.WaitDuration, st.WaitCount, false
					continue
				}
				dWait := st.WaitDuration - prevWait
				dCount := st.WaitCount - prevCount
				prevWait, prevCount = st.WaitDuration, st.WaitCount
				writeStall.observe(now, dWait, dCount)
			}
		}
	}()
}

// observe folds one sample into the current event. Separated from the ticker so
// a test can drive it with synthetic samples instead of real contention.
func (s *stallWatch) observe(now time.Time, dWait time.Duration, dCount int64) {
	jammed := dWait >= time.Duration(float64(stallSample)*stallRatio)

	s.mu.Lock()
	defer s.mu.Unlock()

	if jammed {
		if s.current == nil {
			// Back-date to the start of the interval that was blocked: the
			// contention was already happening throughout it.
			s.current = &StallEvent{Start: now.Add(-stallSample), Ongoing: true}
			s.total++
		}
		s.current.Duration = now.Sub(s.current.Start)
		s.current.Blocked += dWait
		s.current.Waits += dCount
		if s.current.Dump == "" && s.current.Duration >= s.dumpAfter && s.dumper != nil {
			// Captured while it is still stuck, which is the only moment the
			// stack traces name the culprit. Afterwards there is nothing to see.
			s.current.Dump = s.dumper()
		}
		s.lastSeen = now
		return
	}

	if s.current != nil {
		s.current.Ongoing = false
		if s.current.Duration > s.longest {
			s.longest = s.current.Duration
		}
		s.recent = append(s.recent, *s.current)
		if len(s.recent) > stallHistory {
			s.recent = s.recent[len(s.recent)-stallHistory:]
		}
		s.current = nil
	}
}

// WriteStallState is the summary the panel and /health/db report.
type WriteStallState struct {
	Watching bool
	Stalled  bool          // a stall is happening right now
	Current  *StallEvent   // non-nil while Stalled
	Recent   []StallEvent  // newest last
	Total    int64         // stalls observed since boot
	Longest  time.Duration // the worst one since boot
	// WaitCount and WaitDuration are the raw cumulative counters, so a reader
	// can see the totals without inferring them from the event list.
	WaitCount    int64
	WaitDuration time.Duration
	InUse        int
	MaxOpen      int
}

// WriteStall reports the write pool's contention. It never touches the database
// and never takes a connection, which is the point: it has to answer during the
// incident it describes.
func WriteStall() WriteStallState {
	out := WriteStallState{}
	if DB != nil {
		st := DB.Stats()
		out.WaitCount, out.WaitDuration = st.WaitCount, st.WaitDuration
		out.InUse, out.MaxOpen = st.InUse, st.MaxOpenConnections
	}
	writeStall.mu.Lock()
	defer writeStall.mu.Unlock()
	out.Watching = writeStall.started
	out.Total, out.Longest = writeStall.total, writeStall.longest
	if writeStall.current != nil {
		c := *writeStall.current
		out.Stalled, out.Current = true, &c
	}
	if n := len(writeStall.recent); n > 0 {
		out.Recent = append([]StallEvent(nil), writeStall.recent...)
	}
	return out
}

// writeGoroutineDump captures every goroutine's stack to the cache directory and
// returns the path, or "" if it could not be written.
//
// This is the difference between an outage that has to be reproduced to be
// understood and one that explains itself. It is taken DURING the stall, when
// the stacks still show what is holding the connection; five minutes later
// there is nothing to look at, which is precisely why this class of fault
// survived so long.
func writeGoroutineDump() string {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	return persistStallDump(buf[:n])
}

// stallDumpKeep is how many snapshots are retained. Enough to compare a repeat
// occurrence against the first, few enough that a recurring stall cannot fill
// the disk — which would turn a diagnostic into a second outage.
const stallDumpKeep = 3

// stallDumpDir is where snapshots live: beside the database, in the state
// directory, NOT in the cache directory.
//
// The audit finding that moved them. nginx roots the cache directory for the
// ACME challenge — `location ^~ /.well-known/acme-challenge/ { root CACHE_DIR; }`
// appears in every vhost this product writes. Today that location is narrow
// enough that nothing else under the cache directory is reachable, so the
// original placement was not exploitable. It was one edit away from being so, in
// a shell script this package cannot see, and the thing it would publish is a
// goroutine dump full of internal paths and function names.
//
// A diagnostic's safety must not depend on the contents of an unrelated file.
// The state directory is served by nothing.
func stallDumpDir() string {
	if config.Cfg.DBPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(config.Cfg.DBPath), "stalls")
}

// persistStallDump writes one snapshot and prunes older ones.
func persistStallDump(b []byte) string {
	dir := stallDumpDir()
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	name := filepath.Join(dir, fmt.Sprintf("writestall-%s.txt", time.Now().UTC().Format("20060102T150405Z")))
	// 0600: stack traces name internal functions and file paths. Useful to the
	// operator, nobody else's business.
	if err := os.WriteFile(name, b, 0o600); err != nil {
		return ""
	}
	pruneStallDumps(dir)
	return name
}

func pruneStallDumps(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".txt" {
			names = append(names, e.Name())
		}
	}
	// The filename carries a sortable UTC timestamp, so lexical order is
	// chronological order.
	sort.Strings(names)
	for len(names) > stallDumpKeep {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}
