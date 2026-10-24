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
// the database — telemetry must never take the site down. A batch the
// database refuses is kept and tried again (see Retries), and while the
// install refuses writes it is not tried at all (see Writer.HoldWhile): a
// failing disk or a lock held through one flush used to cost every event in
// the batch, uncounted.
package dbbatch

import (
	"context"
	"database/sql"
	"sync/atomic"
	"time"
)

// MaxAttempts is how many flushes in a row one batch may fail before it is
// discarded and counted as lost: enough to ride out a lock held through a
// flush or two, few enough that a row the database will never take cannot
// hold every row queued behind it.
const MaxAttempts = 3

// Retries counts a queue's consecutive failed flushes of the batch at its
// front. The zero value is ready; the queue's own lock guards it.
type Retries struct{ n int }

// Failed records a failed flush and reports whether the batch is now to be
// discarded (and counted as lost) rather than tried again.
func (r *Retries) Failed() bool {
	r.n++
	if r.n >= MaxAttempts {
		r.n = 0
		return true
	}
	return false
}

// OK records a flush that wrote its batch.
func (r *Retries) OK() { r.n = 0 }

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
	lost     atomic.Int64
	hold     func() bool
	retries  Retries       // the Run goroutine's alone
	stopped  chan struct{} // closed when Run returns
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
		stopped:  make(chan struct{}),
	}
}

// Stopped is closed when Run has returned, its last batch written or counted
// lost, so a shutdown can wait for it before it closes the database.
func (w *Writer) Stopped() <-chan struct{} {
	if w == nil {
		c := make(chan struct{})
		close(c)
		return c
	}
	return w.stopped
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

// HoldWhile makes the writer keep its batch, not write it, while held()
// reports true: the install's mode refuses writes because its disk is
// failing, and a write then is one more write to a failing disk. Ops go on
// queueing until the buffer is full. Call before Run.
func (w *Writer) HoldWhile(held func() bool) {
	if w != nil {
		w.hold = held
	}
}

// Lost reports how many ops were not written: their batch failed
// MaxAttempts flushes in a row or was still refused or held at shutdown, or
// the op itself failed in a batch that was written.
func (w *Writer) Lost() int64 {
	if w == nil {
		return 0
	}
	return w.lost.Load()
}

// Buffered reports how many ops wait in the buffer, and its size.
func (w *Writer) Buffered() (n, capacity int) {
	if w == nil {
		return 0, 0
	}
	return len(w.ch), cap(w.ch)
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
//
// A batch that is held or fails stays the batch: the buffer is not read into
// it while it is full, so a failing database backs ops up into the bounded
// buffer, where an overflow is dropped and counted, never into memory.
func (w *Writer) Run(done <-chan struct{}) {
	if w == nil || w.db == nil {
		return
	}
	defer close(w.stopped)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	batch := make([]Op, 0, w.maxBatch)
	flush := func() {
		if len(batch) == 0 || (w.hold != nil && w.hold()) {
			return
		}
		switch {
		case w.flush(batch):
			w.retries.OK()
		case w.retries.Failed():
			w.lost.Add(int64(len(batch)))
		default:
			return // kept, and tried again on the next tick
		}
		batch = batch[:0]
	}
	for {
		in := w.ch
		if len(batch) >= w.maxBatch {
			in = nil // the batch is full and not yet written
		}
		select {
		case <-done:
			// Shutdown writes what it can, once each; what the database
			// refuses, or the mode still holds, is counted as lost.
			fill := func() {
				for len(batch) < w.maxBatch {
					select {
					case op := <-w.ch:
						batch = append(batch, op)
					default:
						return
					}
				}
			}
			for {
				fill()
				if len(batch) == 0 {
					return
				}
				if (w.hold != nil && w.hold()) || !w.flush(batch) {
					w.lost.Add(int64(len(batch) + len(w.ch)))
					return
				}
				batch = batch[:0]
			}
		case op := <-in:
			batch = append(batch, op)
			if len(batch) >= w.maxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// flush applies a batch in one transaction and reports whether it was
// written. Each op is isolated from a panic so one bad op can neither abort
// the batch nor crash the process; an op that fails or panics in a batch that
// is written is counted as lost. A failed commit is rolled back.
func (w *Writer) flush(batch []Op) bool {
	ctx, cancel := context.WithTimeout(context.Background(), w.txWindow)
	defer cancel()
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return false
	}
	failed := 0
	for _, op := range batch {
		func() {
			defer func() {
				if recover() != nil {
					failed++
				}
			}()
			if op(ctx, tx) != nil {
				failed++
			}
		}()
	}
	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		return false
	}
	w.lost.Add(int64(failed))
	return true
}
