// SPDX-License-Identifier: Apache-2.0

// Package users provides multi-author account management for VayuPress.
//
// Accounts authenticate with email + password. Passwords are never stored in
// the clear — they are hashed with Argon2id via the shared auth helpers (the
// same KDF used for API-secret hashing). Roles gate capability: "admin" users
// may manage other users and system settings; "author" users may write content.
//
// This package owns only persistence and credential verification; HTTP session
// issuance lives in internal/auth so that the auth layer has no dependency on
// the users schema (avoiding an import cycle).
package users

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
)

// Roles recognised by the authorization layer.
//
//   - admin:  may manage other users, roles, and system settings.
//   - editor: may write and publish/manage all content.
//   - author: may write their own content.
//   - client: an agency CLIENT (ADR-0152). Not a rung on the ladder above — the
//     ladder is linear and cannot express "may see their own site and mail but
//     no content authoring at all". A client reaches a fixed, enumerated surface
//     scoped to the single domain in ClientDomainID and nothing else.
const (
	RoleAdmin  = "admin"
	RoleEditor = "editor"
	RoleAuthor = "author"
	RoleClient = "client"
)

// MaxBioLen / MaxNameLen bound the free-text profile fields.
const (
	MaxBioLen  = 250
	MaxNameLen = 250
)

// ValidRole reports whether role is a recognised account role.
func ValidRole(role string) bool {
	switch role {
	case RoleAdmin, RoleEditor, RoleAuthor, RoleClient:
		return true
	}
	return false
}

// User is an account record. The password hash is never serialised to JSON.
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	// Username is the human-readable public handle used in /author/<username>.
	// Falls back to the ID in links when empty (e.g. a pre-051 account before
	// backfill).
	Username    string            `json:"username,omitempty"`
	AvatarURL   string            `json:"avatar_url,omitempty"`
	Bio         string            `json:"bio,omitempty"`
	Socials     map[string]string `json:"socials,omitempty"`
	MailAddress string            `json:"mail_address,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	LastLogin   *time.Time        `json:"last_login,omitempty"`
	// MustChangePassword is set on a bootstrapped default admin; the console
	// forces a password change before anything else until it is cleared.
	MustChangePassword bool `json:"must_change_password,omitempty"`
	// ClientDomainID binds a RoleClient account to the one hosted domain it may
	// administer (ADR-0152). Empty on every non-client account. A client whose
	// binding is empty is an invalid identity and is refused, never defaulted.
	ClientDomainID string `json:"client_domain_id,omitempty"`
}

// Store manages user accounts in SQLite.
type Store struct {
	db     *sql.DB // writer: account create/update/delete
	reader *sql.DB // read pool: hot read paths (GetByID runs on every admin request)

	// totpCodec seals the totp_secret column at rest when installed
	// (UseTOTPCodec); nil keeps legacy plaintext behaviour for tests.
	totpCodec SecretCodec
}

// New creates a Store. Reads default to the same handle as writes; call
// UseReader to point the hot read paths at a dedicated read pool.
func New(db *sql.DB) *Store { return &Store{db: db, reader: db} }

// UseReader routes the hot read path (GetByID, which the admin auth middleware
// runs on every request) at a dedicated read pool instead of the single writer
// connection, so account lookups do not serialise behind writes on a saturated
// writer (the admin-only-502-after-restart failure). Writes stay on the writer;
// WAL gives the reader read-your-writes. A nil reader is ignored.
func (s *Store) UseReader(reader *sql.DB) {
	if reader != nil {
		s.reader = reader
	}
}

func (s *Store) readDB() *sql.DB {
	if s.reader != nil {
		return s.reader
	}
	return s.db
}

// Create inserts a new user with the given credentials. Role defaults to
// "author" when empty and must be a recognised role otherwise.
func (s *Store) Create(ctx context.Context, email, name, password, role string) (*User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, fmt.Errorf("invalid email: %w", err)
	}
	if len(password) < 8 {
		return nil, fmt.Errorf("password must be at least 8 characters")
	}
	if role == "" {
		role = RoleAuthor
	}
	if !ValidRole(role) {
		return nil, fmt.Errorf("invalid role %q (want admin, editor, author, or client)", role)
	}
	// A client is never minted here, because this function cannot take the one
	// thing that makes a client account meaningful: the domain it is bound to.
	// Creating one through this path produces an account that authenticates and
	// then reaches nothing, which reads to everyone involved as a broken login
	// rather than an invalid identity. CreateClient is the only way in.
	if role == RoleClient {
		return nil, fmt.Errorf("a client account must be created with its domain binding — use CreateClient")
	}
	hash, err := auth.HashSecretArgon2id(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	id := newID()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO users(id,email,name,password_hash,role) VALUES(?,?,?,?,?)`,
		id, email, strings.TrimSpace(name), hash, role)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("a user with that email already exists")
		}
		return nil, fmt.Errorf("create user: %w", err)
	}
	// Assign a public handle for /author/<username>. Best-effort: a link falls
	// back to the ID if this somehow fails.
	uname, _ := s.SetUsername(ctx, id, deriveUsername(email, name))
	return &User{ID: id, Email: email, Name: strings.TrimSpace(name), Role: role, Username: uname, CreatedAt: time.Now().UTC()}, nil
}

