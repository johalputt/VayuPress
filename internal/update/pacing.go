// SPDX-License-Identifier: Apache-2.0

package update

import "github.com/johalputt/vayupress/internal/pacedio"

// Pacing starts the pacers for one backup or export: Pages for copying the
// database (a step is that many database pages, internal/sqlitecopy) and
// Chunks for streaming the archive (a step is that many pacedio.Chunk bytes).
// Two, because the units differ and a pacer grows each step from the last. A
// nil func runs that part at full speed, which the command line and the swap at
// boot want: an operator at a terminal, or no visitor yet to yield to.
type Pacing struct {
	Pages, Chunks func() pacedio.Pacer
}

func (p Pacing) pages() pacedio.Pacer {
	if p.Pages == nil {
		return pacedio.FullSpeed{}
	}
	return p.Pages()
}

func (p Pacing) chunks() pacedio.Pacer {
	if p.Chunks == nil {
		return pacedio.FullSpeed{}
	}
	return p.Chunks()
}
