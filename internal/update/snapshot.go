package update

// snapshot.go — full-site backup, export and restore.
//
// A "snapshot" is a single self-describing .tar.gz that contains a *consistent*
// copy of the entire VayuPress SQLite database (which includes every article,
// member, comment, API key, and — crucially — all site/theme settings, since
// settings live in the site_settings table), plus a human-readable manifest and
// a portable settings dump. It is the one file an operator needs to move a site
// between machines or roll back to a known-good state.
//
// Design constraints (production-grade):
//   - No size limits. Export streams the archive straight to the caller's
//     io.Writer and the DB is copied with io.Copy, never buffered in memory;
//     restore streams the upload through the gzip/tar readers to disk. A 50 GB
//     database exports and restores in constant memory.
//   - Consistency. The DB is copied with `VACUUM INTO`, which takes a read
//     snapshot and writes a fully-checkpointed, defragmented standalone file —
//     no torn pages, no need to also ship the -wal/-shm sidecars.
//   - Safe restore. A restore never mutates the live database in place. The
//     validated incoming DB is staged next to the live file as
//     "<db>.pending-restore"; the actual swap (with an automatic safety backup
//     of the current DB) happens at process start via ApplyPendingRestore,
//     before any connection is opened. The web layer triggers a restart to
//     complete it, so a restore is atomic and crash-safe.
//
// Pure stdlib + database/sql; no third-party archive or compression deps.

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// snapshotFormat identifies the archive so a restore can refuse unrelated files.
	snapshotFormat        = "vayupress-snapshot"
	snapshotFormatVersion = 1

	// Canonical archive member names.
	snapshotManifestName = "manifest.json"
	snapshotSettingsName = "settings.json"
	snapshotDBName       = "vayupress.db"

	// PendingRestoreSuffix is appended to the DB path to stage a validated
	// restore that ApplyPendingRestore swaps in at the next startup.
	PendingRestoreSuffix = ".pending-restore"
)

// SnapshotManifest is the self-describing header embedded in every snapshot.
type SnapshotManifest struct {
	Format        string    `json:"format"`
	FormatVersion int       `json:"format_version"`
	AppVersion    string    `json:"app_version"`
	CreatedAt     time.Time `json:"created_at"`
	DBFileName    string    `json:"db_file_name"`
	DBSizeBytes   int64     `json:"db_size_bytes"`
	DBSHA256      string    `json:"db_sha256"`
	SettingsCount int       `json:"settings_count"`
}

// ExportSnapshot writes a complete, consistent snapshot of db to w as a
// streaming .tar.gz. It never buffers the database in memory, so there is no
// practical size limit. tmpDir is used for the transient VACUUM copy; version
// is recorded in the manifest. The caller owns w (e.g. an http.ResponseWriter)
// and any Content-Disposition headers.
func ExportSnapshot(ctx context.Context, w io.Writer, db *sql.DB, dbPath, tmpDir, version string) error {
	if db == nil {
		return fmt.Errorf("update: nil db")
	}
	if tmpDir == "" {
		tmpDir = os.TempDir()
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("update: mkdir tmp: %w", err)
	}

	// 1. Consistent, checkpointed standalone copy of the live DB.
	tmpDB := filepath.Join(tmpDir, fmt.Sprintf("vp-export-%d.db", time.Now().UnixNano()))
	_ = os.Remove(tmpDB) // VACUUM INTO refuses to overwrite an existing file
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", tmpDB); err != nil {
		return fmt.Errorf("update: vacuum into snapshot: %w", err)
	}
	defer os.Remove(tmpDB)

	fi, err := os.Stat(tmpDB)
	if err != nil {
		return fmt.Errorf("update: stat snapshot db: %w", err)
	}

	// 2. Hash the DB copy so the manifest can be verified on restore.
	sum, err := fileSHA256(tmpDB)
	if err != nil {
		return err
	}

	// 3. Portable settings dump (also present inside the DB; included for
	//    readability and cross-tool portability).
	settingsJSON, settingsCount := dumpSettings(ctx, db)

	manifest := SnapshotManifest{
		Format:        snapshotFormat,
		FormatVersion: snapshotFormatVersion,
		AppVersion:    version,
		CreatedAt:     time.Now().UTC(),
		DBFileName:    snapshotDBName,
		DBSizeBytes:   fi.Size(),
		DBSHA256:      sum,
		SettingsCount: settingsCount,
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("update: marshal manifest: %w", err)
	}

	// 4. Stream the archive: manifest → settings → DB (largest last).
	gz := gzip.NewWriter(w)
	gz.Name = snapshotDBName
	tw := tar.NewWriter(gz)

	now := manifest.CreatedAt
	if err := writeTarBytes(tw, snapshotManifestName, manifestJSON, now); err != nil {
		return err
	}
	if err := writeTarBytes(tw, snapshotSettingsName, settingsJSON, now); err != nil {
		return err
	}
	if err := writeTarFile(tw, snapshotDBName, tmpDB, fi); err != nil {
		return err
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("update: close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("update: close gzip: %w", err)
	}
	return nil
}

