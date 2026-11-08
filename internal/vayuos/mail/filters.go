// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	netmail "net/mail"
	"strconv"
	"strings"
	"time"
)

// A mailbox's rules, applied to each message as it arrives.
//
// A rule is WHEN all (or any) of its conditions hold, THEN its actions. Rules
// run in the order the holder sets; the first that matches decides, unless it
// says to keep going, in which case the rules below it are checked too and
// their actions add up (the last folder named wins). Matching is
// case-insensitive on the decoded header or text, as the reader sees it.
//
// Mail a rule files into Junk or Trash is treated like junk-filtered mail: it
// is never forwarded and never answered by the vacation reply. A rejected
// message is not stored at all, and the sending server is told so while it is
// still connected (inboundDeliver, smtpd), so nothing is bounced back later.
// A forward reaches only an address that has confirmed by a link
// (ConfirmRuleForward): a rule is the quietest way to copy a mailbox's mail
// out, so the address it copies to has to say yes first.

// RuleCondition is one test of a rule.
type RuleCondition struct {
	Field string `json:"field"` // from | to | subject | body | domain | attachment | size | list | header
	Op    string `json:"op"`    // contains | not-contains | is | ends-with; larger (size, in MB); any (attachment, list)
	Value string `json:"value,omitempty"`
	Name  string `json:"name,omitempty"` // the header's name, for field "header"
}

// RuleAction is one thing a rule does.
type RuleAction struct {
	Kind string `json:"kind"`          // move | label | markread | pin | forward | reject | delete
	Arg  string `json:"arg,omitempty"` // the folder, label or address
}

// FilterRule is one delivery rule for a mailbox.
type FilterRule struct {
	ID         int64           `json:"id"`
	Mailbox    string          `json:"mailbox"`
	Any        bool            `json:"any"` // any condition, rather than all
	Conditions []RuleCondition `json:"conditions"`
	Actions    []RuleAction    `json:"actions"`
	KeepGoing  bool            `json:"keep_going"`
	Enabled    bool            `json:"enabled"`
	Position   int             `json:"position"`
	CreatedAt  time.Time       `json:"created_at"`

	// The single-condition shape the first rules were made in: still accepted
	// by CreateFilter, and how a stored rule of that shape is read back.
	Field    string `json:"field,omitempty"`
	Contains string `json:"contains,omitempty"`
	Action   string `json:"action,omitempty"`
	Target   string `json:"target,omitempty"`
}

// ErrRejectedByRule is a message one of the recipient's rules refused.
var ErrRejectedByRule = errors.New("vayumail: refused by the recipient's rules")

var ruleTextFields = map[string]bool{"from": true, "to": true, "subject": true, "body": true, "domain": true, "header": true, "list": true}
var ruleTextOps = map[string]bool{"contains": true, "not-contains": true, "is": true, "ends-with": true}

const (
	// maxFiltersPerMailbox bounds the rule list so evaluation stays cheap.
	maxFiltersPerMailbox = 50
	maxRuleConditions    = 10
	maxRuleActions       = 6
	// ruleBodyBytes bounds how much of a body a "body contains" rule reads.
	ruleBodyBytes = 256 << 10
)

// ensureFilterTable creates the rules table on first use, and gives a table
// made before rules had several conditions its new columns, keeping its rows.
func (s *AccountStore) ensureFilterTable() error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_filters(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		mailbox TEXT NOT NULL,
		field TEXT NOT NULL,
		contains TEXT NOT NULL,
		action TEXT NOT NULL,
		target TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`); err != nil {
		return err
	}
	for col, def := range map[string]string{
		"rule":     `TEXT NOT NULL DEFAULT ''`,
		"position": `INTEGER NOT NULL DEFAULT 0`,
		"enabled":  `INTEGER NOT NULL DEFAULT 1`,
	} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(1) FROM pragma_table_info('vayumail_filters') WHERE name=?`, col).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := s.db.Exec(`ALTER TABLE vayumail_filters ADD COLUMN ` + col + ` ` + def); err != nil {
				return err
			}
		}
	}
	return nil
}

