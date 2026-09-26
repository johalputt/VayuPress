// SPDX-License-Identifier: Apache-2.0

package vayukeep

// pace.go — sealing and restoring at the pace the host can spare.
//
// Sealing a generation reads the whole data directory and writes it again,
// encrypted; the restore drill reads it all back. On a large install each is
// minutes of disk bandwidth. Both stream through here, and every chunk asks the
// caller's pacer first, so a backup yields to visitors between any two chunks
// instead of only between whole generations.

import (
	"context"
	"io"
)

// Pacer is asked before each chunk how many chunks may follow, and blocks while
// the host is busy. *pace.Job is one.
type Pacer interface {
	Next(ctx context.Context) (int, error)
}

// paceChunk is the unit a pacer's answer counts: 256 KiB. The pacer's smallest
// batch is one chunk, so a busy host still sees the work move.
const paceChunk = 256 << 10

// pacedIO spends a budget of bytes, asking the pacer for more when it runs out.
type pacedIO struct {
	ctx    context.Context
	p      Pacer
	budget int
}

func (x *pacedIO) take(n int) (int, error) {
	if x.budget <= 0 {
		chunks, err := x.p.Next(x.ctx)
		if err != nil {
			return 0, err
		}
		x.budget = max(chunks, 1) * paceChunk
	}
	n = min(n, x.budget)
	x.budget -= n
	return n, nil
}

type pacedWriter struct {
	w io.Writer
	pacedIO
}

// Write writes p in as many paced pieces as the budget allows, so no single
// large write escapes the pacer.
func (pw *pacedWriter) Write(p []byte) (int, error) {
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

type pacedReader struct {
	r io.Reader
	pacedIO
}

func (pr *pacedReader) Read(p []byte) (int, error) {
	n, err := pr.take(len(p))
	if err != nil {
		return 0, err
	}
	m, err := pr.r.Read(p[:n])
	pr.budget += n - m // what was not read stays in the budget
	return m, err
}

func (e *Engine) pacedWriter(ctx context.Context, w io.Writer) io.Writer {
	if e.cfg.Pace == nil {
		return w
	}
	return &pacedWriter{w: w, pacedIO: pacedIO{ctx: ctx, p: e.cfg.Pace()}}
}

func (e *Engine) pacedReader(ctx context.Context, r io.Reader) io.Reader {
	if e.cfg.Pace == nil {
		return r
	}
	return &pacedReader{r: r, pacedIO: pacedIO{ctx: ctx, p: e.cfg.Pace()}}
}
