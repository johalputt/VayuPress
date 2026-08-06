// SPDX-License-Identifier: Apache-2.0

package analytics

// recorder_test.go — a page view must not be able to take the site down.
//
// ATTACK: hold the write connection and then send traffic.
//
// SQLite has one writer, so the pool has one connection. The old hot path fired
// `go analytics.Record(context.Background(), …)` per view, and a Background
// context has a nil Done channel — database/sql waits for a free connection
// forever. So any process holding the write connection turned every arriving
// view into a goroutine parked in an unbounded queue, and the stall outlived
// its own cause: once the connection freed, the whole backlog drained one
// statement at a time while sign-ins, admin actions and MCP calls waited behind
// it.
//
// That is why an outage lasted minutes when the thing that caused it lasted
// seconds, why it ended with no restart and nothing in the log, and why a busier
// site hurt longer — traffic is what fills the queue.
//
// The test that catches it is written the way the incident happened: take the
// connection first, then serve traffic, then look at who had to queue. Note the
// property it asserts — NOT that a page view is fast. The old code did not slow
// the page view down either; it starved everything else. A first draft asserted
// the wrong half and passed against the defect verbatim.

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// singleWriterStore mirrors production exactly: one write connection, WAL, the
// same two tables. The cap is the whole point — with a larger pool none of this
// can be reproduced.
func singleWriterStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	d, err := sql.Open("sqlite3", "file:"+t.TempDir()+"/a.db?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	d.SetMaxOpenConns(1)
	d.SetMaxIdleConns(1)
	for _, q := range []string{
		`CREATE TABLE analytics_daily(day TEXT NOT NULL,domain_id TEXT NOT NULL DEFAULT '',path TEXT NOT NULL,views INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(day,domain_id,path))`,
		`CREATE TABLE analytics_referrers(day TEXT NOT NULL,domain_id TEXT NOT NULL DEFAULT '',host TEXT NOT NULL,hits INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(day,domain_id,host))`,
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return New(d), d
}

// THE test, and it is not the one written first.
//
// The first version asserted that RecordAsync returns quickly while the write
// connection is held. It passed — and it also passed with the ORIGINAL defect
// pasted back in, which is how this version came to exist. Of course it did:
// the old code was `go func() { Record(…) }()`, so the page view never waited
// either. The visitor whose view triggered the write was fine.
//
// The damage was to everyone else. Each view parked a goroutine in a queue in
// front of the one write connection, with no deadline and no bound, and every
// caller that genuinely needed to write — a sign-in, an admin save, an MCP tool
// — went to the back of it. So the property to assert is not "a view is fast".
// It is that **traffic arriving during a stall does not queue on the writer at
// all**, which database/sql counts for us: WaitCount is the number of callers
// that had to wait for a connection.
//
// Under the old code this is one wait per view. Under this one it is zero,
// because nothing here touches the database.
func TestTrafficDuringAStallNeverQueuesOnTheWriteConnection(t *testing.T) {
	s, d := singleWriterStore(t)

	// A helper process, a checkpoint, a slow fsync — anything at all.
	hold, err := d.Conn(context.Background())
	if err != nil {
		t.Fatalf("take the write connection: %v", err)
	}
	defer func() { _ = hold.Close() }()

	base := d.Stats().WaitCount
	goroutinesBefore := runtime.NumGoroutine()

	const views = 5000
	var wg sync.WaitGroup
	for i := 0; i < views; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.RecordAsync("", fmt.Sprintf("/post-%d", i%50), "https://example.com/x")
		}(i)
	}
	wg.Wait()

	// Give any goroutine the old code would have spawned time to actually reach
	// the pool and be counted. With this code nothing was spawned, so this is
	// just a pause.
	time.Sleep(500 * time.Millisecond)

	if queued := d.Stats().WaitCount - base; queued > 10 {
		t.Errorf("%d of %d page views queued for the write connection. That queue has no deadline "+
			"and no bound: it outlives whatever caused the stall, and every sign-in, admin action "+
			"and MCP call drains behind it. This is the defect that 502'd a live site with nothing "+
			"in the log.", queued, views)
	}

	// And the goroutines must not accumulate. One per view is how a momentary
	// lock hold becomes an unbounded backlog.
	if grew := runtime.NumGoroutine() - goroutinesBefore; grew > views/10 {
		t.Errorf("goroutine count grew by %d while serving %d views with the writer held; view "+
			"counting must not allocate a goroutine per visitor", grew, views)
	}

	// Nothing was lost while the database was unavailable.
	st := s.CollectorStats()
	if st.Pending != views {
		t.Errorf("buffered %d of %d views; a view arriving during a stall must still be counted",
			st.Pending, views)
	}
	if st.Dropped != 0 {
		t.Errorf("dropped %d views into a 50-path buffer, which is nowhere near the cap", st.Dropped)
	}
}

