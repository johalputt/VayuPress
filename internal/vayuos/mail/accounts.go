package mail

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Account is an admin-managed mailbox identity (email + password). It is
// independent of VayuPress CMS users so an operator can hand out mail-only
// addresses. Password hashing is done by the caller (the cmd layer, using the
// project's Argon2id helper); this store only persists the hash.
type Account struct {
	Email     string    `json:"email"`
	FullName  string    `json:"full_name"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// AccountStore persists mail accounts in SQLite.
type AccountStore struct{ db *sql.DB }

// NewAccountStore opens the store, creating its table if needed.
func NewAccountStore(db *sql.DB) (*AccountStore, error) {
	s := &AccountStore{db: db}
	if db == nil {
		return s, nil
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_accounts(
		email TEXT PRIMARY KEY,
		password_hash TEXT NOT NULL,
		full_name TEXT NOT NULL DEFAULT '',
		active INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`)
	return s, err
}

func normEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// Create adds a mail account. passwordHash must already be hashed by the caller.
func (s *AccountStore) Create(ctx context.Context, email, passwordHash, fullName string) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	email = normEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		return errors.New("invalid email address")
	}
	if passwordHash == "" {
		return errors.New("password required")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO vayumail_accounts(email,password_hash,full_name,active,created_at) VALUES(?,?,?,1,?)`,
		email, passwordHash, strings.TrimSpace(fullName), time.Now().UTC())
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return errors.New("an account with that email already exists")
	}
	return err
}

// SetPasswordHash updates the stored hash for an existing account.
func (s *AccountStore) SetPasswordHash(ctx context.Context, email, passwordHash string) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE vayumail_accounts SET password_hash=? WHERE email=?`, passwordHash, normEmail(email))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no such account")
	}
	return nil
}

// Delete removes an account.
func (s *AccountStore) Delete(ctx context.Context, email string) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM vayumail_accounts WHERE email=?`, normEmail(email))
	return err
}

// HashFor returns the stored password hash for an account, or "" if unknown or
// inactive. The caller verifies it with the project's Argon2id helper.
func (s *AccountStore) HashFor(ctx context.Context, email string) string {
	if s.db == nil {
		return ""
	}
	var hash string
	_ = s.db.QueryRowContext(ctx, `SELECT password_hash FROM vayumail_accounts WHERE email=? AND active=1`, normEmail(email)).Scan(&hash)
	return hash
}

// List returns all accounts (no hashes).
func (s *AccountStore) List(ctx context.Context) ([]Account, error) {
	out := []Account{}
	if s.db == nil {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT email,full_name,active,created_at FROM vayumail_accounts ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Account
		var active int
		if err := rows.Scan(&a.Email, &a.FullName, &active, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Active = active == 1
		out = append(out, a)
	}
	return out, rows.Err()
}
