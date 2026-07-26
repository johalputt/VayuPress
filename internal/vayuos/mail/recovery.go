package mail

// recovery.go — account recovery for VayuMail mailboxes (ADR-0144).
//
// Before this, a holder who forgot their mailbox password was permanently locked
// out, and the lockout was total: IMAP/SMTP need the password, the mobile app
// needs an app password minted while they still had access, and webmail
// authenticates by mailing a one-time link TO THE MAILBOX THEY CANNOT OPEN. The
// only way back in was an administrator calling SetPasswordHash.
//
// Two factors are stored here, both enrolled in advance while authenticated —
// trust cannot be bootstrapped at recovery time, so a factor that could be
// established after access is lost would be one an attacker could establish too:
//
//   - Recovery CODES: ten single-use codes, shown once, Argon2id-hashed. No
//     network, no third party, no outbound queue — they work in a Tor Space, and
//     they work when the mail plane itself is what broke. This is the default.
//   - A recovery ADDRESS: verified by round-trip, and required to be OFF THIS
//     INSTALL. Not merely a different domain: one install serves many mail
//     domains from domain-partitioned Maildir trees, so a same-install second
//     domain would rebuild the very loop this feature exists to open.
//
// A password reset is a pure authentication event. PGP private keys are sealed
// with a server-side DEK (keyenvelope.go); the clearnet Maildir is plaintext, and
// a Tor Space encrypts mail at rest under that same keystore DEK — never under
// the holder's password. So no key rewrap and no data loss are involved.
//
// THE INVARIANT: no recovery-relevant secret may be derived from the mailbox
// password. The moment one is, a reset silently destroys mail, and ADR-0144 has
// to be revisited before any of this can be trusted again.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
)

const (
	// RecoveryCodeCount is how many single-use codes a generation produces.
	RecoveryCodeCount = 10
	// RecoveryTokenTTL bounds a reset link's life. Short, because the link is a
	// bearer credential for the highest-value account in the system.
	RecoveryTokenTTL = 30 * time.Minute
	// RecoveryContactTTL bounds an unconfirmed recovery-address verification.
	RecoveryContactTTL = 24 * time.Hour
)

// ErrNoRecovery is returned when an account has no usable recovery factor. It is
// deliberately NOT surfaced to an unauthenticated caller — see the handler layer,
// which must answer identically whether or not an address exists.
var ErrNoRecovery = errors.New("vayumail: no recovery factor enrolled")

// RecoveryStatus summarises what an account can currently recover with. It
// carries no secrets, so it is safe to render in the console.
type RecoveryStatus struct {
	Email string `json:"email"`
	// Contact is the verified recovery address, empty when none is confirmed.
	Contact string `json:"contact,omitempty"`
	// ContactPending is an address awaiting its verification round-trip. It does
	// not count as a factor: an unverified address is a typo that would redirect
	// the master key to a stranger.
	ContactPending string `json:"contact_pending,omitempty"`
	// CodesRemaining counts unused recovery codes.
	CodesRemaining int `json:"codes_remaining"`
	// Ready reports whether ANY factor could actually recover this mailbox.
	Ready bool `json:"ready"`
}

// ensureRecovery creates the recovery tables and columns on first use. Same
// tolerant re-run pattern as the other stores here: SQLite reports "duplicate
// column name" when a column already exists, which is success.
func (s *AccountStore) ensureRecovery() error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	// Codes are per-account and single-use. used_at is kept rather than deleting
	// the row so a support conversation can distinguish "never had codes" from
	// "burned through all ten", which are very different situations.
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_recovery_codes(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		email TEXT NOT NULL,
		code_hash TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		used_at DATETIME);`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_vayumail_recovery_codes_email
		ON vayumail_recovery_codes(email)`); err != nil {
		return err
	}
	// Only the token's SHA-256 is stored: a database read must not yield a working
	// reset link. SHA-256 (not Argon2id) is correct here because the token is 32
	// bytes of CSPRNG output — there is no low-entropy secret to slow an attacker
	// down over, and the lookup is on the hot path of a rate-limited endpoint.
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_recovery_tokens(
		token_hash TEXT PRIMARY KEY,
		email TEXT NOT NULL,
		purpose TEXT NOT NULL DEFAULT 'reset',
		expires_at DATETIME NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_vayumail_recovery_tokens_email
		ON vayumail_recovery_tokens(email)`); err != nil {
		return err
	}
	for _, stmt := range []string{
		`ALTER TABLE vayumail_accounts ADD COLUMN recovery_contact TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE vayumail_accounts ADD COLUMN recovery_contact_pending TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	return nil
}

