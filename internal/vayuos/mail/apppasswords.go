// SPDX-License-Identifier: Apache-2.0

package mail

// apppasswords.go — per-mailbox app passwords and device identities.
//
// An app password is a device-scoped credential for IMAP/POP3/SMTP: generated
// once, shown once (and carried in the rotating setup QR), stored only as an
// Argon2id hash, and revocable individually without touching the mailbox's main
// password. Rotating the setup QR revokes the previous QR's credential, so a
// photographed QR goes stale — the property a fixed QR can never have.
//
// A DEVICE (ADR-0129) is an app-password row carrying a device identity on
// top: a random device_id, a platform hint, and an approval status. A device
// registers itself with the mailbox password, starts life 'pending', and must
// be approved from the (2FA-protected) web console before its credential
// authenticates on the mail protocols — so a stolen mailbox password alone can
// never sync mail to a new device.

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Device lifecycle states. Rows created before device identities existed have
// no device_id and keep status 'approved' (they were minted from the
// 2FA-protected console, so the migration default is safe).
const (
	DeviceStatusPending  = "pending"
	DeviceStatusApproved = "approved"
	DeviceStatusBlocked  = "blocked"
)

// AppPassword is one device credential (hash omitted from JSON).
type AppPassword struct {
	ID         int64     `json:"id"`
	Email      string    `json:"email"`
	Label      string    `json:"label"`
	DeviceID   string    `json:"device_id,omitempty"` // empty for plain app passwords
	Platform   string    `json:"platform,omitempty"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"` // zero = never used
}

// AppPasswordCredential is the verification view of one stored credential:
// just enough for the auth path to match a presented secret and enforce the
// device-approval status, never the metadata.
type AppPasswordCredential struct {
	ID     int64
	Hash   string
	Status string
}

// ensureAppPasswords creates the table on first use and applies the idempotent
// device-identity migrations (ADR-0129). SQLite errors with "duplicate column
// name" when a column already exists, which we treat as success — same
// tolerant re-run pattern as the vayumail_accounts migrations.
func (s *AccountStore) ensureAppPasswords() error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_app_passwords(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		email TEXT NOT NULL,
		label TEXT NOT NULL DEFAULT '',
		hash TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`); err != nil {
		return err
	}
	for _, stmt := range []string{
		// Device identity: a random id the mobile app holds so it can poll its
		// own approval status. NULL/'' for plain app passwords.
		`ALTER TABLE vayumail_app_passwords ADD COLUMN device_id TEXT`,
		`ALTER TABLE vayumail_app_passwords ADD COLUMN platform TEXT NOT NULL DEFAULT ''`,
		// Approval status: existing rows default to 'approved' — they were minted
		// from the 2FA-protected console, so the migration changes no behaviour.
		`ALTER TABLE vayumail_app_passwords ADD COLUMN status TEXT NOT NULL DEFAULT 'approved'`,
		`ALTER TABLE vayumail_app_passwords ADD COLUMN last_used_at INTEGER`,
	} {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	// device_id is unique when set (a partial index leaves legacy NULL rows alone).
	_, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_vayumail_device_id
		ON vayumail_app_passwords(device_id) WHERE device_id IS NOT NULL AND device_id <> ''`)
	return err
}

