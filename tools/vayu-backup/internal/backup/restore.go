package backup

import (
	"fmt"
	"os"
)

// RestoreOptions configures a restore operation.
type RestoreOptions struct {
	BackupPath string
	DBPath     string
	Force      bool
}

// Restore extracts the SQLite database from a backup archive.
func Restore(opts RestoreOptions) error {
	// Check if target exists.
	if _, err := os.Stat(opts.DBPath); err == nil {
		if !opts.Force {
			return fmt.Errorf("target file %q already exists; use --force to overwrite", opts.DBPath)
		}
	}

	_, files, err := readArchive(opts.BackupPath)
	if err != nil {
		return err
	}

	dbData, ok := files["vayupress.db"]
	if !ok {
		return fmt.Errorf("vayupress.db not found in archive")
	}

	if err := os.WriteFile(opts.DBPath, dbData, 0644); err != nil {
		return fmt.Errorf("write db: %w", err)
	}

	return nil
}
