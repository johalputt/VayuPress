// SPDX-License-Identifier: Apache-2.0

package analytics

// collectqueue.go — batched ingest for the legacy extended /collect path.
//
// Collect (the synchronous writer) executes 2..26 statements per beacon, each
// on the SINGLE SQLite write connection: a session probe, a session upsert,
// the pageview row, one row per event-data property. Under load that serialises
// every beacon behind every other and starves everything else that needs the
// writer (2025 audit A2). The engagement store already proved the fix shape:
// buffer in memory on the request goroutine — cheap CPU only — and let one
// goroutine apply whole batches in single transactions.
//
// One transaction per chunk instead of 2-26 per beacon inverts the scaling
// again: write cost is bounded by distinct visitors per flush interval, not by
// traffic. The HTTP handler switches to CollectAsync; Collect stays synchronous
// for tests and administrative imports so both paths share identical
// normalisation code.

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// evFlushInterval bounds dashboard staleness for collected events.
	evFlushInterval = 2 * time.Second

	// evFlushTimeout is the deadline on one flush; this file must never
	// reproduce the unbounded-queue defect it exists to remove.
	evFlushTimeout = 15 * time.Second

	// evQueueCap bounds buffered events by COUNT. Past it beacons are dropped
	// and counted — losing some analytics under an extreme burst beats losing
	// the site.
	evQueueCap = 8192

	// evFlushChunk bounds rows in one transaction.
	evFlushChunk = 256
)

// eventSeq disambiguates event IDs built inside the same nanosecond (Windows
// coarse clocks make that real). Appended to the monotonic timestamp.
var eventSeq atomic.Uint64

// collectEvent is one fully-normalised beacon ready to write.
type collectEvent struct {
	sessionArgs []any // INSERT OR IGNORE INTO analytics_sessions VALUES(...)
	pvArgs      []any // INSERT INTO analytics_pageviews VALUES(...)
	eventRows   [][]any
}

// evCollector buffers collected events between flushes.
type evCollector struct {
	mu sync.Mutex
	q  []collectEvent

	dropped   int64
	flushed   int64
	writes    int64
	lastFlush time.Time
	lastErr   string
	running   bool
}

// CollectAsync normalises a beacon exactly like Collect and queues it without
// touching the database or blocking on anything but a short mutex. Safe to
// call from request goroutines at full traffic.
func (s *Store) CollectAsync(req CollectRequest, ip, ua, domainID string) {
	if s == nil || s.evq == nil {
		return
	}
	ev := buildCollectEvent(req, ip, ua, domainID)
	if ev == nil {
		return
	}
	c := s.evq
	c.mu.Lock()
	if len(c.q) < evQueueCap {
		c.q = append(c.q, *ev)
	} else {
		c.dropped++
	}
	c.mu.Unlock()
}

// buildCollectEvent mirrors Collect's normalisation step for step. Returns nil
// when the beacon must be dropped (no usable identity).
func buildCollectEvent(req CollectRequest, ip, ua, domainID string) *collectEvent {
	path := normalizePathExtended(req.URL)
	query := ""
	if i := strings.IndexAny(req.URL, "?#"); i >= 0 && i+1 < len(req.URL) {
		query = req.URL[i+1:]
		if len(query) > 512 {
			query = query[:512]
		}
	}
	host := strings.TrimSpace(req.Hostname)
	vid := visitorID(ip, ua, host)
	if vid == "" {
		return nil // fail closed: no safe pseudonym exists
	}
	sid := sessionID(vid)
	now := time.Now().UTC()

	et := req.EventType
	if et != 2 {
		et = 1
	}
	name := trunc(req.EventName, 200)
	eventID := "e" + itoa64(now.UnixNano()) + "-" + itoa64(int64(eventSeq.Add(1)))

	ev := &collectEvent{
		sessionArgs: []any{sid, vid, coarseBrowser(ua), coarseOS(ua), coarseDevice(ua), "", "",
			trunc(req.Geo.Country, 2), trunc(req.Geo.Region, 80), trunc(req.Geo.City, 120), now},
		pvArgs: []any{eventID, sid, path, query, trunc(req.PageTitle, 300), referrerHostExtended(req.Referrer), trunc(host, 200),
			trunc(req.UTMSource, 100), trunc(req.UTMMedium, 100), trunc(req.UTMCampaign, 100),
			trunc(req.UTMContent, 100), trunc(req.UTMTerm, 100), et, name, now, domainID},
	}
	if et == 2 && len(req.EventData) > 0 {
		n := 0
		for k, v := range req.EventData {
			if n >= maxEventDataProps {
				break
			}
			n++
			ev.eventRows = append(ev.eventRows, []any{eventID, trunc(k, 100), trunc(v, 500), now})
		}
	}
	return ev
}

