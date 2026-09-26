// SPDX-License-Identifier: Apache-2.0

// Package pace decides how fast heavy work may run, from what the host is
// doing now.
//
// Refreshing every page, a backup, the restore drill and the pre-update
// snapshot each read or write the whole database. Run at full speed on a
// large site they took the site down (johal.in, 2026-09-26: a cleared cache
// on a 17 GB database). So each asks the pacer before every batch, and the
// pacer answers from live measurements, never from a configured guess:
//
//   - Go: run the batch, and the next may be larger;
//   - Ease: run a smaller batch, then pause;
//   - Wait: run nothing now; ask again shortly.
//
// Every answer carries its reason ("disk stalled 34% of the last 10 s"), which
// the console shows beside the job. A job that only ever waits gets nowhere,
// so a job that must finish (a backup) has a floor: after FloorAfter of
// waiting it runs its smallest batch once per FloorEvery.
package pace

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Level is the pacer's answer, ordered from permissive to strict.
type Level int

const (
	Go Level = iota
	Ease
	Wait
)

// String is the level as the console shows it, at the start of a line.
func (l Level) String() string {
	switch l {
	case Ease:
		return "Easing"
	case Wait:
		return "Waiting"
	}
	return "Running"
}

// Verdict is an answer and why.
type Verdict struct {
	Level  Level
	Reason string
}

// Sources are the readings the pacer decides from. Each reports whether it
// could be taken; a reading that cannot be taken is left out, never read as
// idle.
type Sources struct {
	// ReadPool and WritePool return the cumulative time callers have spent
	// queued for a connection (database/sql's WaitDuration).
	ReadPool, WritePool func() (time.Duration, bool)
	// Pressure returns the kernel's "some avg10" percentage for cpu, io or
	// memory (hoststat.Pressure).
	Pressure func(resource string) (float64, bool)
	// Load1 and CPUs stand in for cpu pressure where the kernel has no PSI.
	Load1 func() (float64, bool)
	CPUs  int
	// Mem returns total and available memory in bytes.
	Mem func() (total, available uint64, ok bool)
	// P95 returns the 95th-percentile response time of the last minutes, ms.
	P95 func() (int64, bool)
}

// Thresholds, as [ease, wait]. Each is where that signal means visitors or
// the console are starting to pay for background work, and then paying
// clearly.
var (
	// Seconds callers spent queued per second of wall clock. A pool that
	// queues at all is full; queueing half of every second is the outage.
	poolQueued = [2]float64{0.05, 0.5}
	// PSI "some avg10", percent.
	ioStalled  = [2]float64{15, 40}
	cpuStalled = [2]float64{30, 60}
	memStalled = [2]float64{5, 20}
	// Runnable tasks per CPU, only where there is no cpu PSI.
	loadPerCPU = [2]float64{1, 2}
	// Share of memory still available, percent: easing below the first,
	// waiting below the second.
	memAvailable = [2]float64{10, 5}
	// Response time at the 95th percentile, ms.
	p95ms = [2]float64{800, 2000}
)

// Pacer answers from Sources. Safe for concurrent use.
type Pacer struct {
	src Sources
	now func() time.Time

	mu        sync.Mutex
	lastAt    time.Time
	lastRead  time.Duration
	lastWrite time.Duration
}

// New returns a pacer reading src.
func New(src Sources) *Pacer { return &Pacer{src: src, now: time.Now} }

type finding struct {
	level  Level
	reason string
}

// grade places v against [ease, wait] thresholds, rising.
func grade(v float64, t [2]float64) Level {
	switch {
	case v >= t[1]:
		return Wait
	case v >= t[0]:
		return Ease
	}
	return Go
}

