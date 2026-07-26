// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// TestPruneDelivered proves the Outbox auto-clear removes only DELIVERED rows
// older than the cutoff — pending/failed rows and recent delivered rows stay.
func TestPruneDelivered(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	q, err := NewQueue(db, DefaultConfig(), func(context.Context, string, []string, []byte) error { return nil })
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}

	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	recent := now.Add(-1 * time.Hour)
	ins := func(state string, created time.Time) {
		if _, err := db.Exec(`INSERT INTO vayumail_queue(from_addr,to_json,raw,state,created_at) VALUES('a@x.test','["b@y.test"]','x',?,?)`, state, created); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	ins("delivered", old)    // should be pruned
	ins("delivered", recent) // recent → kept
	ins("pending", old)      // not delivered → kept
	ins("failed", old)       // not delivered → kept

	n, err := q.PruneDelivered(context.Background(), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned %d rows, want exactly 1 (the old delivered row)", n)
	}
	var remaining int
	_ = db.QueryRow(`SELECT COUNT(1) FROM vayumail_queue`).Scan(&remaining)
	if remaining != 3 {
		t.Fatalf("%d rows remain, want 3 (recent delivered + pending + failed)", remaining)
	}
}

// TestQueueRetentionSetter pins the runtime setter used by the Outbox control.
func TestQueueRetentionSetter(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.QueueRetentionDays = 30
	e := NewEngine(&cfg, nil, nil)
	if got := e.QueueRetentionDays(); got != 30 {
		t.Fatalf("initial retention = %d, want 30 (from config)", got)
	}
	e.SetQueueRetentionDays(7)
	if got := e.QueueRetentionDays(); got != 7 {
		t.Fatalf("after set = %d, want 7", got)
	}
	e.SetQueueRetentionDays(-5) // negative clamps to 0 (off)
	if got := e.QueueRetentionDays(); got != 0 {
		t.Fatalf("negative should clamp to 0, got %d", got)
	}
}
