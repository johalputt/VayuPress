// SPDX-License-Identifier: Apache-2.0

package ads

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func newCountStore(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	if _, err := s.db.Exec(`CREATE TABLE ad_events(slot_id TEXT NOT NULL,domain_id TEXT NOT NULL DEFAULT '',day TEXT NOT NULL,seen INTEGER NOT NULL DEFAULT 0,clicks INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(slot_id,day))`); err != nil {
		t.Fatal(err)
	}
	return s
}

// Counts add up per slot and day, are read per site over a window, and what
// a failed write held is kept for the next.
func TestAdCountsAddUpPerSiteAndDay(t *testing.T) {
	s := newCountStore(t)
	c := NewCounter(s.db)
	ctx := context.Background()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	c.Add("site-a", "ad_1", 1, 0, now)
	c.Add("site-a", "ad_1", 1, 1, now)
	c.Add("site-a", "ad_1", 5, 0, now.AddDate(0, 0, -40))
	c.Add("site-b", "ad_2", 3, 0, now)
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	c.Add("site-a", "ad_1", 1, 0, now)
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.Totals(ctx, "site-a", 30, now)
	if err != nil {
		t.Fatal(err)
	}
	if got["ad_1"] != (Counts{Seen: 3, Clicks: 1}) || len(got) != 1 {
		t.Errorf("site-a's last 30 days: %+v", got)
	}

	if _, err := s.db.Exec(`DROP TABLE ad_events`); err != nil {
		t.Fatal(err)
	}
	c.Add("site-a", "ad_1", 2, 0, now)
	if err := c.Flush(ctx); err == nil {
		t.Fatal("a write with no table reported success")
	}
	if len(c.pending) != 1 || c.pending[countKey{"site-a", "ad_1", "2026-11-07"}].Seen != 2 {
		t.Errorf("what the failed write held is gone: %+v", c.pending)
	}
}

// A flood of new slot-days is dropped past the bound, not held.
func TestAdCountsAreBounded(t *testing.T) {
	c := NewCounter(nil)
	now := time.Now()
	for i := 0; i < maxPendingCounts+10; i++ {
		c.Add("s", "ad_"+strconv.Itoa(i), 1, 0, now)
	}
	if len(c.pending) != maxPendingCounts || c.dropped != 10 {
		t.Errorf("held %d, dropped %d", len(c.pending), c.dropped)
	}
}
