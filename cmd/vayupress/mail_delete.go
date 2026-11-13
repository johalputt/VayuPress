// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
)

// deleteMailbox deletes a mailbox: first every way into it that lives
// outside the mail store and is held under its address, then the mailbox
// itself (vmail.Engine.DeleteMailbox: its rows, then its mail, Drive and key
// set aside). Both delete paths, the console's button and the JSON endpoint,
// come here.
//
// The sign-ins are held under "vmail:" and the address, not under the
// account, and a console session or a passkey is checked only for an
// account at that address. Left in place, the address given out again
// handed its next holder's mailbox to the last one: their passkey, their
// session cookie, a key minted for a tool they connected. They go first,
// and a failure stops the delete, so an address is never free to be given
// out again while any of them still works.
func (a *App) deleteMailbox(ctx context.Context, email string) (string, error) {
	principal := "vmail:" + strings.ToLower(strings.TrimSpace(email))
	if a.passkeys != nil {
		if _, err := a.passkeys.RemoveAll(ctx, principal); err != nil {
			return "", err
		}
	}
	if a.sessions != nil {
		if _, err := a.sessions.DestroyForUser(ctx, principal); err != nil {
			return "", err
		}
	}
	if a.apiKeys != nil {
		if _, err := a.apiKeys.RevokeOwnedBy(ctx, principal); err != nil {
			return "", err
		}
	}
	return a.vayuMail.DeleteMailbox(ctx, email)
}
