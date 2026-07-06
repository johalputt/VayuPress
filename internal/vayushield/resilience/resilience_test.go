package resilience

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestLimiterBurstThenThrottle(t *testing.T) {
	// 0 refill/sec so we can test the burst ceiling deterministically.
	l := NewLimiter(0, 5, 4096)
	allowed := 0
	for i := 0; i < 10; i++ {
		if l.Allow("1.2.3.4") {
			allowed++
		}
	}
	if allowed != 5 {
		t.Fatalf("burst of 5 should allow exactly 5, got %d", allowed)
	}
}

func TestLimiterRefill(t *testing.T) {
	l := NewLimiter(100, 1, 4096) // 100 tokens/sec, burst 1
	if !l.Allow("k") {
		t.Fatal("first request should pass")
	}
	if l.Allow("k") {
		t.Fatal("immediate second request should be throttled")
	}
	time.Sleep(20 * time.Millisecond) // ~2 tokens refilled
	if !l.Allow("k") {
		t.Fatal("after refill the request should pass")
	}
}

func TestLimiterPerKeyIsolation(t *testing.T) {
	l := NewLimiter(0, 1, 4096)
	if !l.Allow("a") || !l.Allow("b") {
		t.Fatal("distinct keys must not share a bucket")
	}
	if l.Allow("a") {
		t.Fatal("key a should now be throttled")
	}
}

func TestLimiterMemoryBounded(t *testing.T) {
	l := NewLimiter(0, 5, limiterShards*4) // tiny cap
	for i := 0; i < 100000; i++ {
		l.Allow("ip-" + strconv.Itoa(i))
	}
	total := 0
	for i := range l.shards {
		l.shards[i].mu.Lock()
		total += len(l.shards[i].m)
		l.shards[i].mu.Unlock()
	}
	if total > limiterShards*8 {
		t.Fatalf("limiter memory not bounded: %d keys retained", total)
	}
}

func TestLimiterLiveRateChange(t *testing.T) {
	l := NewLimiter(0, 1, 4096)
	_ = l.Allow("k")
	if l.Allow("k") {
		t.Fatal("should be throttled at burst 1")
	}
	l.SetRate(0, 10) // raise burst live
	// A brand-new key now gets the new burst.
	got := 0
	for i := 0; i < 10; i++ {
		if l.Allow("fresh") {
			got++
		}
	}
	if got != 10 {
		t.Fatalf("live burst change should allow 10, got %d", got)
	}
}

func TestInFlightLoadShed(t *testing.T) {
	var f InFlight
	f.SetMax(2)
	first := f.Acquire()
	second := f.Acquire()
	if !first || !second {
		t.Fatal("first two acquires should succeed")
	}
	if f.Acquire() {
		t.Fatal("third acquire should be shed")
	}
	f.Release()
	if !f.Acquire() {
		t.Fatal("after release a slot should be free")
	}
	if f.Current() != 2 {
		t.Fatalf("in-flight should be 2, got %d", f.Current())
	}
}

func TestInFlightDisabled(t *testing.T) {
	var f InFlight // max 0 = disabled
	for i := 0; i < 1000; i++ {
		if !f.Acquire() {
			t.Fatal("disabled gate must always admit")
		}
	}
}

func TestBlocklistTTL(t *testing.T) {
	b := NewBlocklist()
	if b.Blocked("1.2.3.4") {
		t.Fatal("unknown key must not be blocked")
	}
	b.Block("1.2.3.4", 40*time.Millisecond)
	if !b.Blocked("1.2.3.4") {
		t.Fatal("key should be blocked")
	}
	if b.Len() != 1 {
		t.Fatalf("blocklist len want 1 got %d", b.Len())
	}
	time.Sleep(60 * time.Millisecond)
	if b.Blocked("1.2.3.4") {
		t.Fatal("entry should have expired")
	}
	if b.Len() != 0 {
		t.Fatalf("expired entry should be dropped, len=%d", b.Len())
	}
}

func TestControllerUnderAttack(t *testing.T) {
	var c Controller
	if c.UnderAttack() {
		t.Fatal("no threshold set → never under attack")
	}
	c.SetThreshold(50)
	for i := 0; i < 60; i++ {
		c.Observe()
	}
	if !c.UnderAttack() {
		t.Fatal("60 req in one second with threshold 50 must trip")
	}
	if c.RPS() < 50 {
		t.Fatalf("RPS should reflect the burst, got %d", c.RPS())
	}
}

func TestControllerRelaxes(t *testing.T) {
	var c Controller
	c.SetThreshold(1000) // high threshold
	for i := 0; i < 10; i++ {
		c.Observe()
	}
	if c.UnderAttack() {
		t.Fatal("light traffic must not be flagged")
	}
}

func TestConcurrentSafety(t *testing.T) {
	l := NewLimiter(1000, 100, 65536)
	b := NewBlocklist()
	var f InFlight
	f.SetMax(1000)
	var c Controller
	c.SetThreshold(10000)

	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				key := "ip-" + strconv.Itoa((g*7+i)%500)
				l.Allow(key)
				c.Observe()
				if f.Acquire() {
					f.Release()
				}
				if i%50 == 0 {
					b.Block(key, time.Second)
				}
				b.Blocked(key)
			}
		}(g)
	}
	wg.Wait()
}
