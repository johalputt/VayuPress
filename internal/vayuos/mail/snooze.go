// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"errors"
	"time"
)

// Snooze: hide a message until a chosen time, then resurface it.
//
// Snoozing physically moves the message into the dedicated Snoozed folder (so
// every client — panel and IMAP — sees consistent state) and records a wake
// row. A background sweeper moves due messages back to their original folder;
// the Maildir re-delivery lands them in new/ without the Seen flag, so a woken
// message resurfaces as unread, exactly like Gmail's snooze. If the operator
// moves a snoozed message out by hand, its stale wake row is discarded on the
// next sweep — a snooze can never "wake" a message that left the folder.

// snoozeSweepInterval is how often due snoozes are woken.
const snoozeSweepInterval = time.Minute

// snoozeRow is one pending wake.
type snoozeRow struct {
	Mailbox    string
	ID         string // message id within the Snoozed folder
	OrigFolder string
	Until      time.Time
}

// ensureSnoozeTable creates the wake table on first use (idempotent).
func (s *AccountStore) ensureSnoozeTable() error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_snooze(
		mailbox TEXT NOT NULL,
		id TEXT NOT NULL,
		orig_folder TEXT NOT NULL,
		until DATETIME NOT NULL,
		PRIMARY KEY(mailbox, id));`)
	return err
}

// recordSnooze stores one wake row (upsert: re-snoozing replaces the time).
func (s *AccountStore) recordSnooze(ctx context.Context, mailbox, id, origFolder string, until time.Time) error {
	if err := s.ensureSnoozeTable(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO vayumail_snooze(mailbox,id,orig_folder,until) VALUES(?,?,?,?)
		 ON CONFLICT(mailbox,id) DO UPDATE SET orig_folder=excluded.orig_folder, until=excluded.until`,
		normEmail(mailbox), id, origFolder, until.UTC())
	return err
}

// dueSnoozes returns every wake row at or past its time.
func (s *AccountStore) dueSnoozes(ctx context.Context, now time.Time) []snoozeRow {
	out := []snoozeRow{}
	if err := s.ensureSnoozeTable(); err != nil {
		return out
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT mailbox,id,orig_folder,until FROM vayumail_snooze WHERE until<=?`, now.UTC())
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var r snoozeRow
		if rows.Scan(&r.Mailbox, &r.ID, &r.OrigFolder, &r.Until) == nil {
			out = append(out, r)
		}
	}
	_ = rows.Err()
	return out
}

// clearSnooze removes one wake row.
func (s *AccountStore) clearSnooze(ctx context.Context, mailbox, id string) {
	if s.ensureSnoozeTable() != nil {
		return
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM vayumail_snooze WHERE mailbox=? AND id=?`, normEmail(mailbox), id)
}

// Snooze hides a message until the given time: it is moved into the Snoozed
// folder and a wake row is recorded. Sent/Drafts and the Snoozed folder itself
// cannot be snoozed.
func (e *Engine) Snooze(username, folder, id string, until time.Time) error {
	if e.maildir == nil || e.accounts == nil {
		return errors.New("vayumail: not started")
	}
	folder = canonicalFolder(folder)
	switch folder {
	case "Sent", "Drafts", "Snoozed":
		return errors.New("this folder cannot be snoozed")
	}
	if until.Before(time.Now()) {
		return errors.New("the wake time is in the past")
	}
	// mailboxKey, like every other folder operation on the engine. This method
	// predates VayuDomains and assumed username was a bare localpart on the
	// primary domain: it hardcoded e.cfg.Domain, passed username straight through
	// as the Maildir localpart, and built the wake key as username+"@"+domain —
	// which for a full secondary address produced "bob@shop.example@example.com",
	// a mailbox that does not exist. Snooze therefore did nothing at all outside
	// the primary domain, and said so nowhere.
	dom, local := e.mailboxKey(username)
	raw, err := e.maildir.ReadRawFolder(dom, local, folder, id)
	if err != nil {
		return err
	}
	nid, err := e.maildir.DeliverTo(dom, local, "Snoozed", raw)
	if err != nil {
		return err
	}
	if err := e.accounts.recordSnooze(context.Background(), local+"@"+dom, nid, folder, until); err != nil {
		// Could not record the wake: undo rather than strand the message asleep.
		_ = e.maildir.deleteMessage(dom, local, "Snoozed", nid)
		return err
	}
	return e.maildir.deleteMessage(dom, local, folder, id)
}

// sweepSnoozes wakes every due message: moved back to its original folder
// (re-delivery drops the Seen flag, so it resurfaces unread) and its row is
// cleared. Stale rows — the message left Snoozed by hand — are discarded.
func (e *Engine) sweepSnoozes(now time.Time) {
	if e.maildir == nil || e.accounts == nil {
		return
	}
	ctx := context.Background()
	for _, r := range e.accounts.dueSnoozes(ctx, now) {
		// mailboxKey, not a bare split: the row stores a full address, and every
		// other folder operation (ListFolder, DeleteMessage) resolves it this way.
		//
		// This used to keep the localpart and pass e.cfg.Domain — the PRIMARY
		// domain — whatever domain the mailbox was actually on. For a secondary
		// mailbox that looked for the message in the primary's Maildir, where it
		// does not exist, so the move failed; the error is discarded, and the row
		// is cleared regardless (by design, to drop rows whose message was moved
		// out of Snoozed by hand). The message therefore stayed in Snoozed for
		// ever, never woke, and was never retried — snooze silently did nothing at
		// all for every mailbox outside the primary domain.
		dom, local := e.mailboxKey(r.Mailbox)
		if local != "" {
			_ = e.maildir.MoveBetween(dom, local, r.ID, "Snoozed", canonicalFolder(r.OrigFolder))
		}
		e.accounts.clearSnooze(ctx, r.Mailbox, r.ID)
	}
}

// snoozeSweeper is the background wake loop, stopped by the engine's done
// channel alongside the queue worker.
func (e *Engine) snoozeSweeper() {
	t := time.NewTicker(snoozeSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-e.done:
			return
		case now := <-t.C:
			e.sweepSnoozes(now)
		}
	}
}
