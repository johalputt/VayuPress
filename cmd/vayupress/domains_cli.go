// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
)

// runDomainsCLI powers `vayupress domains …` — the read/notify surface the
// privileged TLS+nginx helper (scripts/setup-vayudomain.sh) drives from, since the
// unprivileged server process can never run certbot or reload nginx itself
// (ADR-0132, P4). The binary only *records* per-domain TLS state; the helper acts
// on it out-of-process.
//
// P5 adds the manual sync gate: a newly registered domain sits on sync_state=hold
// and is invisible to `hosts` (the helper's work list) until the operator
// approves it — from VayuOS → Domains ("Sync now") or `domains sync <host>`.
//
//	vayupress domains list                 # every registered domain, human-readable
//	vayupress domains hosts [--mail|--all|--hold]  # secondary hosts, one per line (for scripts)
//	vayupress domains sync <host>|--all    # approve a domain (or all) for provisioning
//	vayupress domains hold <host>|--all    # pause provisioning/maintenance for a domain (or all)
//	vayupress domains set-tls <host> <s>   # record a domain's tls_state (pending|active|failed)
func runDomainsCLI(args []string, out io.Writer) error {
	reg := domain.New(dbpkg.DB, dbpkg.RDB)
	ctx := context.Background()
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "list", "":
		list, err := reg.List(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%-32s %-9s %-5s %-8s %s\n", "HOST", "ROLE", "MAIL", "SYNC", "TLS")
		for _, d := range list {
			role := "secondary"
			if d.IsPrimary {
				role = "primary"
			}
			mail := "off"
			if d.MailEnabled {
				mail = "on"
			}
			// The primary is provisioned outside the registry, so its sync
			// column is a dash rather than a state the helper would act on.
			syncCol := d.EffectiveSyncState()
			if d.IsPrimary {
				syncCol = "—"
			}
			fmt.Fprintf(out, "%-32s %-9s %-5s %-8s %s\n", d.Host, role, mail, syncCol, d.TLSState)
		}
		return nil

	case "hosts":
		// Secondary hosts only (the primary's cert is the existing certbot cert,
		// managed outside the registry). By default only SYNC-APPROVED domains are
		// listed — this is the helper's work list, and the P5 gate keeps domains on
		// manual hold out of it. --all lists every active secondary regardless of
		// sync state (diagnostics), --hold lists only the held ones (so scripts can
		// tell the operator what is waiting), --mail restricts to mail_enabled.
		onlyMail, all, onlyHold := false, false, false
		for _, a := range args[1:] {
			switch a {
			case "--mail":
				onlyMail = true
			case "--all":
				all = true
			case "--hold":
				onlyHold = true
			}
		}
		list, err := reg.List(ctx)
		if err != nil {
			return err
		}
		for _, d := range list {
			if d.IsPrimary || d.Status != domain.StatusActive {
				continue
			}
			if onlyMail && !d.MailEnabled {
				continue
			}
			switch {
			case onlyHold:
				if d.IsSyncApproved() {
					continue
				}
			case all:
				// every active secondary
			default:
				if !d.IsSyncApproved() {
					continue
				}
			}
			fmt.Fprintln(out, d.Host)
		}
		return nil

	case "sync", "hold":
		if len(args) < 2 {
			return fmt.Errorf("usage: vayupress domains %s <host>|--all", sub)
		}
		state := domain.SyncApproved
		if sub == "hold" {
			state = domain.SyncHold
		}
		// Bulk: approve/hold every secondary at once, so a batch of freshly
		// registered domains can be released without touching each row.
		if strings.TrimSpace(args[1]) == "--all" {
			n, err := reg.SetAllSyncState(ctx, state)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "%d domain(s) set sync_state=%s\n", n, state)
			return nil
		}
		host := strings.TrimSpace(args[1])
		d, err := reg.Resolve(ctx, host)
		if err != nil {
			return fmt.Errorf("no registered domain for host %q: %w", host, err)
		}
		if d.IsPrimary {
			return fmt.Errorf("the primary domain is provisioned outside the registry")
		}
		if err := reg.SetSyncState(ctx, d.ID, state); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s sync_state=%s\n", d.Host, state)
		return nil

	case "set-tls":
		if len(args) < 3 {
			return fmt.Errorf("usage: vayupress domains set-tls <host> <pending|active|failed>")
		}
		host := strings.TrimSpace(args[1])
		state := strings.TrimSpace(args[2])
		switch state {
		case domain.TLSPending, domain.TLSActive, domain.TLSFailed:
		default:
			return fmt.Errorf("invalid tls state %q (want pending|active|failed)", state)
		}
		d, err := reg.Resolve(ctx, host)
		if err != nil {
			return fmt.Errorf("no registered domain for host %q: %w", host, err)
		}
		if d.IsPrimary {
			return fmt.Errorf("the primary domain's TLS is managed outside the registry")
		}
		if err := reg.SetTLSState(ctx, d.ID, state); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s tls_state=%s\n", host, state)
		return nil

	default:
		return fmt.Errorf("unknown domains subcommand %q (want list|hosts|sync|hold|set-tls)", sub)
	}
}
