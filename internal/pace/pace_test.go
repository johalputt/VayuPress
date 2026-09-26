// SPDX-License-Identifier: Apache-2.0

package pace

import (
	"context"
	"strings"
	"testing"
	"time"
)

// quiet is a host with room on every signal.
func quiet() Sources {
	return Sources{
		ReadPool:  func() (time.Duration, bool) { return 0, true },
		WritePool: func() (time.Duration, bool) { return 0, true },
		Pressure:  func(string) (float64, bool) { return 1, true },
		Load1:     func() (float64, bool) { return 0.2, true },
		CPUs:      4,
		Mem:       func() (uint64, uint64, bool) { return 100, 60, true },
		P95:       func() (int64, bool) { return 40, true },
	}
}

// clocked returns a pacer whose clock the test moves.
func clocked(src Sources) (*Pacer, *time.Time) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	p := New(src)
	p.now = func() time.Time { return now }
	return p, &now
}

func TestAQuietHostSaysGo(t *testing.T) {
	p, now := clocked(quiet())
	p.Check()
	*now = now.Add(time.Second)
	if v := p.Check(); v.Level != Go {
		t.Fatalf("a quiet host answered %v: %s", v.Level, v.Reason)
	}
}

// One seed per signal, each asserting its own reason.
func TestEachSignalSlowsTheJobAndSaysWhy(t *testing.T) {
	cases := []struct {
		name   string
		seed   func(*Sources)
		level  Level
		reason string
	}{
		{"read pool queueing", func(s *Sources) {
			var w time.Duration
			s.ReadPool = func() (time.Duration, bool) { w += 700 * time.Millisecond; return w, true }
		}, Wait, "readers are queuing for the database: 700 ms"},
		{"write pool queueing", func(s *Sources) {
			var w time.Duration
			s.WritePool = func() (time.Duration, bool) { w += 100 * time.Millisecond; return w, true }
		}, Ease, "writers are queuing for the database: 100 ms"},
		{"disk stalled", func(s *Sources) {
			s.Pressure = func(r string) (float64, bool) { return map[string]float64{"io": 45}[r], true }
		}, Wait, "disk stalled 45% of the last 10 s"},
		{"cpu stalled", func(s *Sources) {
			s.Pressure = func(r string) (float64, bool) { return map[string]float64{"cpu": 35}[r], true }
		}, Ease, "CPU stalled 35% of the last 10 s"},
		{"memory stalled", func(s *Sources) {
			s.Pressure = func(r string) (float64, bool) { return map[string]float64{"memory": 25}[r], true }
		}, Wait, "memory stalled 25% of the last 10 s"},
		{"load where there is no PSI", func(s *Sources) {
			s.Pressure = func(string) (float64, bool) { return 0, false }
			s.Load1 = func() (float64, bool) { return 9, true }
		}, Wait, "load 9.0 on 4 CPUs"},
		{"memory running out", func(s *Sources) {
			s.Mem = func() (uint64, uint64, bool) { return 100, 8, true }
		}, Ease, "8% of memory available"},
		{"slow pages", func(s *Sources) {
			s.P95 = func() (int64, bool) { return 2500, true }
		}, Wait, "pages taking 2500 ms at the 95th percentile"},
	}
	for _, c := range cases {
		src := quiet()
		c.seed(&src)
		p, now := clocked(src)
		p.Check()
		*now = now.Add(time.Second)
		v := p.Check()
		if v.Level != c.level || !strings.Contains(v.Reason, c.reason) {
			t.Errorf("%s: got %v %q, want %v with %q", c.name, v.Level, v.Reason, c.level, c.reason)
		}
	}
}

// Load stands in for CPU pressure only where the kernel has none: with PSI
// present, a high load (I/O wait counts in it) is not a second opinion.
func TestLoadIsReadOnlyWithoutCPUPressure(t *testing.T) {
	src := quiet()
	src.Load1 = func() (float64, bool) { return 9, true }
	p, _ := clocked(src)
	if v := p.Check(); v.Level != Go {
		t.Errorf("with CPU pressure available, load still decided: %v %q", v.Level, v.Reason)
	}
}

