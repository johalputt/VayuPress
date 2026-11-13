// SPDX-License-Identifier: Apache-2.0

package mail

// exits.go — the standing ways a mailbox's mail and files leave it without
// anyone signing in: the forwarding address, rules that forward, links to
// files, and files shared with other mailboxes.
//
// They are what someone who took a mailbox over leaves behind to keep
// reading it, and a password reset, which ends every way IN, does not end
// them: each may as well be the holder's own, and deleting a holder's
// forwarding on a reset would lose their mail. So the reset's notice names
// them, for the holder to look at.

import (
	"context"
	"sort"
	"strings"
)

// MailboxExits is what keeps leaving a mailbox on its own.
type MailboxExits struct {
	ForwardTo    string   // the forwarding address, "" for none
	RuleForwards []string // addresses enabled rules forward to
	FileLinks    int      // links to files and folders, open
	FileShares   int      // files and folders shared with mailboxes here
}

// Any reports whether anything leaves the mailbox on its own.
func (x MailboxExits) Any() bool {
	return x.ForwardTo != "" || len(x.RuleForwards) > 0 || x.FileLinks > 0 || x.FileShares > 0
}

// StandingExits is what leaves the mailbox at address on its own.
func (e *Engine) StandingExits(ctx context.Context, address string) MailboxExits {
	var x MailboxExits
	if e.accounts == nil {
		return x
	}
	mbox := normEmail(address)
	x.ForwardTo = e.accounts.ForwardFor(ctx, mbox)
	if rules, err := e.accounts.FiltersFor(ctx, mbox); err == nil {
		seen := map[string]bool{}
		for _, r := range rules {
			if !r.Enabled {
				continue
			}
			for _, a := range r.Actions {
				if a.Kind == "forward" && a.Arg != "" && !seen[strings.ToLower(a.Arg)] {
					seen[strings.ToLower(a.Arg)] = true
					x.RuleForwards = append(x.RuleForwards, a.Arg)
				}
			}
		}
		sort.Strings(x.RuleForwards)
	}
	if e.accounts.ensureDriveTables() == nil {
		_ = e.accounts.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_drive_shares WHERE mailbox=?`, mbox).Scan(&x.FileLinks)
		_ = e.accounts.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_drive_grants WHERE mailbox=?`, mbox).Scan(&x.FileShares)
	}
	return x
}