// CreateAppPassword stores a new app-password hash for a mailbox and returns
// its id. The caller generates the plaintext and hashes it (Argon2id).
func (s *AccountStore) CreateAppPassword(ctx context.Context, email, label, hash string) (int64, error) {
	if err := s.ensureAppPasswords(); err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO vayumail_app_passwords(email,label,hash,created_at) VALUES(?,?,?,?)`,
		normEmail(email), label, hash, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CreateDevice stores a new device-scoped credential for a mailbox: an app
// password carrying a device identity and an approval status. The caller
// generates the device id and the plaintext secret and hashes it (Argon2id).
func (s *AccountStore) CreateDevice(ctx context.Context, email, label, platform, deviceID, hash, status string) (int64, error) {
	if err := s.ensureAppPasswords(); err != nil {
		return 0, err
	}
	if strings.TrimSpace(deviceID) == "" {
		return 0, errors.New("device id required")
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO vayumail_app_passwords(email,label,hash,device_id,platform,status,created_at) VALUES(?,?,?,?,?,?,?)`,
		normEmail(email), label, hash, deviceID, platform, normDeviceStatus(status), time.Now().UTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// normDeviceStatus clamps a status to the known lifecycle states, falling back
// to 'pending' so an unexpected value can never accidentally grant access.
func normDeviceStatus(status string) string {
	switch status {
	case DeviceStatusApproved, DeviceStatusBlocked, DeviceStatusPending:
		return status
	}
	return DeviceStatusPending
}

// AppPasswordCredentials returns the stored credential rows for a mailbox
// (few rows; used by the auth path to verify a presented secret and enforce
// the per-device approval status).
//
// AUDIT FINDING (Section 2). This is the single chokepoint every enrolled device
// authenticates through — IMAP, POP3, submission and the private-key sync all
// reach mail via verifyCredentialScoped, which reads exactly this list. It did
// not consult the account's state, so disabling a mailbox cut nothing: HashFor
// is the only query carrying "AND active=1", so the raw password stopped working
// — which is precisely what made it look like disabling had worked — while every
// enrolled phone kept syncing. An operator disabling a mailbox because a device
// was stolen believed they had cut it off.
//
// The predicate is "no DISABLED row exists", deliberately not "an ACTIVE row
// exists", and the difference is an outage. A CMS user is a first-class mailbox
// holder here: verifyCredentialScoped authenticates them through userStore and
// GetUserByEmail resolves their mailbox, with no vayumail_accounts row anywhere
// in it — the bootstrap admin has exactly that shape. Requiring a row would have
// logged every CMS-only holder out of their own mail on upgrade. An address with
// no row keeps behaving exactly as it does today.
//
// Deletion is closed at the source instead, in Delete: an account row that is
// gone cannot be told apart from a CMS user's here, so the credential rows are
// removed with the account rather than filtered out afterwards.
func (s *AccountStore) AppPasswordCredentials(ctx context.Context, email string) []AppPasswordCredential {
	if s.ensureAppPasswords() != nil {
		return nil
	}
	addr := normEmail(email)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,hash,COALESCE(status,'approved') FROM vayumail_app_passwords
		 WHERE email=?
		   AND NOT EXISTS (SELECT 1 FROM vayumail_accounts WHERE email=? AND active=0)
		 LIMIT 20`, addr, addr)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []AppPasswordCredential
	for rows.Next() {
		var c AppPasswordCredential
		if rows.Scan(&c.ID, &c.Hash, &c.Status) == nil && c.Hash != "" {
			out = append(out, c)
		}
	}
	_ = rows.Err()
	return out
}

// DeviceCredential returns the verification view of ONE device row, looked up
// by (mailbox, device id) — used by the device-status endpoint so a device can
// only ever prove possession of its own credential.
func (s *AccountStore) DeviceCredential(ctx context.Context, email, deviceID string) (id int64, hash, status string, ok bool) {
	if s.ensureAppPasswords() != nil || strings.TrimSpace(deviceID) == "" {
		return 0, "", "", false
	}
	err := s.db.QueryRowContext(ctx,
		`SELECT id,hash,COALESCE(status,'approved') FROM vayumail_app_passwords WHERE email=? AND device_id=?`,
		normEmail(email), deviceID).Scan(&id, &hash, &status)
	return id, hash, status, err == nil && hash != ""
}

// SetDeviceStatus moves one credential through the approval lifecycle
// (pending/approved/blocked), scoped to the mailbox so a caller can never act
// on another account's device.
func (s *AccountStore) SetDeviceStatus(ctx context.Context, email string, id int64, status string) error {
	if err := s.ensureAppPasswords(); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE vayumail_app_passwords SET status=? WHERE id=? AND email=?`,
		normDeviceStatus(status), id, normEmail(email))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// TouchAppPassword records that a credential just authenticated (best-effort;
// last-used is operator-facing telemetry, never a security decision).
func (s *AccountStore) TouchAppPassword(ctx context.Context, id int64) {
	if s.db == nil {
		return
	}
	_, _ = s.db.ExecContext(ctx,
		`UPDATE vayumail_app_passwords SET last_used_at=? WHERE id=?`, time.Now().Unix(), id)
}

// ListDevices returns every device-scoped credential across all mailboxes
// (metadata only, no hashes), pending first so the console surfaces approvals
// that need action.
func (s *AccountStore) ListDevices(ctx context.Context) []AppPassword {
	if s.ensureAppPasswords() != nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,email,label,COALESCE(device_id,''),COALESCE(platform,''),COALESCE(status,'approved'),created_at,COALESCE(last_used_at,0)
		 FROM vayumail_app_passwords WHERE device_id IS NOT NULL AND device_id <> ''
		 ORDER BY (status='pending') DESC, id DESC LIMIT 200`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []AppPassword
	for rows.Next() {
		var p AppPassword
		var lastUsed int64
		if rows.Scan(&p.ID, &p.Email, &p.Label, &p.DeviceID, &p.Platform, &p.Status, &p.CreatedAt, &lastUsed) == nil {
			if lastUsed > 0 {
				p.LastUsedAt = time.Unix(lastUsed, 0).UTC()
			}
			out = append(out, p)
		}
	}
	_ = rows.Err()
	return out
}

// ListAppPasswords returns a mailbox's plain (non-device) app passwords
// (metadata only, no hashes). Device rows live in ListDevices — the two cards
// in the console never show the same credential twice.
func (s *AccountStore) ListAppPasswords(ctx context.Context, email string) []AppPassword {
	if s.ensureAppPasswords() != nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,email,label,created_at FROM vayumail_app_passwords
		 WHERE email=? AND (device_id IS NULL OR device_id='') ORDER BY id DESC LIMIT 20`, normEmail(email))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []AppPassword
	for rows.Next() {
		var p AppPassword
		if rows.Scan(&p.ID, &p.Email, &p.Label, &p.CreatedAt) == nil {
			out = append(out, p)
		}
	}
	_ = rows.Err()
	return out
}

