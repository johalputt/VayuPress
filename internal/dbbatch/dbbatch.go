// SPDX-License-Identifier: Apache-2.0

// Package dbbatch provides a bounded, batched, best-effort background writer for
// high-frequency, low-value telemetry writes (analytics beacons, bot-signature
// sightings, challenge/block records).
//
// VayuPress uses a single SQLite writer connection. Writing telemetry
// synchronously on the request path turns normal — and especially attack —
// traffic into a storm of tiny fsync'd transactions that saturates that one
// writer and starves everything else (the dynamic VayuOS admin pages in
// particular). A Writer instead accepts write closures on a bounded buffer and a
// single background goroutine flushes them in BATCHED transactions (one fsync
// per batch), so the writer stays free and the request path never blocks.
//
// It is deliberately best-effort: if the buffer is full (an extreme flood) the
// op is dropped and counted rather than allowed to block a request or overwhelm
// the database, and a failed batch is discarded — telemetry must never take the
// site down.
package dbbatch

import (
	"context"
	"database/sql"
	"sync/atomic"
	"time"
)

// Op is a single write, run inside a shared batched transaction. It must use the
// provided ctx (NOT a request context, which would already be cancelled) and
// must not commit/rollback — the Writer owns the transaction.
type Op func(ctx context.Context, tx *sql.Tx) error

// Writer batches Ops into periodic transactions on a single background goroutine.
type Writer struct {
	db       *sql.DB
	ch       chan Op
	maxBatch int
	interval time.Duration
	txWindow time.Duration
	dropped  atomic.Int64
}

// New creates a Writer. Sensible defaults are applied for non-positive values
// (buffer 4096, maxBatch 256, interval 1s).
func New(db *sql.DB, buffer, maxBatch int, interval time.Duration) *Writer {
	if buffer <= 0 {
		buffer = 4096
	}
	if maxBatch <= 0 {
		maxBatch = 256
	}
	if interval <= 0 {
		interval = time.Second
	}
	return &Writer{
		db:       db,
		ch:       make(chan Op, buffer),
		maxBatch: maxBatch,
		interval: interval,
		txWindow: 30 * time.Second,
	}
}

// Submit enqueues an op without blocking. If the buffer is full the op is dropped
// and counted (see Dropped). Safe on a nil Writer (no-op).
func (w *Writer) Submit(op Op) {
	if w == nil || op == nil {
		return
	}
	select {
	case w.ch <- op:
	default:
		w.dropped.Add(1)
	}
}

// Dropped reports how many ops were shed because the buffer was full.
func (w *Writer) Dropped() int64 {
	if w == nil {
		return 0
	}
	return w.dropped.Load()
}

// Run drives the flush loop until done is closed, at which point it drains and
// flushes whatever is buffered and returns. Launch it in its own goroutine once.
func (w *Writer) Run(done <-chan struct{}) {
	if w == nil || w.db == nil {
		return
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	batch := make([]Op, 0, w.maxBatch)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		w.flush(batch)
		batch = batch[:0]
	}
	for {
		select {
		case <-done:
			for {
				select {
				case op := <-w.ch:
					batch = append(batch, op)
					if len(batch) >= w.maxBatch {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case op := <-w.ch:
			batch = append(batch, op)
			if len(batch) >= w.maxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// flush applies a batch in one transaction. Each op is isolated from a panic so
// one bad op can neither abort the batch nor crash the process; a failed commit
// is rolled back and the batch discarded (best-effort).
func (w *Writer) flush(batch []Op) {
	ctx, cancel := context.WithTimeout(context.Background(), w.txWindow)
	defer cancel()
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	for _, op := range batch {
		func() {
			defer func() { _ = recover() }()
			_ = op(ctx, tx)
		}()
	}
	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
	}
}