// ── Recovery codes ───────────────────────────────────────────────────────────

// GenerateRecoveryCodes replaces every code for an account and returns the new
// plaintext set. The caller shows them ONCE; only hashes are kept.
//
// Replacing rather than appending is deliberate. A holder regenerates codes
// because they believe the old sheet is compromised or lost, and leaving the old
// ones live would make regeneration a no-op against exactly the threat that
// prompted it.
func (s *AccountStore) GenerateRecoveryCodes(ctx context.Context, email string) ([]string, error) {
	if err := s.ensureRecovery(); err != nil {
		return nil, err
	}
	email = normEmail(email)
	if !s.Exists(ctx, email) {
		return nil, fmt.Errorf("vayumail: no such account")
	}

	codes := make([]string, 0, RecoveryCodeCount)
	hashes := make([]string, 0, RecoveryCodeCount)
	for i := 0; i < RecoveryCodeCount; i++ {
		c, err := newRecoveryCode()
		if err != nil {
			return nil, err
		}
		// Argon2id, not SHA-256: a code is short enough to be typed by a human, so
		// a leaked table must not be brute-forceable at GPU speed.
		h, err := auth.HashSecretArgon2id(c)
		if err != nil {
			return nil, err
		}
		codes = append(codes, c)
		hashes = append(hashes, h)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM vayumail_recovery_codes WHERE email=?`, email); err != nil {
		return nil, err
	}
	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO vayumail_recovery_codes(email,code_hash) VALUES(?,?)`, email, h); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return codes, nil
}

// ConsumeRecoveryCode verifies a code and burns it, reporting whether it was
// valid. It is the only way a code is spent.
//
// Every unused code must be compared, because the hashes are salted and there is
// nothing to look the code up by. That cost is bounded (ten Argon2id runs) and
// the endpoint above is rate-limited; the alternative — an unsalted lookup key —
// would make a leaked table trivially reversible.
func (s *AccountStore) ConsumeRecoveryCode(ctx context.Context, email, code string) bool {
	if err := s.ensureRecovery(); err != nil {
		return false
	}
	email = normEmail(email)
	code = normalizeRecoveryCode(code)
	if code == "" {
		return false
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,code_hash FROM vayumail_recovery_codes WHERE email=? AND used_at IS NULL`, email)
	if err != nil {
		return false
	}
	type candidate struct {
		id   int64
		hash string
	}
	var cands []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.hash); err == nil {
			cands = append(cands, c)
		}
	}
	_ = rows.Err()
	_ = rows.Close()

	for _, c := range cands {
		if !auth.VerifySecretArgon2id(code, c.hash) {
			continue
		}
		// Atomic single-use, the same guard as members.ConsumeLoginToken: two
		// concurrent redemptions of one code both match here, but only the UPDATE
		// that actually changes a row may proceed. Without it a captured code could
		// be raced into two resets.
		res, err := s.db.ExecContext(ctx,
			`UPDATE vayumail_recovery_codes SET used_at=CURRENT_TIMESTAMP WHERE id=? AND used_at IS NULL`, c.id)
		if err != nil {
			return false
		}
		if n, _ := res.RowsAffected(); n == 1 {
			return true
		}
		return false
	}
	return false
}

// CountRecoveryCodes returns how many unused codes remain.
func (s *AccountStore) CountRecoveryCodes(ctx context.Context, email string) int {
	if err := s.ensureRecovery(); err != nil {
		return 0
	}
	var n int
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM vayumail_recovery_codes WHERE email=? AND used_at IS NULL`,
		normEmail(email)).Scan(&n)
	return n
}

