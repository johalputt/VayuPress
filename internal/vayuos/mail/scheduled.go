// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
)

// Send later: a message held until a chosen time, then sent through
// ComposeRich exactly as Send would have sent it.
//
// What is held is the message as the composer resolved it (sender, recipients,
// signature already in the body, the HTML rendering, attachments, the
// encryption choice), not the assembled MIME: assembling at the due time is
// what puts the real sending time in the Date header, files the Sent copy when
// it actually goes, and encrypts to the keys on file then.
//
// Due times are compared with the wall clock on every pass, never armed as a
// timer for the remaining duration. A timer measures elapsed (monotonic) time,
// so after the clock is corrected it would fire at the wrong wall time; and a
// timer does not survive a restart, where a row does.
//
// A row moves waiting → sending → (deleted once sent | failed). Claiming it
// with a conditional UPDATE is what keeps a Send now and the sweep, or two
// passes, from sending it twice.

// scheduledSweepInterval is how often due messages are looked for.
const scheduledSweepInterval = 30 * time.Second

// Scheduled message states.
const (
	ScheduledWaiting = "waiting"
	ScheduledSending = "sending"
	ScheduledFailed  = "failed"
	// scheduledCancelling holds a row while Cancel files it back as a draft.
	scheduledCancelling = "cancelling"
)

// interruptedNote is what a row left mid-send or mid-cancel by a stopped
// server says. It is not sent again by itself: it may already have gone.
const interruptedNote = "The server stopped while this was being handled, so it may already have gone. Look in Sent and Drafts before sending it again."

// ErrScheduledBusy is a Cancel or Send now on a message already being sent.
var ErrScheduledBusy = errors.New("this message is being sent now")

// heldMessage is what a row stores: the message as it will be sent, and the
// body as the sender typed it. Cancel files the typed body back as a draft,
// because the composer appends the signature again when the draft is sent.
type heldMessage struct {
	Compose ComposeMessage
	Typed   string
}

// ScheduledMessage is one held message, as the mailbox lists it.
type ScheduledMessage struct {
	ID        int64
	Owner     string
	Due       time.Time
	State     string
	LastError string
	Subject   string
	To        []string
}

