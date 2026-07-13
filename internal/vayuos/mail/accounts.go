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

// normRole normalises a role string and falls back to the least-privilege
// mailbox role when empty/invalid, so an unspecified role never silently grants
// console access (security: console roles must be chosen deliberately).
func normRole(r string) string {
	r = strings.ToLower(strings.TrimSpace(r))
	if r == "" {
		return RoleMailbox
	}
	if builtinRoleSet[r] {
		return r
	}
	// Custom role: allow a conservative identifier charset only.
	for _, c := range r {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return RoleMailbox
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
	Email       string `json:"email"`
	FullName    string `json:"full_name"`
	Role        string `json:"role"`
	Active      bool   `json:"active"`
	TOTPEnabled bool   `json:"totp_enabled"`
	QuotaBytes  int64  `json:"quota_bytes"` // mailbox storage limit; 0 = unlimited
	Signature   string `json:"signature"`   // plain-text mail signature, appended on send
	ForwardTo   string `json:"forward_to"`  // auto-forward copies of inbound mail here; empty = off
	// RequireDeviceApproval: only approved device credentials sync mail over
	// IMAP/POP3/submission (ADR-0129). On by default.
	RequireDeviceApproval bool      `json:"require_device_approval"`
	CreatedAt             time.Time `json:"created_at"`
	// AvatarType is the MIME of the stored profile picture ("" = none). The image
	// bytes are fetched separately (Avatar) so listing stays light.
	AvatarType string `json:"avatar_type,omitempty"`
}

// maxSignatureBytes bounds a stored mail signature.
const maxSignatureBytes = 4096

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
		// Per-mailbox storage quota in bytes; 0 means unlimited. Existing accounts
		// default to unlimited so the migration changes no behaviour.
		`ALTER TABLE vayumail_accounts ADD COLUMN quota_bytes INTEGER NOT NULL DEFAULT 0`,
		// Per-account plain-text mail signature, appended on send. Empty for
		// existing accounts, so the migration changes no behaviour.
		`ALTER TABLE vayumail_accounts ADD COLUMN signature TEXT NOT NULL DEFAULT ''`,
		// Per-account auto-forward target: inbound mail is filed locally AND a
		// copy is relayed to this address. Empty (the default) means off.
		`ALTER TABLE vayumail_accounts ADD COLUMN forward_to TEXT NOT NULL DEFAULT ''`,
		// Vacation autoresponder (RFC 3834): enabled flag, subject/body, and an
		// optional active window (RFC3339 date-times; empty = unbounded side).
		`ALTER TABLE vayumail_accounts ADD COLUMN autoreply_enabled INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE vayumail_accounts ADD COLUMN autoreply_subject TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE vayumail_accounts ADD COLUMN autoreply_body TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE vayumail_accounts ADD COLUMN autoreply_from TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE vayumail_accounts ADD COLUMN autoreply_until TEXT NOT NULL DEFAULT ''`,
		// Device approval (ADR-0129): when 1 (the secure default), the raw mailbox
		// password no longer authenticates the mail protocols — only an APPROVED
		// device credential syncs mail. Bootstrapping (member login, device
		// registration) still accepts the password, so this can never lock the
		// holder out of registering a device.
		`ALTER TABLE vayumail_accounts ADD COLUMN require_device_approval INTEGER NOT NULL DEFAULT 1`,
		// Retention (ADR-0130): read mail the holder hasn't deliberately saved
		// is permanently deleted this many days after it was read. 0 (the
		// default) disables the sweep, so the migration changes no behaviour.
		`ALTER TABLE vayumail_accounts ADD COLUMN retention_days INTEGER NOT NULL DEFAULT 0`,
		// Per-mailbox profile picture: the raw image bytes and its validated MIME.
		// avatar_type != '' signals presence without pulling the blob into List().
		// Empty for existing accounts, so the migration changes no behaviour.
		`ALTER TABLE vayumail_accounts ADD COLUMN avatar_blob BLOB`,
		`ALTER TABLE vayumail_accounts ADD COLUMN avatar_type TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return s, err
		}
	}
	// Autoresponder dedupe log: one row per (mailbox, correspondent) recording
	// when we last auto-replied, so a sender gets at most one reply per window.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_autoreply_log(
		mailbox TEXT NOT NULL,
		sender TEXT NOT NULL,
		sent_at DATETIME NOT NULL,
		PRIMARY KEY(mailbox, sender));`); err != nil {
		return s, err
	}
	// Alias addresses: extra receive-only addresses that deliver into an existing
	// mailbox (single-level: an alias always points at a real account, never at
	// another alias — enforced at creation).
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_aliases(
		alias TEXT PRIMARY KEY,
		target TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`); err != nil {
		return s, err
	}
	return s, nil
}

// SetQuota sets an account's mailbox storage limit in bytes (0 = unlimited).
func (s *AccountStore) SetQuota(ctx context.Context, email string, bytes int64) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	if bytes < 0 {
		bytes = 0
	}
	res, err := s.db.ExecContext(ctx, `UPDATE vayumail_accounts SET quota_bytes=? WHERE email=?`, bytes, normEmail(email))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no such account")
	}
	return nil
}

// QuotaFor returns an account's storage limit in bytes (0 = unlimited / unknown).
func (s *AccountStore) QuotaFor(ctx context.Context, email string) int64 {
	if s.db == nil {
		return 0
	}
	var q int64
	_ = s.db.QueryRowContext(ctx, `SELECT quota_bytes FROM vayumail_accounts WHERE email=?`, normEmail(email)).Scan(&q)
	return q
}

func normEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// SetSignature stores an account's plain-text mail signature (bounded). An empty
// string clears it.
func (s *AccountStore) SetSignature(ctx context.Context, email, sig string) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	if len(sig) > maxSignatureBytes {
		sig = sig[:maxSignatureBytes]
	}
	res, err := s.db.ExecContext(ctx, `UPDATE vayumail_accounts SET signature=? WHERE email=?`, sig, normEmail(email))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no such account")
	}
	return nil
}

// SignatureFor returns an account's stored mail signature (empty when unset).
func (s *AccountStore) SignatureFor(ctx context.Context, email string) string {
	if s.db == nil {
		return ""
	}
	var sig string
	_ = s.db.QueryRowContext(ctx, `SELECT signature FROM vayumail_accounts WHERE email=?`, normEmail(email)).Scan(&sig)
	return sig
}

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
	rows, err := s.db.QueryContext(ctx, `SELECT email,full_name,role,active,totp_enabled,quota_bytes,signature,forward_to,require_device_approval,created_at,avatar_type FROM vayumail_accounts ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Account
		var active, totpEnabled, reqApproval int
		if err := rows.Scan(&a.Email, &a.FullName, &a.Role, &active, &totpEnabled, &a.QuotaBytes, &a.Signature, &a.ForwardTo, &reqApproval, &a.CreatedAt, &a.AvatarType); err != nil {
			return nil, err
		}
		a.Active = active == 1
		a.TOTPEnabled = totpEnabled == 1
		a.RequireDeviceApproval = reqApproval == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetAvatar stores a mailbox's profile picture (raw bytes + validated MIME). The
// caller enforces the size/type limits (see the cmd-layer upload handler).
func (s *AccountStore) SetAvatar(ctx context.Context, email string, blob []byte, mime string) error {
	if s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE vayumail_accounts SET avatar_blob=?, avatar_type=? WHERE email=?`,
		blob, mime, strings.ToLower(strings.TrimSpace(email)))
	return err
}

// ClearAvatar removes a mailbox's profile picture (the card falls back to initials).
func (s *AccountStore) ClearAvatar(ctx context.Context, email string) error {
	if s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE vayumail_accounts SET avatar_blob=NULL, avatar_type='' WHERE email=?`,
		strings.ToLower(strings.TrimSpace(email)))
	return err
}

