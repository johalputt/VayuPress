// SPDX-License-Identifier: Apache-2.0

package update

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSweepDBBackups(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "vayupress.db")

	// The live database and its WAL/SHM/journal sidecars must NEVER be touched.
	live := []string{
		db,
		filepath.Join(dir, "vayupress.db-wal"),
		filepath.Join(dir, "vayupress.db-shm"),
		filepath.Join(dir, "vayupress.db-journal"),
	}
	for _, p := range live {
		mustWriteFile(t, p, "LIVE")
	}

	// Five timestamped snapshots with increasing mtime (oldest → newest).
	var snaps []string
	for i := 0; i < 5; i++ {
		p := filepath.Join(dir, fmt.Sprintf("vayupress.backup-2026010%d-000000.db", i+1))
		mustWriteFile(t, p, "snapshot")
		ts := time.Unix(int64(1_000_000+i*60), 0)
		if err := os.Chtimes(p, ts, ts); err != nil {
			t.Fatal(err)
		}
		snaps = append(snaps, p)
	}
	// Orphaned backup journals (must all be removed).
	for i := 0; i < 3; i++ {
		mustWriteFile(t, filepath.Join(dir, fmt.Sprintf("vayupress.backup-2026010%d-000000.db-journal", i+1)), "J")
	}

	removed, reclaimed := SweepDBBackups(db, 2)
	if removed != 6 { // 3 oldest snapshots + 3 orphan journals
		t.Errorf("removed = %d, want 6", removed)
	}
	if reclaimed <= 0 {
		t.Errorf("reclaimed = %d, want > 0", reclaimed)
	}

	// The live files survive untouched.
	for _, p := range live {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("live file removed by sweep: %s", p)
		}
	}
	// The newest two snapshots survive; the oldest three are gone.
	for i, p := range snaps {
		_, err := os.Stat(p)
		if i >= 3 && err != nil {
			t.Errorf("newest snapshot #%d should have survived: %v", i, err)
		}
		if i < 3 && err == nil {
			t.Errorf("old snapshot #%d should have been pruned", i)
		}
	}
	// No backup journals remain.
	if m, _ := filepath.Glob(filepath.Join(dir, "vayupress.backup-*.db-journal")); len(m) != 0 {
		t.Errorf("orphan journals remain: %v", m)
	}
}

// TestSweepKeepsAllWhenUnderLimit: nothing removed when snapshots <= keep.
func TestSweepKeepsAllWhenUnderLimit(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "vayupress.db")
	mustWriteFile(t, db, "LIVE")
	mustWriteFile(t, filepath.Join(dir, "vayupress.backup-20260101-000000.db"), "s")
	if removed, _ := SweepDBBackups(db, 3); removed != 0 {
		t.Errorf("removed = %d, want 0 when under the keep limit", removed)
	}
}

func mustWriteFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
