// SPDX-License-Identifier: Apache-2.0

package mail

// handover.go — witnessed access after a mailbox is handed to its owner
// (ADR-0152 Phase 5).
//
// WHAT THIS DELIVERS. Once a mailbox is handed over, the operator can no longer
// open it THROUGH THE PRODUCT: not from the panel, not with their own console
// password over IMAP, not by resetting its password or clearing its second
// factor, and not by minting a credential for it. What remains is a
// command-line break-glass that cannot run without writing a permanent,
// client-visible record.
//
// WHAT IT DOES NOT DELIVER, and must never be described as delivering. The
// messages are ordinary files on a server the operator runs. Anyone with direct
// access to that machine, its database or a backup can still read them, and
// nothing here records that. Only encryption under a key the server does not
// hold would prevent it, and ADR-0152 D4 records why that is deliberately not
// built — six critical findings against it, none of them about the cryptography.
//
// The claim the product may make is stated verbatim in ADR-0152 D4. It says
// "your mail is not encrypted" in those words. Nothing in this file licenses a
// stronger sentence, and a panel row that implies one is a defect of the same
// class as a wrong number.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

// ErrHandedOver is returned when an operator read is refused because the
// mailbox has been handed to its owner.
var ErrHandedOver = errors.New("vayumail: this mailbox has been handed over; operator access is refused")

// handoverTTL bounds how long a handover verdict is cached.
//
// The state changes at most a handful of times in a mailbox's life, and it is
// consulted on every read, so a cache is warranted. It is deliberately SHORT:
// the failure mode of a stale cache here is serving an operator a mailbox that
// was handed over seconds ago, and the whole point of the feature is that the
// answer is trustworthy.
const handoverTTL = 30 * time.Second

type handoverCache struct {
	mu   sync.RWMutex
	seen map[string]handoverEntry
}

type handoverEntry struct {
	handed bool
	until  time.Time
}

// normaliseMailbox reduces a mailbox key to the form the handover table stores.
//
// Reads arrive keyed either as a bare local part (primary domain) or as a full
// address (secondary domain), and both must resolve to the same verdict — a
// handover that can be bypassed by asking for the same mailbox a different way
// is not a handover.
func (s *AccountStore) normaliseMailbox(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return ""
	}
	if strings.Contains(key, "@") {
		return key
	}
	dom := strings.ToLower(strings.TrimSpace(s.defaultDomain))
	if dom == "" {
		return key
	}
	return key + "@" + dom
}

// IsHandedOver reports whether a mailbox has been handed to its owner.
func (s *AccountStore) IsHandedOver(key string) bool {
	mbox := s.normaliseMailbox(key)
	if mbox == "" || s.db == nil {
		return false
	}
	s.handoverOnce.Do(func() { s.handover = &handoverCache{seen: map[string]handoverEntry{}} })
	now := time.Now()
	s.handover.mu.RLock()
	ent, ok := s.handover.seen[mbox]
	s.handover.mu.RUnlock()
	if ok && now.Before(ent.until) {
		return ent.handed
	}
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(1) FROM mail_handover WHERE mailbox=? AND handed_at IS NOT NULL`, mbox).Scan(&n)
	if err != nil {
		// FAIL CLOSED. A database that cannot answer "has this been handed over?"
		// must not be read as "no". The operator loses panel access to a mailbox
		// until the query works again; the alternative is reading a client's mail
		// because a query failed, which is the outcome the feature exists to
		// prevent and the one nobody would notice.
		return true
	}
	handed := n > 0
	s.handover.mu.Lock()
	s.handover.seen[mbox] = handoverEntry{handed: handed, until: now.Add(handoverTTL)}
	s.handover.mu.Unlock()
	return handed
}

// HandOver marks a mailbox as belonging to its owner. It is ONE-WAY: the
// database refuses to clear handed_at (migration 081 trigger), so this cannot be
// undone by the party who runs the database.
func (s *AccountStore) HandOver(ctx context.Context, key, by, recoveryContact string) error {
	mbox := s.normaliseMailbox(key)
	if mbox == "" {
		return errors.New("vayumail: handover needs a mailbox")
	}
	if s.db == nil {
		return errors.New("vayumail: not started")
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO mail_handover(mailbox,handed_at,handed_by,recovery_contact) VALUES(?,CURRENT_TIMESTAMP,?,?)
		 ON CONFLICT(mailbox) DO UPDATE SET handed_at=COALESCE(mail_handover.handed_at,CURRENT_TIMESTAMP),handed_by=excluded.handed_by,recovery_contact=excluded.recovery_contact,updated_at=CURRENT_TIMESTAMP`,
		mbox, by, recoveryContact); err != nil {
		return err
	}
	s.invalidateHandover(mbox)
	return s.AppendLedger(ctx, mbox, by, "handover", "operator panel access to this mailbox ended")
}

