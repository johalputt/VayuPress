// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/dbbatch"
)

// refusedFlush runs flush while the single write connection is held by
// someone else, as a lock held past the flush's deadline does, and returns
// its error.
func refusedFlush(t *testing.T, d *sql.DB, flush func(context.Context) error) error {
	t.Helper()
	hold, err := d.Conn(context.Background())
	if err != nil {
		t.Fatalf("take the write connection: %v", err)
	}
	defer func() { _ = hold.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	return flush(ctx)
}

func dailyViews(t *testing.T, d *sql.DB) map[string]int64 {
	t.Helper()
	rows, err := d.Query(`SELECT path, views FROM analytics_daily`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var p string
		var n int64
		if err := rows.Scan(&p, &n); err != nil {
			t.Fatal(err)
		}
		out[p] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Views a refused flush carried are kept, merged with those counted since,
// and written by the next flush: a lock held through one flush loses none.
func TestARefusedFlushKeepsItsViewsForTheNext(t *testing.T) {
	s, d := singleWriterStore(t)
	for i := 0; i < 3; i++ {
		s.RecordAsync("", "/x", "")
	}
	s.RecordAsync("", "/y", "https://ref.example/a")
	if err := refusedFlush(t, d, s.Flush); err == nil {
		t.Fatal("the flush claimed to write while the connection was held")
	}
	if st := s.CollectorStats(); st.Flushed != 0 || st.Lost != 0 || st.Pending != 4 {
		t.Fatalf("after one refusal: flushed %d, lost %d, pending %d; want 0, 0 and the 4 views kept", st.Flushed, st.Lost, st.Pending)
	}
	s.RecordAsync("", "/x", "")
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := dailyViews(t, d); got["/x"] != 4 || got["/y"] != 1 {
		t.Errorf("written %v, want /x 4 and /y 1", got)
	}
	if st := s.CollectorStats(); st.Flushed != 5 || st.Lost != 0 {
		t.Errorf("flushed %d, lost %d; want 5 and 0", st.Flushed, st.Lost)
	}
}

// Views refused dbbatch.MaxAttempts flushes in a row are counted lost, and
// not before; nothing refused is counted as flushed.
func TestViewsRefusedEveryTimeAreCountedLost(t *testing.T) {
	s, d := singleWriterStore(t)
	s.RecordAsync("", "/x", "")
	s.RecordAsync("", "/x", "")
	for i := 1; i <= dbbatch.MaxAttempts; i++ {
		if st := s.CollectorStats(); st.Lost != 0 {
			t.Fatalf("before refusal %d, %d already counted lost", i, st.Lost)
		}
		_ = refusedFlush(t, d, s.Flush)
	}
	st := s.CollectorStats()
	if st.Lost != 2 || st.Flushed != 0 || st.Pending != 0 {
		t.Errorf("after %d refusals: lost %d, flushed %d, pending %d; want 2, 0, 0", dbbatch.MaxAttempts, st.Lost, st.Flushed, st.Pending)
	}
}

// While the install refuses writes, views are held, not written, and the
// panel says so; released, they are written.
func TestHeldViewsWaitForTheRelease(t *testing.T) {
	s, d := singleWriterStore(t)
	held := true
	s.HoldWhile(func() bool { return held })
	s.RecordAsync("", "/x", "")
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := dailyViews(t, d); len(got) != 0 {
		t.Fatalf("held, and written: %v", got)
	}
	if st := s.CollectorStats(); !st.Held || st.Pending != 1 {
		t.Fatalf("held: panel says held %v, pending %d", st.Held, st.Pending)
	}
	held = false
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := dailyViews(t, d); got["/x"] != 1 {
		t.Errorf("released, written %v", got)
	}
}

// extSingleWriter is newExtStore on one connection, so holding it refuses
// every write, as a lock held past the deadline does.
func extSingleWriter(t *testing.T) *Store {
	t.Helper()
	s := newExtStore(t)
	s.db.SetMaxOpenConns(1)
	return s
}

func pageviews(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM analytics_pageviews`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func beacon(s *Store, i int) {
	s.CollectAsync(CollectRequest{URL: fmt.Sprintf("/p%d", i), Hostname: "site.test", EventType: 1},
		fmt.Sprintf("198.51.100.%d", i), "Mozilla/5.0 Firefox/131.0", "")
}

// Beacons a refused flush carried stay at the front of the queue and are
// written by the next flush.
func TestARefusedBeaconFlushIsTriedAgain(t *testing.T) {
	s := extSingleWriter(t)
	for i := 1; i <= 3; i++ {
		beacon(s, i)
	}
	if err := refusedFlush(t, s.db, s.FlushEvents); err == nil {
		t.Fatal("the flush claimed to write while the connection was held")
	}
	if st := s.EventCollectorStats(); st.Buffered != 3 || st.Lost != 0 || st.Flushed != 0 {
		t.Fatalf("after one refusal: buffered %d, lost %d, flushed %d; want 3, 0, 0", st.Buffered, st.Lost, st.Flushed)
	}
	beacon(s, 4)
	if err := s.FlushEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := pageviews(t, s); n != 4 {
		t.Errorf("%d page views written, want the 3 kept and the 1 since", n)
	}
	if st := s.EventCollectorStats(); st.Flushed != 4 || st.Lost != 0 || st.Buffered != 0 {
		t.Errorf("flushed %d, lost %d, buffered %d; want 4, 0, 0", st.Flushed, st.Lost, st.Buffered)
	}
}

// Beacons refused dbbatch.MaxAttempts flushes in a row are counted lost, and
// not before.
func TestBeaconsRefusedEveryTimeAreCountedLost(t *testing.T) {
	s := extSingleWriter(t)
	beacon(s, 1)
	beacon(s, 2)
	for i := 1; i <= dbbatch.MaxAttempts; i++ {
		if st := s.EventCollectorStats(); st.Lost != 0 {
			t.Fatalf("before refusal %d, %d already counted lost", i, st.Lost)
		}
		_ = refusedFlush(t, s.db, s.FlushEvents)
	}
	if st := s.EventCollectorStats(); st.Lost != 2 || st.Buffered != 0 || st.Flushed != 0 {
		t.Errorf("lost %d, buffered %d, flushed %d; want 2, 0, 0", st.Lost, st.Buffered, st.Flushed)
	}
}

// While the install refuses writes, beacons are held to the queue's cap,
// past which they are dropped and counted; released, the held are written.
func TestHeldBeaconsWaitForTheRelease(t *testing.T) {
	s := extSingleWriter(t)
	held := true
	s.HoldWhile(func() bool { return held })
	for i := 1; i <= 3; i++ {
		beacon(s, i)
	}
	if err := s.FlushEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := pageviews(t, s); n != 0 {
		t.Fatalf("held, and %d written", n)
	}
	if st := s.EventCollectorStats(); !st.Held || st.Buffered != 3 {
		t.Fatalf("held: panel says held %v, buffered %d", st.Held, st.Buffered)
	}
	held = false
	if err := s.FlushEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := pageviews(t, s); n != 3 {
		t.Errorf("released, %d written; want 3", n)
	}
}
