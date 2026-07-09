package calibrate

import (
	"sync"
	"testing"
	"time"
)

func atSec(sec *int64) func() time.Time {
	return func() time.Time { return time.Unix(*sec, 0) }
}

// feed pushes served challenges with the given number passed, then advances
// one window so the fold happens.
func feed(c *Calibrator, sec *int64, served, passed int) {
	for i := 0; i < served; i++ {
		c.Served()
	}
	for i := 0; i < passed; i++ {
		c.Passed()
	}
	*sec += windowSec
	c.rotate()
}

func TestHighPassRateLoosens(t *testing.T) {
	sec := int64(1_000_000)
	c := New()
	c.now = atSec(&sec)
	// 100 challenged, 96 solved: we are challenging humans.
	feed(c, &sec, 100, 96)
	if b := c.Bias(); b != step {
		t.Fatalf("bias after one FP-heavy window = %v, want %v", b, step)
	}
	// Keeps loosening but never past the cap.
	for i := 0; i < 10; i++ {
		feed(c, &sec, 100, 95)
	}
	if b := c.Bias(); b != maxBias {
		t.Fatalf("bias = %v, want cap %v", b, maxBias)
	}
}

func TestLowPassRateRestores(t *testing.T) {
	sec := int64(2_000_000)
	c := New()
	c.now = atSec(&sec)
	feed(c, &sec, 100, 95) // loosen once
	feed(c, &sec, 100, 95) // loosen twice
	if b := c.Bias(); b < 2*step-1e-9 {
		t.Fatalf("setup bias = %v", b)
	}
	// Bots arrive: 100 challenged, 10 solved — pull the slack back.
	feed(c, &sec, 100, 10)
	feed(c, &sec, 100, 10)
	if b := c.Bias(); b > 1e-9 {
		t.Fatalf("bias after bot-heavy windows = %v, want 0", b)
	}
	// Never negative — loosen-only invariant.
	feed(c, &sec, 100, 0)
	if b := c.Bias(); b != 0 {
		t.Fatalf("bias must clamp at 0, got %v", b)
	}
}

func TestSmallSamplesDriftBackNotCalibrate(t *testing.T) {
	sec := int64(3_000_000)
	c := New()
	c.now = atSec(&sec)
	feed(c, &sec, 100, 95) // bias = step
	// A quiet window with 5 challenges all passed must NOT loosen further
	// (below minSample) — it drifts back instead.
	feed(c, &sec, 5, 5)
	want := step - drift
	if b := c.Bias(); b < want-1e-9 || b > want+1e-9 {
		t.Fatalf("bias after quiet window = %v, want %v", b, want)
	}
	// Long silence decays all the way to zero.
	for i := 0; i < 20; i++ {
		feed(c, &sec, 0, 0)
	}
	if b := c.Bias(); b != 0 {
		t.Fatalf("bias after long silence = %v, want 0", b)
	}
}

func TestMidRatePassHoldsSteady(t *testing.T) {
	sec := int64(4_000_000)
	c := New()
	c.now = atSec(&sec)
	feed(c, &sec, 100, 95) // bias = step
	// 70% pass rate: ambiguous — hold, don't oscillate.
	feed(c, &sec, 100, 70)
	if b := c.Bias(); b != step {
		t.Fatalf("bias after ambiguous window = %v, want unchanged %v", b, step)
	}
}

func TestConcurrentUseIsRaceFree(t *testing.T) {
	c := New()
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				c.Served()
				if i%3 == 0 {
					c.Passed()
				}
				_ = c.Bias()
			}
		}()
	}
	wg.Wait()
	s, p, _ := c.Snapshot()
	if s <= 0 || p <= 0 {
		t.Fatalf("snapshot counters should be positive: served=%d passed=%d", s, p)
	}
}