// normalise checks a rule and puts it in its stored shape.
func (r *FilterRule) normalise() error {
	if r.Field != "" && len(r.Conditions) == 0 {
		r.Conditions = []RuleCondition{{Field: r.Field, Op: "contains", Value: r.Contains}}
	}
	if r.Action != "" && len(r.Actions) == 0 {
		r.Actions = []RuleAction{{Kind: r.Action, Arg: r.Target}}
	}
	if len(r.Conditions) == 0 || len(r.Conditions) > maxRuleConditions {
		return fmt.Errorf("a rule has between 1 and %d conditions", maxRuleConditions)
	}
	if len(r.Actions) == 0 || len(r.Actions) > maxRuleActions {
		return fmt.Errorf("a rule has between 1 and %d actions", maxRuleActions)
	}
	for i := range r.Conditions {
		if err := r.Conditions[i].normalise(); err != nil {
			return err
		}
	}
	rejects := 0
	for i := range r.Actions {
		if err := r.Actions[i].normalise(r.Mailbox); err != nil {
			return err
		}
		if r.Actions[i].Kind == "reject" {
			rejects++
		}
	}
	if rejects > 0 && len(r.Actions) > 1 {
		return errors.New("a rule that rejects does nothing else: the message is not kept")
	}
	// The single-condition fields mirror a rule that has that shape, so the
	// places that show a rule in one line still can.
	r.Field, r.Contains, r.Action, r.Target = "", "", "", ""
	if len(r.Conditions) == 1 && r.Conditions[0].Op == "contains" && (r.Conditions[0].Field == "from" || r.Conditions[0].Field == "to" || r.Conditions[0].Field == "subject") {
		r.Field, r.Contains = r.Conditions[0].Field, r.Conditions[0].Value
	}
	if len(r.Actions) == 1 {
		r.Action, r.Target = r.Actions[0].Kind, r.Actions[0].Arg
	}
	return nil
}

func (c *RuleCondition) normalise() error {
	c.Field = strings.ToLower(strings.TrimSpace(c.Field))
	c.Op = strings.ToLower(strings.TrimSpace(c.Op))
	c.Value = strings.TrimSpace(c.Value)
	c.Name = strings.TrimSpace(c.Name)
	switch {
	case c.Field == "attachment":
		c.Op, c.Value = "any", ""
	case c.Field == "size":
		mb, err := strconv.ParseFloat(c.Value, 64)
		if err != nil || mb <= 0 || mb > 1024 {
			return errors.New("a size is a number of MB, more than 0")
		}
		c.Op = "larger"
	case c.Field == "list" && c.Value == "":
		c.Op = "any"
	case ruleTextFields[c.Field]:
		if !ruleTextOps[c.Op] {
			return errors.New("a condition is contains, does not contain, is, or ends with")
		}
		if c.Value == "" || len(c.Value) > 200 {
			return errors.New("a condition needs the words to look for, at most 200 characters")
		}
		if c.Field == "header" && (c.Name == "" || strings.ContainsAny(c.Name, ": \t\r\n") || len(c.Name) > 60) {
			return errors.New("a header condition needs the header's name")
		}
	default:
		return errors.New("a condition is on from, to, subject, body, the sender's domain, an attachment, size, a mailing list or a header")
	}
	return nil
}

func (a *RuleAction) normalise(mailbox string) error {
	a.Kind = strings.ToLower(strings.TrimSpace(a.Kind))
	a.Arg = strings.TrimSpace(a.Arg)
	switch a.Kind {
	case "move":
		// Snoozed is the snooze action's: a rule filing there would put mail
		// to sleep forever, since only Engine.Snooze records a wake row.
		if strings.EqualFold(a.Arg, "Snoozed") || strings.EqualFold(a.Arg, "Inbox") || (!isStandardFolder(a.Arg) && !ValidFolderName(a.Arg)) {
			return errors.New("invalid target folder")
		}
		if isStandardFolder(a.Arg) {
			a.Arg = canonicalFolder(a.Arg)
		}
	case "label":
		if !ValidLabel(a.Arg) {
			return errors.New("a label is letters, digits, spaces, '-' and '_', at most 30")
		}
	case "forward":
		addr, err := netmail.ParseAddress(a.Arg)
		if err != nil || !strings.Contains(addr.Address, "@") {
			return errors.New("forward needs one address")
		}
		a.Arg = normEmail(addr.Address)
		if a.Arg == normEmail(mailbox) {
			return errors.New("a mailbox cannot forward to itself")
		}
	case "markread", "pin", "reject", "delete":
		a.Arg = ""
	default:
		return errors.New("invalid action")
	}
	return nil
}