// ensureScheduledTable creates the table on first use (idempotent). Owner is
// the sender's bare address; account removal deletes its rows (accounts.go,
// perAddressTables), so nothing goes out from a mailbox that is gone.
func (e *Engine) ensureScheduledTable() error {
	if e.db == nil {
		return errors.New("vayumail: no storage")
	}
	_, err := e.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_scheduled(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		owner TEXT NOT NULL,
		due_unix INTEGER NOT NULL,
		state TEXT NOT NULL DEFAULT 'waiting',
		last_error TEXT NOT NULL DEFAULT '',
		subject TEXT NOT NULL DEFAULT '',
		to_json TEXT NOT NULL DEFAULT '[]',
		message BLOB NOT NULL);
	CREATE INDEX IF NOT EXISTS idx_vms_state_due ON vayumail_scheduled(state, due_unix);
	CREATE INDEX IF NOT EXISTS idx_vms_owner ON vayumail_scheduled(owner);`)
	return err
}

// Schedule holds m to be sent at due on behalf of owner (the bare sending
// address) and returns its id. typed is the body before the signature was
// added, what a cancelled message goes back to Drafts with.
func (e *Engine) Schedule(ctx context.Context, owner string, m ComposeMessage, typed string, due time.Time) (int64, error) {
	if err := e.ensureScheduledTable(); err != nil {
		return 0, err
	}
	if len(m.To)+len(m.CC)+len(m.BCC) == 0 {
		return 0, errors.New("vayumail: no recipients")
	}
	body, err := json.Marshal(heldMessage{Compose: m, Typed: typed})
	if err != nil {
		return 0, err
	}
	// Every recipient the sender can see in the list, Bcc included: it is the
	// sender's own list of their own message.
	shown := append(append(append([]string{}, m.To...), m.CC...), m.BCC...)
	toJSON, _ := json.Marshal(shown)
	res, err := e.db.ExecContext(ctx,
		`INSERT INTO vayumail_scheduled(owner,due_unix,state,subject,to_json,message) VALUES(?,?,?,?,?,?)`,
		normEmail(owner), due.Unix(), ScheduledWaiting, m.Subject, string(toJSON), body)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ScheduledFor lists owner's held messages, soonest first.
func (e *Engine) ScheduledFor(ctx context.Context, owner string) ([]ScheduledMessage, error) {
	if err := e.ensureScheduledTable(); err != nil {
		return nil, err
	}
	rows, err := e.db.QueryContext(ctx,
		`SELECT id,owner,due_unix,state,last_error,subject,to_json FROM vayumail_scheduled WHERE owner=? ORDER BY due_unix, id`,
		normEmail(owner))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScheduledMessage{}
	for rows.Next() {
		var s ScheduledMessage
		var due int64
		var toJSON string
		if err := rows.Scan(&s.ID, &s.Owner, &due, &s.State, &s.LastError, &s.Subject, &toJSON); err != nil {
			return nil, err
		}
		s.Due = time.Unix(due, 0).UTC()
		_ = json.Unmarshal([]byte(toJSON), &s.To)
		out = append(out, s)
	}
	return out, rows.Err()
}

// claimScheduled moves owner's row id from one of the states in from to to,
// and reports whether it was in one of them. The one conditional UPDATE is the
// lock: of two callers, one moves it and the other finds it moved.
func (e *Engine) claimScheduled(ctx context.Context, owner string, id int64, to string, from ...string) (bool, error) {
	q := `UPDATE vayumail_scheduled SET state=? WHERE id=? AND state IN (?` + strings.Repeat(",?", len(from)-1) + `)`
	args := []any{to, id}
	for _, f := range from {
		args = append(args, f)
	}
	if owner != "" {
		q += ` AND owner=?`
		args = append(args, normEmail(owner))
	}
	res, err := e.db.ExecContext(ctx, q, args...)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// scheduledMessage reads the held message of row id.
func (e *Engine) scheduledMessage(ctx context.Context, id int64) (heldMessage, error) {
	var h heldMessage
	var body []byte
	if err := e.db.QueryRowContext(ctx, `SELECT message FROM vayumail_scheduled WHERE id=?`, id).Scan(&body); err != nil {
		return h, err
	}
	return h, json.Unmarshal(body, &h)
}

// CancelScheduled takes owner's held message id out of the schedule and files
// it in Drafts, so nothing written is lost; it returns the draft's id. A
// message already being sent cannot be cancelled (ErrScheduledBusy).
func (e *Engine) CancelScheduled(ctx context.Context, owner string, id int64) (string, error) {
	if err := e.ensureScheduledTable(); err != nil {
		return "", err
	}
	ok, err := e.claimScheduled(ctx, owner, id, scheduledCancelling, ScheduledWaiting, ScheduledFailed)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", e.whyNotClaimed(ctx, owner, id)
	}
	h, err := e.scheduledMessage(ctx, id)
	if err == nil {
		m := h.Compose
		var draft string
		if draft, err = e.SaveDraftWithAttachments(m.From, m.To, m.CC, m.BCC, m.Subject, h.Typed, m.Attachments); err == nil {
			_, err = e.db.ExecContext(ctx, `DELETE FROM vayumail_scheduled WHERE id=?`, id)
			return draft, err
		}
	}
	// Not filed: it stays scheduled, as failed, so it neither goes nor vanishes.
	e.failScheduled(ctx, id, "Could not move it back to Drafts: "+err.Error())
	return "", err
}

// SendScheduledNow sends owner's held message id at once.
func (e *Engine) SendScheduledNow(ctx context.Context, owner string, id int64) error {
	if err := e.ensureScheduledTable(); err != nil {
		return err
	}
	ok, err := e.claimScheduled(ctx, owner, id, ScheduledSending, ScheduledWaiting, ScheduledFailed)
	if err != nil {
		return err
	}
	if !ok {
		return e.whyNotClaimed(ctx, owner, id)
	}
	if err := e.sendClaimed(ctx, id); err != nil {
		return err
	}
	// Now means now: deliver what it queued rather than at the next pass.
	go func() { _, _, _ = e.queue.ProcessDue(context.Background(), time.Now()) }()
	return nil
}

// whyNotClaimed explains a claim that found nothing: gone (sent, cancelled or
// never this owner's), or busy.
func (e *Engine) whyNotClaimed(ctx context.Context, owner string, id int64) error {
	var state string
	err := e.db.QueryRowContext(ctx, `SELECT state FROM vayumail_scheduled WHERE id=? AND owner=?`, id, normEmail(owner)).Scan(&state)
	if err != nil {
		return errors.New("this message is no longer scheduled")
	}
	return ErrScheduledBusy
}

// sendClaimed sends the row already claimed as sending, deleting it once sent
// and marking it failed, with the reason, otherwise. The checks Send makes on
// the sender are made again: either may have changed since it was scheduled.
func (e *Engine) sendClaimed(ctx context.Context, id int64) error {
	var owner string
	if err := e.db.QueryRowContext(ctx, `SELECT owner FROM vayumail_scheduled WHERE id=?`, id).Scan(&owner); err != nil {
		return err
	}
	h, err := e.scheduledMessage(ctx, id)
	switch {
	case err != nil:
	case e.MailboxReadOnly(owner):
		err = errors.New("this mailbox is now read-only, so it cannot send")
	case e.MailboxOverQuota(owner):
		err = errors.New("this mailbox is full; delete some mail, then send it again")
	default:
		_, err = e.ComposeRich(ctx, h.Compose)
	}
	if err != nil {
		e.failScheduled(ctx, id, err.Error())
		return err
	}
	_, err = e.db.ExecContext(ctx, `DELETE FROM vayumail_scheduled WHERE id=?`, id)
	return err
}

func (e *Engine) failScheduled(ctx context.Context, id int64, why string) {
	if _, err := e.db.ExecContext(ctx, `UPDATE vayumail_scheduled SET state=?, last_error=? WHERE id=?`, ScheduledFailed, why, id); err != nil {
		logging.LogError("vayumail", "recording a scheduled message's failure", err.Error())
	}
}

// SendDueScheduled sends every held message due at now and returns how many
// went.
func (e *Engine) SendDueScheduled(ctx context.Context, now time.Time) int {
	if e.ensureScheduledTable() != nil {
		return 0
	}
	rows, err := e.db.QueryContext(ctx,
		`SELECT id FROM vayumail_scheduled WHERE state=? AND due_unix<=? ORDER BY due_unix, id LIMIT 100`,
		ScheduledWaiting, now.Unix())
	if err != nil {
		logging.LogError("vayumail", "listing scheduled messages due", err.Error())
		return 0
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		logging.LogError("vayumail", "listing scheduled messages due", err.Error())
	}
	_ = rows.Close()
	sent := 0
	for _, id := range ids {
		if ok, err := e.claimScheduled(ctx, "", id, ScheduledSending, ScheduledWaiting); err != nil || !ok {
			continue
		}
		if e.sendClaimed(ctx, id) == nil {
			sent++
		}
	}
	return sent
}

// failInterruptedScheduled marks rows a stopped server left mid-send or
// mid-cancel. Sending them again could send twice, and dropping them could
// lose them; so they wait, failed and saying why, for the sender to choose.
func (e *Engine) failInterruptedScheduled(ctx context.Context) {
	if e.ensureScheduledTable() != nil {
		return
	}
	if _, err := e.db.ExecContext(ctx, `UPDATE vayumail_scheduled SET state=?, last_error=? WHERE state IN (?,?)`,
		ScheduledFailed, interruptedNote, ScheduledSending, scheduledCancelling); err != nil {
		logging.LogError("vayumail", "recovering scheduled messages after a restart", err.Error())
	}
}

// scheduledSender is the background send loop, stopped by the engine's done
// channel alongside the queue worker. What it sends is queued, so the queue is
// run straight after rather than at its own next pass.
func (e *Engine) scheduledSender() {
	t := time.NewTicker(scheduledSweepInterval)
	defer t.Stop()
	for {
		if n := e.SendDueScheduled(context.Background(), time.Now()); n > 0 {
			logging.LogInfo("vayumail", fmt.Sprintf("sent %d scheduled message(s)", n))
			_, _, _ = e.queue.ProcessDue(context.Background(), time.Now())
		}
		select {
		case <-e.done:
			return
		case <-t.C:
		}
	}
}
