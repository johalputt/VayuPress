// SPDX-License-Identifier: Apache-2.0

package analytics

// recorder.go — counting a page view without queueing behind the write lock.
//
// # The outage this exists to prevent
//
// Every public page view used to do this:
//
//	go func() { a.analytics.Record(context.Background(), scope, path, ref) }()
//
// Three properties of that line combine into a site-wide stall.
//
//  1. The writer pool is capped at ONE connection (db.go: SetMaxOpenConns(1)),
//     because SQLite has one writer.
//  2. context.Background() has a nil Done channel, so database/sql waits for a
//     free connection FOREVER. Not busy_timeout — that governs SQLite once you
//     hold a connection. This is the queue in front of it, and it has no
//     deadline.
//  3. Nothing bounds how many of those goroutines exist. One per view.
//
// So whenever anything holds the write connection for a while — a helper
// process, a long checkpoint, a slow fsync — every view arriving during that
// window parks a goroutine in an unbounded queue. The stall then OUTLIVES its
// own cause: once the connection frees, the backlog drains one upsert at a
// time through a single connection, and everything that genuinely needs to
// write (a sign-in, an admin action, an MCP tool call) is stuck behind it.
//
// That is why the reported outage lasted minutes when the thing that triggered
// it lasted seconds, why it resolved with no restart and nothing in the log,
// and why it hurt more on the busiest sites: outage length scales with traffic,
// because traffic is what fills the queue.
//
// # What replaces it
//
// Counting a view is an UPSERT increment on (day, domain, path). A thousand
// views of one page is a thousand statements that could have been one statement
// adding a thousand. So the recorder buffers in memory, keyed exactly the way
// the table is keyed, and a single goroutine flushes the totals on a tick.
//
// The result inverts the old scaling. Write volume is now bounded by the number
// of DISTINCT pages viewed per interval, not by traffic — a page under heavy
// load costs one row-update every few seconds no matter how many people read
// it. The busiest install writes least per view.
//
// Three properties are worth stating because they are what make it safe:
//
//   - RecordAsync never touches the database and never blocks. It takes a
//     mutex, increments an int, and returns.
//   - The buffer is bounded. Past the cap it DROPS and counts drops, which is
//     visible on the panel. Losing a view count is a rounding error; losing the
//     site is an outage.
//   - Every flush carries a deadline. Nothing in this file can wait forever,
//     which was the whole defect.

import (
	"context"
	"sync"
	"time"
)

// Flush cadence and bounds.
const (
	// flushInterval bounds how stale the dashboard can be, and sets the write
	// rate: at most one flush per interval regardless of traffic.
	flushInterval = 5 * time.Second

	// flushTimeout is the deadline on a flush. It exists so that this package
	// can never reproduce the defect it was written to remove: a write from
	// here waits a bounded time and then gives up, whatever else holds the
	// connection.
	flushTimeout = 15 * time.Second

	// maxPendingKeys bounds the buffer by DISTINCT keys, not by view volume.
	// A million views of one page is one key. Reaching this cap means a million
	// distinct paths in five seconds, which is a scan, not an audience.
	maxPendingKeys = 20000

	// flushChunk bounds how many rows one transaction covers. A flush must not
	// become the stall it prevents, so a very large batch is split and the
	// connection released between chunks.
	flushChunk = 500

	// maxHostLen is the longest referrer host that will be buffered — the DNS
	// limit. See RecordAsync for why this is a discard and not a truncation.
	maxHostLen = 253
)

type viewKey struct{ day, scope, path string }
type refKey struct{ day, scope, host string }

// collector is the in-memory tally between flushes.
type collector struct {
	mu    sync.Mutex
	views map[viewKey]int64
	refs  map[refKey]int64

	// Counters for the panel. Guarded by mu so a reader sees a consistent set.
	dropped   int64
	flushed   int64
	writes    int64
	lastFlush time.Time
	lastErr   string
	running   bool
}

func newCollector() *collector {
	return &collector{
		views: make(map[viewKey]int64),
		refs:  make(map[refKey]int64),
	}
}

// RecordAsync counts one page view. It never touches the database, never
// allocates a goroutine and never blocks on anything but a mutex held for the
// duration of a map increment.
//
// scope must already be resolved from the request, on the request goroutine —
// the same contract Record documents, and for the same reason: attribution
// derived from anything a visitor sends is attribution a visitor can forge.
func (s *Store) RecordAsync(scope, path, referrer string) {
	if s == nil || s.coll == nil {
		return
	}
	path = normalizePath(path)
	if path == "" {
		return
	}
	day := time.Now().UTC().Format("2006-01-02")

	// The referrer host comes from a header the VISITOR controls, and buffering
	// changed what that costs. Previously it went straight to SQLite, so a
	// preposterous value bloated a table; now it is held in memory until the next
	// flush, and 20,000 distinct keys each carrying a header-sized "host" is a
	// memory-exhaustion path that this file would have introduced.
	//
	// A DNS name cannot exceed 253 octets. Anything longer is not a host that
	// exists, so it is discarded rather than truncated — truncating would invent
	// a hostname and attribute real traffic to it.
	host := referrerHost(referrer)
	if len(host) > maxHostLen {
		host = ""
	}

	c := s.coll
	c.mu.Lock()
	defer c.mu.Unlock()

	// The cap is checked per map and only for keys that are NOT already
	// present: an existing key costs no additional memory, so a site under
	// sustained load on a fixed set of pages never drops.
	vk := viewKey{day: day, scope: scope, path: path}
	if _, ok := c.views[vk]; ok || len(c.views) < maxPendingKeys {
		c.views[vk]++
	} else {
		c.dropped++
	}
	if host != "" {
		rk := refKey{day: day, scope: scope, host: host}
		if _, ok := c.refs[rk]; ok || len(c.refs) < maxPendingKeys {
			c.refs[rk]++
		} else {
			c.dropped++
		}
	}
}

