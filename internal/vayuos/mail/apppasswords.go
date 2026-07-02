package mail

// apppasswords.go — per-mailbox app passwords.
//
// An app password is a device-scoped credential for IMAP/POP3/SMTP: generated
// once, shown once (and carried in the rotating setup QR), stored only as an
// Argon2id hash, and revocable individually without touching the mailbox's main
// password. Rotating the setup QR revokes the previous QR's credential, so a
// photographed QR goes stale — the property a fixed QR can never have.

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AppPassword is one device credential (hash omitted from JSON).
type AppPassword struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

// ensureAppPasswords creates the table on first use (idempotent).
func (s *AccountStore) ensureAppPasswords() error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_app_passwords(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		email TEXT NOT NULL,
		label TEXT NOT NULL DEFAULT '',
		hash TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`)
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

// AppPasswordHashes returns the stored hashes for a mailbox (few rows; used by
// the auth path to verify a presented credential).
func (s *AccountStore) AppPasswordHashes(ctx context.Context, email string) []string {
	if s.ensureAppPasswords() != nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT hash FROM vayumail_app_passwords WHERE email=? LIMIT 20`, normEmail(email))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if rows.Scan(&h) == nil && h != "" {
			out = append(out, h)
		}
	}
	_ = rows.Err()
	return out
}

// ListAppPasswords returns a mailbox's app passwords (metadata only, no hashes).
func (s *AccountStore) ListAppPasswords(ctx context.Context, email string) []AppPassword {
	if s.ensureAppPasswords() != nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,email,label,created_at FROM vayumail_app_passwords WHERE email=? ORDER BY id DESC LIMIT 20`, normEmail(email))
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
