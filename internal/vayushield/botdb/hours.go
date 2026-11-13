// SPDX-License-Identifier: Apache-2.0

package botdb

import (
	"context"
	"sort"
	"sync"
	"time"
)

// hourSettle is how long after an hour ends its count is taken as final.
// Events are written through a queue, so one recorded at 10:59:59 can land a
// moment after 11:00; an hour is kept only once that moment has passed.
const hourSettle = 5 * time.Minute

// hourCache is the install's refusals and challenges for each finished hour.
//
// Shield's chart counts a fortnight an hour. Counting it from the events on
// every visit grew with the fortnight's traffic, and a finished hour's count
// never changes, since every event is written for the moment it happens. So
// the finished hours are counted once and kept, and a visit counts only the
// hours since.
type hourCache struct {
	mu   sync.Mutex
	from time.Time // the first hour kept
	upto time.Time // the first hour not yet kept
	done map[string]HourBucket
	now  func() time.Time // the clock; time.Now unless a test sets one
}

// ReadHours is the whole install's refusals and challenges an hour over the
// last hours (whole hours, the first one in full), and nothing else of the
// trail: what a chart a day needs.
func (s *Store) ReadHours(ctx context.Context, hours int) ([]HourBucket, error) {
	if s == nil || s.reader() == nil {
		return nil, nil
	}
	c := &s.hours
	now := time.Now().UTC()
	if c.now != nil {
		now = c.now().UTC()
	}
	start := now.Add(-time.Duration(hours) * time.Hour).Truncate(time.Hour)
	settled := now.Add(-hourSettle).Truncate(time.Hour)

	c.mu.Lock()
	defer c.mu.Unlock()
	// A window reaching further back than what is kept starts the keeping
	// again from there, rather than leave a gap at its start.
	if c.done == nil || start.Before(c.from) {
		c.done, c.from, c.upto = map[string]HourBucket{}, start, start
	}
	if c.upto.Before(settled) {
		kept, err := s.hourly(ctx, ` WHERE created_at>=? AND created_at<?`, []any{sqliteTime(c.upto), sqliteTime(settled)})
		if err != nil {
			return nil, err
		}
		for _, b := range kept {
			c.done[b.Hour] = b
		}
		c.upto = settled
	}
	live, err := s.hourly(ctx, ` WHERE created_at>=?`, []any{sqliteTime(c.upto)})
	if err != nil {
		return nil, err
	}

	first := start.Format("2006-01-02 15:00")
	out := make([]HourBucket, 0, len(c.done)+len(live))
	for h, b := range c.done {
		switch {
		case h < first:
			delete(c.done, h) // out of every window from now on
		default:
			out = append(out, b)
		}
	}
	if start.After(c.from) {
		c.from = start
	}
	out = append(out, live...)
	sort.Slice(out, func(i, j int) bool { return out[i].Hour < out[j].Hour })
	return out, nil
}
