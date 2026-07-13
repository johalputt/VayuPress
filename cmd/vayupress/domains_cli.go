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
//	vayupress domains list                 # every registered domain, human-readable
//	vayupress domains hosts [--mail]       # secondary hosts, one per line (for scripts)
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
		fmt.Fprintf(out, "%-32s %-9s %-5s %s\n", "HOST", "ROLE", "MAIL", "TLS")
		for _, d := range list {
			role := "secondary"
			if d.IsPrimary {
				role = "primary"
			}
			mail := "off"
			if d.MailEnabled {
				mail = "on"
			}
			fmt.Fprintf(out, "%-32s %-9s %-5s %s\n", d.Host, role, mail, d.TLSState)
		}
		return nil

	case "hosts":
		// Secondary hosts only (the primary's cert is the existing certbot cert,
		// managed outside the registry). --mail restricts to mail_enabled domains.
		onlyMail := false
		for _, a := range args[1:] {
			if a == "--mail" {
				onlyMail = true
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
			fmt.Fprintln(out, d.Host)
		}
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
		return fmt.Errorf("unknown domains subcommand %q (want list|hosts|set-tls)", sub)
	}
}
