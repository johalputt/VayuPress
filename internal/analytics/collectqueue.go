// SPDX-License-Identifier: Apache-2.0

package analytics

// collectqueue.go — batched ingest for the legacy extended /collect path.
//
// Collect (the synchronous writer) executes 2..26 statements per beacon, each
// on the SINGLE SQLite write connection: a session probe, a session upsert,
// the pageview row, one row per event-data property. Under load that serialises
// every beacon behind every other and starves everything else that needs the
// writer. The engagement store already proved the fix shape:
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

	"github.com/johalputt/vayupress/internal/dbbatch"
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

// idLen is the stored length of a visit or visitor ID, in hex characters.
const idLen = 32

// eventSeq disambiguates event IDs built inside the same nanosecond (Windows
// coarse clocks make that real). Appended to the monotonic timestamp.
var eventSeq atomic.Uint64

// collectEvent is one fully-normalised beacon ready to write.
type collectEvent struct {
	sessionArgs []any // the visit's upsert into analytics_sessions
	pvArgs      []any // INSERT INTO analytics_pageviews VALUES(...)
	eventRows   [][]any
}

// evCollector buffers collected events between flushes.
type evCollector struct {
	mu sync.Mutex
	q  []collectEvent

	dropped   int64
	lost      int64
	flushed   int64
	writes    int64
	lastFlush time.Time
	lastErr   string
	running   bool
	retries   dbbatch.Retries

	// flushing serialises flushes: the batch at the front stays in q until
	// it is written, so two flushes would write it twice.
	flushing sync.Mutex
}

// CollectAsync normalises a beacon exactly like Collect and queues it without
// touching the database or blocking on anything but a short mutex. Safe to
// call from request goroutines at full traffic.
func (s *Store) CollectAsync(req CollectRequest, ip, ua, domainID string) {
	if s == nil || s.evq == nil {
		return
	}
	ev := s.buildCollectEvent(req, ip, ua, domainID)
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

// buildCollectEvent normalises one beacon and decides its visit. Returns nil
// when the event must not be stored: no identity can be derived (no salt), or
// it is the visit's last page view delivered again.
func (s *Store) buildCollectEvent(req CollectRequest, ip, ua, domainID string) *collectEvent {
	path := NormalizePath(req.URL)
	query := ""
	if i := strings.IndexAny(req.URL, "?#"); i >= 0 && i+1 < len(req.URL) {
		query = req.URL[i+1:]
		if len(query) > 512 {
			query = query[:512]
		}
	}
	host := strings.TrimSpace(req.Hostname)
	now := s.now().UTC()

	et := req.EventType
	name := trunc(req.EventName, 200)
	switch {
	case req.search != nil:
		et = eventSearch
	case et == eventOutbound || et == eventDownload:
		target, ok := linkTarget(req.EventName, host, et == eventDownload)
		if !ok {
			return nil
		}
		name = target
	case et != 2:
		et = 1
	}
	hit := s.visits.Observe(domainID, ip, ua, req.Lang, path, et == 1, now)
	if hit.Session == "" || hit.Repeat {
		return nil
	}
	// 128 bits of each hash is stored, not 256: the IDs are on every row and
	// in three indexes, and on johal.in's volume the other half was ~30 MB a
	// copy with nothing to gain (a collision needs ~2^64 visits).
	hit.Session, hit.Visitor = hit.Session[:idLen], hit.Visitor[:idLen]
	view := 0
	landing := ""
	if et == 1 {
		view, landing = 1, path
	}
	eventID := "e" + itoa64(now.UnixNano()) + "-" + itoa64(int64(eventSeq.Add(1)))

	ev := &collectEvent{
		sessionArgs: []any{hit.Session, hit.Visitor, coarseBrowser(ua), coarseOS(ua), coarseDevice(ua), "", "",
			trunc(req.Geo.Country, 2), trunc(req.Geo.Region, 80), trunc(req.Geo.City, 120), now,
			domainID, now, view, landing},
		pvArgs: []any{eventID, hit.Session, path, query, trunc(req.PageTitle, 300), referrerHostExtended(req.Referrer), trunc(host, 200),
			trunc(req.UTMSource, 100), trunc(req.UTMMedium, 100), trunc(req.UTMCampaign, 100),
			trunc(req.UTMContent, 100), trunc(req.UTMTerm, 100), et, name, now, domainID, hit.Visitor},
	}
	if req.search != nil {
		ev.eventRows = append(ev.eventRows, searchResults(req.search, now, eventID))
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
//
// A chunk stays at the front of the queue until it is written. One the
// database refuses (a lock held past the deadline, a failing disk) is tried
// again on the next flush, and after dbbatch.MaxAttempts refusals in a row
// discarded and counted lost. While the install refuses writes (HoldWhile)
// nothing is tried, and the queue fills to its cap, past which beacons are
// dropped and counted.
func (s *Store) FlushEvents(ctx context.Context) error {
	c := s.evq
	if c == nil || s.held() {
		return nil
	}
	c.flushing.Lock()
	defer c.flushing.Unlock()
	for {
		c.mu.Lock()
		n := len(c.q)
		if n > evFlushChunk {
			n = evFlushChunk
		}
		// Beacons arriving while this chunk is written are appended after it,
		// so the chunk's elements are not moved by anyone but this flush.
		batch := c.q[:n:n]
		c.mu.Unlock()
		if n == 0 {
			return nil
		}
		err := s.applyCollectBatch(ctx, batch)
		c.mu.Lock()
		c.lastFlush = time.Now().UTC()
		switch {
		case err == nil:
			c.q = c.q[n:]
			c.retries.OK()
			c.writes += int64(n)
			c.flushed += int64(n)
			c.lastErr = ""
		case c.retries.Failed():
			c.q = c.q[n:]
			c.lost += int64(n)
			c.lastErr = err.Error()
		default:
			c.lastErr = err.Error()
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
	// The visit row opens on its first event and is brought up to date by
	// every later one: the overview reads visits, bounces, lengths and
	// landing pages from it (migration 113). A visit that opened on a custom
	// event lands on its first page view.
	ss, err := tx.PrepareContext(ctx, `INSERT INTO analytics_sessions(id,visitor_id,browser,os,device,screen,language,country,region,city,created_at,domain_id,last_at,events,views,landing) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,1,?,?)
		ON CONFLICT(id) DO UPDATE SET last_at=MAX(COALESCE(last_at,excluded.last_at),excluded.last_at), events=events+1, views=views+excluded.views,
		  landing=CASE WHEN landing='' THEN excluded.landing ELSE landing END`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer ss.Close()
	pv, err := tx.PrepareContext(ctx, `INSERT INTO analytics_pageviews(id,session_id,url_path,url_query,page_title,referrer,hostname,utm_source,utm_medium,utm_campaign,utm_content,utm_term,event_type,event_name,created_at,domain_id,visitor_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
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
		Lost:       c.lost,
		Held:       s.held(),
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
