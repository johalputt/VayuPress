// SPDX-License-Identifier: Apache-2.0

package ads

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

// Counts are how often a slot was seen and clicked.
type Counts struct{ Seen, Clicks int64 }

type countKey struct{ site, slot, day string }

// maxPendingCounts bounds what the counter holds between flushes: past it a
// new slot-day is dropped rather than letting a flood of beacons grow memory.
const maxPendingCounts = 20000

// Counter gathers ad impressions and clicks in memory and writes them in
// batches (Flush), so a busy page does not queue a write per reader on the
// single database writer.
type Counter struct {
	db      *sql.DB
	mu      sync.Mutex
	pending map[countKey]Counts
	dropped int64
}

// NewCounter returns a Counter writing to db's ad_events (migration 132).
func NewCounter(db *sql.DB) *Counter { return &Counter{db: db, pending: map[countKey]Counts{}} }

// Add counts seen impressions and clicks of one of site's slots, on now's day.
func (c *Counter) Add(site, slot string, seen, clicks int64, now time.Time) {
	k := countKey{site, slot, now.UTC().Format("2006-01-02")}
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.pending[k]
	if !ok && len(c.pending) >= maxPendingCounts {
		c.dropped++
		return
	}
	cur.Seen += seen
	cur.Clicks += clicks
	c.pending[k] = cur
}

// Flush writes what the counter holds. What could not be written is kept
// for the next flush.
func (c *Counter) Flush(ctx context.Context) error {
	c.mu.Lock()
	batch := c.pending
	c.pending = map[countKey]Counts{}
	c.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		c.keep(batch)
		return err
	}
	for k, v := range batch {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO ad_events(slot_id,domain_id,day,seen,clicks) VALUES(?,?,?,?,?)
			 ON CONFLICT(slot_id,day) DO UPDATE SET seen=seen+excluded.seen,clicks=clicks+excluded.clicks`,
			k.slot, k.site, k.day, v.Seen, v.Clicks); err != nil {
			_ = tx.Rollback()
			c.keep(batch)
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		c.keep(batch)
		return err
	}
	return nil
}

func (c *Counter) keep(batch map[countKey]Counts) {
	for k, v := range batch {
		c.Add(k.site, k.slot, v.Seen, v.Clicks, mustDay(k.day))
	}
}

func mustDay(day string) time.Time {
	t, _ := time.Parse("2006-01-02", day)
	return t
}

// Totals are site's slots' counts over the days days up to now, by slot id.
func (s *Store) Totals(ctx context.Context, site string, days int, now time.Time) (map[string]Counts, error) {
	since := now.UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx,
		`SELECT slot_id,SUM(seen),SUM(clicks) FROM ad_events WHERE domain_id=? AND day>=? GROUP BY slot_id`, site, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Counts{}
	for rows.Next() {
		var id string
		var c Counts
		if err := rows.Scan(&id, &c.Seen, &c.Clicks); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

// EnabledIDs are site's enabled slot ids: the only ids a beacon from that
// site's pages may count.
func (s *Store) EnabledIDs(ctx context.Context, site string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM ad_slots WHERE domain_id=? AND enabled=1`, site)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