// ── Recovery address ─────────────────────────────────────────────────────────

// SetRecoveryContactPending records an address awaiting verification. It does
// NOT become usable until VerifyRecoveryContact confirms the round-trip.
//
// accepts reports whether a domain is served by this install (Config.
// AcceptsMailDomain). It is a parameter rather than a package lookup so the rule
// is testable and so the store keeps no dependency on engine configuration.
func (s *AccountStore) SetRecoveryContactPending(ctx context.Context, email, contact string, accepts func(domain string) bool) error {
	if err := s.ensureRecovery(); err != nil {
		return err
	}
	email = normEmail(email)
	contact = normEmail(contact)
	if err := validRecoveryContact(email, contact, accepts); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE vayumail_accounts SET recovery_contact_pending=? WHERE email=?`, contact, email)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("vayumail: no such account")
	}
	return nil
}

// validRecoveryContact enforces the off-install rule. A recovery address that
// this install serves is not a recovery address at all: losing the mailbox
// password loses both, which is precisely the loop ADR-0144 exists to break. The
// check is against every domain the install accepts mail for, not just the
// primary — a same-install secondary domain fails in exactly the same way while
// looking, to the holder, like a different provider.
func validRecoveryContact(email, contact string, accepts func(domain string) bool) error {
	if contact == "" {
		return fmt.Errorf("recovery address is required")
	}
	if contact == email {
		return fmt.Errorf("the recovery address cannot be the mailbox itself")
	}
	at := strings.LastIndex(contact, "@")
	if at <= 0 || at == len(contact)-1 || strings.Count(contact, "@") != 1 {
		return fmt.Errorf("enter a valid email address")
	}
	domain := contact[at+1:]
	if strings.ContainsAny(contact, " \t\r\n,;<>\"") || !strings.Contains(domain, ".") {
		return fmt.Errorf("enter a valid email address")
	}
	if accepts != nil && accepts(domain) {
		return fmt.Errorf("choose an address on a different mail provider — %s is hosted on this server, "+
			"so losing this mailbox would lose the recovery address with it", domain)
	}
	return nil
}

// VerifyRecoveryContact promotes the pending address to the confirmed one.
func (s *AccountStore) VerifyRecoveryContact(ctx context.Context, email string) error {
	if err := s.ensureRecovery(); err != nil {
		return err
	}
	email = normEmail(email)
	var pending string
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(recovery_contact_pending,'') FROM vayumail_accounts WHERE email=?`, email).Scan(&pending); err != nil {
		return fmt.Errorf("vayumail: no such account")
	}
	if pending == "" {
		return fmt.Errorf("no recovery address is awaiting verification")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE vayumail_accounts SET recovery_contact=?, recovery_contact_pending='' WHERE email=?`, pending, email)
	return err
}

// ClearRecoveryContact removes both the confirmed and pending addresses.
func (s *AccountStore) ClearRecoveryContact(ctx context.Context, email string) error {
	if err := s.ensureRecovery(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE vayumail_accounts SET recovery_contact='', recovery_contact_pending='' WHERE email=?`, normEmail(email))
	return err
}

// RecoveryContact returns the CONFIRMED recovery address, or "" when none is.
func (s *AccountStore) RecoveryContact(ctx context.Context, email string) string {
	if err := s.ensureRecovery(); err != nil {
		return ""
	}
	var c string
	_ = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(recovery_contact,'') FROM vayumail_accounts WHERE email=? AND active=1`,
		normEmail(email)).Scan(&c)
	return c
}

// ── Reset tokens ─────────────────────────────────────────────────────────────

// CreateRecoveryToken issues a one-time reset token and returns the raw value to
// embed in the emailed link. Only its hash is stored.
func (s *AccountStore) CreateRecoveryToken(ctx context.Context, email string) (string, error) {
	if err := s.ensureRecovery(); err != nil {
		return "", err
	}
	email = normEmail(email)
	token, err := randomHex(32)
	if err != nil {
		return "", err
	}
	exp := time.Now().UTC().Add(RecoveryTokenTTL).Format("2006-01-02 15:04:05")
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO vayumail_recovery_tokens(token_hash,email,expires_at) VALUES(?,?,?)`,
		hashRecoveryToken(token), email, exp); err != nil {
		return "", err
	}
	return token, nil
}

