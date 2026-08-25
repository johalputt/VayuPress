// SPDX-License-Identifier: Apache-2.0

// Package members implements reader memberships and content paywalls for
// VayuPress.
//
// Readers are distinct from admin authors (internal/users). A member is just an
// email with a tier (free or paid) and a status. Authentication is passwordless:
// a member requests a magic link, receives a one-time token by email, and
// exchanges it for a member session cookie. This keeps the reader experience
// frictionless and stores no reader passwords.
//
// Paywalls are expressed per article via article_access.level (public|members).
// Enforcement (serving a preview + CTA to non-members) lives in the HTTP layer;
// this package owns persistence, tokens, and tier logic only.
//
// Payments are optional and decoupled: tiers can be set manually by an admin, or
// upgraded automatically by the Stripe webhook receiver. VayuPress never embeds a
// payment SDK — it only reacts to a signed webhook.
package members

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// Tiers and access levels.
const (
	TierFree = "free"
	TierPaid = "paid"

	AccessPublic  = "public"  // anyone
	AccessMembers = "members" // any logged-in member (free or paid)
	AccessPaid    = "paid"    // paid members only
)

// LoginTokenTTL bounds how long a magic link is valid.
const LoginTokenTTL = 30 * time.Minute

// SessionTTL bounds a member login session.
const SessionTTL = 30 * 24 * time.Hour

// Member is a reader account.
type Member struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	Name            string     `json:"name"`
	Note            string     `json:"note,omitempty"`
	Tier            string     `json:"tier"`
	Status          string     `json:"status"`
	NewsletterOptIn bool       `json:"newsletter_opt_in"`
	ReplyNotify     bool       `json:"reply_notify"`
	StripeCustomer  string     `json:"-"`
	Labels          []string   `json:"labels,omitempty"`
	Country         string     `json:"country,omitempty"` // ISO alpha-2, GDPR-safe (no IP stored)
	Region          string     `json:"region,omitempty"`
	City            string     `json:"city,omitempty"`
	LastSeenAt      *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	// VerifiedAt is when this person proved they control the address — the magic
	// link was consumed, a mailbox credential authenticated, or a payment
	// completed. Every creation path stamps it, so nil means the row predates that
	// rule and was never confirmed.
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	// DomainID is the VayuDomains attribution: which domain this member signed up
	// on ("" = the primary / unassigned bucket). Login stays keyed by the globally
	// unique email, so this is a reporting dimension, not a read filter (Stage 4).
	DomainID string `json:"domain_id,omitempty"`
	// Gender is optional (neutral default) and only steers the deterministic auto
	// avatar. AvatarChoice selects what the member's avatar shows: "" = auto,
	// "photo" = their uploaded picture, "cartoon:N" = a prebuilt cartoon. The photo
	// blob itself is never loaded by the canonical member SELECT.
	Gender       string `json:"gender,omitempty"`
	AvatarChoice string `json:"avatar_choice,omitempty"`
}

// IsPaid reports whether the member has an active paying membership. Any active
// tier other than the built-in free tier counts as paid, so custom premium
// tiers unlock "paid" content just like the default paid tier does.
func (m *Member) IsPaid() bool {
	return m != nil && m.Status == "active" && m.Tier != "" && m.Tier != TierFree
}

// DisplayName returns the member's name, falling back to the local part of
// their email address so the portal always has something friendly to show.
func (m *Member) DisplayName() string {
	if m == nil {
		return ""
	}
	if strings.TrimSpace(m.Name) != "" {
		return m.Name
	}
	if i := strings.IndexByte(m.Email, '@'); i > 0 {
		return m.Email[:i]
	}
	return m.Email
}

// Store manages members, magic-link tokens, sessions, and per-article access.
type Store struct{ db *sql.DB }

// New creates a Store.
func New(db *sql.DB) *Store { return &Store{db: db} }

// memberCols is the canonical SELECT column list for scanning into a Member.
const memberCols = `id,email,name,note,tier,status,newsletter_opt_in,reply_notify,stripe_customer,last_seen_at,created_at,country,region,city,domain_id,gender,avatar_choice,verified_at`

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...interface{}) error
}