// Avatar returns a mailbox's stored profile picture and its MIME, or (nil,"")
// when none is set.
func (s *AccountStore) Avatar(ctx context.Context, email string) ([]byte, string, error) {
	if s.db == nil {
		return nil, "", nil
	}
	var blob []byte
	var mime string
	err := s.db.QueryRowContext(ctx, `SELECT avatar_blob, avatar_type FROM vayumail_accounts WHERE email=?`,
		strings.ToLower(strings.TrimSpace(email))).Scan(&blob, &mime)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	return blob, mime, err
}

// CountsByHost returns the number of mail accounts per domain host, keyed by the
// lowercased domain part of each account's address (the part after the last '@').
// It is a read-only reporting helper for the VayuDomains admin surface — the
// account model keys mailboxes by full email, so the host is derived rather than
// stored, and no delivery/auth path is involved (VayuDomains Stage 3a).
func (s *AccountStore) CountsByHost(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	if s.db == nil {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT email FROM vayumail_accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, err
		}
		if i := strings.LastIndexByte(email, '@'); i >= 0 {
			out[strings.ToLower(email[i+1:])]++
		}
	}
	return out, rows.Err()
}

// SetRequireDeviceApproval flips the per-mailbox device-approval enforcement
// (ADR-0129). Turning it OFF restores the pre-device behaviour: the raw
// mailbox password authenticates the mail protocols again.
func (s *AccountStore) SetRequireDeviceApproval(ctx context.Context, email string, on bool) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	v := 0
	if on {
		v = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE vayumail_accounts SET require_device_approval=? WHERE email=?`, v, normEmail(email))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no such account")
	}
	return nil
}

// RequireDeviceApproval reports whether device approval is enforced for a
// mailbox. An identity WITHOUT an account row (e.g. a CMS-only user) reports
// enforced too — the secure default — and its registered devices remain
// approvable from the console's Devices card, so there is always a path to a
// working credential. Only a nil store (no storage at all, hence no devices to
// approve) reports off.
// RetentionDays returns how many days after being READ a message may live
// before the retention sweep permanently deletes it (0 = retention off).
func (s *AccountStore) RetentionDays(ctx context.Context, email string) int {
	if s.db == nil {
		return 0
	}
	days := 0
	_ = s.db.QueryRowContext(ctx,
		`SELECT retention_days FROM vayumail_accounts WHERE email=?`, normEmail(email)).Scan(&days)
	if days < 0 {
		days = 0
	}
	return days
}

// SetRetentionDays stores the mailbox's auto-delete window (0 disables).
func (s *AccountStore) SetRetentionDays(ctx context.Context, email string, days int) error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	if days < 0 {
		days = 0
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE vayumail_accounts SET retention_days=? WHERE email=?`, days, normEmail(email))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("vayumail: no such account")
	}
	return nil
}

func (s *AccountStore) RequireDeviceApproval(ctx context.Context, email string) bool {
	if s.db == nil {
		return false
	}
	on := 1
	_ = s.db.QueryRowContext(ctx,
		`SELECT require_device_approval FROM vayumail_accounts WHERE email=?`, normEmail(email)).Scan(&on)
	return on == 1
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
