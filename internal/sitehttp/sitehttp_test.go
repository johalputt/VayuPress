// SPDX-License-Identifier: Apache-2.0

package sitehttp

import (
	"context"
	"database/sql"
	"math"
	"math/rand"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	prev := config.Cfg.DBPath
	config.Cfg.DBPath = filepath.Join(t.TempDir(), "sitehttp.db")
	if err := dbpkg.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dbpkg.ClosePools(); _ = dbpkg.DB.Close(); config.Cfg.DBPath = prev })
	return dbpkg.DB
}

// Each bucket is at most a tenth wider than the bound below it, so a time
// said as its bucket's bound is within a tenth of itself.
func TestEveryBucketIsWithinATenth(t *testing.T) {
	for i := 1; i < len(bounds); i++ {
		lo, hi := bounds[i-1], bounds[i]
		if hi <= lo {
			t.Fatalf("bound %d (%d) is not above %d", i, hi, lo)
		}
		if lo >= 10 && float64(hi-lo) > 0.1*float64(lo)+1e-9 {
			t.Errorf("bucket (%d, %d] is wider than a tenth of %d", lo, hi, lo)
		}
	}
	for _, ms := range []int64{0, 1, 9, 10, 11, 57, 999, 1000, 29999, 30000, 45000} {
		b := bucket(ms)
		if want := min(ms, 30000); bounds[b] < want || (b > 0 && bounds[b-1] >= want) {
			t.Errorf("%d ms is in bucket (%d, %d]", ms, bounds[max(b-1, 0)], bounds[b])
		}
	}
}

// A percentile read from the buckets is the exact one's bucket: never below
// it, and within a tenth above it.
func TestAPercentileIsWithinATenthOfTheExactOne(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	h := map[int]int64{}
	var all []int64
	for i := 0; i < 5000; i++ {
		ms := int64(math.Exp(rng.Float64() * 8)) // 1 ms to about 3 s
		all = append(all, ms)
		h[bucket(ms)]++
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	for _, q := range []float64{0.5, 0.75, 0.95} {
		exact := all[int(math.Ceil(q*float64(len(all))))-1]
		got := percentile(h, q)
		if got < exact || float64(got) > float64(exact)*1.1+1 {
			t.Errorf("p%.0f = %d from the buckets, exact %d", q*100, got, exact)
		}
	}
}

// What is recorded is what is read: each status in its class, the pages and
// their times, those the page cache answered and those it rendered, each
// site its own, and two writes add up. A cache hit that is not a page (a
// 304) is not a cached page.
func TestWhatIsRecordedIsRead(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	r := &Recorder{}
	now := time.Now()
	r.Record("s1", 200, true, Cached, 5*time.Millisecond, now)
	r.Record("s1", 200, true, Rendered, 120*time.Millisecond, now)
	r.Record("s1", 200, false, NoCache, time.Millisecond, now)
	r.Record("s1", 304, false, Cached, 0, now)
	r.Record("s1", 404, false, NoCache, 0, now)
	r.Record("s1", 410, false, NoCache, 0, now)
	r.Record("s1", 503, false, NoCache, 0, now)
	r.Record("s2", 500, false, NoCache, 0, now)
	if err := r.Flush(ctx, db); err != nil {
		t.Fatal(err)
	}
	r.Record("s1", 200, true, Cached, 7*time.Millisecond, now)
	r.Record("s1", 200, true, Rendered, 5*time.Millisecond, now)
	if err := r.Flush(ctx, db); err != nil {
		t.Fatal(err)
	}
	got, days, err := Read(ctx, db, "s1", 1)
	if err != nil {
		t.Fatal(err)
	}
	want := Summary{Requests: 9, S2xx: 5, S3xx: 1, S4xx: 2, S404: 1, S5xx: 1, Pages: 4, MeanMS: 137.0 / 4, P50MS: 5, P75MS: 7, P95MS: bounds[bucket(120)],
		Cached: 2, CachedMeanMS: 6, Rendered: 2, RenderedMeanMS: 62.5}
	if got != want {
		t.Errorf("site one = %+v\nwant      %+v", got, want)
	}
	if len(days) != 1 || days[0].Summary != want || days[0].Day != now.UTC().Format("2006-01-02") {
		t.Errorf("site one by day = %+v", days)
	}
	if all, _, _ := Read(ctx, db, AllSites, 1); all.Requests != 10 || all.S5xx != 2 {
		t.Errorf("every site = %+v, want 10 requests and 2 server errors", all)
	}
	if two, _, _ := Read(ctx, db, "s2", 1); two.Requests != 1 || two.Pages != 0 || two.P95MS != 0 {
		t.Errorf("site two = %+v, want 1 request and no pages", two)
	}
}

// A write that fails keeps what it could not write for the next one.
func TestAFailedWriteLosesNothing(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	r := &Recorder{}
	r.Record("s1", 200, true, Rendered, 7*time.Millisecond, time.Now())
	r.Record("s1", 200, true, Cached, 3*time.Millisecond, time.Now())
	if _, err := db.Exec(`ALTER TABLE site_http_minutes RENAME TO held`); err != nil {
		t.Fatal(err)
	}
	if err := r.Flush(ctx, db); err == nil {
		t.Fatal("a write to a missing table succeeded")
	}
	if _, err := db.Exec(`ALTER TABLE held RENAME TO site_http_minutes`); err != nil {
		t.Fatal(err)
	}
	if err := r.Flush(ctx, db); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := Read(ctx, db, "s1", 1); got.Requests != 2 || got.Pages != 2 || got.P95MS != 7 || got.Rendered != 1 || got.RenderedMeanMS != 7 || got.Cached != 1 || got.CachedMeanMS != 3 {
		t.Errorf("after the failed write = %+v, want the rendered page and the cached one", got)
	}
}

// The purge deletes the rows older than the days kept, and only those.
func TestThePurgeKeepsTheDaysKept(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	old := time.Now().UTC().AddDate(0, 0, -40).Format("2006-01-02")
	for _, q := range []string{
		`INSERT INTO site_http_minutes(domain_id,minute,requests) VALUES('s1','` + old + ` 10:00:00',1)`,
		`INSERT INTO site_http_latency(domain_id,hour,bucket,n) VALUES('s1','` + old + ` 10:00:00',3,1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	r := &Recorder{}
	r.Record("s1", 200, true, NoCache, time.Millisecond, time.Now())
	if err := r.Flush(ctx, db); err != nil {
		t.Fatal(err)
	}
	if n, err := Purge(ctx, db, 30); err != nil || n != 2 {
		t.Errorf("purged %d rows (%v), want the 2 old ones", n, err)
	}
	if got, _, _ := Read(ctx, db, "s1", 60); got.Requests != 1 {
		t.Errorf("after the purge = %+v, want today's request alone", got)
	}
}

// Each read is a range of an index, for one site and for every site.
func TestTheReadsAreRanges(t *testing.T) {
	db := openDB(t)
	for _, site := range []string{"s1", AllSites} {
		clause, carg := scopeClause(site)
		args := append([]any{"2026-10-01 00:00:00", "2026-11-01 00:00:00"}, carg...)
		for _, q := range []string{minutesSQL(clause), latencySQL(clause)} {
			rows, err := db.Query("EXPLAIN QUERY PLAN "+q, args...)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var id, parent, notused int
				var detail string
				if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(detail, "SCAN site_http") {
					t.Errorf("%q walks the table: %s", site, detail)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			_ = rows.Close()
		}
	}
}