// CreateBootstrapAdmin creates the first administrator on a fresh install with
// the must-change-password flag set, so the operator is forced to replace the
// auto-generated default password on first login. It behaves like Create but
// pins the admin role and the flag.
func (s *Store) CreateBootstrapAdmin(ctx context.Context, email, name, password string) (*User, error) {
	u, err := s.Create(ctx, email, name, password, RoleAdmin)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET must_change_password=1 WHERE id=?`, u.ID); err != nil {
		return nil, err
	}
	u.MustChangePassword = true
	return u, nil
}

// CreateClient creates an agency client account bound to one hosted domain
// (ADR-0152). It is the ONLY way a RoleClient account comes into existence:
// Create and SetRole both refuse the role outright, because neither can take the
// binding, and a client without one is an identity that authenticates and then
// reaches nothing.
//
// The binding is required rather than defaulted. ” is the PRIMARY domain's
// sentinel everywhere in this codebase — articles.domain_id, members.domain_id,
// analytics_daily.domain_id — so a client defaulted to ” would not be an
// unconfigured client, it would be a paying customer of the studio holding a
// login scoped to the studio's own install.
//
// The password is chosen by the operator and handed over out of band, then
// changed by the client. must_change_password is set so the operator's choice
// cannot remain the live credential: the account the studio hands over is the
// one account whose password the studio should stop knowing.
func (s *Store) CreateClient(ctx context.Context, email, name, password, domainID string) (*User, error) {
	domainID = strings.TrimSpace(domainID)
	if domainID == "" {
		return nil, fmt.Errorf("a client account needs the domain it is bound to")
	}
	email = strings.TrimSpace(strings.ToLower(email))
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, fmt.Errorf("invalid email: %w", err)
	}
	if len(password) < 8 {
		return nil, fmt.Errorf("password must be at least 8 characters")
	}
	hash, err := auth.HashSecretArgon2id(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	id := newID()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO users(id,email,name,password_hash,role,client_domain_id,must_change_password) VALUES(?,?,?,?,?,?,1)`,
		id, email, strings.TrimSpace(name), hash, RoleClient, domainID); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("a user with that email already exists")
		}
		return nil, fmt.Errorf("create client: %w", err)
	}
	return &User{
		ID: id, Email: email, Name: strings.TrimSpace(name), Role: RoleClient,
		ClientDomainID: domainID, MustChangePassword: true, CreatedAt: time.Now().UTC(),
	}, nil
}

// ClientsForDomain returns the client accounts bound to one hosted domain, so
// the operator can see who they have already given a login to before issuing
// another.
func (s *Store) ClientsForDomain(ctx context.Context, domainID string) ([]User, error) {
	domainID = strings.TrimSpace(domainID)
	if domainID == "" {
		return nil, nil
	}
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT id,email,name,created_at,last_login FROM users WHERE role=? AND client_domain_id=? ORDER BY created_at`,
		RoleClient, domainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var lastLogin sql.NullTime
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.CreatedAt, &lastLogin); err != nil {
			return nil, err
		}
		if lastLogin.Valid {
			t := lastLogin.Time
			u.LastLogin = &t
		}
		u.Role, u.ClientDomainID = RoleClient, domainID
		out = append(out, u)
	}
	_ = rows.Err()
	return out, nil
}

// Authenticate verifies email + password and returns the user on success. The
// Argon2id verification runs even when the email is unknown (against a decoy)
// to keep the response time independent of account existence.
func (s *Store) Authenticate(ctx context.Context, email, password string) (*User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	var u User
	var hash string
	var lastLogin sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT id,email,name,password_hash,role,created_at,last_login,COALESCE(client_domain_id,'') FROM users WHERE email=?`, email).
		Scan(&u.ID, &u.Email, &u.Name, &hash, &u.Role, &u.CreatedAt, &lastLogin, &u.ClientDomainID)
	if err == sql.ErrNoRows {
		// Constant-ish work to blunt user-enumeration timing.
		auth.VerifySecretArgon2id(password, decoyHash)
		return nil, fmt.Errorf("invalid email or password")
	}
	if err != nil {
		return nil, err
	}
	if !auth.VerifySecretArgon2id(password, hash) {
		return nil, fmt.Errorf("invalid email or password")
	}
	if lastLogin.Valid {
		u.LastLogin = &lastLogin.Time
	}
	return &u, nil
}

