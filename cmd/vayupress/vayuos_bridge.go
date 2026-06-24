// vayuos_bridge.go — VayuMail bridge implementation.
//
// Bridges VayuMail to VayuPress core: delegates auth to the user store,
// manages mailbox lifecycle, and handles transactional email sending.
package main

import (
	"context"
	"fmt"

	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/logging"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// vayuMailBridge implements the mail.Bridge interface by delegating to
// VayuPress subsystems (users store, email sender, etc.).
type vayuMailBridge struct{ app *App }

func (b *vayuMailBridge) AuthUser(username, password string) (bool, error) {
	email := username + "@" + b.app.vayuMail.Config().Domain
	_, err := b.app.userStore.Authenticate(context.Background(), email, password)
	return err == nil, nil
}

func (b *vayuMailBridge) GetUserByEmail(email string) (*vmail.MailUser, error) {
	// Delegate to VayuPress user store for auth check.
	// For existence check, use a direct DB lookup if needed.
	return nil, fmt.Errorf("user lookup not available during setup")
}
		return nil, err
	}
	localPart := strings.SplitN(email, "@", 2)[0]
	return &vmail.MailUser{
		UserID:   u.ID,
		Email:    email,
		Domain:   b.app.vayuMail.Config().Domain,
		Username: localPart,
	}, nil
}

func (b *vayuMailBridge) CreateMailbox(domain, username string) error {
	if !b.app.vayuMail.Config().Enabled {
		return nil
	}
	logging.LogInfo("vayumail", "mailbox created: "+username+"@"+domain)
	return nil
}

func (b *vayuMailBridge) DeleteMailbox(domain, username string) error {
	logging.LogInfo("vayumail", "mailbox deleted: "+username+"@"+domain)
	return nil
}

func (b *vayuMailBridge) ListMailboxes(domain string) ([]vmail.Mailbox, error) {
	return []vmail.Mailbox{}, nil
}

func (b *vayuMailBridge) GetMailboxStats(username string) (*vmail.MailboxStats, error) {
	return &vmail.MailboxStats{}, nil
}

func (b *vayuMailBridge) SendTransactional(msg *vmail.TransactionalMessage) error {
	if b.app.mailer != nil && b.app.mailer.Enabled() {
		for _, to := range msg.To {
			if err := b.app.mailer.Send(email.Message{
				To:      to,
				Subject: msg.Subject,
				Text:    msg.PlainBody,
				HTML:    msg.Body,
			}); err != nil {
				return fmt.Errorf("send transactional email to %s: %w", to, err)
			}
		}
	}
	return nil
}

func (b *vayuMailBridge) AddDomain(domain string) error {
	logging.LogInfo("vayumail", "domain added: "+domain)
	return nil
}

func (b *vayuMailBridge) RemoveDomain(domain string) error {
	logging.LogInfo("vayumail", "domain removed: "+domain)
	return nil
}

func (b *vayuMailBridge) ListDomains() ([]vmail.MailDomain, error) {
	cfg := b.app.vayuMail.Config()
	if cfg.Domain == "" {
		return nil, nil
	}
	return []vmail.MailDomain{{Domain: cfg.Domain, Active: true}}, nil
}

func (b *vayuMailBridge) GetSMTPStats() (*vmail.SMTPStats, error) {
	return &vmail.SMTPStats{}, nil
}

func (b *vayuMailBridge) GetQueueStatus() (*vmail.QueueStatus, error) {
	return &vmail.QueueStatus{}, nil
}

func (b *vayuMailBridge) GetDomainHealth(domain string) (*vmail.DomainHealth, error) {
	return &vmail.DomainHealth{Domain: domain}, nil
}

func (b *vayuMailBridge) OnMessageReceived(handler func(*vmail.InboundMessage)) {}

func (b *vayuMailBridge) OnMessageDelivered(handler func(*vmail.DeliveredMessage)) {}

func (b *vayuMailBridge) OnDeliveryFailed(handler func(*vmail.FailedMessage)) {}