// StageRestore reads a snapshot from src (streamed, unbounded), validates the
// embedded database, and stages it at "<dbPath>.pending-restore" for an atomic
// swap on the next startup. It returns the snapshot manifest. It does NOT touch
// the live database — call ApplyPendingRestore at startup (and restart) to
// complete the restore.
func StageRestore(ctx context.Context, src io.Reader, dbPath, tmpDir string) (*SnapshotManifest, error) {
	if tmpDir == "" {
		tmpDir = filepath.Dir(dbPath)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, fmt.Errorf("update: mkdir tmp: %w", err)
	}

	gz, err := gzip.NewReader(src)
	if err != nil {
		return nil, fmt.Errorf("update: open gzip (not a valid snapshot?): %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	// Stream the DB member to a temp file; keep small members in memory.
	stagedDB := filepath.Join(tmpDir, fmt.Sprintf("vp-restore-%d.db", time.Now().UnixNano()))
	defer os.Remove(stagedDB) // removed unless we rename it into place

	var manifest *SnapshotManifest
	var gotDB bool

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("update: read archive: %w", err)
		}
		switch filepath.Base(hdr.Name) {
		case snapshotManifestName:
			data, err := io.ReadAll(io.LimitReader(tr, 1<<20)) // 1 MiB is ample
			if err != nil {
				return nil, fmt.Errorf("update: read manifest: %w", err)
			}
			var m SnapshotManifest
			if err := json.Unmarshal(data, &m); err != nil {
				return nil, fmt.Errorf("update: parse manifest: %w", err)
			}
			manifest = &m
		case snapshotDBName:
			out, err := os.Create(stagedDB)
			if err != nil {
				return nil, fmt.Errorf("update: create staged db: %w", err)
			}
			if _, err := io.Copy(out, tr); err != nil { //nolint:gosec — trusted operator upload, streamed to disk
				out.Close()
				return nil, fmt.Errorf("update: extract db: %w", err)
			}
			if err := out.Sync(); err != nil {
				out.Close()
				return nil, fmt.Errorf("update: sync staged db: %w", err)
			}
			if err := out.Close(); err != nil {
				return nil, fmt.Errorf("update: close staged db: %w", err)
			}
			gotDB = true
		default:
			// Unknown member (e.g. settings.json) — skip its bytes.
			_, _ = io.Copy(io.Discard, tr)
		}
	}

	if manifest == nil {
		return nil, fmt.Errorf("update: archive is missing %s — not a VayuPress snapshot", snapshotManifestName)
	}
	if manifest.Format != snapshotFormat {
		return nil, fmt.Errorf("update: unrecognised snapshot format %q", manifest.Format)
	}
	if !gotDB {
		return nil, fmt.Errorf("update: archive is missing %s", snapshotDBName)
	}

	// Verify the DB hash matches the manifest (detects truncation/corruption).
	if manifest.DBSHA256 != "" {
		sum, err := fileSHA256(stagedDB)
		if err != nil {
			return nil, err
		}
		if sum != manifest.DBSHA256 {
			return nil, fmt.Errorf("update: snapshot checksum mismatch — archive is corrupt or incomplete")
		}
	}

	// Validate the staged file really is a healthy VayuPress database.
	if err := validateSQLiteDB(stagedDB); err != nil {
		return nil, err
	}

	// Atomically move the validated copy into the pending-restore slot.
	pending := dbPath + PendingRestoreSuffix
	_ = os.Remove(pending)
	if err := os.Rename(stagedDB, pending); err != nil {
		// Cross-device fallback: copy then drop the temp file.
		if cerr := copyFile(stagedDB, pending, 0o644); cerr != nil {
			return nil, fmt.Errorf("update: stage restore (rename %v, copy %v)", err, cerr)
		}
	}
	return manifest, nil
}