// invalidateHandover drops a cached verdict so a handover takes effect at once
// rather than up to handoverTTL later.
func (s *AccountStore) invalidateHandover(mbox string) {
	if s.handover == nil {
		return
	}
	s.handover.mu.Lock()
	delete(s.handover.seen, mbox)
	s.handover.mu.Unlock()
}

// AppendLedger records one access event against a mailbox.
//
// The ledger is append-only in the DATABASE (migration 081 triggers), not merely
// by convention here, and each entry chains the previous entry's hash. Chaining
// does not make tampering impossible — the operator owns the database — it makes
// it DETECTABLE, which is the honest property and the one the client is told
// about. A record that could be quietly rewritten would be worse than none,
// because it would be believed.
func (s *AccountStore) AppendLedger(ctx context.Context, mailbox, actor, action, detail string) error {
	if s.db == nil {
		return nil
	}
	mbox := s.normaliseMailbox(mailbox)
	var prev string
	_ = s.db.QueryRowContext(ctx,
		`SELECT entry_hash FROM mail_access_ledger ORDER BY seq DESC LIMIT 1`).Scan(&prev)
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	sum := sha256.Sum256([]byte(prev + "\x00" + ts + "\x00" + mbox + "\x00" + actor + "\x00" + action + "\x00" + detail))
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO mail_access_ledger(ts,mailbox,actor,action,detail,prev_hash,entry_hash) VALUES(?,?,?,?,?,?,?)`,
		ts, mbox, actor, action, detail, prev, hex.EncodeToString(sum[:]))
	return err
}

// LedgerEntry is one recorded access, as shown to the mailbox's owner.
type LedgerEntry struct {
	Seq    int64     `json:"seq"`
	TS     time.Time `json:"ts"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Detail string    `json:"detail"`
}

// Ledger returns the recorded access events for one mailbox, newest first.
func (s *AccountStore) Ledger(ctx context.Context, key string, limit int) ([]LedgerEntry, error) {
	if s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq,ts,actor,action,detail FROM mail_access_ledger WHERE mailbox=? ORDER BY seq DESC LIMIT ?`,
		s.normaliseMailbox(key), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LedgerEntry
	for rows.Next() {
		var en LedgerEntry
		var ts string
		if err := rows.Scan(&en.Seq, &ts, &en.Actor, &en.Action, &en.Detail); err != nil {
			return nil, err
		}
		en.TS, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, en)
	}
	_ = rows.Err()
	return out, nil
}

// VerifyLedger walks the chain and reports the first sequence number whose hash
// does not follow from its predecessor, or 0 when the chain is intact.
//
// This is what makes "tampering shows up" a checkable statement rather than a
// marketing one. It is also why the client is asked to keep the notices they are
// sent: the chain proves internal consistency, and an operator who rewrote the
// whole chain would produce a consistent one.
func (s *AccountStore) VerifyLedger(ctx context.Context) (badSeq int64, err error) {
	if s.db == nil {
		return 0, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq,ts,mailbox,actor,action,detail,prev_hash,entry_hash FROM mail_access_ledger ORDER BY seq`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	prev := ""
	for rows.Next() {
		var seq int64
		var ts, mbox, actor, action, detail, ph, eh string
		if err := rows.Scan(&seq, &ts, &mbox, &actor, &action, &detail, &ph, &eh); err != nil {
			return 0, err
		}
		if ph != prev {
			return seq, nil
		}
		sum := sha256.Sum256([]byte(ph + "\x00" + ts + "\x00" + mbox + "\x00" + actor + "\x00" + action + "\x00" + detail))
		if hex.EncodeToString(sum[:]) != eh {
			return seq, nil
		}
		prev = eh
	}
	return 0, rows.Err()
}

// Engine delegates the handover surface to its account store, so callers that
// hold either type ask the same question of the same table.
func (e *Engine) IsHandedOver(key string) bool {
	if e == nil || e.accounts == nil {
		return false
	}
	return e.accounts.IsHandedOver(key)
}

// HandOver marks a mailbox as belonging to its owner.
func (e *Engine) HandOver(ctx context.Context, key, by, recoveryContact string) error {
	if e == nil || e.accounts == nil {
		return errors.New("vayumail: not started")
	}
	return e.accounts.HandOver(ctx, key, by, recoveryContact)
}

// AppendLedger records one access event.
func (e *Engine) AppendLedger(ctx context.Context, mailbox, actor, action, detail string) error {
	if e == nil || e.accounts == nil {
		return nil
	}
	return e.accounts.AppendLedger(ctx, mailbox, actor, action, detail)
}

// Ledger returns a mailbox's recorded access events, newest first.
func (e *Engine) Ledger(ctx context.Context, key string, limit int) ([]LedgerEntry, error) {
	if e == nil || e.accounts == nil {
		return nil, nil
	}
	return e.accounts.Ledger(ctx, key, limit)
}