// The consequence, stated as the operator experienced it: after a stall clears,
// the next caller that needs to write must get the connection promptly rather
// than waiting for a backlog of view counts to drain through it one at a time.
func TestAWriterIsNotStarvedByTrafficThatArrivedDuringAStall(t *testing.T) {
	s, d := singleWriterStore(t)

	hold, err := d.Conn(context.Background())
	if err != nil {
		t.Fatalf("take the write connection: %v", err)
	}
	// Enough traffic that a backlog takes measurably longer to drain than the
	// person waiting on the next write is willing to sit for. Below roughly this
	// volume the drain finishes inside the threshold and the test cannot tell the
	// two implementations apart — which was true of its first version.
	const views = 40000
	for i := 0; i < views; i++ {
		s.RecordAsync("", fmt.Sprintf("/post-%d", i%50), "")
	}
	time.Sleep(500 * time.Millisecond) // let any queue form
	_ = hold.Close()                   // the stall ends

	// A sign-in, an admin save, an MCP tool call — something a person is waiting on.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err = d.ExecContext(ctx, `INSERT INTO analytics_daily(day,domain_id,path,views) VALUES('2026-01-01','','/login',1)`)
	took := time.Since(start)
	if err != nil {
		t.Fatalf("a legitimate write failed after the stall cleared: %v (after %v)", err, took)
	}
	if took > 250*time.Millisecond {
		t.Errorf("a legitimate write waited %v after the stall cleared, behind view counts that "+
			"arrived while it was stuck. The outage outlasting its own cause is exactly what was "+
			"reported.", took)
	}
	_ = s
}

// Traffic must not become write volume. A thousand views of one page is one
// statement adding a thousand, not a thousand statements adding one — that
// inversion is what makes a busy install write LESS per view, not more.
func TestManyViewsOfOnePageCostOneWrite(t *testing.T) {
	s, d := singleWriterStore(t)
	for i := 0; i < 1000; i++ {
		s.RecordAsync("", "/hot", "")
	}
	before := d.Stats().WaitCount
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	var views int64
	if err := d.QueryRow(`SELECT views FROM analytics_daily WHERE path='/hot'`).Scan(&views); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if views != 1000 {
		t.Errorf("counted %d of 1000 views; coalescing must not lose counts", views)
	}
	st := s.CollectorStats()
	if st.Writes != 1 {
		t.Errorf("1000 views cost %d statements; the whole point is that they cost one", st.Writes)
	}
	if st.Flushed != 1000 {
		t.Errorf("reported %d views flushed, want 1000", st.Flushed)
	}
	_ = before
}

// Distinct pages are kept distinct — coalescing must not merge what the table
// keys separately, or the busiest page absorbs everyone else's traffic.
func TestCoalescingKeepsPathsScopesAndReferrersApart(t *testing.T) {
	s, d := singleWriterStore(t)
	s.RecordAsync("", "/a", "https://one.example/p")
	s.RecordAsync("", "/a", "https://one.example/q") // same host, same key
	s.RecordAsync("", "/b", "https://two.example/p")
	s.RecordAsync("dom42", "/a", "")
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	for _, c := range []struct {
		scope, path string
		want        int64
	}{
		{"", "/a", 2}, {"", "/b", 1}, {"dom42", "/a", 1},
	} {
		var got int64
		err := d.QueryRow(`SELECT views FROM analytics_daily WHERE domain_id=? AND path=?`,
			c.scope, c.path).Scan(&got)
		if err != nil {
			t.Errorf("scope %q path %q: %v", c.scope, c.path, err)
			continue
		}
		if got != c.want {
			t.Errorf("scope %q path %q: got %d views, want %d", c.scope, c.path, got, c.want)
		}
	}
	var oneHits, twoHits int64
	_ = d.QueryRow(`SELECT hits FROM analytics_referrers WHERE host='one.example'`).Scan(&oneHits)
	_ = d.QueryRow(`SELECT hits FROM analytics_referrers WHERE host='two.example'`).Scan(&twoHits)
	if oneHits != 2 || twoHits != 1 {
		t.Errorf("referrer hits: one.example=%d (want 2) two.example=%d (want 1)", oneHits, twoHits)
	}
}

// The buffer is bounded by DISTINCT keys and drops rather than growing. An
// unbounded in-memory buffer is the same failure as an unbounded goroutine
// queue with a different name.
func TestTheBufferIsBoundedAndDropsAreCounted(t *testing.T) {
	s, _ := singleWriterStore(t)
	for i := 0; i < maxPendingKeys+500; i++ {
		s.RecordAsync("", fmt.Sprintf("/p-%d", i), "")
	}
	st := s.CollectorStats()
	if st.Buffered > maxPendingKeys {
		t.Errorf("buffered %d distinct keys, above the %d cap; the buffer is unbounded",
			st.Buffered, maxPendingKeys)
	}
	if st.Dropped < 500 {
		t.Errorf("dropped %d, want at least 500 — a drop that is not counted is a drop nobody "+
			"can see on the panel", st.Dropped)
	}
	// A key already in the buffer must never be dropped: a site under sustained
	// load on a fixed set of pages costs no additional memory and must keep
	// counting accurately.
	before := s.CollectorStats().Dropped
	for i := 0; i < 1000; i++ {
		s.RecordAsync("", "/p-0", "")
	}
	if after := s.CollectorStats().Dropped; after != before {
		t.Errorf("dropped %d views for a path already in the buffer; an existing key costs no "+
			"memory and must never be dropped", after-before)
	}
}