// ApplyPendingRestore swaps a staged "<dbPath>.pending-restore" file over the
// live database, after taking an automatic safety backup of the current DB. It
// MUST be called at process start, before the database is opened. It returns
// whether a restore was applied. Stale -wal/-shm sidecars are removed so the
// fresh database opens cleanly.
func ApplyPendingRestore(dbPath, backupDir string) (bool, error) {
	pending := dbPath + PendingRestoreSuffix
	if _, err := os.Stat(pending); err != nil {
		return false, nil // nothing staged
	}

	// Safety net: back up whatever is currently live before we overwrite it, so
	// a bad restore is itself recoverable.
	if _, err := os.Stat(dbPath); err == nil && backupDir != "" {
		if _, berr := CreateBackup(dbPath, backupDir); berr != nil {
			// Non-fatal: a failed pre-restore backup must not block recovery, but
			// surface it so the operator knows.
			fmt.Fprintf(os.Stderr, "update: pre-restore backup failed (continuing): %v\n", berr)
		}
	}

	if err := os.Rename(pending, dbPath); err != nil {
		if cerr := copyFile(pending, dbPath, 0o644); cerr != nil {
			return false, fmt.Errorf("update: apply restore (rename %v, copy %v)", err, cerr)
		}
		_ = os.Remove(pending)
	}
	// The restored DB is fully checkpointed (VACUUM INTO); any sidecars from the
	// previous database are stale and must go.
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	return true, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

// validateSQLiteDB opens path read-only and confirms it is an intact SQLite
// database carrying the core VayuPress schema. This stops an operator from
// restoring a random or corrupt file over their live site.
func validateSQLiteDB(path string) error {
	db, err := sql.Open("sqlite3", path+"?mode=ro&_busy_timeout=3000")
	if err != nil {
		return fmt.Errorf("update: open staged db: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return fmt.Errorf("update: staged file is not a valid database: %w", err)
	}

	var integ string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integ); err != nil {
		return fmt.Errorf("update: integrity check failed: %w", err)
	}
	if integ != "ok" {
		return fmt.Errorf("update: integrity check failed: %s", integ)
	}

	for _, tbl := range []string{"schema_migrations", "articles", "site_settings"} {
		var name string
		err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl,
		).Scan(&name)
		if err == sql.ErrNoRows {
			return fmt.Errorf("update: snapshot is not a VayuPress database (missing table %q)", tbl)
		}
		if err != nil {
			return fmt.Errorf("update: inspect staged db: %w", err)
		}
	}
	return nil
}

// dumpSettings serialises the site_settings table to deterministic JSON. It is
// best-effort: a query error yields an empty object so export never fails over
// settings alone (the authoritative copy is inside the DB regardless).
func dumpSettings(ctx context.Context, db *sql.DB) ([]byte, int) {
	out := map[string]string{}
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM site_settings`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var k, v string
			if rows.Scan(&k, &v) == nil {
				out[k] = v
			}
		}
	}
	// Deterministic key order for reproducible archives.
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make([][2]string, 0, len(keys))
	for _, k := range keys {
		ordered = append(ordered, [2]string{k, out[k]})
	}
	data, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return []byte("[]"), len(out)
	}
	return data, len(out)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("update: open for hash: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("update: hash: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeTarBytes(tw *tar.Writer, name string, data []byte, mod time.Time) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o644,
		Size:    int64(len(data)),
		ModTime: mod,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("update: tar header %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("update: tar write %s: %w", name, err)
	}
	return nil
}

func writeTarFile(tw *tar.Writer, name, path string, fi os.FileInfo) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("update: open %s: %w", path, err)
	}
	defer f.Close()
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o644,
		Size:    fi.Size(),
		ModTime: fi.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("update: tar header %s: %w", name, err)
	}
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("update: tar copy %s: %w", name, err)
	}
	return nil
}
