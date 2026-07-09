package prefilter

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// fixedNow pins the clock so tests control window rotation explicitly.
func fixedNow(sec *int64) func() time.Time {
	return func() time.Time { return time.Unix(*sec, 0) }
}

func TestObservationOnlyWithoutPressure(t *testing.T) {
	p := New()
	// Hammer one IP way past any budget with pressure=false: never shed.
	for i := 0; i < 10000; i++ {
		if p.Check("203.0.113.7", false) {
			t.Fatal("must never shed without pressure")
		}
	}
	if p.Shed() != 0 {
		t.Fatalf("shed counter = %d, want 0", p.Shed())
	}
}

func TestLightClientNeverShedUnderPressure(t *testing.T) {
	p := New()
	// A real reader's worth of traffic (well under the 60/window budget),
	// under full pressure: shed probability must be exactly zero.
	for i := 0; i < 50; i++ {
		if p.Check("198.51.100.9", true) {
			t.Fatalf("light client shed at request %d", i)
		}
	}
}

func TestHeavyHitterShedsUnderPressure(t *testing.T) {
	p := New()
	// Warm the sketch far past budget without pressure.
	for i := 0; i < 5000; i++ {
		p.Check("203.0.113.13", false)
	}
	// Under pressure a source at ~85x budget should shed the vast majority.
	shed := 0
	for i := 0; i < 1000; i++ {
		if p.Check("203.0.113.13", true) {
			shed++
		}
	}
	if shed < 800 {
		t.Fatalf("heavy hitter shed only %d/1000; want >= 800", shed)
	}
	// But never 100% — the maxShed cap must let a trickle through so the
	// downstream classifier still sees the traffic.
	if shed == 1000 {
		t.Fatal("shedding must be capped below 100%")
	}
}

func TestSubnetAggregationCatchesSpreadFlood(t *testing.T) {
	p := New()
	// 250 distinct IPs in ONE /24, each individually under the per-IP budget
	// (40 requests < 60), but 10,000 requests as a group — far past the subnet
	// budget of 60*8=480.
	for host := 1; host <= 250; host++ {
		ip := fmt.Sprintf("192.0.2.%d", host)
		for i := 0; i < 40; i++ {
			p.Check(ip, false)
		}
	}
	shed := 0
	for i := 0; i < 1000; i++ {
		if p.Check("192.0.2.99", true) {
			shed++
		}
	}
	if shed < 700 {
		t.Fatalf("spread /24 flood shed only %d/1000; want >= 700", shed)
	}
	// A light client in a DIFFERENT subnet is untouched at the same moment.
	for i := 0; i < 20; i++ {
		if p.Check("198.51.100.20", true) {
			t.Fatal("client outside the hot subnet must not be shed")
		}
	}
}

func TestWindowRotationDecaysEstimates(t *testing.T) {
	sec := int64(1000)
	p := New()
	p.now = fixedNow(&sec)
	for i := 0; i < 5000; i++ {
		p.Check("203.0.113.50", false)
	}
	// Two full windows later both sketch epochs have rotated past the burst.
	sec += 2 * windowSec
	shed := 0
	for i := 0; i < 200; i++ {
		if p.Check("203.0.113.50", true) {
			shed++
		}
	}
	// The old flood is forgotten; only the fresh ~200 requests count, which
	// crosses the 60 budget late — so shedding must be far below the >80%
	// steady-flood level.
	if shed > 120 {
		t.Fatalf("stale flood still shedding %d/200 after rotation; decay broken", shed)
	}
}

func TestFairShareLiveTuning(t *testing.T) {
	p := New()
	if p.share() != defaultFairShare {
		t.Fatalf("default share = %d, want %d", p.share(), defaultFairShare)
	}
	p.SetFairShare(500)
	if p.share() != 500 {
		t.Fatalf("share = %d, want 500", p.share())
	}
	// 300 requests < 500 budget: no shedding even under pressure.
	for i := 0; i < 300; i++ {
		if p.Check("203.0.113.80", true) {
			t.Fatal("under raised budget, must not shed")
		}
	}
	p.SetFairShare(0)
	if p.share() != defaultFairShare {
		t.Fatalf("share after reset = %d, want default", p.share())
	}
}

func TestShedProbCurve(t *testing.T) {
	if got := shedProb(60, 60); got != 0 {
		t.Fatalf("at budget: prob = %v, want 0", got)
	}
	if got := shedProb(120, 60); got < 0.49 || got > 0.51 {
		t.Fatalf("at 2x budget: prob = %v, want ~0.5", got)
	}
	if got := shedProb(1<<40, 60); got != maxShed {
		t.Fatalf("extreme excess: prob = %v, want cap %v", got, maxShed)
	}
	if got := shedProb(10, 0); got != 0 {
		t.Fatalf("zero budget must disable: prob = %v", got)
	}
}

func TestSubnetOf(t *testing.T) {
	cases := map[string]string{
		"192.0.2.55":          "g/192.0.2.0/24",
		"2001:db8:abcd:12::7": "g/2001:db8:abcd::/48",
		"not-an-ip":           "g/not-an-ip",
	}
	for in, want := range cases {
		if got := subnetOf(in); got != want {
			t.Fatalf("subnetOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConcurrentCheckIsRaceFree(t *testing.T) {
	p := New()
	var wg sync.WaitGroup
	for g := 0; g < 100; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ip := fmt.Sprintf("203.0.113.%d", id%250)
			for i := 0; i < 500; i++ {
				p.Check(ip, i%2 == 0)
			}
		}(g)
	}
	wg.Wait()
	if p.WindowRate() <= 0 {
		t.Fatal("window rate telemetry should be positive after traffic")
	}
}