// SaveRule stores a rule for its mailbox: a new one goes last, one with an ID
// replaces that rule of the same mailbox.
func (s *AccountStore) SaveRule(ctx context.Context, r FilterRule) (int64, error) {
	if err := s.ensureFilterTable(); err != nil {
		return 0, err
	}
	r.Mailbox = normEmail(r.Mailbox)
	if r.Mailbox == "" {
		return 0, errors.New("mailbox required")
	}
	if err := r.normalise(); err != nil {
		return 0, err
	}
	body, err := json.Marshal(struct {
		Any        bool            `json:"any"`
		Conditions []RuleCondition `json:"conditions"`
		Actions    []RuleAction    `json:"actions"`
		KeepGoing  bool            `json:"keep_going"`
	}{r.Any, r.Conditions, r.Actions, r.KeepGoing})
	if err != nil {
		return 0, err
	}
	enabled := 0
	if r.Enabled {
		enabled = 1
	}
	if r.ID > 0 {
		res, err := s.db.ExecContext(ctx,
			`UPDATE vayumail_filters SET field=?, contains=?, action=?, target=?, rule=?, enabled=? WHERE id=? AND mailbox=?`,
			r.Field, r.Contains, r.Action, r.Target, string(body), enabled, r.ID, r.Mailbox)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return 0, errors.New("no such rule")
		}
		return r.ID, nil
	}
	var n, last int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(MAX(position),0) FROM vayumail_filters WHERE mailbox=?`, r.Mailbox).Scan(&n, &last)
	if n >= maxFiltersPerMailbox {
		return 0, errors.New("too many rules for this mailbox")
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO vayumail_filters(mailbox,field,contains,action,target,rule,position,enabled,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		r.Mailbox, r.Field, r.Contains, r.Action, r.Target, string(body), last+1, enabled, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CreateFilter stores a rule of the single-condition shape, switched on.
func (s *AccountStore) CreateFilter(ctx context.Context, r FilterRule) error {
	r.Enabled = true
	_, err := s.SaveRule(ctx, r)
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

// SetRuleEnabled switches one rule on or off.
func (s *AccountStore) SetRuleEnabled(ctx context.Context, mailbox string, id int64, on bool) error {
	if err := s.ensureFilterTable(); err != nil {
		return err
	}
	v := 0
	if on {
		v = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE vayumail_filters SET enabled=? WHERE id=? AND mailbox=?`, v, id, normEmail(mailbox))
	return err
}

