// SPDX-License-Identifier: Apache-2.0

package sigcache

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/vayushield/botdb"
)

func TestCacheHitMatchesLookup(t *testing.T) {
	c := New(time.Minute)
	sig := &botdb.StoredSignature{FingerprintHash: "fp1", Classification: botdb.ClassBadBot, Confidence: 0.9}
	c.Put("fp1", sig, true)
	got, found, hit := c.Get("fp1")
	if !hit || !found {
		t.Fatalf("expected hit+found, got hit=%v found=%v", hit, found)
	}
	if got.Classification != botdb.ClassBadBot || got.Confidence != 0.9 {
		t.Fatalf("cached signature differs from stored: %+v", got)
	}
	// Returned value is a copy — mutating it must not corrupt the cache.
	got.Confidence = 0.1
	again, _, _ := c.Get("fp1")
	if again.Confidence != 0.9 {
		t.Fatal("cache returned an aliased value — mutation leaked back")
	}
}

// TestNegativeCached is the load-bearing case: a fingerprint the DB does not
// know is cached as a negative and reused, so a swarm/flash-crowd of unknown
// fingerprints never re-hits SQLite.
func TestNegativeCached(t *testing.T) {
	c := New(time.Minute)
	c.Put("missing", nil, false)
	_, found, hit := c.Get("missing")
	if !hit {
		t.Fatal("negative result must be cached (hit)")
	}
	if found {
		t.Fatal("negative result must report found=false")
	}
	if c.Misses() != 0 {
		t.Fatalf("a cached negative must not count as a miss, got %d", c.Misses())
	}
}

func TestTTLReLookup(t *testing.T) {
	now := time.Now()
	c := New(30 * time.Second)
	c.now = func() time.Time { return now }
	c.Put("fp", nil, false)
	if _, _, hit := c.Get("fp"); !hit {
		t.Fatal("fresh entry should hit")
	}
	now = now.Add(31 * time.Second)
	if _, _, hit := c.Get("fp"); hit {
		t.Fatal("expired entry must miss (re-lookup)")
	}
}

func TestForgetAndClear(t *testing.T) {
	c := New(time.Minute)
	c.Put("a", &botdb.StoredSignature{FingerprintHash: "a"}, true)
	c.Put("b", nil, false)
	c.Forget("a")
	if _, _, hit := c.Get("a"); hit {
		t.Fatal("Forget must drop the entry")
	}
	if _, _, hit := c.Get("b"); !hit {
		t.Fatal("Forget must not touch other keys")
	}
	c.Clear()
	if c.Len() != 0 {
		t.Fatalf("Clear must empty the cache, len=%d", c.Len())
	}
}

// TestEvictUnderFloodBounded proves a 1M-distinct-fingerprint flood cannot grow
// the cache without bound — total entries stay within the shard-cap ceiling.
func TestEvictUnderFloodBounded(t *testing.T) {
	c := New(time.Hour)
	// Well above the shards*perShardCap (262 144) ceiling, so eviction must fire.
	for i := 0; i < 400_000; i++ {
		c.Put("fp"+strconv.Itoa(i), nil, false)
	}
	max := shards * perShardCap
	if got := c.Len(); got > max {
		t.Fatalf("cache grew past the cap: %d > %d", got, max)
	}
}

func TestDisabledCacheAlwaysMisses(t *testing.T) {
	c := New(0) // ttl<=0 disables
	c.Put("x", nil, false)
	if _, _, hit := c.Get("x"); hit {
		t.Fatal("a disabled cache must never hit")
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	c := New(time.Minute)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				k := "fp" + strconv.Itoa((g*2000+i)%500)
				if i%2 == 0 {
					c.Put(k, nil, false)
				} else {
					c.Get(k)
				}
				if i%97 == 0 {
					c.Forget(k)
				}
			}
		}(g)
	}
	wg.Wait()
}
