// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/backup"
	"github.com/johalputt/vayupress/internal/secrets"
)

// restoreInstall is a data directory as an install keeps one: its database,
// its secrets store's key file beside it, and VayuMail's storage under it.
// It answers an engine on that directory, with the Drive key the app would
// derive at boot.
func restoreInstall(t *testing.T, dir string) (*Engine, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(dir, "vayupress.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s, err := NewAccountStore(db)
	if err != nil {
		t.Fatal(err)
	}
	// The keyring's table as the shipped migration makes it (one statement
	// a line), not restated here.
	mig, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "042-api-keys-envelope.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(mig), "\n") {
		if strings.HasPrefix(line, "CREATE TABLE IF NOT EXISTS secret_keyring") {
			if _, err := db.Exec(line); err != nil {
				t.Fatal(err)
			}
		}
	}
	key, err := secrets.New(db, nil, filepath.Join(dir, ".vayu-secret-kek")).Derive("vayumail-drive-v1")
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.StorageDir = filepath.Join(dir, "vayudata", "mail")
	e := &Engine{cfg: cfg, maildir: NewMaildir(filepath.Join(cfg.StorageDir, "maildir")), accounts: s, db: db}
	e.UseDriveKey(key)
	return e, db
}

// TestDriveComesBackFromABackupWhole — files and their versions are kept,
// the data directory is backed up and then lost, the backup restored: every
// file opens and reads to the sum it was kept with, and the upkeep finds
// nothing damaged. The backup carries the secrets store's key file with the
// files it unlocks, inside its own encryption.
func TestDriveComesBackFromABackupWhole(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	e, db := restoreInstall(t, dir)
	if err := e.accounts.Create(context.Background(), calBox, "h", "Ankush", "mailbox"); err != nil {
		t.Fatal(err)
	}
	bodies := map[string][]byte{
		"plan.txt":  []byte("the plan, as it stands"),
		"photo.jpg": bytes.Repeat([]byte{0xff, 0xd8, 0x01}, 3*driveChunk),
		"empty.bin": nil,
		"notes.md":  []byte("# notes"),
	}
	ids := map[string]int64{}
	for name, body := range bodies {
		id, err := e.DrivePut(ankush, 0, name, "", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	// A version, too.
	if _, err := e.DrivePut(ankush, 0, "plan.txt", "", bytes.NewReader([]byte("the plan, changed"))); err != nil {
		t.Fatal(err)
	}
	bodies["plan.txt"] = []byte("the plan, changed")
	_ = db.Close()

	var archive bytes.Buffer
	if err := backup.CreateWithOptions(&archive, "a passphrase for the test", dir, backup.Options{}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.ExtractStaged(&archive, "a passphrase for the test", dir); err != nil {
		t.Fatal(err)
	}

	e2, db2 := restoreInstall(t, dir)
	defer db2.Close()
	for name, body := range bodies {
		if got := read(t, e2, ankush, ids[name]); got != string(body) {
			t.Errorf("%s came back as %d bytes, not %d", name, len(got), len(body))
		}
		list, _ := e2.DriveList(ankush, 0)
		for _, d := range list {
			if d.ID == ids[name] && d.Sum != hexSum(body) {
				t.Errorf("%s does not read to the sum it was kept with", name)
			}
		}
	}
	vs, _ := e2.DriveVersions(ankush, ids["plan.txt"])
	if len(vs) != 1 || readVersion(t, e2, ankush, ids["plan.txt"], vs[0].ID) != "the plan, as it stands" {
		t.Errorf("the earlier version did not come back: %+v", vs)
	}
	if _, _, damaged := e2.careForDrive(time.Now().Add(driveCheckEvery + time.Hour)); damaged != 0 {
		t.Errorf("the upkeep found %d damaged after the restore", damaged)
	}
}