// ConsumeRecoveryToken validates and deletes a reset token, returning the
// account it belongs to. Single-use and atomic, so a captured link cannot be
// replayed into a second reset.
func (s *AccountStore) ConsumeRecoveryToken(ctx context.Context, token string) (string, error) {
	if err := s.ensureRecovery(); err != nil {
		return "", err
	}
	if token == "" {
		return "", fmt.Errorf("invalid or expired link")
	}
	h := hashRecoveryToken(token)
	var email string
	var exp time.Time
	if err := s.db.QueryRowContext(ctx,
		`SELECT email,expires_at FROM vayumail_recovery_tokens WHERE token_hash=?`, h).Scan(&email, &exp); err != nil {
		return "", fmt.Errorf("invalid or expired link")
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM vayumail_recovery_tokens WHERE token_hash=?`, h)
	if err != nil {
		return "", fmt.Errorf("invalid or expired link")
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", fmt.Errorf("invalid or expired link")
	}
	// Expiry is checked AFTER the delete so an expired token is also burned rather
	// than left for a later attempt.
	if time.Now().UTC().After(exp.UTC()) {
		return "", fmt.Errorf("link expired")
	}
	return email, nil
}

// InvalidateRecoveryTokens drops every outstanding token for an account. Called
// on any password change, so a reset link minted before the change is dead.
func (s *AccountStore) InvalidateRecoveryTokens(ctx context.Context, email string) {
	if err := s.ensureRecovery(); err != nil {
		return
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM vayumail_recovery_tokens WHERE email=?`, normEmail(email))
}

// ── Credential revocation ────────────────────────────────────────────────────

// RevokeAllAppPasswords removes every app password for a mailbox and reports how
// many were destroyed.
//
// This is the step a password reset cannot skip. App passwords are INDEPENDENT
// credentials, not derivatives of the mailbox password: an attacker who enrolled
// a device keeps full IMAP and SMTP access after the victim "recovers", and the
// victim has no reason to suspect it — they changed their password, so they
// believe they locked the door. Recovery that leaves them alive is theatre.
func (s *AccountStore) RevokeAllAppPasswords(ctx context.Context, email string) (int, error) {
	if err := s.ensureAppPasswords(); err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM vayumail_app_passwords WHERE email=?`, normEmail(email))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// HoldQueuedOutbound parks an account's undelivered mail in the 'held' state and
// reports how many messages were stopped.
//
// Sending is an attacker's first act — outbound reply-to-all fraud, or a sweep of
// password resets at other services using this address. A reset therefore stops
// the queue for that sender rather than letting whatever was staged go out under
// the recovered account. 'held' is a terminal state for the delivery loop, which
// only ever selects 'pending', so this cannot race a send already in flight into
// a half-delivered state; the operator releases them with Requeue.
func (q *Queue) HoldQueuedOutbound(ctx context.Context, from string) (int, error) {
	if q == nil || q.db == nil {
		return 0, errors.New("vayumail: no queue")
	}
	res, err := q.db.ExecContext(ctx,
		`UPDATE vayumail_queue SET state='held', last_error='held: account password was reset'
		 WHERE from_addr=? AND state IN ('pending','failed')`, strings.ToLower(strings.TrimSpace(from)))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// VerifyApprovedDevice reports whether secret matches an APPROVED app password
// for this mailbox, returning the matching credential's id.
//
// This is the trusted-device recovery factor: a holder whose phone still works
// has already proved, at enrolment time, that they controlled the account. It is
// the one factor that needs no advance planning beyond having used the app.
//
// It gates on status only. last_used_at looks tempting as a freshness check, but
// TouchAppPassword documents it as best-effort operator telemetry and explicitly
// "never a security decision" — a missed write would lock out a legitimate holder,
// which is the opposite of what a recovery path is for. Approval status is the
// real lifecycle gate, and a device the operator blocked is refused here.
func (s *AccountStore) VerifyApprovedDevice(ctx context.Context, email, secret string) (int64, bool) {
	if secret == "" {
		return 0, false
	}
	for _, c := range s.AppPasswordCredentials(ctx, email) {
		if c.Status != DeviceStatusApproved {
			continue
		}
		if auth.VerifySecretArgon2id(secret, c.Hash) {
			return c.ID, true
		}
	}
	return 0, false
}

// HoldOutboundFor parks an account's undelivered mail. Exposed on the Engine so
// the queue itself stays unexported — callers outside this package have no
// business reaching into delivery state for anything else.
func (e *Engine) HoldOutboundFor(ctx context.Context, from string) (int, error) {
	if e == nil || e.queue == nil {
		return 0, errors.New("vayumail: queue not started")
	}
	return e.queue.HoldQueuedOutbound(ctx, from)
}

// ── Status ───────────────────────────────────────────────────────────────────

// RecoveryStatusFor summarises an account's enrolled factors.
func (s *AccountStore) RecoveryStatusFor(ctx context.Context, email string) RecoveryStatus {
	email = normEmail(email)
	st := RecoveryStatus{Email: email}
	if err := s.ensureRecovery(); err != nil {
		return st
	}
	_ = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(recovery_contact,''),COALESCE(recovery_contact_pending,'')
		 FROM vayumail_accounts WHERE email=?`, email).Scan(&st.Contact, &st.ContactPending)
	st.CodesRemaining = s.CountRecoveryCodes(ctx, email)
	// Only CONFIRMED factors count. A pending address is not recovery.
	st.Ready = st.Contact != "" || st.CodesRemaining > 0
	return st
}