// StartEventCollector drains the queue until ctx is cancelled, then applies a
// final flush. Double start is a no-op, like StartCollector.
func (s *Store) StartEventCollector(ctx context.Context) {
	if s == nil || s.evq == nil {
		return
	}
	c := s.evq
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.mu.Unlock()

	go func() {
		t := time.NewTicker(evFlushInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				fctx, cancel := context.WithTimeout(context.Background(), evFlushTimeout)
				_ = s.FlushEvents(fctx)
				cancel()
				c.mu.Lock()
				c.running = false
				c.mu.Unlock()
				return
			case <-t.C:
				fctx, cancel := context.WithTimeout(context.Background(), evFlushTimeout)
				_ = s.FlushEvents(fctx)
				cancel()
			}
		}
	}()
}

// FlushEvents applies every queued event inside chunk-sized transactions.
func (s *Store) FlushEvents(ctx context.Context) error {
	c := s.evq
	if c == nil {
		return nil
	}
	for {
		c.mu.Lock()
		n := len(c.q)
		if n > evFlushChunk {
			n = evFlushChunk
		}
		batch := c.q[:n]
		c.q = c.q[n:]
		c.mu.Unlock()
		if n == 0 {
			return nil
		}
		err := s.applyCollectBatch(ctx, batch)
		c.mu.Lock()
		c.writes += int64(n)
		c.lastFlush = time.Now().UTC()
		if err != nil {
			c.lastErr = err.Error()
		} else {
			c.flushed += int64(n)
			c.lastErr = ""
		}
		c.mu.Unlock()
		if err != nil {
			return err
		}
	}
}

func (s *Store) applyCollectBatch(ctx context.Context, batch []collectEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	ss, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO analytics_sessions(id,visitor_id,browser,os,device,screen,language,country,region,city,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer ss.Close()
	pv, err := tx.PrepareContext(ctx, `INSERT INTO analytics_pageviews(id,session_id,url_path,url_query,page_title,referrer,hostname,utm_source,utm_medium,utm_campaign,utm_content,utm_term,event_type,event_name,created_at,domain_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer pv.Close()
	var ed *sql.Stmt
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO analytics_event_data(event_id,property_key,property_value,created_at) VALUES(?,?,?,?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	ed = stmt
	defer ed.Close()
	for i := range batch {
		if _, err := ss.ExecContext(ctx, batch[i].sessionArgs...); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := pv.ExecContext(ctx, batch[i].pvArgs...); err != nil {
			_ = tx.Rollback()
			return err
		}
		for _, r := range batch[i].eventRows {
			if _, err := ed.ExecContext(ctx, r...); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
	}
	return tx.Commit()
}

// EventCollectorStats reports the queue's state for the panel.
func (s *Store) EventCollectorStats() CollectorState {
	if s == nil || s.evq == nil {
		return CollectorState{BufferedHi: evQueueCap}
	}
	c := s.evq
	c.mu.Lock()
	defer c.mu.Unlock()
	return CollectorState{
		Running:    c.running,
		Buffered:   len(c.q),
		BufferedHi: evQueueCap,
		Dropped:    c.dropped,
		Flushed:    c.flushed,
		Writes:     c.writes,
		LastFlush:  c.lastFlush,
		LastErr:    c.lastErr,
	}
}

// itoa64 renders n in decimal without importing strconv for three call sites.
func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