// scanMember reads one member row selected with memberCols.
func scanMember(sc scanner) (*Member, error) {
	var m Member
	var note, stripe string
	var optIn, replyNotify int
	var lastSeen, verified sql.NullTime
	if err := sc.Scan(&m.ID, &m.Email, &m.Name, &note, &m.Tier, &m.Status, &optIn, &replyNotify, &stripe, &lastSeen, &m.CreatedAt, &m.Country, &m.Region, &m.City, &m.DomainID, &m.Gender, &m.AvatarChoice, &verified); err != nil {
		return nil, err
	}
	m.Note = note
	m.StripeCustomer = stripe
	m.NewsletterOptIn = optIn != 0
	m.ReplyNotify = replyNotify != 0
	if lastSeen.Valid {
		t := lastSeen.Time.UTC()
		m.LastSeenAt = &t
	}
	if verified.Valid {
		t := verified.Time.UTC()
		m.VerifiedAt = &t
	}
	return &m, nil
}

// Upsert ensures a member row exists for email and returns it (primary domain).
func (s *Store) Upsert(ctx context.Context, email string) (*Member, error) {
	return s.UpsertScoped(ctx, "", email)
}

// UpsertScoped ensures a member row exists for email and returns it, attributing
// a brand-new signup to the given domain scope (VayuDomains Stage 4: "" = the
// primary domain, byte-identical; a secondary domain id records where the member
// signed up). An existing member keeps its original domain — email is globally
// unique, so a member is a single entity found by email regardless of scope.
//
// Calling this asserts the address has just been PROVEN: the caller consumed a
// magic link, authenticated a mailbox credential, or completed a payment. There
// is deliberately no way to create a member without that proof, so an address
// that was merely typed into a form never becomes a member — and verified_at is
// stamped here rather than left to each caller to remember.
func (s *Store) UpsertScoped(ctx context.Context, scope, email string) (*Member, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, fmt.Errorf("invalid email: %w", err)
	}
	if m, err := s.Get(ctx, email); err == nil {
		// An existing row that predates the verification rule is confirmed now: the
		// caller just proved control of the address.
		if m.VerifiedAt == nil {
			now := time.Now().UTC()
			if _, err := s.db.ExecContext(ctx, `UPDATE members SET verified_at=? WHERE id=? AND verified_at IS NULL`, now, m.ID); err == nil {
				m.VerifiedAt = &now
			}
		}
		return m, nil
	}
	id := randHex(12)
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO members(id,email,domain_id,verified_at) VALUES(?,?,?,?)`, id, email, scope, now); err != nil {
		return nil, fmt.Errorf("upsert member: %w", err)
	}
	s.recordEventTx(ctx, id, EventSignup, "", 0)
	return &Member{ID: id, Email: email, Tier: TierFree, Status: "active", NewsletterOptIn: true, DomainID: scope, CreatedAt: now, VerifiedAt: &now}, nil
}

// Exists reports whether a member row already exists for email. It is the
// enumeration-safe way to ask "is this a returning address?" without creating
// anything — used to decide whether a first-time welcome is due.
func (s *Store) Exists(ctx context.Context, email string) bool {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM members WHERE email=?`,
		strings.TrimSpace(strings.ToLower(email))).Scan(&one)
	return err == nil
}

// Unverified returns members that were never confirmed — rows created by the
// pre-verification signup path that shipped before verified_at existed. They are
// the only members whose address may not be real, so the console lists them for
// removal.
func (s *Store) Unverified(ctx context.Context, limit int) ([]Member, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+memberCols+` FROM members WHERE verified_at IS NULL ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// CountUnverified returns how many members were never confirmed. The count is
// a real query that can really fail: reporting 0 on error would tell the
// operator the cleanup queue is empty when it may not be (audit).
func (s *Store) CountUnverified(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM members WHERE verified_at IS NULL`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// RevokeSessions drops every live session and pending magic-link token for an
// address, returning how many sessions died. The member row itself is untouched.
//
// This exists for VayuMail account recovery (ADR-0144). A mailbox holder reaches
// webmail through a member session, so a password reset that left those sessions
// alive would leave an attacker signed in through the victim's own recovery. The
// pending login tokens go too: a magic link the attacker requested minutes ago is
// a working key that would otherwise outlive the reset.
func (s *Store) RevokeSessions(ctx context.Context, email string) (int, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return 0, fmt.Errorf("email required")
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM member_sessions WHERE member_id IN (SELECT id FROM members WHERE email=?)`, email)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM member_login_tokens WHERE email=?`, email); err != nil {
		return int(n), err
	}
	return int(n), nil
}