// UnrecoverableAccounts lists active mailboxes with no usable factor at all.
// This is the readiness view: the failure is silent until the day it matters, so
// it has to be visible before then.
func (s *AccountStore) UnrecoverableAccounts(ctx context.Context) []string {
	if err := s.ensureRecovery(); err != nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.email FROM vayumail_accounts a
		WHERE a.active=1 AND COALESCE(a.recovery_contact,'')=''
		  AND NOT EXISTS (SELECT 1 FROM vayumail_recovery_codes c
		                  WHERE c.email=a.email AND c.used_at IS NULL)
		ORDER BY a.email`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err == nil {
			out = append(out, e)
		}
	}
	_ = rows.Err()
	return out
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// recoveryCodeAlphabet excludes the characters people misread when copying a
// code off paper: 0/O, 1/I/L, and the vowels that let a random string spell
// something unfortunate.
const recoveryCodeAlphabet = "23456789BCDFGHJKMNPQRSTVWXYZ"

// newRecoveryCode returns a code in "XXXX-XXXX-XXXX" form. Twelve characters
// from a 28-symbol alphabet is ~57 bits — far beyond guessing against a
// rate-limited endpoint, and still short enough to write down.
func newRecoveryCode() (string, error) {
	const n = 12
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, 0, n+2)
	for i, v := range b {
		if i > 0 && i%4 == 0 {
			out = append(out, '-')
		}
		// Modulo bias here is negligible (256 mod 28 = 4, a <1.6% skew on four
		// symbols) and irrelevant at 57 bits, but rejection sampling costs nothing.
		for v >= 252 {
			var one [1]byte
			if _, err := rand.Read(one[:]); err != nil {
				return "", err
			}
			v = one[0]
		}
		out = append(out, recoveryCodeAlphabet[int(v)%len(recoveryCodeAlphabet)])
	}
	return string(out), nil
}

// normalizeRecoveryCode makes comparison forgiving of how a human retypes a code
// from paper: any case, any spacing, dashes optional.
func normalizeRecoveryCode(c string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(c) {
		if r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) != 12 {
		return ""
	}
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12]
}

func hashRecoveryToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
