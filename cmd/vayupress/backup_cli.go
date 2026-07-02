package main

// backup_cli.go — `vayupress backup` / `vayupress restore`: operator-only
// encrypted backups of the whole data directory (SQLite DB + settings, media,
// VayuMail maildirs, PGP key store). The passphrase never touches disk or
// argv history — it is read from VAYU_BACKUP_PASSPHRASE or prompted on stdin.
// A stolen backup file is useless without it (see internal/backup).

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/backup"
	"github.com/johalputt/vayupress/internal/config"
)

// backupPassphrase resolves the passphrase: env var first, then stdin prompt.
func backupPassphrase(out io.Writer) (string, error) {
	if p := os.Getenv("VAYU_BACKUP_PASSPHRASE"); strings.TrimSpace(p) != "" {
		return p, nil
	}
	fmt.Fprint(out, "Backup passphrase (input not hidden; prefer VAYU_BACKUP_PASSPHRASE): ")
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return "", fmt.Errorf("no passphrase provided")
	}
	p := strings.TrimSpace(sc.Text())
	if p == "" {
		return "", fmt.Errorf("empty passphrase")
	}
	return p, nil
}

func runBackupCLI(cmd string, args []string, out io.Writer) error {
	dataDir := filepath.Dir(config.Cfg.DBPath)
	switch cmd {
	case "backup":
		fs := flag.NewFlagSet("backup", flag.ContinueOnError)
		outPath := fs.String("out", "", "output file (default vayupress-backup-<date>.vpbk)")
		src := fs.String("data", dataDir, "data directory to back up")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *outPath == "" {
			*outPath = "vayupress-backup-" + time.Now().UTC().Format("20060102-150405") + ".vpbk"
		}
		pass, err := backupPassphrase(out)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(*outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		fmt.Fprintf(out, "Encrypting %s → %s …\n", *src, *outPath)
		if err := backup.Create(f, pass, *src); err != nil {
			os.Remove(*outPath)
			return err
		}
		st, _ := f.Stat()
		fmt.Fprintf(out, "Done — %d bytes, AES-256-GCM, Argon2id-keyed. Without the passphrase this file is unreadable by any tool; store the passphrase separately.\n", st.Size())
		fmt.Fprintln(out, "Tip: stop the vayupress service (or pick a quiet moment) for a perfectly consistent database snapshot.")
		return nil

	case "restore":
		fs := flag.NewFlagSet("restore", flag.ContinueOnError)
		inPath := fs.String("in", "", "backup file (.vpbk) to restore")
		dest := fs.String("dest", dataDir, "directory to restore into")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *inPath == "" {
			return fmt.Errorf("usage: vayupress restore -in <file.vpbk> [-dest dir]")
		}
		pass, err := backupPassphrase(out)
		if err != nil {
			return err
		}
		f, err := os.Open(*inPath)
		if err != nil {
			return err
		}
		defer f.Close()
		fmt.Fprintf(out, "Decrypting %s → %s …\n", *inPath, *dest)
		if err := backup.Extract(f, pass, *dest); err != nil {
			return err
		}
		fmt.Fprintln(out, "Restored. Restart the vayupress service to pick everything up: posts, settings, mailboxes, PGP keys — all back.")
		return nil
	}
	return fmt.Errorf("unknown command %q", cmd)
}