// Delete removes a member and everything keyed to them: sessions (so any live
// cookie dies immediately), pending magic-link tokens, subscription and event
// history. It refuses to touch a verified member — removing a real person's
// account is a different, deliberate operation, and this exists to clear
// never-confirmed rows.
// ErrVerified is returned by Delete when the member has confirmed their
// account. Match it with errors.Is — a wording-drifted string comparison
// silently converted a deliberate refusal into a misleading not-found (audit).
var ErrVerified = errors.New("member is verified")

func (s *Store) Delete(ctx context.Context, email string) error {
	email = strings.TrimSpace(strings.ToLower(email))
	m, err := s.Get(ctx, email)
	if err != nil {
		return fmt.Errorf("no such member")
	}
	if m.VerifiedAt != nil {
		return ErrVerified
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []struct {
		sql, arg string
	}{
		{`DELETE FROM member_sessions WHERE member_id=?`, m.ID},
		{`DELETE FROM member_events WHERE member_id=?`, m.ID},
		{`DELETE FROM member_subscriptions WHERE member_id=?`, m.ID},
		{`DELETE FROM member_label_map WHERE member_id=?`, m.ID},
		{`DELETE FROM member_login_tokens WHERE email=?`, m.Email},
		{`DELETE FROM members WHERE id=?`, m.ID},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.arg); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CountsByDomain returns the number of members per signup domain, keyed by
// domain_id ("" = the primary / unassigned bucket). Mirrors
// articles.CountsByDomain so the VayuOS Domains page can show a per-domain member
// count beside the article and mailbox counts (VayuDomains Stage 4).
func (s *Store) CountsByDomain(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT domain_id, COUNT(1) FROM members GROUP BY domain_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var dom string
		var n int
		if err := rows.Scan(&dom, &n); err != nil {
			return nil, err
		}
		out[dom] = n
	}
	return out, rows.Err()
}

// Get returns the member with the given email.
func (s *Store) Get(ctx context.Context, email string) (*Member, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+memberCols+` FROM members WHERE email=?`,
		strings.TrimSpace(strings.ToLower(email)))
	m, err := scanMember(row)
	if err != nil {
		return nil, err
	}
	m.Labels, _ = s.LabelsForMember(ctx, m.ID)
	return m, nil
}

// GetByID returns the member with the given id.
func (s *Store) GetByID(ctx context.Context, id string) (*Member, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+memberCols+` FROM members WHERE id=?`, id)
	m, err := scanMember(row)
	if err != nil {
		return nil, err
	}
	m.Labels, _ = s.LabelsForMember(ctx, m.ID)
	return m, nil
}

// GetByStripeCustomer returns the member linked to a Stripe customer id. Used by
// the webhook receiver to reconcile subscription updates and cancellations that
// arrive keyed by customer rather than by email.
func (s *Store) GetByStripeCustomer(ctx context.Context, customer string) (*Member, error) {
	if strings.TrimSpace(customer) == "" {
		return nil, fmt.Errorf("empty stripe customer")
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+memberCols+` FROM members WHERE stripe_customer=?`, customer)
	m, err := scanMember(row)
	if err != nil {
		return nil, err
	}
	m.Labels, _ = s.LabelsForMember(ctx, m.ID)
	return m, nil
}

// SetStripeCustomer links a member (by email) to a Stripe customer id so later
// subscription webhooks — which arrive keyed by customer, not email — can
// reconcile back to the member. Best-effort: a no-op when the member is absent.
func (s *Store) SetStripeCustomer(ctx context.Context, email, customer string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	customer = strings.TrimSpace(customer)
	if email == "" || customer == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE members SET stripe_customer=? WHERE email=?`, customer, email)
	return err
}

// SetMailAddress records the VayuMail address a member claimed with their tier's
// included mailbox (empty clears it). Stored outside memberCols so the canonical
// member SELECT is unchanged; read with MailAddressFor.
func (s *Store) SetMailAddress(ctx context.Context, email, mailAddr string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return fmt.Errorf("email required")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE members SET mail_address=? WHERE email=?`,
		strings.ToLower(strings.TrimSpace(mailAddr)), email)
	return err
}

