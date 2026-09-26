// SPDX-License-Identifier: Apache-2.0

package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/johalputt/vayupress/internal/pacedio"
	"github.com/johalputt/vayupress/internal/sqlitecopy"
)

// CreateBackup writes a consistent copy of the SQLite database at dbPath into
// destDir as a timestamped .tar.gz and returns the archive path.
//
// It used to archive the live file and its -wal and -shm byte for byte while
// the site kept writing, which can capture a pair that restores into a corrupt
// database: the defect ADR-0145 removed from every other backup path, still
// here in the one taken before an update. The database is now copied through
// one pinned read transaction (internal/sqlitecopy) and archived alone, since
// the copy has folded the write-ahead log in. Both the copy and the archive are
// paced by pc.
func CreateBackup(ctx context.Context, dbPath, destDir string, pc Pacing) (string, error) {
	if dbPath == "" {
		return "", fmt.Errorf("update: empty dbPath")
	}
	if _, err := os.Stat(dbPath); err != nil {
		return "", fmt.Errorf("update: nothing to back up: %w", err)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("update: mkdir backup dir: %w", err)
	}

	ts := time.Now().UTC().Format("20060102T150405Z")
	base := filepath.Base(dbPath)
	// The copy lands beside the archive, on the backup directory's disk, never
	// in the default temporary directory: that is a RAM-backed tmpfs on most
	// distributions, and a multi-gigabyte copy into it takes the machine down.
	snap := filepath.Join(destDir, ".snapshot-"+ts+".db")
	_ = os.Remove(snap)
	if err := sqlitecopy.Copy(ctx, dbPath, snap, pc.pages(), nil); err != nil {
		return "", fmt.Errorf("update: consistent copy: %w", err)
	}
	defer os.Remove(snap)
	fi, err := os.Stat(snap)
	if err != nil {
		return "", err
	}

	archivePath := filepath.Join(destDir, fmt.Sprintf("backup-%s-%s.tar.gz", base, ts))
	out, err := os.Create(archivePath)
	if err != nil {
		return "", fmt.Errorf("update: create archive: %w", err)
	}
	complete := false
	defer func() {
		out.Close()
		if !complete {
			_ = os.Remove(archivePath)
		}
	}()
	gz := gzip.NewWriter(pacedio.NewWriter(ctx, out, pc.chunks()))
	tw := tar.NewWriter(gz)
	if err := writeTarFile(tw, base, snap, fi); err != nil {
		return "", err
	}
	if err := tw.Close(); err != nil {
		return "", fmt.Errorf("update: close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return "", fmt.Errorf("update: close gzip: %w", err)
	}
	if err := out.Sync(); err != nil {
		return "", err
	}
	complete = true

	// Retention: prune older pre-update backups, keeping the newest N. Scoped to
	// THIS database's base name so it can never touch another DB's backups, and
	// best-effort so a prune failure never fails the backup. The archive just
	// written is the newest by mod time, so it is always within the keep window
	// (pruning only ever deletes strictly older files).
	_, _ = pruneBackups(destDir, fmt.Sprintf("backup-%s-*.tar.gz", base), backupKeep())

	return archivePath, nil
}
