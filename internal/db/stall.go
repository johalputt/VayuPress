// SPDX-License-Identifier: Apache-2.0

package db

// stall.go — noticing that a connection pool is jammed, and saying so.
//
// Two pools are watched the same way: the single write connection, and the
// public read pool. The read pool was added after the 2026-09-26 outage on
// johal.in: crawler renders held every read connection, and the console waited
// out its 30-second deadline behind them. Nothing measured that queue either,
// and the cause was found only by a goroutine dump taken by hand during the
// fault. This file now takes that dump itself.
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
// A second counts as stalled on either of two signals, because neither sees
// every stall:
//
//   - WaitDuration grew by nearly a full second or more. Callers spent the
//     whole interval queued between them. It is a floor rather than a
//     threshold on depth: waiting for a crowd to form would miss the case
//     where the crowd is what the stall creates.
//   - Every connection stayed in use at every tenth-of-a-second look, and a
//     caller queued while they were. This is the one caller stuck behind a
//     full pool, which WaitDuration cannot see: database/sql adds a wait to
//     it only when the wait ENDS, so a caller queued for a minute counts as
//     nothing for that minute. A real read pool with every connection held
//     and one caller queued read WaitCount 1, WaitDuration 0 after ten
//     seconds (stall_test.go).
//
// A full pool with nobody queued is busy, not stalled.
//
// # What this does NOT claim
//
// DBStats has no gauge for "goroutines waiting right now", so this file does not
// report one. It reports what it can actually measure: how long the contention
// lasted, and the total time callers spent queued during it. A panel row that
// invented a live waiter count would be the same defect as a posture verdict for
// a control nobody verified.

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

// Stall watchdog tuning.
const (
	// stallSample is how often DBStats is read. Cheap: it takes the pool's
	// mutex briefly and copies a struct.
	stallSample = time.Second

	// stallTick is how often the pool is looked at within a sample, to tell a
	// pool that stayed full from one that was full now and then.
	stallTick  = 100 * time.Millisecond
	stallTicks = int(stallSample / stallTick)

	// stallRatio is the fraction of a sample interval that must be spent
	// waiting for the interval to count as stalled. Below 1.0 because timer
	// jitter means a fully-blocked second measures slightly under a second.
	stallRatio = 0.8

	// stallHistory is how many recent stalls are kept for the panel.
	stallHistory = 20
)

// StallEvent is one period during which a pool was continuously contended.
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
	// name prefixes the snapshot files, so each pool's are told apart and
	// pruned apart.
	name string
	// pool is read at every sample rather than once, because the pools are
	// opened after this package's variables are initialised.
	pool func() *sql.DB

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

var (
	writeStall = &stallWatch{name: "writestall", pool: func() *sql.DB { return DB }, dumpAfter: 5 * time.Second}

	// readStall watches RDB itself, not Reader(): where there is no read pool,
	// Reader() is the writer, which writeStall already watches.
	//
	// The read pool holds many connections, so the same floor means more here:
	// a caller queued for a whole second is a second in which every read
	// connection was taken.
	readStall = &stallWatch{name: "readstall", pool: func() *sql.DB { return RDB }, dumpAfter: 5 * time.Second}
)

// StartStallWatch begins sampling the write pool and the read pool. Safe to
// call more than once; only the first call starts each sampler.
func StartStallWatch(stop <-chan struct{}) {
	writeStall.start(stop)
	readStall.start(stop)
}

func (s *stallWatch) start(stop <-chan struct{}) {
	s.mu.Lock()
	if s.started || s.pool() == nil {
		s.mu.Unlock()
		return
	}
	s.started = true
	if s.dumper == nil {
		s.dumper = func() string { return goroutineDump(s.name) }
	}
	s.mu.Unlock()

	go func() {
		t := time.NewTicker(stallTick)
		defer t.Stop()
		var prevWait time.Duration
		var prevCount int64
		var h heldPool
		first := true
		ticks := 0
		for {
			select {
			case <-stop:
				return
			case now := <-t.C:
				p := s.pool()
				if p == nil {
					continue
				}
				st := p.Stats()
				if first {
					prevWait, prevCount, first = st.WaitDuration, st.WaitCount, false
					h = newHeldPool(st.WaitCount)
					continue
				}
				h.look(st.InUse, st.MaxOpenConnections, st.WaitCount)
				if ticks++; ticks < stallTicks {
					continue
				}
				ticks = 0
				held := h.second(st.WaitCount)
				dWait := st.WaitDuration - prevWait
				dCount := st.WaitCount - prevCount
				prevWait, prevCount = st.WaitDuration, st.WaitCount
				s.observe(now, dWait, dCount, held)
			}
		}
	}()
}

