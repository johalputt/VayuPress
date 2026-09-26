// SPDX-License-Identifier: Apache-2.0

// Package pacedio streams bytes at the pace a pacer allows: before each chunk
// the pacer is asked how many chunks may follow, and it blocks while the host
// is busy. Backups (VayuKeep), the pre-update backup and the export stream
// through it, so a large copy yields to visitors between any two chunks.
//
// A leaf package on purpose: VayuKeep's engine must not depend on a SQL driver,
// and the pacer that reads the host (internal/pace) does.
package pacedio

import (
	"context"
	"io"
	"math"
)

// Pacer is asked before each step how large it may be, and blocks while the
// host is busy. *pace.Job is one.
type Pacer interface {
	Next(ctx context.Context) (int, error)
}

// FullSpeed is a pacer that never waits, for work an operator runs by hand at
// a terminal and for tests.
type FullSpeed struct{}

// Next allows any size of step.
func (FullSpeed) Next(ctx context.Context) (int, error) { return math.MaxInt32, ctx.Err() }

// Chunk is the unit a pacer's answer counts for a stream: 256 KiB. The
// smallest batch is one chunk, so a busy host still sees the work move.
const Chunk = 256 << 10

// budget spends bytes, asking the pacer for more when it runs out.
type budget struct {
	ctx  context.Context
	p    Pacer
	left int
}

func (b *budget) take(n int) (int, error) {
	if b.left <= 0 {
		chunks, err := b.p.Next(b.ctx)
		if err != nil {
			return 0, err
		}
		b.left = min(max(chunks, 1), math.MaxInt32/Chunk) * Chunk
	}
	n = min(n, b.left)
	b.left -= n
	return n, nil
}

type writer struct {
	w io.Writer
	budget
}

// NewWriter paces writes to w.
func NewWriter(ctx context.Context, w io.Writer, p Pacer) io.Writer {
	return &writer{w: w, budget: budget{ctx: ctx, p: p}}
}

// Write writes p in as many paced pieces as the budget allows, so no single
// large write escapes the pacer.
func (pw *writer) Write(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		n, err := pw.take(len(p) - written)
		if err != nil {
			return written, err
		}
		m, err := pw.w.Write(p[written : written+n])
		written += m
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

type reader struct {
	r io.Reader
	budget
}

// NewReader paces reads from r.
func NewReader(ctx context.Context, r io.Reader, p Pacer) io.Reader {
	return &reader{r: r, budget: budget{ctx: ctx, p: p}}
}

func (pr *reader) Read(p []byte) (int, error) {
	n, err := pr.take(len(p))
	if err != nil {
		return 0, err
	}
	m, err := pr.r.Read(p[:n])
	pr.left += n - m // what was not read stays in the budget
	return m, err
}
