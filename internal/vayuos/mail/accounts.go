package mail

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Mail account roles. Roles govern what an authenticated mailbox holder may do
// (send / delete / manage). Account management itself is always restricted to
// the VayuPress admin session (the panel routes are admin-only); roles add a
// finer permission layer used by per-account actions and future SMTP
// submission. Operators may also assign a custom (free-form) role string.
const (
	RoleAdministrator = "administrator" // full access
	RoleEditor        = "editor"        // send + delete + read
	RoleAuthor        = "author"        // send + read (no delete)
	RoleReviewer      = "reviewer"      // read-only (no send, no delete)
	// RoleMailbox is a mail-only identity: it can send and read its own
	// mailbox, but unlike administrator it grants NO access to the wider
	// VayuOS console — when such an account signs in through the membership
	// portal it is confined to the VayuMail surface (see resolveMailMember).
	RoleMailbox = "mailbox" // send + read own mailbox only; no console access
)

// BuiltinRoles is the set of first-class roles, in display order.
var BuiltinRoles = []string{RoleAdministrator, RoleEditor, RoleAuthor, RoleReviewer, RoleMailbox}

var builtinRoleSet = map[string]bool{
	RoleAdministrator: true, RoleEditor: true, RoleAuthor: true, RoleReviewer: true, RoleMailbox: true,
}

// IsBuiltinRole reports whether r is one of the first-class roles.
func IsBuiltinRole(r string) bool { return builtinRoleSet[strings.ToLower(strings.TrimSpace(r))] }

// normRole normalises a role string and falls back to author when empty/invalid.
func normRole(r string) string {
	r = strings.ToLower(strings.TrimSpace(r))
	if r == "" {
		return RoleAuthor
	}
	if builtinRoleSet[r] {
		return r
	}
	// Custom role: allow a conservative identifier charset only.
	for _, c := range r {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return RoleAuthor
		}
	}
	if len(r) > 32 {
		r = r[:32]
	}
	return r
}

// RoleCanSend reports whether a role may send mail (everyone except reviewer).
func RoleCanSend(role string) bool { return normRole(role) != RoleReviewer }

// RoleCanDelete reports whether a role may delete messages (admin/editor).
func RoleCanDelete(role string) bool {
	r := normRole(role)
	return r == RoleAdministrator || r == RoleEditor
}

// RoleCanManageAccounts reports whether a role may manage other accounts.
func RoleCanManageAccounts(role string) bool { return normRole(role) == RoleAdministrator }

// Account is an admin-managed mailbox identity (email + password + role). It is
// independent of VayuPress CMS users so an operator can hand out mail-only
// addresses. Password hashing is done by the caller (the cmd layer, using the
// project's Argon2id helper); this store only persists the hash.
type Account struct {
	Email       string    `json:"email"`
	FullName    string    `json:"full_name"`
	Role        string    `json:"role"`
	Active      bool      `json:"active"`
	TOTPEnabled bool      `json:"totp_enabled"`
	CreatedAt   time.Time `json:"created_at"`
}

// AccountStore persists mail accounts in SQLite.
type AccountStore struct{ db *sql.DB }

// NewAccountStore opens the store, creating its table if needed.
func NewAccountStore(db *sql.DB) (*AccountStore, error) {
	s := &AccountStore{db: db}
	if db == nil {
		return s, nil
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_accounts(
		email TEXT PRIMARY KEY,
		password_hash TEXT NOT NULL,
		full_name TEXT NOT NULL DEFAULT '',
		active INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`); err != nil {
		return s, err
	}
	// Idempotent migration: add the role column for stores created before
	// v1.13.0. SQLite errors with "duplicate column name" if it already exists,
	// which we treat as success.
	if _, err := db.Exec(`ALTER TABLE vayumail_accounts ADD COLUMN role TEXT NOT NULL DEFAULT 'author'`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return s, err
	}
	// Idempotent migration: optional TOTP 2FA for mailbox login. totp_secret
	// holds the base32 secret once enrolment begins; totp_enabled flips to 1
	// only after the operator verifies a code, so a half-finished enrolment can
	// never lock anyone out. Both default to "off" for existing accounts.
	for _, stmt := range []string{
		`ALTER TABLE vayumail_accounts ADD COLUMN totp_secret TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE vayumail_accounts ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return s, err
		}
	}
	return s, nil
}

func normEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// Create adds a mail account. passwordHash must already be hashed by the caller.
func (s *AccountStore) Create(ctx context.Context, email, passwordHash, fullName, role string) error {
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
		`INSERT INTO vayumail_accounts(email,password_hash,full_name,role,active,created_at) VALUES(?,?,?,?,1,?)`,
		email, passwordHash, strings.TrimSpace(fullName), normRole(role), time.Now().UTC())
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return errors.New("an account with that email already exists")
	}
	return err
}

// SetRole updates an account's role.
func (s *AccountStore) SetRole(ctx context.Context, email, role string) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE vayumail_accounts SET role=? WHERE email=?`, normRole(role), normEmail(email))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no such account")
	}
	return nil
}

// RoleFor returns the role of an account, or "" if unknown.
func (s *AccountStore) RoleFor(ctx context.Context, email string) string {
	if s.db == nil {
		return ""
	}
	var role string
	_ = s.db.QueryRowContext(ctx, `SELECT role FROM vayumail_accounts WHERE email=?`, normEmail(email)).Scan(&role)
	return role
}

// SetPasswordHash updates the stored hash for an existing account.
func (s *AccountStore) SetPasswordHash(ctx context.Context, email, passwordHash string) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	if passwordHash == "" {
		return errors.New("password required")
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

// SetActive enables or disables an account. A disabled account keeps its
// mailbox and password but cannot authenticate for SMTP/IMAP (HashFor returns
// "" for inactive accounts).
func (s *AccountStore) SetActive(ctx context.Context, email string, active bool) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	v := 0
	if active {
		v = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE vayumail_accounts SET active=? WHERE email=?`, v, normEmail(email))
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
	rows, err := s.db.QueryContext(ctx, `SELECT email,full_name,role,active,totp_enabled,created_at FROM vayumail_accounts ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Account
		var active, totpEnabled int
		if err := rows.Scan(&a.Email, &a.FullName, &a.Role, &active, &totpEnabled, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Active = active == 1
		a.TOTPEnabled = totpEnabled == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// ── TOTP 2FA ─────────────────────────────────────────────────────────────────

// SetTOTPSecret stores a freshly generated (not-yet-verified) secret for an
// account and leaves 2FA disabled. EnableTOTP flips it on once a code is
// verified, so an abandoned enrolment never locks the holder out.
func (s *AccountStore) SetTOTPSecret(ctx context.Context, email, secret string) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	if strings.TrimSpace(secret) == "" {
		return errors.New("secret required")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE vayumail_accounts SET totp_secret=?, totp_enabled=0 WHERE email=?`, secret, normEmail(email))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no such account")
	}
	return nil
}

// EnableTOTP marks 2FA active for an account that already has a stored secret.
func (s *AccountStore) EnableTOTP(ctx context.Context, email string) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE vayumail_accounts SET totp_enabled=1 WHERE email=? AND totp_secret<>''`, normEmail(email))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no enrolment in progress for this account")
	}
	return nil
}

// DisableTOTP turns 2FA off and clears the stored secret.
func (s *AccountStore) DisableTOTP(ctx context.Context, email string) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE vayumail_accounts SET totp_secret='', totp_enabled=0 WHERE email=?`, normEmail(email))
	return err
}

// TOTPStatus returns the stored secret and whether 2FA is currently enforced
// for the account. An empty secret (or an unknown account) reports disabled.
func (s *AccountStore) TOTPStatus(ctx context.Context, email string) (secret string, enabled bool) {
	if s.db == nil {
		return "", false
	}
	var en int
	_ = s.db.QueryRowContext(ctx,
		`SELECT totp_secret, totp_enabled FROM vayumail_accounts WHERE email=?`, normEmail(email)).Scan(&secret, &en)
	return secret, en == 1
}
