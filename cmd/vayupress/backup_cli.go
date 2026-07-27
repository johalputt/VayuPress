// SPDX-License-Identifier: Apache-2.0

package main

// backup_cli.go — `vayupress backup` / `vayupress restore`: operator-only
// encrypted backups of the whole data directory (SQLite DB + settings, media,
// VayuMail maildirs, PGP key store).
//
// The database is archived from a `VACUUM INTO` snapshot, never from the live
// file. Copying a running SQLite database and its `-wal` byte-for-byte can
// capture a torn state that restores into a corrupt database, and no amount of
// care at restore time can repair bytes that were inconsistent when they were
// read. This used to be handled by printing advice ("stop the service, or pick
// a quiet moment"); advice is not a mechanism (ADR-0145).
//
// The passphrase never touches disk or argv — see backup_passphrase.go.
// A stolen backup file is useless without it (see internal/backup).

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/johalputt/vayupress/internal/backup"
	"github.com/johalputt/vayupress/internal/config"
)

// snapshotLiveDB writes a consistent, fully-checkpointed copy of the SQLite
// database at dbPath to dest. `VACUUM INTO` reads through a single read
// transaction, so the result is a point-in-time image even while the service is
// writing — and it folds the WAL in, so the snapshot needs no sidecars.
func snapshotLiveDB(ctx context.Context, dbPath, dest string) error {
	db, err := sql.Open("sqlite3", dbPath+"?_busy_timeout=15000&_journal_mode=WAL")
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	// VACUUM INTO refuses to overwrite, so the destination must not exist.
	_ = os.Remove(dest)
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", dest); err != nil {
		return fmt.Errorf("consistent database snapshot failed: %w", err)
	}
	return nil
}

// dbArchiveOptions builds the substitution that puts a consistent snapshot into
// the archive under the database's normal name, and skips the `-wal`/`-shm`
// sidecars whose contents the snapshot has already folded in. It returns a
// cleanup func for the temporary snapshot.
//
// When the database does not live inside the directory being archived (an
// operator backing up an unrelated path), nothing is substituted and the walk
// proceeds verbatim.
func dbArchiveOptions(ctx context.Context, srcDir, dbPath string, out io.Writer) (backup.Options, func(), error) {
	noop := func() {}
	if dbPath == "" {
		return backup.Options{}, noop, nil
	}
	rel, err := filepath.Rel(filepath.Clean(srcDir), filepath.Clean(dbPath))
	if err != nil || rel == "." || filepath.IsAbs(rel) || len(rel) > 1 && rel[:2] == ".." {
		return backup.Options{}, noop, nil // database is outside this directory
	}
	if _, err := os.Stat(dbPath); err != nil {
		return backup.Options{}, noop, nil // nothing to snapshot yet
	}
	tmpDir, err := os.MkdirTemp("", "vayupress-snapshot-")
	if err != nil {
		return backup.Options{}, noop, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	snap := filepath.Join(tmpDir, "snapshot.db")
	fmt.Fprintln(out, "Taking a consistent database snapshot (VACUUM INTO) …")
	if err := snapshotLiveDB(ctx, dbPath, snap); err != nil {
		cleanup()
		return backup.Options{}, noop, err
	}
	slash := filepath.ToSlash(rel)
	return backup.Options{
		Substitute: map[string]string{slash: snap},
		Skip: map[string]bool{
			slash + "-wal": true,
			slash + "-shm": true,
		},
	}, cleanup, nil
}

func runBackupCLI(cmd string, args []string, out io.Writer) error {
	dataDir := filepath.Dir(config.Cfg.DBPath)
	switch cmd {
	case "backup":
		fs := flag.NewFlagSet("backup", flag.ContinueOnError)
		outPath := fs.String("out", "", "output file (default vayupress-backup-<date>.vpbk)")
		src := fs.String("data", dataDir, "data directory to back up")
		passFile := fs.String("passphrase-file", "", "read the passphrase from this file instead of prompting")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *outPath == "" {
			*outPath = "vayupress-backup-" + time.Now().UTC().Format("20060102-150405") + ".vpbk"
		}
		pass, err := resolvePassphrase(out, *passFile)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		opts, cleanup, err := dbArchiveOptions(ctx, *src, config.Cfg.DBPath, out)
		if err != nil {
			return err
		}
		defer cleanup()

		f, err := os.OpenFile(*outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // operator-chosen output path
		if err != nil {
			return err
		}
		defer f.Close()
		fmt.Fprintf(out, "Encrypting %s → %s …\n", *src, *outPath)
		if err := backup.CreateWithOptions(f, pass, *src, opts); err != nil {
			os.Remove(*outPath)
			return err
		}
		st, _ := f.Stat()
		fmt.Fprintf(out, "Done — %d bytes, AES-256-GCM under an Argon2id-wrapped data key, every frame chained to the last. Without the passphrase this file is unreadable by any tool; store the passphrase separately.\n", st.Size())
		if len(opts.Substitute) > 0 {
			fmt.Fprintln(out, "The database was captured from a consistent snapshot, so this backup is restorable without stopping the service.")
		}
		return nil

	case "restore":
		fs := flag.NewFlagSet("restore", flag.ContinueOnError)
		inPath := fs.String("in", "", "backup file (.vpbk) to restore")
		dest := fs.String("dest", dataDir, "directory to restore into")
		passFile := fs.String("passphrase-file", "", "read the passphrase from this file instead of prompting")
		verifyOnly := fs.Bool("verify", false, "verify the archive end to end without writing anything")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *inPath == "" {
			return fmt.Errorf("usage: vayupress restore -in <file.vpbk> [-dest dir] [-verify]")
		}
		pass, err := resolvePassphrase(out, *passFile)
		if err != nil {
			return err
		}
		f, err := os.Open(*inPath) //nolint:gosec // operator-chosen input path
		if err != nil {
			return err
		}
		defer f.Close()

		if *verifyOnly {
			fmt.Fprintf(out, "Verifying %s …\n", *inPath)
			if err := backup.Verify(f, pass); err != nil {
				return err
			}
			fmt.Fprintln(out, "Verified — the passphrase is correct, every frame authenticates, the chain is unbroken and the archive reaches its final marker. This archive is restorable.")
			return nil
		}

		fmt.Fprintf(out, "Decrypting %s → %s …\n", *inPath, *dest)
		displaced, err := backup.ExtractStaged(f, pass, *dest)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "Restored. Restart the vayupress service to pick everything up: posts, settings, mailboxes, PGP keys — all back.")
		if displaced != "" {
			fmt.Fprintf(out, "Your previous data directory was preserved at %s — delete it once you have confirmed the restore is the generation you wanted.\n", displaced)
		}
		return nil
	}
	return fmt.Errorf("unknown command %q", cmd)
}
