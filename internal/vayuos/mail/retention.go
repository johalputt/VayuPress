// SPDX-License-Identifier: Apache-2.0

package mail

// retention.go — auto-delete of read mail (ADR-0130). A mailbox may opt in
// to a retention window: read messages the holder hasn't deliberately saved
// are PERMANENTLY deleted once they have been read for longer than the
// window. "Saved" means pinned (the Maildir F flag) or living in a folder
// that represents intent — Sent, Drafts, Archive, and Snoozed are never
// touched. Deletion is final: files are removed, not moved to Trash.
//
// Read time comes from the file's mtime, stamped by setMessageFlags at the
// Seen transition (renames preserve mtime, so it survives later flag
// changes). Mail read before that stamping existed falls back to its
// delivery mtime — retention can only fire EARLIER for such legacy mail,
// never keep it longer, which is documented in the ADR.

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// retentionSweepFolders are the folders retention may delete from. Sent,
// Drafts, Archive, and Snoozed carry deliberate intent and are exempt.
var retentionSweepFolders = []string{"Inbox", "Junk", "Trash"}

// retentionLoop runs the sweep for every opted-in mailbox once shortly
// after boot and then hourly, until the engine stops.
func (e *Engine) retentionLoop() {
	const initialDelay = 2 * time.Minute
	select {
	case <-e.done:
		return
	case <-time.After(initialDelay):
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		e.sweepRetention(time.Now())
		e.pruneDeliveredQueue(time.Now())
		select {
		case <-e.done:
			return
		case <-ticker.C:
		}
	}
}

// pruneDeliveredQueue auto-clears delivered outbound-queue rows older than the
// operator's retention window (0 = off). The Sent Maildir copy is a separate
// store and is never touched, so pruning the queue only trims the Outbox's
// delivery-status log, not the sent mail itself.
func (e *Engine) pruneDeliveredQueue(now time.Time) {
	days := e.QueueRetentionDays()
	if days <= 0 || e.queue == nil {
		return
	}
	cutoff := now.AddDate(0, 0, -days)
	if n, err := e.queue.PruneDelivered(context.Background(), cutoff); err != nil {
		slog.Warn("vayumail queue retention: prune", "err", err)
	} else if n > 0 {
		slog.Info("vayumail queue retention: pruned delivered rows", "count", n, "olderThanDays", days)
	}
}

// sweepRetention applies every opted-in mailbox's retention window as of
// now. Failures are logged and never stop the loop.
func (e *Engine) sweepRetention(now time.Time) {
	if e.maildir == nil || e.accounts == nil {
		return
	}
	ctx := context.Background()
	accs, err := e.accounts.List(ctx)
	if err != nil {
		slog.Warn("vayumail retention: list accounts", "err", err)
		return
	}
	for _, ac := range accs {
		days := e.accounts.RetentionDays(ctx, ac.Email)
		if days <= 0 {
			continue
		}
		user := ac.Email
		if i := strings.Index(user, "@"); i > 0 {
			user = user[:i]
		}
		n, err := e.maildir.SweepRetention(e.cfg.Domain, user, days, now)
		if err != nil {
			slog.Warn("vayumail retention: sweep", "mailbox", ac.Email, "err", err)
			continue
		}
		if n > 0 {
			slog.Info("vayumail retention: deleted read mail",
				"mailbox", ac.Email, "count", n, "days", days)
			e.auditRetention(ac.Email, n, days)
		}
	}
}

// auditRetention is set by the host application (an audit-log writer);
// nil-safe so the engine stays independent of the database layer.
func (e *Engine) auditRetention(email string, count, days int) {
	if e.retentionAudit != nil {
		e.retentionAudit(email, count, days)
	}
}

// SetRetentionAudit installs the audit callback invoked after a sweep that
// deleted anything. Call before Start.
func (e *Engine) SetRetentionAudit(fn func(email string, count, days int)) {
	e.retentionAudit = fn
}

// SweepRetention permanently deletes this mailbox's read, unpinned mail
// whose read time (file mtime — see setMessageFlags) is older than the
// window. Only cur/ is scanned: unread mail lives in new/ and is never
// eligible. Returns how many messages were removed.
func (m *Maildir) SweepRetention(domain, username string, days int, now time.Time) (int, error) {
	if days <= 0 {
		return 0, nil
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	deleted := 0
	for _, folder := range retentionSweepFolders {
		dir := filepath.Join(m.folderDir(domain, username, folder), "cur")
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // folder may not exist yet
		}
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			_, flags := splitMaildirFlags(ent.Name())
			if !strings.ContainsRune(flags, 'S') || strings.ContainsRune(flags, 'F') {
				continue // unread or pinned — never touched
			}
			info, err := ent.Info()
			if err != nil || info.ModTime().After(cutoff) {
				continue
			}
			if err := os.Remove(filepath.Join(dir, ent.Name())); err == nil {
				deleted++
			}
		}
	}
	return deleted, nil
}