// A flush must not wait forever either — that was the original defect, and
// moving it from the request path to a background goroutine does not fix it,
// it only hides it.
func TestAFlushGivesUpRatherThanWaitingForever(t *testing.T) {
	s, d := singleWriterStore(t)
	s.RecordAsync("", "/x", "")

	hold, err := d.Conn(context.Background())
	if err != nil {
		t.Fatalf("take the write connection: %v", err)
	}
	defer func() { _ = hold.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = s.Flush(ctx)
	took := time.Since(start)
	if err == nil {
		t.Fatal("the flush claimed to succeed while the write connection was held by someone else")
	}
	if took > 3*time.Second {
		t.Errorf("the flush waited %v past a 300ms deadline; nothing in this package may wait "+
			"without a bound", took)
	}
}

// A failed flush must not silently discard the counts it was carrying... or, if
// it does, it must say so. This pins whichever is true so the panel's numbers
// mean something.
func TestAFailedFlushIsVisibleOnThePanel(t *testing.T) {
	s, d := singleWriterStore(t)
	s.RecordAsync("", "/x", "")
	hold, _ := d.Conn(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = s.Flush(ctx)
	_ = hold.Close()

	st := s.CollectorStats()
	if st.LastErr == "" {
		t.Error("a flush failed and the panel shows no error; an operator has no way to know " +
			"their view counts are not being written")
	}
}

// The flusher must actually run. A recorder that buffers into a map nobody
// drains loses every view in silence — the same class of defect as a setting
// that is writable but never read, which this repo has already shipped once.
func TestTheCollectorReportsWhetherItIsRunning(t *testing.T) {
	s, _ := singleWriterStore(t)
	if s.CollectorStats().Running {
		t.Error("a Store with no started flusher reports Running:true, so a missing StartCollector " +
			"call would be invisible")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.StartCollector(ctx)
	if !s.CollectorStats().Running {
		t.Error("StartCollector ran and Running is still false")
	}
	// Idempotent: two flushers would race to halve each other's batches.
	s.StartCollector(ctx)
	cancel()
}

// Cancelling the collector flushes what is left, so a clean shutdown does not
// throw away the last interval.
func TestShutdownFlushesTheLastInterval(t *testing.T) {
	s, d := singleWriterStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	s.StartCollector(ctx)
	s.RecordAsync("", "/last", "")
	cancel()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int64
		if err := d.QueryRow(`SELECT views FROM analytics_daily WHERE path='/last'`).Scan(&n); err == nil && n == 1 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Error("a view counted just before shutdown was never written; the final flush did not happen")
}

// AUDIT FINDING. The referrer host is visitor-controlled, and buffering changed
// what a preposterous one costs.
//
// Before, it went straight to SQLite: a silly value bloated a table. Now it is
// held in memory until the next flush, so 20,000 distinct keys each carrying a
// header-sized "host" is a memory-exhaustion path that this file would have
// introduced. Go's server allows around a megabyte of headers, and Referer is
// one of them.
//
// A DNS name cannot exceed 253 octets, so anything longer is discarded — not
// truncated, because truncating invents a hostname and attributes real traffic
// to it.
func TestAnAbsurdReferrerHostIsDiscardedNotBuffered(t *testing.T) {
	s, d := singleWriterStore(t)

	huge := strings.Repeat("a", 200000) + ".example"
	for i := 0; i < 200; i++ {
		s.RecordAsync("", fmt.Sprintf("/p-%d", i), "https://"+huge+"/x")
	}
	// The VIEW still counts. Refusing the referrer must not refuse the visit.
	if got := s.CollectorStats().Pending; got != 200 {
		t.Errorf("buffered %d of 200 views; a bad referrer must not lose the page view", got)
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	var refRows int
	if err := d.QueryRow(`SELECT COUNT(*) FROM analytics_referrers`).Scan(&refRows); err != nil {
		t.Fatal(err)
	}
	if refRows != 0 {
		t.Errorf("%d referrer row(s) written for a 200KB hostname; a visitor can hold arbitrary "+
			"memory until the next flush and put it in the database afterwards", refRows)
	}

	// A real host of legal length is still counted — the guard must not have
	// closed the feature to close the hole.
	s.RecordAsync("", "/p-0", "https://news.example.com/a")
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	var hits int64
	if err := d.QueryRow(`SELECT hits FROM analytics_referrers WHERE host='news.example.com'`).
		Scan(&hits); err != nil || hits != 1 {
		t.Errorf("a legitimate referrer was not counted (hits=%d err=%v)", hits, err)
	}
}
