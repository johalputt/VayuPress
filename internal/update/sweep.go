package update

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// sweep.go — self-healing cleanup of the timestamped SQLite snapshots that the
// shell update path (scripts/update-vayupress.sh: `sqlite3 .backup`) drops NEXT
// TO the live database as "<base>.backup-<ts>.db", plus the orphaned
// "<base>.backup-<ts>.db-journal" sidecars those leave behind. Unlike the
// self-update's own .tar.gz archives (pruned in retention.go), nothing pruned
// these, so over many updates they accumulate into multi-GB clutter the operator
// had to clear by hand from the Storage panel.
//
// The engine runs continuously, so it is the right place to keep this tidy: a
// sweep at boot and once a day keeps the newest few snapshots and removes the
// rest. It is deliberately narrow — it only ever matches the "<base>.backup-*"
// family, so the live database and its "-wal"/"-shm"/"-journal" sidecars (which
// carry NO ".backup-" infix) can never be selected.

const dbBackupKeepDefault = 3

// dbBackupKeep is the number of DB-adjacent snapshots to retain, tunable via
// VAYU_DB_BACKUP_KEEP (clamped 1..100).
func dbBackupKeep() int {
	if v := os.Getenv("VAYU_DB_BACKUP_KEEP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 100 {
			return n
		}
	}
	return dbBackupKeepDefault
}

// SweepDBBackupsDefault runs SweepDBBackups with the configured retention count
// (VAYU_DB_BACKUP_KEEP, default 3). It is the entry point the engine calls at
// boot and on a daily timer.
func SweepDBBackupsDefault(dbPath string) (removed int, reclaimed int64) {
	return SweepDBBackups(dbPath, dbBackupKeep())
}

// SweepDBBackups prunes the timestamped snapshots and orphaned sidecars beside
// dbPath, keeping the newest `keep` "<base>.backup-*.db" copies. It returns the
// number of files removed and the bytes reclaimed. Best-effort: individual
// remove errors are skipped so a permission glitch never aborts the sweep, and it
// never touches the live database or its own WAL/SHM/journal.
func SweepDBBackups(dbPath string, keep int) (removed int, reclaimed int64) {
	if strings.TrimSpace(dbPath) == "" {
		return 0, 0
	}
	if keep < 1 {
		keep = 1
	}
	dir := filepath.Dir(dbPath)
	base := strings.TrimSuffix(filepath.Base(dbPath), ".db")
	prefix := base + ".backup-"

	// Orphaned sidecars of a completed/aborted `.backup` — always safe to remove
	// (the live journal/WAL/SHM lack the ".backup-" infix and never match here).
	for _, suffix := range []string{".db-journal", ".db-wal", ".db-shm"} {
		r, b := removeAllMatching(filepath.Join(dir, prefix+"*"+suffix))
		removed += r
		reclaimed += b
	}

	// Snapshot copies: keep the newest `keep`, delete the older ones.
	r, b := pruneOldest(filepath.Join(dir, prefix+"*.db"), keep)
	removed += r
	reclaimed += b
	return removed, reclaimed
}

// removeAllMatching deletes every regular file matching glob.
func removeAllMatching(glob string) (removed int, reclaimed int64) {
	matches, err := filepath.Glob(glob)
	if err != nil {
		return 0, 0
	}
	for _, p := range matches {
		fi, statErr := os.Stat(p)
		if statErr != nil || !fi.Mode().IsRegular() {
			continue
		}
		if os.Remove(p) == nil {
			removed++
			reclaimed += fi.Size()
		}
	}
	return removed, reclaimed
}

// pruneOldest keeps the newest `keep` regular files matching glob (by mtime) and
// deletes the rest.
func pruneOldest(glob string, keep int) (removed int, reclaimed int64) {
	matches, err := filepath.Glob(glob)
	if err != nil || len(matches) <= keep {
		return 0, 0
	}
	type entry struct {
		path string
		mod  time.Time
		size int64
	}
	entries := make([]entry, 0, len(matches))
	for _, p := range matches {
		fi, statErr := os.Stat(p)
		if statErr != nil || !fi.Mode().IsRegular() {
			continue
		}
		entries = append(entries, entry{path: p, mod: fi.ModTime(), size: fi.Size()})
	}
	if len(entries) <= keep {
		return 0, 0
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.After(entries[j].mod) })
	for _, e := range entries[keep:] {
		if os.Remove(e.path) == nil {
			removed++
			reclaimed += e.size
		}
	}
	return removed, reclaimed
}