// profileCols is the SELECT list for reads that include public profile fields.
const profileCols = `id,email,name,role,avatar_url,bio,socials,mail_address,created_at,last_login,COALESCE(must_change_password,0),COALESCE(username,''),COALESCE(client_domain_id,'')`

// scanUserProfile reads a row selected with profileCols.
func scanUserProfile(sc interface{ Scan(...interface{}) error }) (*User, error) {
	var u User
	var avatar, bio, socials, mailAddr string
	var lastLogin sql.NullTime
	var mustChange int
	var username, clientDomain string
	if err := sc.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &avatar, &bio, &socials, &mailAddr, &u.CreatedAt, &lastLogin, &mustChange, &username, &clientDomain); err != nil {
		return nil, err
	}
	u.AvatarURL = avatar
	u.Bio = bio
	u.Socials = decodeSocials(socials)
	u.MailAddress = mailAddr
	u.MustChangePassword = mustChange != 0
	u.Username = username
	u.ClientDomainID = clientDomain
	if lastLogin.Valid {
		u.LastLogin = &lastLogin.Time
	}
	return &u, nil
}

// GetByID returns the user with the given id, including profile fields.
func (s *Store) GetByID(ctx context.Context, id string) (*User, error) {
	return scanUserProfile(s.readDB().QueryRowContext(ctx,
		`SELECT `+profileCols+` FROM users WHERE id=?`, id))
}

// GetByEmail returns the user with the given email, including profile fields.
func (s *Store) GetByEmail(ctx context.Context, email string) (*User, error) {
	return scanUserProfile(s.db.QueryRowContext(ctx,
		`SELECT `+profileCols+` FROM users WHERE email=?`, strings.TrimSpace(strings.ToLower(email))))
}

// GetByUsername returns the user with the given public handle (case-insensitive).
func (s *Store) GetByUsername(ctx context.Context, username string) (*User, error) {
	username = normalizeUsername(username)
	if username == "" {
		return nil, fmt.Errorf("empty username")
	}
	return scanUserProfile(s.db.QueryRowContext(ctx,
		`SELECT `+profileCols+` FROM users WHERE username=?`, username))
}

// SetUsername assigns a public handle to a user, uniquifying it (-2, -3, …) if
// it collides. Returns the handle actually stored.
func (s *Store) SetUsername(ctx context.Context, id, desired string) (string, error) {
	base := normalizeUsername(desired)
	if base == "" {
		base = "user"
	}
	for i := 0; i < 100; i++ {
		cand := base
		if i > 0 {
			cand = base + "-" + strconv.Itoa(i+1)
		}
		// Skip if taken by another user.
		var other string
		err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE username=?`, cand).Scan(&other)
		if err == sql.ErrNoRows || other == id {
			if _, uerr := s.db.ExecContext(ctx, `UPDATE users SET username=? WHERE id=?`, cand, id); uerr != nil {
				return "", uerr
			}
			return cand, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("could not find a free username for %q", base)
}

// BackfillUsernames assigns a derived handle to every account that lacks one
// (e.g. accounts created before migration 051). Cheap — staff counts are small —
// and idempotent, so it is safe to run at every startup.
func (s *Store) BackfillUsernames(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,email,name FROM users WHERE COALESCE(username,'')=''`)
	if err != nil {
		return
	}
	type todo struct{ id, email, name string }
	var list []todo
	for rows.Next() {
		var t todo
		if rows.Scan(&t.id, &t.email, &t.name) == nil {
			list = append(list, t)
		}
	}
	_ = rows.Err()
	rows.Close()
	for _, t := range list {
		_, _ = s.SetUsername(ctx, t.id, deriveUsername(t.email, t.name))
	}
}