// DeleteAppPassword revokes one app password by id, scoped to the mailbox so a
// caller can never revoke another account's credential.
func (s *AccountStore) DeleteAppPassword(ctx context.Context, email string, id int64) error {
	if err := s.ensureAppPasswords(); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM vayumail_app_passwords WHERE id=? AND email=?`, id, normEmail(email))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteAppPasswordsByLabel revokes every app password with the given label on
// a mailbox — used to rotate the setup-QR credential (one label, one live QR).
func (s *AccountStore) DeleteAppPasswordsByLabel(ctx context.Context, email, label string) error {
	if err := s.ensureAppPasswords(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM vayumail_app_passwords WHERE email=? AND label=?`, normEmail(email), label)
	return err
}

// PrunePendingDevices deletes device credentials for a mailbox that have never
// been approved and are older than olderThan, returning how many went.
//
// It exists because the device ceiling could lock a mailbox out of its own mail.
// Every sign-in registers a new row and nothing dedupes them, so a phone set up
// a few times over its life walks toward the cap on its own — and once there,
// registration is refused and only an operator can clear it. A pending row is a
// credential that was never approved and therefore never used for anything, so
// reclaiming an old one costs nothing.
//
// Approved and blocked rows are never touched: an approved device is somebody's
// working mail, and a blocked one is a decision an operator made deliberately —
// silently reaping either would be the fix causing a worse bug than the one it
// closes.
func (s *AccountStore) PrunePendingDevices(ctx context.Context, email string, olderThan time.Duration) (int64, error) {
	if err := s.ensureAppPasswords(); err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM vayumail_app_passwords WHERE email=? AND status=? AND created_at < ?`,
		normEmail(email), DeviceStatusPending, time.Now().UTC().Add(-olderThan))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
