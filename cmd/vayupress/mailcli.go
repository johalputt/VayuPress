// SPDX-License-Identifier: Apache-2.0

package main

// mailcli.go — the VayuMail break-glass CLI (ADR-0144).
//
// Every recovery factor has to be enrolled in advance, which leaves one case no
// flow can reach: the last administrator forgets their own mailbox password and
// holds no codes. There is nothing left to authenticate them with, and inventing
// something would mean inventing a factor an attacker could use too.
//
// The honest answer is a documented break-glass, run on the host as the service
// user. It grants nothing new — a shell already reaches the keystore DEK and the
// Maildir — but it is deliberately loud and audited, so it can never be the quiet
// path of least resistance.

import (
	"context"
	"fmt"
	"io"
	"strings"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/vayuos/mail"
)

func mailUsage() error {
	return fmt.Errorf(`usage:
  vayupress mail passwd <address> <new-password>   reset a mailbox password (break-glass)
  vayupress mail recovery <address>                show what this mailbox can recover with
  vayupress mail unrecoverable                     list mailboxes with no recovery factor`)
}

// runMailCLI dispatches the mail subcommands. db must already be open.
func runMailCLI(ctx context.Context, args []string, out io.Writer, accts *mail.AccountStore) error {
	if len(args) == 0 {
		return mailUsage()
	}
	if accts == nil {
		return fmt.Errorf("vayumail is not configured on this install (set DOMAIN)")
	}

	switch args[0] {
	case "passwd":
		if len(args) < 3 {
			return fmt.Errorf("usage: vayupress mail passwd <address> <new-password>")
		}
		addr := strings.ToLower(strings.TrimSpace(args[1]))
		pass := args[2]
		if !accts.Exists(ctx, addr) {
			return fmt.Errorf("no such mailbox: %s", addr)
		}

		// The same pipeline as every other reset. A break-glass that skipped
		// revocation would be the one path that leaves an attacker's device
		// connected — and it is the path used precisely when something has gone
		// wrong, so it is the last place to cut corners.
		deps := mailResetDeps{
			SetPasswordHash:    accts.SetPasswordHash,
			RevokeAppPasswords: accts.RevokeAllAppPasswords,
			InvalidateTokens:   accts.InvalidateRecoveryTokens,
			Audit:              dbpkg.AuditLog,
			// No notification channels: the mailer and the queue belong to a running
			// engine, and this command deliberately runs without starting one.
		}
		// The break-glass override must be recordable even here, where no engine is
		// running — the ledger is a table, not a service. Without this the command
		// refuses on a handed-over mailbox, which is the correct failure.
		deps.handedOver = accts.IsHandedOver
		deps.RecordAccess = accts.AppendLedger
		res, err := applyMailPasswordReset(ctx, deps, addr, pass, mailResetByBreakGlass, "cli:break-glass")
		if err != nil {
			return err
		}

		fmt.Fprintf(out, "password reset for %s\n", addr)
		fmt.Fprintf(out, "  %d app password(s) revoked — every mail app must be set up again\n",
			res.AppPasswordsRevoked)
		fmt.Fprintln(out, "  outstanding recovery links invalidated")
		for _, p := range res.Problems {
			fmt.Fprintf(out, "  WARNING: %s\n", p)
		}
		// Say plainly what this command did NOT do, so nobody assumes a running
		// server's sessions or queue were touched.
		fmt.Fprintln(out, "\nNot done by this command (the server is not running here):")
		fmt.Fprintln(out, "  - live webmail sessions are not ended")
		fmt.Fprintln(out, "  - queued outbound mail is not held")
		fmt.Fprintln(out, "  - no notification was sent to the holder")
		fmt.Fprintln(out, "Restart the service, then review Outbox and connected devices in the console.")
		fmt.Fprintln(out, "\nThis was recorded in the audit log as a break-glass reset.")
		return nil

	case "recovery":
		if len(args) < 2 {
			return fmt.Errorf("usage: vayupress mail recovery <address>")
		}
		st := accts.RecoveryStatusFor(ctx, args[1])
		fmt.Fprintf(out, "%s\n", st.Email)
		fmt.Fprintf(out, "  recovery codes remaining : %d\n", st.CodesRemaining)
		fmt.Fprintf(out, "  verified address         : %s\n", orNone(st.Contact))
		fmt.Fprintf(out, "  awaiting verification    : %s\n", orNone(st.ContactPending))
		if st.Ready {
			fmt.Fprintln(out, "  status                   : can be recovered")
		} else {
			fmt.Fprintln(out, "  status                   : CANNOT be recovered — nothing usable is enrolled")
		}
		return nil

	case "unrecoverable":
		stuck := accts.UnrecoverableAccounts(ctx)
		if len(stuck) == 0 {
			// Distinguish "all covered" from "nothing to cover". Reporting an empty
			// install as healthy reads as reassurance the operator has not earned.
			if list, err := accts.List(ctx); err == nil && len(list) == 0 {
				fmt.Fprintln(out, "no mailboxes on this install")
				return nil
			}
			fmt.Fprintln(out, "every active mailbox has at least one working recovery factor")
			return nil
		}
		fmt.Fprintf(out, "%d mailbox(es) cannot be recovered by their holder:\n", len(stuck))
		for _, e := range stuck {
			fmt.Fprintf(out, "  %s\n", e)
		}
		fmt.Fprintln(out, "\nEnrol them under Mail accounts → Account recovery in the console.")
		return nil

	default:
		return mailUsage()
	}
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
