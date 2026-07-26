// SPDX-License-Identifier: Apache-2.0

package update

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// backupKeepDefault is how many timestamped pre-update backups to retain per
// database. Older archives are pruned after each new backup so the update-backup
// directory (and the disk) cannot grow without bound as a site is updated over
// time — the class of unbounded growth an operator otherwise has to clean up by
// hand from the Storage panel.
const backupKeepDefault = 5

// backupKeep returns the retention count, tunable via VAYU_UPDATE_BACKUP_KEEP
// (clamped to 1..100). A value of 1 keeps only the most recent backup.
func backupKeep() int {
	if v := os.Getenv("VAYU_UPDATE_BACKUP_KEEP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 100 {
			return n
		}
	}
	return backupKeepDefault
}

// pruneBackups keeps only the newest `keep` files in dir matching glob (a shell
// pattern like "backup-vayupress.db-*.tar.gz") and deletes the rest, newest
// determined by modification time. It is best-effort: it returns the number of
// files removed and the first error encountered, but callers treat a prune
// failure as non-fatal — retention must never break the backup it follows.
func pruneBackups(dir, glob string, keep int) (int, error) {
	if keep < 1 {
		keep = 1
	}
	matches, err := filepath.Glob(filepath.Join(dir, glob))
	if err != nil {
		return 0, fmt.Errorf("update: glob backups: %w", err)
	}
	if len(matches) <= keep {
		return 0, nil
	}

	type entry struct {
		path string
		mod  time.Time
	}
	entries := make([]entry, 0, len(matches))
	for _, p := range matches {
		fi, statErr := os.Stat(p)
		if statErr != nil || !fi.Mode().IsRegular() {
			continue
		}
		entries = append(entries, entry{path: p, mod: fi.ModTime()})
	}
	if len(entries) <= keep {
		return 0, nil
	}
	// Newest first, then delete everything past the keep window.
	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.After(entries[j].mod) })

	var (
		removed  int
		firstErr error
	)
	for _, e := range entries[keep:] {
		if rmErr := os.Remove(e.path); rmErr != nil {
			if firstErr == nil {
				firstErr = rmErr
			}
			continue
		}
		removed++
	}
	return removed, firstErr
}