// heldPool follows the looks within one sample: whether every connection was
// in use at every look, and whether a caller queued while they were.
type heldPool struct {
	allFull  bool  // every look this second found every connection in use
	fullFrom int64 // WaitCount at the look BEFORE the pool was seen full; -1 while not full
	last     int64 // WaitCount at the previous look
}

func newHeldPool(waitCount int64) heldPool {
	return heldPool{allFull: true, fullFrom: -1, last: waitCount}
}

func (h *heldPool) look(inUse, maxOpen int, waitCount int64) {
	switch full := maxOpen > 0 && inUse >= maxOpen; {
	case !full:
		h.allFull, h.fullFrom = false, -1
	case h.fullFrom < 0:
		// From the previous look, not this one: the caller who queued as the
		// last connection went is the one this signal exists for.
		h.fullFrom = h.last
	}
	h.last = waitCount
}

// second closes a sample: held if the pool stayed full throughout and a caller
// has queued since it filled. The next sample starts afresh.
func (h *heldPool) second(waitCount int64) bool {
	held := h.allFull && h.fullFrom >= 0 && waitCount > h.fullFrom
	h.allFull = true
	return held
}

// observe folds one sample into the current event. held says the pool stayed
// full all second while a caller queued. Separated from the ticker so a test
// can drive it with synthetic samples instead of real contention.
func (s *stallWatch) observe(now time.Time, dWait time.Duration, dCount int64, held bool) {
	jammed := held || dWait >= time.Duration(float64(stallSample)*stallRatio)

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

// StallState is the summary the panel and /health/db report for one pool.
type StallState struct {
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

// WriteStall reports the write pool's contention, and ReadStall the read
// pool's. Neither touches the database or takes a connection, which is the
// point: each has to answer during the incident it describes.
func WriteStall() StallState { return writeStall.state() }

// ReadStall: see WriteStall.
func ReadStall() StallState { return readStall.state() }

func (s *stallWatch) state() StallState {
	out := StallState{}
	if p := s.pool(); p != nil {
		st := p.Stats()
		out.WaitCount, out.WaitDuration = st.WaitCount, st.WaitDuration
		out.InUse, out.MaxOpen = st.InUse, st.MaxOpenConnections
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out.Watching = s.started
	out.Total, out.Longest = s.total, s.longest
	if s.current != nil {
		c := *s.current
		out.Stalled, out.Current = true, &c
	}
	if n := len(s.recent); n > 0 {
		out.Recent = append([]StallEvent(nil), s.recent...)
	}
	return out
}

// goroutineDump captures every goroutine's stack to the state directory and
// returns the path, or "" if it could not be written.
//
// This is the difference between an outage that has to be reproduced to be
// understood and one that explains itself. It is taken DURING the stall, when
// the stacks still show what is holding the connection; five minutes later
// there is nothing to look at, which is precisely why this class of fault
// survived so long.
//
// runtime.Stack stops silently at the end of its buffer, so the buffer grows
// until the dump fits. A read-pool stall is a crowd of request goroutines, and
// a snapshot cut off at the first megabyte can end before the one goroutine
// that names the cause. The ceiling keeps a pathological count from turning
// the diagnostic into an allocation the host cannot afford.
func goroutineDump(name string) string {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) || len(buf) >= stallDumpMax {
			return persistStallDump(name, buf[:n])
		}
		buf = make([]byte, 2*len(buf))
	}
}

// stallDumpMax is the largest snapshot taken: tens of thousands of goroutines.
const stallDumpMax = 64 << 20

// stallDumpKeep is how many snapshots are retained for each pool. Enough to compare a repeat
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

// persistStallDump writes one snapshot and prunes older ones of the same name.
func persistStallDump(name string, b []byte) string {
	dir := stallDumpDir()
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.txt", name, time.Now().UTC().Format("20060102T150405Z")))
	// 0600: stack traces name internal functions and file paths. Useful to the
	// operator, nobody else's business.
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return ""
	}
	pruneStallDumps(dir, name)
	return path
}

// pruneStallDumps keeps the newest stallDumpKeep snapshots of one pool. Per
// pool, so a recurring read stall cannot prune away the only snapshot of a
// write stall.
func pruneStallDumps(dir, name string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".txt" && strings.HasPrefix(e.Name(), name+"-") {
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