// ClaimMailAddress atomically reserves a member's single mailbox slot: it sets
// mail_address to addr ONLY if the member currently has none, returning true when
// this call won the reservation. It closes the concurrent-claim TOCTOU (audit
// M13) where two parallel claims with different localparts each passed a
// read-then-act "one mailbox per member" check and each provisioned a real
// mailbox. On the single-writer SQLite exactly one racer's conditional UPDATE
// affects a row; the others get false. The caller provisions only after winning,
// and releases with ClearMailAddressIf if provisioning then fails.
func (s *Store) ClaimMailAddress(ctx context.Context, email, addr string) (bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	addr = strings.ToLower(strings.TrimSpace(addr))
	if email == "" || addr == "" {
		return false, fmt.Errorf("email and address required")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE members SET mail_address=? WHERE email=? AND (mail_address IS NULL OR mail_address='')`,
		addr, email)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ClearMailAddressIf releases a reservation made by ClaimMailAddress when the
// stored address still matches addr (i.e. provisioning failed after the claim),
// so the member can retry. It never clears a different, already-provisioned
// address.
func (s *Store) ClearMailAddressIf(ctx context.Context, email, addr string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	addr = strings.ToLower(strings.TrimSpace(addr))
	if email == "" || addr == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE members SET mail_address='' WHERE email=? AND mail_address=?`, email, addr)
	return err
}

// MailAddressFor returns the mailbox address a member has claimed, or "" if none.
func (s *Store) MailAddressFor(ctx context.Context, email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return ""
	}
	var addr string
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(mail_address,'') FROM members WHERE email=?`, email).Scan(&addr)
	return addr
}

// RecordMailIDAgreement stores a member's acceptance of the mail-ID terms — the
// address they took, a hex SHA-256 of the exact terms text they agreed to, and
// (via the column default) the time — so the operator holds durable proof of
// agreement for every provisioned address.
func (s *Store) RecordMailIDAgreement(ctx context.Context, email, address, termsSHA256 string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	address = strings.ToLower(strings.TrimSpace(address))
	if email == "" || address == "" {
		return fmt.Errorf("email and address required")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO mailid_agreements(id,email,address,terms_sha256) VALUES(?,?,?,?)`,
		"mag_"+randHex(12), email, address, strings.TrimSpace(termsSHA256))
	return err
}

// List returns members, newest first.
func (s *Store) List(ctx context.Context, limit int) ([]Member, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+memberCols+` FROM members ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Attach labels in a second pass so the row cursor is closed first.
	for i := range out {
		out[i].Labels, _ = s.LabelsForMember(ctx, out[i].ID)
	}
	return out, nil
}

// SetTier updates a member's tier by slug. The slug must be the built-in free
// tier or an existing tier in member_tiers. Assigning a tier keeps the member's
// subscription record in sync (see syncSubscriptionForTier).
func (s *Store) SetTier(ctx context.Context, email, tier string) error {
	email = strings.TrimSpace(strings.ToLower(email))
	if !s.tierAssignable(ctx, tier) {
		return fmt.Errorf("invalid tier %q", tier)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE members SET tier=? WHERE email=?`, tier, email)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no member with that email")
	}
	m, err := s.Get(ctx, email)
	if err == nil {
		_ = s.syncSubscriptionForTier(ctx, m.ID, tier)
	}
	return nil
}

// tierAssignable reports whether tier may be assigned to a member: the built-in
// free/paid slugs are always allowed (they are seeded), as is any slug present
// in member_tiers. This keeps SetTier working even before tiers are seeded.
func (s *Store) tierAssignable(ctx context.Context, tier string) bool {
	if tier == TierFree || tier == TierPaid {
		return true
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM member_tiers WHERE slug=?`, tier).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// UpdateProfile updates a member's display name and operator note by email.
func (s *Store) UpdateProfile(ctx context.Context, email, name, note string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE members SET name=?,note=? WHERE email=?`,
		strings.TrimSpace(name), strings.TrimSpace(note), strings.TrimSpace(strings.ToLower(email)))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no member with that email")
	}
	return nil
}