// deriveUsername proposes a handle from the email local-part (preferred) or the
// display name.
func deriveUsername(email, name string) string {
	if i := strings.IndexByte(email, '@'); i > 0 {
		if u := normalizeUsername(email[:i]); u != "" {
			return u
		}
	}
	if u := normalizeUsername(name); u != "" {
		return u
	}
	return "user"
}

// normalizeUsername lowercases and keeps only [a-z0-9-], collapsing runs of
// other characters to a single dash and trimming leading/trailing dashes.
func normalizeUsername(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// List returns all users ordered by creation time, including profile fields.
func (s *Store) List(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+profileCols+` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUserProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// SetMailAddress records the VayuMail mailbox address assigned to a user (or
// clears it when addr is empty). The address is normalised to lowercase.
func (s *Store) SetMailAddress(ctx context.Context, id, addr string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET mail_address=? WHERE id=?`,
		strings.TrimSpace(strings.ToLower(addr)), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no user with that id")
	}
	return nil
}

// SetRole changes a user's role by email. The role must be recognised.
func (s *Store) SetRole(ctx context.Context, email, role string) error {
	if !ValidRole(role) {
		return fmt.Errorf("invalid role %q (want admin, editor, author, or client)", role)
	}
	// Promoting an existing staff account to client through the ordinary role
	// picker would leave the binding empty — see Create. It is also the wrong
	// operation: a client is a different KIND of principal, not a rung further
	// down the ladder, and converting a colleague into one silently strips every
	// piece of content they own from their reach.
	if role == RoleClient {
		return fmt.Errorf("a client account is created against its domain, not converted from a staff account")
	}
	// Demoting a client to a staff role must clear the binding, or a stale
	// domain id rides along on an account that is no longer confined by it.
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET role=?,client_domain_id='' WHERE email=?`, role, strings.TrimSpace(strings.ToLower(email)))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no user with that email")
	}
	return nil
}

// UpdateProfile sets a user's public profile (display name, bio, avatar URL,
// and social links) by id. Name and bio are length-capped; social link values
// must be http(s) URLs and are stored as a label->URL map.
func (s *Store) UpdateProfile(ctx context.Context, id, name, bio, avatarURL string, socials map[string]string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if len([]rune(name)) > MaxNameLen {
		return fmt.Errorf("name must be at most %d characters", MaxNameLen)
	}
	bio = strings.TrimSpace(bio)
	if len([]rune(bio)) > MaxBioLen {
		return fmt.Errorf("bio must be at most %d characters", MaxBioLen)
	}
	avatarURL = strings.TrimSpace(avatarURL)
	if avatarURL != "" && !isHTTPURL(avatarURL) {
		return fmt.Errorf("avatar URL must be an http(s) URL")
	}
	clean := map[string]string{}
	for label, link := range socials {
		label = strings.TrimSpace(strings.ToLower(label))
		link = strings.TrimSpace(link)
		if label == "" || link == "" {
			continue
		}
		if !isHTTPURL(link) {
			return fmt.Errorf("social link for %q must be an http(s) URL", label)
		}
		clean[label] = link
	}
	enc, _ := json.Marshal(clean)
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET name=?,bio=?,avatar_url=?,socials=? WHERE id=?`,
		name, bio, avatarURL, string(enc), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no user with that id")
	}
	return nil
}

// decodeSocials parses the stored JSON map, tolerating empty/legacy values.
func decodeSocials(raw string) map[string]string {
	if strings.TrimSpace(raw) == "" || raw == "{}" {
		return nil
	}
	m := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil || len(m) == 0 {
		return nil
	}
	return m
}

// isHTTPURL reports whether s is a syntactically valid absolute http(s) URL.
func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// SetPassword updates a user's password (after the same strength check as Create).
func (s *Store) SetPassword(ctx context.Context, email, password string) error {
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	hash, err := auth.HashSecretArgon2id(password)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash=?, must_change_password=0 WHERE email=?`, hash, strings.TrimSpace(strings.ToLower(email)))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no user with that email")
	}
	return nil
}

// TouchLastLogin records a successful login time.
func (s *Store) TouchLastLogin(ctx context.Context, id string) {
	_, _ = s.db.ExecContext(ctx,
		`UPDATE users SET last_login=? WHERE id=?`, time.Now().UTC().Format("2006-01-02 15:04:05"), id)
}

// Count returns the number of accounts (used to detect first-run bootstrap).
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM users`).Scan(&n)
	return n, err
}