// MoveRule moves one rule a place up or down in its mailbox's order.
func (s *AccountStore) MoveRule(ctx context.Context, mailbox string, id int64, up bool) error {
	rules, err := s.FiltersFor(ctx, mailbox)
	if err != nil {
		return err
	}
	for i := range rules {
		if rules[i].ID != id {
			continue
		}
		j := i + 1
		if up {
			j = i - 1
		}
		if j < 0 || j >= len(rules) {
			return nil
		}
		rules[i], rules[j] = rules[j], rules[i]
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for k, r := range rules {
			if _, err := tx.ExecContext(ctx, `UPDATE vayumail_filters SET position=? WHERE id=? AND mailbox=?`, k+1, r.ID, normEmail(mailbox)); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		return tx.Commit()
	}
	return errors.New("no such rule")
}

// FiltersFor returns a mailbox's rules in the order they run.
func (s *AccountStore) FiltersFor(ctx context.Context, mailbox string) ([]FilterRule, error) {
	out := []FilterRule{}
	if err := s.ensureFilterTable(); err != nil {
		return out, nil // no storage → no rules; delivery proceeds normally
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,mailbox,field,contains,action,target,rule,position,enabled,created_at FROM vayumail_filters WHERE mailbox=? ORDER BY position, id`, normEmail(mailbox))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r FilterRule
		var body string
		var enabled int
		if err := rows.Scan(&r.ID, &r.Mailbox, &r.Field, &r.Contains, &r.Action, &r.Target, &body, &r.Position, &enabled, &r.CreatedAt); err != nil {
			return out, err
		}
		r.Enabled = enabled != 0
		if body != "" {
			if err := json.Unmarshal([]byte(body), &r); err != nil {
				continue // a row that cannot be read is skipped, not guessed at
			}
		} else {
			// A rule from before several conditions: one condition, one action.
			r.Conditions = []RuleCondition{{Field: r.Field, Op: "contains", Value: r.Contains}}
			r.Actions = []RuleAction{{Kind: r.Action, Arg: r.Target}}
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

// ruleMessage is a message as rules read it, its body parsed only if a rule
// asks for it.
type ruleMessage struct {
	raw    []byte
	parsed *ParsedMessage
}

func (m *ruleMessage) message() *ParsedMessage {
	if m.parsed == nil {
		raw := m.raw
		if len(raw) > ruleBodyBytes {
			raw = raw[:ruleBodyBytes]
		}
		pm := ParseMessage(raw)
		m.parsed = &pm
	}
	return m.parsed
}

// matchCondition reports whether one condition holds for the message.
func matchCondition(c RuleCondition, m *ruleMessage) bool {
	var hay string
	switch c.Field {
	// Matched as a reader sees them: a name or subject in another script
	// arrives as RFC 2047 encoded-words, which a rule's words never match.
	case "from":
		hay = decodeMIMEWord(headerValue(m.raw, "From")) + " " + decodeMIMEWord(headerValue(m.raw, "Reply-To"))
	case "to":
		hay = decodeMIMEWord(headerValue(m.raw, "To")) + " " + decodeMIMEWord(headerValue(m.raw, "Cc"))
	case "subject":
		hay = decodeMIMEWord(headerValue(m.raw, "Subject"))
	case "body":
		pm := m.message()
		hay = pm.Text + " " + pm.HTML
	case "domain":
		if a, err := netmail.ParseAddress(decodeMIMEWord(headerValue(m.raw, "From"))); err == nil {
			_, hay = splitAddress(a.Address)
		}
	case "header":
		hay = decodeMIMEWord(headerValue(m.raw, c.Name))
	case "list":
		hay = headerValue(m.raw, "List-Id")
		if c.Op == "any" {
			return strings.TrimSpace(hay) != ""
		}
	case "attachment":
		return len(m.message().Attachments) > 0
	case "size":
		mb, _ := strconv.ParseFloat(c.Value, 64)
		return float64(len(m.raw)) > mb*(1<<20)
	default:
		return false
	}
	hay, needle := strings.ToLower(strings.TrimSpace(hay)), strings.ToLower(c.Value)
	switch c.Op {
	case "contains":
		return strings.Contains(hay, needle)
	case "not-contains":
		return !strings.Contains(hay, needle)
	case "is":
		return hay == needle
	case "ends-with":
		return strings.HasSuffix(hay, needle)
	}
	return false
}

// ruleMatches reports whether a rule's conditions hold, all or any of them.
func ruleMatches(r FilterRule, m *ruleMessage) bool {
	if len(r.Conditions) == 0 {
		return false
	}
	for _, c := range r.Conditions {
		hit := matchCondition(c, m)
		if r.Any && hit {
			return true
		}
		if !r.Any && !hit {
			return false
		}
	}
	return !r.Any
}

// ruleOutcome is what a mailbox's rules decide for one arriving message.
type ruleOutcome struct {
	folder        string // "" for the Inbox
	markRead, pin bool
	reject        bool
	labels        []string
	forwards      []string
	matched       bool
}

// applyFilters runs a mailbox's rules over a message. Best-effort: a storage
// problem yields no rules, never a delivery failure.
func (e *Engine) applyFilters(mailbox string, raw []byte) ruleOutcome {
	var out ruleOutcome
	if e.accounts == nil {
		return out
	}
	rules, err := e.accounts.FiltersFor(context.Background(), mailbox)
	if err != nil {
		return out
	}
	m := &ruleMessage{raw: raw}
	for _, r := range rules {
		if !r.Enabled || !ruleMatches(r, m) {
			continue
		}
		out.matched = true
		for _, a := range r.Actions {
			switch a.Kind {
			case "move":
				out.folder = a.Arg
			case "delete":
				out.folder = "Trash"
			case "markread":
				out.markRead = true
			case "pin":
				out.pin = true
			case "label":
				out.labels = append(out.labels, a.Arg)
			case "forward":
				if e.accounts.RuleForwardConfirmed(context.Background(), mailbox, a.Arg) {
					out.forwards = append(out.forwards, a.Arg)
				}
			case "reject":
				out.reject = true
				return out
			}
		}
		if !r.KeepGoing {
			break
		}
	}
	return out
}

// RuleMatches is the mail of rd's Inbox, newest first, that a rule would
// have matched: up to limit messages are looked at, and how many were is
// returned, so the holder sees what a rule catches before it runs.
func (e *Engine) RuleMatches(rd Reader, r FilterRule, limit int) (matched []StoredMessage, looked int, err error) {
	if err := r.normalise(); err != nil {
		return nil, 0, err
	}
	list, err := e.ListFolder(rd, "Inbox")
	if err != nil {
		return nil, 0, err
	}
	for _, sm := range list {
		if looked >= limit {
			break
		}
		looked++
		raw, rerr := e.ReadFolderMessage(rd, "Inbox", sm.ID)
		if rerr != nil {
			continue
		}
		if ruleMatches(r, &ruleMessage{raw: raw}) {
			matched = append(matched, sm)
		}
	}
	return matched, looked, nil
}

// RunRule does to the mail already in rd's Inbox what a rule would have done
// on arrival, up to limit messages: everything but forward and reject, which
// are about mail as it comes in. It reports how many messages it changed.
func (e *Engine) RunRule(rd Reader, r FilterRule, limit int) (int, error) {
	matched, _, err := e.RuleMatches(rd, r, limit)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, sm := range matched {
		id, did := sm.ID, false
		for _, a := range r.Actions {
			switch a.Kind {
			case "markread":
				if nid, err := e.MarkRead(rd, "Inbox", id); err == nil {
					id, did = nonEmpty(nid, id), true
				}
			case "pin":
				if nid, err := e.SetPinned(rd, "Inbox", id, true); err == nil {
					id, did = nonEmpty(nid, id), true
				}
			case "label":
				if sm.MessageID != "" && e.SetLabel(rd, sm.MessageID, a.Arg, true) == nil {
					did = true
				}
			}
		}
		// A move goes last: it takes the message out of the Inbox, where the
		// other actions find it.
		for _, a := range r.Actions {
			to := a.Arg
			if a.Kind == "delete" {
				to = "Trash"
			} else if a.Kind != "move" {
				continue
			}
			if e.MoveMessage(rd, id, "Inbox", to) == nil {
				did = true
			}
			break
		}
		if did {
			changed++
		}
	}
	return changed, nil
}

func nonEmpty(s, otherwise string) string {
	if s != "" {
		return s
	}
	return otherwise
}

// ErrNoRuleForward is a forward confirmation that matches nothing waiting.
var ErrNoRuleForward = errors.New("vayumail: that confirmation link is not valid, or was already used")

func (s *AccountStore) ensureRuleForwardTable() error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_rule_forwards(
		mailbox TEXT NOT NULL,
		address TEXT NOT NULL,
		token_hash TEXT NOT NULL DEFAULT '',
		confirmed INTEGER NOT NULL DEFAULT 0,
		created DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY(mailbox, address));`)
	return err
}

// RuleForwardConfirmed reports whether address has confirmed it takes
// mailbox's forwarded mail.
func (s *AccountStore) RuleForwardConfirmed(ctx context.Context, mailbox, address string) bool {
	if s.ensureRuleForwardTable() != nil {
		return false
	}
	var ok int
	_ = s.db.QueryRowContext(ctx, `SELECT confirmed FROM vayumail_rule_forwards WHERE mailbox=? AND address=?`,
		normEmail(mailbox), normEmail(address)).Scan(&ok)
	return ok == 1
}

// RequestRuleForward records that mailbox wants to forward to address and
// returns the token for its confirmation link, stored hashed; "" when the
// address has confirmed already.
func (s *AccountStore) RequestRuleForward(ctx context.Context, mailbox, address string) (string, error) {
	if err := s.ensureRuleForwardTable(); err != nil {
		return "", err
	}
	if s.RuleForwardConfirmed(ctx, mailbox, address) {
		return "", nil
	}
	tok, err := randomHex(32)
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO vayumail_rule_forwards(mailbox,address,token_hash,confirmed,created) VALUES(?,?,?,0,?)
		 ON CONFLICT(mailbox,address) DO UPDATE SET token_hash=excluded.token_hash, created=excluded.created`,
		normEmail(mailbox), normEmail(address), hashRecoveryToken(tok), time.Now().UTC())
	return tok, err
}

// ConfirmRuleForward marks the address whose link carries token as
// confirmed, once, and says which mailbox and address it was.
func (s *AccountStore) ConfirmRuleForward(ctx context.Context, token string) (mailbox, address string, err error) {
	if err := s.ensureRuleForwardTable(); err != nil {
		return "", "", err
	}
	err = s.db.QueryRowContext(ctx,
		`SELECT mailbox,address FROM vayumail_rule_forwards WHERE token_hash=? AND token_hash<>''`, hashRecoveryToken(token)).Scan(&mailbox, &address)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNoRuleForward
	}
	if err != nil {
		return "", "", err
	}
	// The token is cleared as it is used: that is what makes a link work once.
	_, err = s.db.ExecContext(ctx, `UPDATE vayumail_rule_forwards SET confirmed=1, token_hash='' WHERE mailbox=? AND address=?`, mailbox, address)
	return mailbox, address, err
}