// SetNewsletterOptIn toggles whether a member receives the members newsletter.
func (s *Store) SetNewsletterOptIn(ctx context.Context, email string, on bool) error {
	v := 0
	if on {
		v = 1
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE members SET newsletter_opt_in=? WHERE email=?`,
		v, strings.TrimSpace(strings.ToLower(email)))
	return err
}

// SetReplyNotify toggles whether a member is emailed when someone replies to
// their comment.
func (s *Store) SetReplyNotify(ctx context.Context, email string, on bool) error {
	v := 0
	if on {
		v = 1
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE members SET reply_notify=? WHERE email=?`,
		v, strings.TrimSpace(strings.ToLower(email)))
	return err
}

// SetGeoIfEmpty records a member's coarse, GDPR-safe location (country/region/
// city — never an IP) the first time it becomes known, and never overwrites an
// existing value. Best-effort: a failure never blocks sign-in.
func (s *Store) SetGeoIfEmpty(ctx context.Context, id, country, region, city string) {
	if id == "" || (country == "" && region == "" && city == "") {
		return
	}
	_, _ = s.db.ExecContext(ctx,
		`UPDATE members SET country=?,region=?,city=? WHERE id=? AND country='' AND region='' AND city=''`,
		country, region, city, id)
}

// TouchLastSeen records the member's most recent activity. Best-effort.
func (s *Store) TouchLastSeen(ctx context.Context, id string) {
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	_, _ = s.db.ExecContext(ctx, `UPDATE members SET last_seen_at=? WHERE id=?`, now, id)
}

// UpgradeByEmail associates a Stripe customer id with a member (by email),
// creating the member if necessary, sets them to the paid tier, and records an
// active subscription reflecting the paid tier's monthly price so the upgrade
// contributes to MRR. Safe to call repeatedly for the same customer.
func (s *Store) UpgradeByEmail(ctx context.Context, email, stripeCustomer string) error {
	return s.UpgradeByEmailToTier(ctx, email, stripeCustomer, TierPaid, "")
}

// UpgradeByEmailToTier upgrades a member to a specific tier slug. tierSlug
// defaults to the paid tier when empty. stripeSub is the Stripe subscription id
// when known (used for reconciliation), and may be empty.
func (s *Store) UpgradeByEmailToTier(ctx context.Context, email, stripeCustomer, tierSlug, stripeSub string) error {
	if strings.TrimSpace(tierSlug) == "" {
		tierSlug = TierPaid
	}
	m, err := s.Upsert(ctx, email)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE members SET tier=?,stripe_customer=? WHERE id=?`, tierSlug, stripeCustomer, m.ID); err != nil {
		return err
	}
	// Reflect the upgrade as a paying subscription (best-effort; tier price is
	// looked up when available, otherwise the subscription records zero).
	amount, currency, cadence := 0, "USD", "monthly"
	if t, err := s.GetTier(ctx, tierSlug); err == nil {
		amount, currency = t.MonthlyCents, t.Currency
	}
	_ = s.StartSubscription(ctx, SubscriptionInput{
		MemberID: m.ID, TierSlug: tierSlug, Cadence: cadence,
		AmountCents: amount, Currency: currency, StripeSubscription: stripeSub,
	})
	return nil
}

// Count returns the number of members by tier.
func (s *Store) Count(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tier,COUNT(*) FROM members GROUP BY tier`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var tier string
		var n int
		if err := rows.Scan(&tier, &n); err != nil {
			return nil, err
		}
		out[tier] = n
	}
	return out, rows.Err()
}

// ---- magic-link login tokens ------------------------------------------------

