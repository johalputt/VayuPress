// SPDX-License-Identifier: Apache-2.0

package mail

// drive_settings.go — what a holder chooses for their Drive: how many
// earlier versions of a file are kept, and how long Trash keeps what is
// put in it; and Empty Trash, and an earlier version read without bringing
// it back.

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"time"
)

// DriveSettings is a holder's choices for their Drive.
type DriveSettings struct {
	Versions  int // earlier versions kept of each file, 1 to 20
	TrashDays int // days Trash keeps what is put in it, 7 to 90
}

var defaultDriveSettings = DriveSettings{Versions: driveVersions, TrashDays: int(driveTrashFor / (24 * time.Hour))}

// ErrDriveSettings is a choice out of its range.
var ErrDriveSettings = errors.New("vayumail: versions kept are 1 to 20, and days in Trash 7 to 90")

func (s *AccountStore) ensureDriveSettingsTable() error {
	if err := s.ensureDriveTables(); err != nil {
		return err
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_drive_settings(
		mailbox TEXT PRIMARY KEY,
		versions INTEGER NOT NULL,
		trash_days INTEGER NOT NULL);`)
	return err
}

// driveSettingsOf is mbox's choices, or the defaults.
func (e *Engine) driveSettingsOf(ctx context.Context, mbox string) DriveSettings {
	if e.accounts.ensureDriveSettingsTable() != nil {
		return defaultDriveSettings
	}
	var ds DriveSettings
	err := e.accounts.db.QueryRowContext(ctx, `SELECT versions,trash_days FROM vayumail_drive_settings WHERE mailbox=?`, mbox).Scan(&ds.Versions, &ds.TrashDays)
	if err != nil {
		return defaultDriveSettings
	}
	return ds
}

// DriveSettingsOf is rd's choices.
func (e *Engine) DriveSettingsOf(rd Reader) (DriveSettings, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return DriveSettings{}, err
	}
	return e.driveSettingsOf(context.Background(), mbox), nil
}

// SetDriveSettings keeps rd's choices. Fewer versions take effect at once:
// those past the new number are removed from every file.
func (e *Engine) SetDriveSettings(rd Reader, ds DriveSettings) error {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return err
	}
	if ds.Versions < 1 || ds.Versions > 20 || ds.TrashDays < 7 || ds.TrashDays > 90 {
		return ErrDriveSettings
	}
	if err := e.accounts.ensureDriveSettingsTable(); err != nil {
		return err
	}
	ctx := context.Background()
	if _, err := e.accounts.db.ExecContext(ctx, `INSERT INTO vayumail_drive_settings(mailbox,versions,trash_days) VALUES(?,?,?)
		ON CONFLICT(mailbox) DO UPDATE SET versions=excluded.versions, trash_days=excluded.trash_days`, mbox, ds.Versions, ds.TrashDays); err != nil {
		return err
	}
	rows, err := e.accounts.db.QueryContext(ctx, `SELECT DISTINCT file_id FROM vayumail_drive_versions WHERE mailbox=?`, mbox)
	if err != nil {
		return err
	}
	var files []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			files = append(files, id)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, id := range files {
		e.pruneVersions(ctx, mbox, id)
	}
	return nil
}

// DriveEmptyTrash deletes for good everything in rd's Trash, and answers
// how many entries it held.
func (e *Engine) DriveEmptyTrash(rd Reader) (int, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return 0, err
	}
	trashed, err := e.DriveTrashed(rd)
	if err != nil {
		return 0, err
	}
	ctx := context.Background()
	for _, d := range trashed {
		if err := e.driveRemove(ctx, mbox, d.ID); err != nil {
			return 0, err
		}
	}
	e.forgetUsage(mbox)
	return len(trashed), nil
}

// DriveVersionOpen is an earlier version of a file and its bytes, to read
// without making it the file again.
func (e *Engine) DriveVersionOpen(rd Reader, id, version int64) (DriveEntry, io.ReadSeekCloser, error) {
	mbox, err := e.driveOf(rd)
	if err != nil {
		return DriveEntry{}, nil, err
	}
	ctx := context.Background()
	d, err := e.driveEntry(ctx, mbox, id)
	if err != nil || d.IsDir {
		return d, nil, ErrDriveNotFound
	}
	var blob, fkey string
	var size int64
	var sealed bool
	// By its file and its mailbox as well as its number, as a restore asks.
	err = e.accounts.db.QueryRowContext(ctx, `SELECT blob,fkey,size,sealed FROM vayumail_drive_versions WHERE id=? AND file_id=? AND mailbox=?`, version, id, mbox).Scan(&blob, &fkey, &size, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return d, nil, ErrDriveNotFound
	}
	if err != nil {
		return d, nil, err
	}
	d.Size = size
	r, err := e.openKept(mbox, blob, fkey, sealed, size)
	return d, r, err
}
