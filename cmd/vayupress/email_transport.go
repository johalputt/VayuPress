package main

// email_transport.go — make the built-in VayuMail engine the delivery transport
// for transactional email (sign-in links, welcome, newsletter confirmations,
// receipts) whenever external SMTP is not configured.
//
// internal/email.Sender stays transport-agnostic: it exposes SetFallback and we
// inject this closure from main, so the email package never has to import the
// mail engine. When SMTP_HOST is set the external relay is used as before; when
// it is absent but DOMAIN is set (so VayuMail is enabled), mail is DKIM-signed
// and delivered through VayuMail's durable, retried outbound queue instead of
// being silently dropped — which is why sign-in/welcome mail now actually sends.

import (
	"context"
	"errors"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/render"
)

// sendViaVayuMail delivers a single message through the VayuMail engine. It is
// wired as the email.Sender fallback, so it only runs when external SMTP is not
// configured.
func (a *App) sendViaVayuMail(m email.Message) error {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		return errors.New("no mail transport: set SMTP_HOST, or set DOMAIN to enable VayuMail")
	}
	// senderUserID "" skips PGP; transactional mail is sent from the site, not a
	// keyed mailbox. SendSystemMail DKIM-signs and enqueues but never encrypts,
	// so sign-in links and the welcome message arrive as readable text/HTML
	// (not a PGP blob) even when the recipient has a published key.
	_, err := a.vayuMail.SendSystemMail(context.Background(), a.transactionalFrom(), []string{m.To}, m.Subject, m.HTML, m.Text)
	return err
}

// transactionalFrom returns the From header for system mail. It is branded with
// the operator's own site name and domain for uniqueness — "<Site name>
// <noreply@domain>", or "<domain> <noreply@domain>" when no site name is set —
// rather than a generic product name. An explicitly customised SMTP_FROM (i.e.
// anything other than the built-in default) still wins.
func (a *App) transactionalFrom() string {
	domain := strings.TrimSpace(config.Cfg.Domain)
	if a.vayuMail != nil {
		if d := strings.TrimSpace(a.vayuMail.Config().Domain); d != "" {
			domain = d
		}
	}
	if domain == "" {
		domain = "localhost"
	}
	// Honour an operator-customised SMTP_FROM, but ignore the built-in default
	// so transactional mail carries the site's own identity, not a generic name.
	builtinDefault := "VayuPress <noreply@" + strings.TrimSpace(config.Cfg.Domain) + ">"
	if f := strings.TrimSpace(config.Cfg.SMTPFrom); f != "" && f != builtinDefault {
		return f
	}
	addr := "noreply@" + domain
	name := strings.TrimSpace(render.GetActiveSettings().Name)
	if name == "" {
		name = domain // domain as the display name keeps the sender unique per site
	}
	return name + " <" + addr + ">"
}