// CreateLoginToken issues a one-time magic-link token for email and returns the
// raw token (to embed in the emailed link). Only its hash is stored.
func (s *Store) CreateLoginToken(ctx context.Context, email string) (string, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if _, err := mail.ParseAddress(email); err != nil {
		return "", fmt.Errorf("invalid email: %w", err)
	}
	token := randHex(32)
	exp := time.Now().UTC().Add(LoginTokenTTL).Format("2006-01-02 15:04:05")
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO member_login_tokens(token_hash,email,expires_at) VALUES(?,?,?)`,
		hashToken(token), email, exp); err != nil {
		return "", err
	}
	return token, nil
}

// ConsumeLoginToken validates and deletes a magic-link token, returning the
// associated email. Single-use: the row is removed on success.
func (s *Store) ConsumeLoginToken(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", fmt.Errorf("missing token")
	}
	h := hashToken(token)
	var email string
	var exp time.Time
	if err := s.db.QueryRowContext(ctx,
		`SELECT email,expires_at FROM member_login_tokens WHERE token_hash=?`, h).Scan(&email, &exp); err != nil {
		return "", fmt.Errorf("invalid or expired link")
	}
	// Enforce single-use atomically (audit L11): only the caller whose DELETE
	// actually removed the row may proceed. On the single-writer SQLite, two
	// concurrent verifies of the same captured link each SELECT the row, but only
	// one DELETE affects a row — the other sees RowsAffected==0 and is rejected, so
	// a captured magic link can never be replayed into a second session.
	res, err := s.db.ExecContext(ctx, `DELETE FROM member_login_tokens WHERE token_hash=?`, h)
	if err != nil {
		return "", fmt.Errorf("invalid or expired link")
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", fmt.Errorf("invalid or expired link")
	}
	if time.Now().UTC().After(exp.UTC()) {
		return "", fmt.Errorf("link expired")
	}
	return email, nil
}

// ---- member sessions --------------------------------------------------------

// CreateSession issues a member session and returns the raw token.
func (s *Store) CreateSession(ctx context.Context, memberID string) (string, error) {
	token := randHex(32)
	exp := time.Now().UTC().Add(SessionTTL).Format("2006-01-02 15:04:05")
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO member_sessions(token_hash,member_id,expires_at) VALUES(?,?,?)`,
		hashToken(token), memberID, exp); err != nil {
		return "", err
	}
	return token, nil
}

// ValidateSession resolves a session token to a live member.
func (s *Store) ValidateSession(ctx context.Context, token string) (*Member, error) {
	if token == "" {
		return nil, fmt.Errorf("no session")
	}
	var memberID string
	var exp time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT member_id,expires_at FROM member_sessions WHERE token_hash=?`, hashToken(token)).
		Scan(&memberID, &exp)
	if err != nil {
		return nil, fmt.Errorf("invalid session")
	}
	if time.Now().UTC().After(exp.UTC()) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM member_sessions WHERE token_hash=?`, hashToken(token))
		return nil, fmt.Errorf("session expired")
	}
	return s.GetByID(ctx, memberID)
}

// DestroySession removes a member session.
func (s *Store) DestroySession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM member_sessions WHERE token_hash=?`, hashToken(token))
	return err
}

// PurgeExpired clears expired login tokens and sessions.
func (s *Store) PurgeExpired(ctx context.Context) (int64, error) {
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	var total int64
	for _, tbl := range []string{"member_login_tokens", "member_sessions"} {
		res, err := s.db.ExecContext(ctx, "DELETE FROM "+tbl+" WHERE expires_at<?", now)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// ---- per-article access -----------------------------------------------------

// SetAccess sets the access level for a slug (public|members).
func (s *Store) SetAccess(ctx context.Context, slug, level string) error {
	if level != AccessPublic && level != AccessMembers && level != AccessPaid {
		return fmt.Errorf("invalid access level %q", level)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO article_access(slug,level) VALUES(?,?)
		 ON CONFLICT(slug) DO UPDATE SET level=excluded.level`, slug, level)
	return err
}

// GetAccess returns the access level for a slug, defaulting to public.
func (s *Store) GetAccess(ctx context.Context, slug string) string {
	var level string
	err := s.db.QueryRowContext(ctx, `SELECT level FROM article_access WHERE slug=?`, slug).Scan(&level)
	if err != nil || level == "" {
		return AccessPublic
	}
	return level
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