// Check reads every source and answers with the strictest level any of them
// calls for, and the reasons at that level.
func (p *Pacer) Check() Verdict {
	var fs []finding
	add := func(l Level, format string, a ...any) {
		if l > Go {
			fs = append(fs, finding{l, fmt.Sprintf(format, a...)})
		}
	}

	p.mu.Lock()
	now := p.now()
	// On the first check lastAt is zero, so the rate is taken over centuries
	// and reads as none: the first answer never rests on a pool rate.
	dt := now.Sub(p.lastAt).Seconds()
	p.lastAt = now
	pool := func(read func() (time.Duration, bool), last *time.Duration, who string) {
		if read == nil {
			return
		}
		w, ok := read()
		if !ok {
			return
		}
		prev := *last
		*last = w
		if dt <= 0 {
			return
		}
		per := (w - prev).Seconds() / dt
		add(grade(per, poolQueued), "%s are queuing for the database: %.0f ms of waiting per second", who, per*1000)
	}
	pool(p.src.ReadPool, &p.lastRead, "readers")
	pool(p.src.WritePool, &p.lastWrite, "writers")
	p.mu.Unlock()

	cpuPSI := false
	if p.src.Pressure != nil {
		if v, ok := p.src.Pressure("io"); ok {
			add(grade(v, ioStalled), "disk stalled %.0f%% of the last 10 s", v)
		}
		if v, ok := p.src.Pressure("cpu"); ok {
			cpuPSI = true
			add(grade(v, cpuStalled), "CPU stalled %.0f%% of the last 10 s", v)
		}
		if v, ok := p.src.Pressure("memory"); ok {
			add(grade(v, memStalled), "memory stalled %.0f%% of the last 10 s", v)
		}
	}
	if !cpuPSI && p.src.Load1 != nil && p.src.CPUs > 0 {
		if v, ok := p.src.Load1(); ok {
			per := v / float64(p.src.CPUs)
			add(grade(per, loadPerCPU), "load %.1f on %d CPUs", v, p.src.CPUs)
		}
	}
	if p.src.Mem != nil {
		if total, avail, ok := p.src.Mem(); ok && total > 0 {
			pct := 100 * float64(avail) / float64(total)
			l := Go
			switch {
			case pct < memAvailable[1]:
				l = Wait
			case pct < memAvailable[0]:
				l = Ease
			}
			add(l, "%.0f%% of memory available", pct)
		}
	}
	if p.src.P95 != nil {
		if ms, ok := p.src.P95(); ok {
			add(grade(float64(ms), p95ms), "pages taking %d ms at the 95th percentile", ms)
		}
	}

	v := Verdict{Level: Go, Reason: "the host has room"}
	var reasons []string
	for _, f := range fs {
		if f.level > v.Level {
			v.Level, reasons = f.level, nil
		}
		if f.level == v.Level {
			reasons = append(reasons, f.reason)
		}
	}
	if len(reasons) > 0 {
		v.Reason = strings.Join(reasons, "; ")
	}
	return v
}

// Pauses and the floor.
const (
	// EasePause follows an eased batch.
	EasePause = time.Second
	// WaitPoll is how often a waiting job asks again.
	WaitPoll = 2 * time.Second
	// FloorAfter is how long a job with a floor waits before it runs its
	// smallest batch regardless, and FloorEvery how often it then does.
	FloorAfter = 10 * time.Minute
	FloorEvery = 30 * time.Second
)

// JobConfig sizes a job's batches: it starts at Min, grows by Step while the
// host has room (additive increase), halves on Ease (multiplicative decrease),
// and never leaves [Min, Max]. Floor makes it run a Min batch now and then
// however long it has been told to wait.
type JobConfig struct {
	Min, Max, Step int
	Floor          bool
}

// Job paces one piece of heavy work.
type Job struct {
	p     *Pacer
	cfg   JobConfig
	sleep func(context.Context, time.Duration) error
	now   func() time.Time

	mu           sync.Mutex
	size         int
	last         Verdict
	waitingSince time.Time
	lastFloor    time.Time
}

// NewJob starts a job at its smallest batch.
func (p *Pacer) NewJob(cfg JobConfig) *Job {
	if cfg.Min < 1 {
		cfg.Min = 1
	}
	if cfg.Max < cfg.Min {
		cfg.Max = cfg.Min
	}
	if cfg.Step < 1 {
		cfg.Step = 1
	}
	return &Job{p: p, cfg: cfg, sleep: sleepCtx, now: time.Now, size: cfg.Min}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Next blocks until the next batch may run and returns its size. It returns
// an error only when ctx ends.
func (j *Job) Next(ctx context.Context) (int, error) {
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		v := j.p.Check()
		j.mu.Lock()
		j.last = v
		switch v.Level {
		case Go:
			j.waitingSince = time.Time{}
			j.size = min(j.cfg.Max, j.size+j.cfg.Step)
			n := j.size
			j.mu.Unlock()
			return n, nil
		case Ease:
			j.waitingSince = time.Time{}
			j.size = max(j.cfg.Min, j.size/2)
			n := j.size
			j.mu.Unlock()
			if err := j.sleep(ctx, EasePause); err != nil {
				return 0, err
			}
			return n, nil
		}
		now := j.now()
		if j.waitingSince.IsZero() {
			j.waitingSince = now
		}
		if j.cfg.Floor && now.Sub(j.waitingSince) >= FloorAfter && now.Sub(j.lastFloor) >= FloorEvery {
			j.lastFloor = now
			j.size = j.cfg.Min
			j.last.Reason = fmt.Sprintf("at its slowest after %s of waiting: %s", now.Sub(j.waitingSince).Round(time.Minute), v.Reason)
			j.mu.Unlock()
			return j.cfg.Min, nil
		}
		j.mu.Unlock()
		if err := j.sleep(ctx, WaitPoll); err != nil {
			return 0, err
		}
	}
}

// Status is what the console shows beside a running job.
type Status struct {
	Batch        int
	Verdict      Verdict
	WaitingSince time.Time // zero unless the job is waiting
}

// Status reports the job's current pace.
func (j *Job) Status() Status {
	j.mu.Lock()
	defer j.mu.Unlock()
	return Status{Batch: j.size, Verdict: j.last, WaitingSince: j.waitingSince}
}
