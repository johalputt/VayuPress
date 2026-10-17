// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	vasession "github.com/johalputt/vayupress/internal/vayuanalytics/session"
)

// The benchmark harness: a migrated store holding VP_BENCH_EVENTS events
// (default 200,000) across fourteen sites over thirty days, written through
// the ingest path itself, and the reads the console makes of it. Run with
//
//	VP_BENCH_EVENTS=1000000 go test -run '^$' -bench . -benchtime 20x ./internal/analytics/
//
// The distributions are a blog network's, not uniform noise: a few sites take
// most of the traffic, a few pages take most of each site's views, a visit is
// one to six views a minute or two apart, and one event in ten is a custom
// event. Uniform data would flatter every index.

const benchSites = 14

var (
	benchOnce  sync.Once
	benchStore *Store
	benchErr   error
	benchSeeds int
)

func benchEvents() int {
	if n, err := strconv.Atoi(os.Getenv("VP_BENCH_EVENTS")); err == nil && n > 0 {
		return n
	}
	return 200_000
}

func benchSite(i int) string {
	if i == 0 {
		return "" // the primary
	}
	return fmt.Sprintf("site-%02d", i)
}

// zipf draws 0..n-1 with weight 1/(k+1): the first few take most.
func zipf(r *rand.Rand, n int) int {
	z := rand.NewZipf(r, 1.1, 1, uint64(n-1))
	return int(z.Uint64())
}

func openBenchStore(b *testing.B) *Store {
	b.Helper()
	benchOnce.Do(func() {
		dir, err := os.MkdirTemp("", "vp-bench")
		if err != nil {
			benchErr = err
			return
		}
		config.Cfg.DBPath = filepath.Join(dir, "bench.db")
		if benchErr = dbpkg.Init(); benchErr != nil {
			return
		}
		s := New(dbpkg.DB)
		s.UseReader(dbpkg.AdminReader())
		s.UseVisits(vasession.NewMasterHasher([]byte("bench")))
		benchSeeds = benchEvents()
		benchErr = seedBench(s, benchSeeds)
		benchStore = s
	})
	if benchErr != nil {
		b.Fatal(benchErr)
	}
	return benchStore
}

func seedBench(s *Store, n int) error {
	r := rand.New(rand.NewSource(1))
	ctx := context.Background()
	start := time.Now().UTC().AddDate(0, 0, -30)
	span := 30 * 24 * time.Hour
	batch := make([]collectEvent, 0, evFlushChunk)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := s.applyCollectBatch(ctx, batch)
		batch = batch[:0]
		return err
	}
	// Visits are generated in time order, as they arrive.
	type visit struct {
		at          time.Time
		site, views int
		ip          string
	}
	visits := make([]visit, 0, n/3)
	for made := 0; made < n; {
		v := visit{
			at:    start.Add(time.Duration(r.Int63n(int64(span)))),
			site:  zipf(r, benchSites),
			views: 1 + r.Intn(6),
			ip:    fmt.Sprintf("10.%d.%d.%d", r.Intn(64), r.Intn(256), r.Intn(256)),
		}
		visits = append(visits, v)
		made += v.views
	}
	sort.Slice(visits, func(i, j int) bool { return visits[i].at.Before(visits[j].at) })
	written := 0
	for _, v := range visits {
		at := v.at
		for k := 0; k < v.views && written < n; k++ {
			req := CollectRequest{
				URL:      fmt.Sprintf("/post-%d", zipf(r, 500)),
				Hostname: benchSite(v.site) + ".test",
				Lang:     "en",
			}
			if k == 0 && r.Intn(3) == 0 {
				req.Referrer = []string{"https://www.google.com/", "https://news.ycombinator.com/", "https://t.co/x"}[r.Intn(3)]
			}
			if r.Intn(10) == 0 {
				req.EventType, req.EventName = 2, "signup"
			}
			stamp := at
			s.now = func() time.Time { return stamp }
			if ev := s.buildCollectEvent(req, v.ip, "Mozilla/5.0 (X11; Linux x86_64) Firefox/131.0", benchSite(v.site)); ev != nil {
				batch = append(batch, *ev)
			}
			written++
			if len(batch) == evFlushChunk {
				if err := flush(); err != nil {
					return err
				}
			}
			at = at.Add(time.Duration(30+r.Intn(150)) * time.Second)
		}
	}
	s.now = time.Now
	return flush()
}

// BenchmarkIngest is the write cost of one event through the batch path the
// beacon and the served-page counter use: identity, visit, the event row and
// the visit row's upsert, in transactions of evFlushChunk.
func BenchmarkIngest(b *testing.B) {
	s := openBenchStore(b)
	ctx := context.Background()
	batch := make([]collectEvent, 0, evFlushChunk)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := CollectRequest{URL: fmt.Sprintf("/post-%d", i%500), Hostname: "site-01.test", Lang: "en"}
		if ev := s.buildCollectEvent(req, fmt.Sprintf("10.200.%d.%d", (i/256)%256, i%256), "Mozilla/5.0 Firefox/131.0", "site-01"); ev != nil {
			batch = append(batch, *ev)
		}
		if len(batch) == evFlushChunk || i == b.N-1 {
			if err := s.applyCollectBatch(ctx, batch); err != nil {
				b.Fatal(err)
			}
			batch = batch[:0]
		}
	}
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "events/s")
}

func benchRead(b *testing.B, read func(s *Store, ctx context.Context) error) {
	s := openBenchStore(b)
	ctx := context.Background()
	b.ReportMetric(float64(benchSeeds), "seeded")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := read(s, ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOverviewOneSite30Days(b *testing.B) {
	benchRead(b, func(s *Store, ctx context.Context) error {
		_, err := s.OverviewSinceScoped(ctx, benchSite(3), 30)
		return err
	})
}

func BenchmarkOverviewBusiestSite30Days(b *testing.B) {
	benchRead(b, func(s *Store, ctx context.Context) error {
		_, err := s.OverviewSinceScoped(ctx, benchSite(0), 30)
		return err
	})
}

func BenchmarkOverviewEverySite30Days(b *testing.B) {
	benchRead(b, func(s *Store, ctx context.Context) error {
		_, err := s.OverviewSince(ctx, 30)
		return err
	})
}

func BenchmarkTopPagesOneSite30Days(b *testing.B) {
	benchRead(b, func(s *Store, ctx context.Context) error {
		_, err := s.TopPagesScoped(ctx, benchSite(3), 30, 20)
		return err
	})
}

func BenchmarkRealtime(b *testing.B) {
	benchRead(b, func(s *Store, ctx context.Context) error {
		_, err := s.Realtime(ctx)
		return err
	})
}
