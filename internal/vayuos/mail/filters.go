// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Server-side filter rules (Sieve-style), applied at delivery.
//
// A rule is per-mailbox: WHEN <field> contains <value> THEN <action>. Rules
// are evaluated in creation order and the FIRST match wins (the consumer-mail
// model — one predictable action per message, no compounding surprises).
// Matching is case-insensitive substring on the decoded header. Actions:
//
//	move:<Folder>  file into Folder instead of Inbox (Junk/Trash included)
//	markread       file into Inbox already marked read
//	pin            file into Inbox pinned (flagged)
//
// Mail filed into Junk or Trash by a rule is treated like junk-filtered mail:
// it is never auto-forwarded and never triggers the vacation autoresponder.

// FilterRule is one delivery rule for a mailbox.
type FilterRule struct {
	ID        int64     `json:"id"`
	Mailbox   string    `json:"mailbox"`
	Field     string    `json:"field"` // from | to | subject
	Contains  string    `json:"contains"`
	Action    string    `json:"action"` // move | markread | pin
	Target    string    `json:"target"` // folder name for move
	CreatedAt time.Time `json:"created_at"`
}

// filterFields and filterActions are the accepted rule vocabularies.
var filterFields = map[string]bool{"from": true, "to": true, "subject": true}
var filterActions = map[string]bool{"move": true, "markread": true, "pin": true}

// maxFiltersPerMailbox bounds the rule list so evaluation stays O(small).
const maxFiltersPerMailbox = 50

// ensureFilterTable creates the rules table on first use (idempotent).
func (s *AccountStore) ensureFilterTable() error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_filters(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		mailbox TEXT NOT NULL,
		field TEXT NOT NULL,
		contains TEXT NOT NULL,
		action TEXT NOT NULL,
		target TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`)
	return err
}

// CreateFilter validates and stores a rule for a mailbox.
func (s *AccountStore) CreateFilter(ctx context.Context, r FilterRule) error {
	if err := s.ensureFilterTable(); err != nil {
		return err
	}
	r.Mailbox = normEmail(r.Mailbox)
	r.Field = strings.ToLower(strings.TrimSpace(r.Field))
	r.Action = strings.ToLower(strings.TrimSpace(r.Action))
	r.Contains = strings.TrimSpace(r.Contains)
	r.Target = strings.TrimSpace(r.Target)
	if r.Mailbox == "" || r.Contains == "" {
		return errors.New("mailbox and match text are required")
	}
	if !filterFields[r.Field] {
		return errors.New("invalid field")
	}
	if !filterActions[r.Action] {
		return errors.New("invalid action")
	}
	if r.Action == "move" {
		// Snoozed is snooze-action-only: a rule filing there would put mail to
		// sleep forever, since only Engine.Snooze records a wake row.
		if !isStandardFolder(r.Target) || strings.EqualFold(r.Target, "Snoozed") {
			return errors.New("invalid target folder")
		}
	} else {
		r.Target = ""
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_filters WHERE mailbox=?`, r.Mailbox).Scan(&n)
	if n >= maxFiltersPerMailbox {
		return errors.New("too many rules for this mailbox")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO vayumail_filters(mailbox,field,contains,action,target,created_at) VALUES(?,?,?,?,?,?)`,
		r.Mailbox, r.Field, r.Contains, r.Action, r.Target, time.Now().UTC())
	return err
}

// DeleteFilter removes one rule (scoped by mailbox so an id can't cross boxes).
func (s *AccountStore) DeleteFilter(ctx context.Context, mailbox string, id int64) error {
	if err := s.ensureFilterTable(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM vayumail_filters WHERE id=? AND mailbox=?`, id, normEmail(mailbox))
	return err
}

// FiltersFor returns a mailbox's rules in evaluation (creation) order.
func (s *AccountStore) FiltersFor(ctx context.Context, mailbox string) ([]FilterRule, error) {
	out := []FilterRule{}
	if err := s.ensureFilterTable(); err != nil {
		return out, nil // no storage → no rules; delivery proceeds normally
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,mailbox,field,contains,action,target,created_at FROM vayumail_filters WHERE mailbox=? ORDER BY id`, normEmail(mailbox))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r FilterRule
		if err := rows.Scan(&r.ID, &r.Mailbox, &r.Field, &r.Contains, &r.Action, &r.Target, &r.CreatedAt); err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// isStandardFolder reports whether name is one of the fixed mail folders.
func isStandardFolder(name string) bool {
	for _, f := range StandardFolders {
		if strings.EqualFold(f, name) {
			return true
		}
	}
	return false
}

// matchFilter reports whether one rule matches the message headers.
func matchFilter(r FilterRule, raw []byte) bool {
	needle := strings.ToLower(r.Contains)
	if needle == "" {
		return false
	}
	var hay string
	switch r.Field {
	case "from":
		hay = headerValue(raw, "From") + " " + headerValue(raw, "Reply-To")
	case "to":
		hay = headerValue(raw, "To") + " " + headerValue(raw, "Cc")
	case "subject":
		hay = headerValue(raw, "Subject")
	default:
		return false
	}
	return strings.Contains(strings.ToLower(hay), needle)
}

// applyFilters evaluates a mailbox's rules against a message and returns the
// matched rule (first match wins) or nil when no rule matches. Best-effort: a
// storage problem yields no rules, never a delivery failure.
func (e *Engine) applyFilters(mailbox string, raw []byte) *FilterRule {
	if e.accounts == nil {
		return nil
	}
	rules, err := e.accounts.FiltersFor(context.Background(), mailbox)
	if err != nil {
		return nil
	}
	for i := range rules {
		if matchFilter(rules[i], raw) {
			return &rules[i]
		}
	}
	return nil
}
