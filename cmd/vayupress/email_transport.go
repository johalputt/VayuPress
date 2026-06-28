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
	// keyed mailbox. SendMail DKIM-signs and enqueues for durable delivery.
	_, err := a.vayuMail.SendMail(context.Background(), a.transactionalFrom(), []string{m.To}, m.Subject, m.HTML, m.Text, "")
	return err
}

// transactionalFrom returns the From header for system mail. An operator-set
// SMTP_FROM always wins; otherwise it builds "<Site name> <noreply@domain>" from
// the VayuMail domain so DKIM signs for a domain the engine is authoritative for.
func (a *App) transactionalFrom() string {
	if f := strings.TrimSpace(config.Cfg.SMTPFrom); f != "" {
		return f
	}
	domain := strings.TrimSpace(config.Cfg.Domain)
	if a.vayuMail != nil {
		if d := strings.TrimSpace(a.vayuMail.Config().Domain); d != "" {
			domain = d
		}
	}
	if domain == "" {
		domain = "localhost"
	}
	addr := "noreply@" + domain
	if name := strings.TrimSpace(render.GetActiveSettings().Name); name != "" {
		return name + " <" + addr + ">"
	}
	return addr
}