// A reading that cannot be taken is left out, never read as idle or as busy.
func TestUnavailableReadingsAreLeftOut(t *testing.T) {
	p, now := clocked(Sources{
		ReadPool: func() (time.Duration, bool) { return time.Hour, false },
		Pressure: func(string) (float64, bool) { return 99, false },
		Mem:      func() (uint64, uint64, bool) { return 100, 0, false },
		P95:      func() (int64, bool) { return 9999, false },
	})
	p.Check()
	*now = now.Add(time.Second)
	if v := p.Check(); v.Level != Go {
		t.Errorf("readings marked unavailable decided the answer: %v %q", v.Level, v.Reason)
	}
}

// fakeJob is a job whose pacer answers from a script and whose sleeps move a
// fake clock instead of waiting.
func fakeJob(cfg JobConfig, levels ...Level) (*Job, *time.Time) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	i := 0
	p := New(Sources{P95: func() (int64, bool) {
		l := levels[min(i, len(levels)-1)]
		i++
		return map[Level]int64{Go: 10, Ease: 900, Wait: 5000}[l], true
	}})
	j := p.NewJob(cfg)
	j.now = func() time.Time { return now }
	j.sleep = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() }
	return j, &now
}

func TestBatchesGrowByAStepAndHalveOnEase(t *testing.T) {
	j, _ := fakeJob(JobConfig{Min: 10, Max: 45, Step: 10}, Go, Go, Go, Go, Ease, Ease, Ease)
	var got []int
	for i := 0; i < 7; i++ {
		n, err := j.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	want := []int{20, 30, 40, 45, 22, 11, 10}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("batches %v, want %v: grow by the step to the cap, halve on ease, never below the minimum", got, want)
		}
	}
}

// A job with a floor finishes, slowly, however long the host stays busy; one
// without it waits.
func TestTheFloorRunsTheSmallestBatchAfterTenMinutes(t *testing.T) {
	j, now := fakeJob(JobConfig{Min: 5, Max: 50, Step: 5, Floor: true}, Wait)
	start := *now
	// A deadline in the job's own time, so a floor that never comes fails
	// here instead of hanging the suite.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sleep := j.sleep
	j.sleep = func(c context.Context, d time.Duration) error {
		if now.Sub(start) > 2*time.Hour {
			cancel()
		}
		return sleep(c, d)
	}
	n, err := j.Next(ctx)
	if err != nil {
		t.Fatalf("two hours of waiting and no floor batch: %v", err)
	}
	if waited := now.Sub(start); n != 5 || waited < FloorAfter || waited > FloorAfter+WaitPoll {
		t.Fatalf("the floor ran a batch of %d after %s; want 5 after %s", n, waited, FloorAfter)
	}
	if s := j.Status(); !strings.Contains(s.Verdict.Reason, "at its slowest after 10m0s of waiting") {
		t.Errorf("the floor's reason does not say so: %q", s.Verdict.Reason)
	}
	start = *now
	if _, err := j.Next(ctx); err != nil {
		t.Fatal(err)
	}
	if waited := now.Sub(start); waited < FloorEvery-WaitPoll || waited > FloorEvery+WaitPoll {
		t.Errorf("the next floor batch came after %s, want about %s", waited, FloorEvery)
	}

	nofloor, _ := fakeJob(JobConfig{Min: 5, Max: 50, Step: 5}, Wait)
	nctx, ncancel := context.WithCancel(context.Background())
	defer ncancel()
	calls := 0
	nofloor.sleep = func(c context.Context, d time.Duration) error {
		if calls++; calls > int((FloorAfter+time.Hour)/WaitPoll) {
			ncancel()
		}
		return c.Err()
	}
	if _, err := nofloor.Next(nctx); err == nil {
		t.Error("a job without a floor ran a batch while the host said wait")
	}
}
