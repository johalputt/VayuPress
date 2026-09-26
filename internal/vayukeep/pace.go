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

	"github.com/johalputt/vayupress/internal/pacedio"
)

func (e *Engine) pacedWriter(ctx context.Context, w io.Writer) io.Writer {
	if e.cfg.Pace == nil {
		return w
	}
	return pacedio.NewWriter(ctx, w, e.cfg.Pace())
}

func (e *Engine) pacedReader(ctx context.Context, r io.Reader) io.Reader {
	if e.cfg.Pace == nil {
		return r
	}
	return pacedio.NewReader(ctx, r, e.cfg.Pace())
}