// StartCollector runs the flusher until ctx is cancelled, then flushes once
// more so a clean shutdown does not discard the last interval's counts.
//
// Calling it twice is a no-op on the second call; the tally is a single shared
// buffer and two flushers would race to halve each other's batches.
func (s *Store) StartCollector(ctx context.Context) {
	if s == nil || s.coll == nil {
		return
	}
	c := s.coll
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.mu.Unlock()

	go func() {
		t := time.NewTicker(flushInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				// A detached context: the parent is already cancelled, and a
				// final flush that inherited it would be dead on arrival.
				fctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
				_ = s.Flush(fctx)
				cancel()
				c.mu.Lock()
				c.running = false
				c.mu.Unlock()
				return
			case <-t.C:
				fctx, cancel := context.WithTimeout(ctx, flushTimeout)
				_ = s.Flush(fctx)
				cancel()
			}
		}
	}()
}

// Flush writes the buffered totals. It is safe to call directly (tests, and the
// final flush at shutdown) and returns the first error it hit, having still
// attempted the rest.
//
// The buffer is swapped out under the lock and written outside it, so a slow
// database delays the write and never the requests feeding it.
func (s *Store) Flush(ctx context.Context) error {
	if s == nil || s.coll == nil {
		return nil
	}
	c := s.coll

	c.mu.Lock()
	views, refs := c.views, c.refs
	if len(views) == 0 && len(refs) == 0 {
		c.lastFlush = time.Now()
		c.mu.Unlock()
		return nil
	}
	c.views, c.refs = make(map[viewKey]int64), make(map[refKey]int64)
	c.mu.Unlock()

	var firstErr error
	rows := 0
	total := int64(0)

	// Chunked so one enormous batch cannot hold the single write connection for
	// an unbounded time — the exact failure mode this file removes.
	batch := make([]queryArgs, 0, flushChunk)
	flushBatch := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.execBatch(ctx, batch); err != nil && firstErr == nil {
			firstErr = err
		}
		batch = batch[:0]
	}

	for k, n := range views {
		batch = append(batch, queryArgs{
			Query: `INSERT INTO analytics_daily(day,domain_id,path,views) VALUES(?,?,?,?)
			    ON CONFLICT(day,domain_id,path) DO UPDATE SET views=views+excluded.views`,
			Args: []interface{}{k.day, k.scope, k.path, n},
		})
		rows++
		total += n
		if len(batch) >= flushChunk {
			flushBatch()
		}
	}
	for k, n := range refs {
		batch = append(batch, queryArgs{
			Query: `INSERT INTO analytics_referrers(day,domain_id,host,hits) VALUES(?,?,?,?)
			    ON CONFLICT(day,domain_id,host) DO UPDATE SET hits=hits+excluded.hits`,
			Args: []interface{}{k.day, k.scope, k.host, n},
		})
		rows++
		if len(batch) >= flushChunk {
			flushBatch()
		}
	}
	flushBatch()

	c.mu.Lock()
	c.lastFlush = time.Now()
	c.flushed += total
	c.writes += int64(rows)
	if firstErr != nil {
		c.lastErr = firstErr.Error()
	} else {
		c.lastErr = ""
	}
	c.mu.Unlock()
	return firstErr
}

// queryArgs is one statement in a flush batch.
type queryArgs struct {
	Query string
	Args  []interface{}
}

// execBatch runs one chunk inside a single transaction, so a chunk costs ONE
// lock acquisition and one commit rather than one of each per row.
func (s *Store) execBatch(ctx context.Context, qs []queryArgs) error {
	if len(qs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, q := range qs {
		if _, err := tx.ExecContext(ctx, q.Query, q.Args...); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// CollectorState is what the panel shows about view counting.
type CollectorState struct {
	Running    bool
	Buffered   int   // distinct keys awaiting a flush
	BufferedHi int   // the cap, so a reader can see how close it is
	Pending    int64 // views awaiting a flush
	Dropped    int64 // views discarded because the buffer was full
	Flushed    int64 // views written since boot
	Writes     int64 // statements used to write them — the compression ratio
	LastFlush  time.Time
	LastErr    string
}

// CollectorStats reports the recorder's state for the panel.
//
// Running is included because a recorder that buffers and never flushes loses
// every view silently, which is the same class of defect as a setting that is
// writable but unread. It must be visible, not assumed.
func (s *Store) CollectorStats() CollectorState {
	if s == nil || s.coll == nil {
		return CollectorState{BufferedHi: maxPendingKeys}
	}
	c := s.coll
	c.mu.Lock()
	defer c.mu.Unlock()
	var pending int64
	for _, n := range c.views {
		pending += n
	}
	return CollectorState{
		Running:    c.running,
		Buffered:   len(c.views) + len(c.refs),
		BufferedHi: maxPendingKeys,
		Pending:    pending,
		Dropped:    c.dropped,
		Flushed:    c.flushed,
		Writes:     c.writes,
		LastFlush:  c.lastFlush,
		LastErr:    c.lastErr,
	}
}