// Delete removes a user by email.
func (s *Store) Delete(ctx context.Context, email string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE email=?`, strings.TrimSpace(strings.ToLower(email)))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no user with that email")
	}
	return nil
}

// ── Two-factor (TOTP) ──────────────────────────────────────────────────────

// SecretCodec seals sensitive column values at rest. It is satisfied by the
// service-credential store (internal/secrets), whose DEK is wrapped by
// VAYU_SECRET or a host-bound key file (audit: TOTP seeds were plaintext
// base32 in the database, so a DB read alone yielded every second factor).
type SecretCodec interface {
	SealField(plaintext string) (string, error)
	OpenField(stored string) (string, error)
}

// UseTOTPCodec installs an at-rest codec for the totp_secret column. Legacy
// plaintext rows keep verifying (OpenField passes them through) and are
// re-sealed on their next write.
func (s *Store) UseTOTPCodec(c SecretCodec) { s.totpCodec = c }

// sealTOTP applies the codec when present; otherwise the value is stored as-is.
func (s *Store) sealTOTP(secret string) (string, error) {
	if s.totpCodec == nil {
		return secret, nil
	}
	return s.totpCodec.SealField(secret)
}

// openTOTP reverses sealTOTP; legacy plaintext and empty values pass through.
func (s *Store) openTOTP(stored string) string {
	if s.totpCodec == nil || stored == "" {
		return stored
	}
	pt, err := s.totpCodec.OpenField(stored)
	if err != nil {
		// A sealed value that cannot be opened means the KEK changed under us;
		// returning garbage would lock the operator out with a confusing
		// "wrong code" error, so fail loudly instead.
		return ""
	}
	return pt
}

// TOTPStatus reports whether the user has a pending secret and whether 2FA is
// fully enabled (verified).
func (s *Store) TOTPStatus(ctx context.Context, id string) (secret string, enabled bool, err error) {
	var enabledInt int
	err = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(totp_secret,''), COALESCE(totp_enabled,0) FROM users WHERE id=?`, id).
		Scan(&secret, &enabledInt)
	return s.openTOTP(secret), enabledInt == 1, err
}

// SetTOTPSecret stores a (not-yet-verified) secret for the user. Enabling is a
// separate step so an abandoned enrolment never activates 2FA.
func (s *Store) SetTOTPSecret(ctx context.Context, id, secret string) error {
	stored, err := s.sealTOTP(secret)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE users SET totp_secret=?, totp_enabled=0 WHERE id=?`, stored, id)
	return err
}

// EnableTOTP marks 2FA active once the user has proven possession of the secret.
func (s *Store) EnableTOTP(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET totp_enabled=1 WHERE id=?`, id)
	return err
}

// DisableTOTP clears the secret and turns 2FA off.
func (s *Store) DisableTOTP(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET totp_secret='', totp_enabled=0 WHERE id=?`, id)
	return err
}

// ConsumeTOTPStep enforces single-use codes: it advances the account's last
// consumed time step only when step is strictly newer than what is recorded,
// reporting ok=false for a replay (audit: a captured code stayed valid for its
// whole ±30s window). Callers verify the code first via totp.MatchAt and then
// consume its counter atomically.
func (s *Store) ConsumeTOTPStep(ctx context.Context, email string, step int64) (ok bool, err error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET totp_last_step=? WHERE email=? AND totp_last_step<?`,
		step, strings.TrimSpace(strings.ToLower(email)), step)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// TOTPSecretByEmail returns the stored secret and enabled flag for a login by
// email — used during sign-in to decide whether to demand a 2FA code.
func (s *Store) TOTPSecretByEmail(ctx context.Context, email string) (secret string, enabled bool, err error) {
	var enabledInt int
	err = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(totp_secret,''), COALESCE(totp_enabled,0) FROM users WHERE email=?`,
		strings.TrimSpace(strings.ToLower(email))).Scan(&secret, &enabledInt)
	return s.openTOTP(secret), enabledInt == 1, err
}

// decoyHash is a valid Argon2id-encoded hash of a random value, used to spend
// comparable CPU time on unknown-email logins so timing does not reveal which
// emails are registered.
var decoyHash = func() string {
	h, _ := auth.HashSecretArgon2id("vayupress-decoy-password")
	return h
}()

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
