// SPDX-License-Identifier: Apache-2.0

package pace

import (
	"runtime"
	"sync"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/hoststat"
	"github.com/johalputt/vayupress/internal/metrics"
)

// hostOnce builds the one pacer every heavy job shares, so they see the same
// readings and none of them counts another's queueing as its own headroom.
var (
	hostOnce  sync.Once
	hostPacer *Pacer
)

// Host returns the pacer reading this install: the database pools (from the
// stall watches' figures), the kernel's pressure and memory figures, and the
// last two minutes of response times.
func Host() *Pacer {
	hostOnce.Do(func() {
		pool := func(st func() dbpkg.StallState) func() (time.Duration, bool) {
			return func() (time.Duration, bool) {
				s := st()
				return s.WaitDuration, s.MaxOpen > 0
			}
		}
		hostPacer = New(Sources{
			ReadPool:  pool(dbpkg.ReadStall),
			WritePool: pool(dbpkg.WriteStall),
			Pressure:  hoststat.Pressure,
			Load1:     hoststat.Load1,
			CPUs:      runtime.NumCPU(),
			Mem:       hoststat.MemInfo,
			P95: func() (int64, bool) {
				ms := metrics.HTTPLatencyWindow.PercentileOver(95, 2)
				return ms, ms > 0
			},
		})
	})
	return hostPacer
}
