// SPDX-License-Identifier: Apache-2.0

package vayukeep

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johalputt/vayupress/internal/pacedio"
)

// countingPacer allows one chunk per ask and counts the asks.
type countingPacer struct {
	mu    sync.Mutex
	calls int
}

func (c *countingPacer) Next(ctx context.Context) (int, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return 1, ctx.Err()
}

// Sealing a generation and reading it back for its test restore both ask the
// pacer as they go, one 256 KiB chunk at a time here, so neither is one burst
// the host cannot yield from. 4 MiB of media is at least sixteen asks each.
func TestSealingAndTheTestRestoreAskThePacerAsTheyGo(t *testing.T) {
	var pacers []*countingPacer
	h := newHarness(t, func(c *Config) {
		c.Pace = func() pacedio.Pacer {
			p := &countingPacer{}
			pacers = append(pacers, p)
			return p
		}
	})
	media := make([]byte, 4<<20)
	if _, err := rand.Read(media); err != nil { // incompressible, so the archive is as large
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.dataDir, "media.bin"), media, 0o640); err != nil {
		t.Fatal(err)
	}
	res := h.engine.BackupNow(context.Background())
	if !res.OK {
		t.Fatalf("the paced backup failed: %s", res.Err)
	}
	if len(pacers) != 2 {
		t.Fatalf("%d pacers were started; want one for the seal and one for the test restore", len(pacers))
	}
	for i, what := range []string{"sealing", "the test restore"} {
		if pacers[i].calls < 16 {
			t.Errorf("%s asked the pacer %d times for 4 MiB in 256 KiB chunks", what, pacers[i].calls)
		}
	}
}
