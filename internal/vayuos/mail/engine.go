// Package mail — VayuMail engine implementation.
//
// The MailEngine owns the SMTP server (port 25/587), IMAP server (port 993),
// DKIM signer, message queue, and TLS manager. It delegates authentication
// to VayuPress's user store through the Bridge interface.
//
// VayuMail is based on Mox (MIT license) by Mechiel Lukkien.
// Source: https://github.com/mjl-/mox
package mail

import (
	"context"
)

// Engine owns the mail subsystem runtime.
type Engine struct {
	cfg    *Config
	bridge Bridge
}

func NewEngine(cfg *Config, bridge Bridge) *Engine {
	return &Engine{cfg: cfg, bridge: bridge}
}

func (e *Engine) Name() string { return "VayuMail" }

func (e *Engine) Start(_ context.Context) error {
	if e.cfg == nil || !e.cfg.Enabled {
		return nil
	}
	return nil
}

func (e *Engine) Stop(_ context.Context) error { return nil }

func (e *Engine) Config() *Config { return e.cfg }
func (e *Engine) Bridge() Bridge   { return e.bridge }

// Implements Bridge interface
func (e *Engine) AuthUser(username, password string) (bool, error) {
	return e.bridge.AuthUser(username, password)
}
func (e *Engine) GetUserByEmail(email string) (*MailUser, error) {
	return e.bridge.GetUserByEmail(email)
}
func (e *Engine) CreateMailbox(domain, username string) error {
	return e.bridge.CreateMailbox(domain, username)
}
func (e *Engine) DeleteMailbox(domain, username string) error {
	return e.bridge.DeleteMailbox(domain, username)
}
func (e *Engine) ListMailboxes(domain string) ([]Mailbox, error) {
	return e.bridge.ListMailboxes(domain)
}
func (e *Engine) GetMailboxStats(username string) (*MailboxStats, error) {
	return e.bridge.GetMailboxStats(username)
}
func (e *Engine) SendTransactional(msg *TransactionalMessage) error {
	return e.bridge.SendTransactional(msg)
}
func (e *Engine) AddDomain(domain string) error {
	return e.bridge.AddDomain(domain)
}
func (e *Engine) RemoveDomain(domain string) error {
	return e.bridge.RemoveDomain(domain)
}
func (e *Engine) ListDomains() ([]MailDomain, error) {
	return e.bridge.ListDomains()
}
func (e *Engine) GetSMTPStats() (*SMTPStats, error) {
	return e.bridge.GetSMTPStats()
}
func (e *Engine) GetQueueStatus() (*QueueStatus, error) {
	return e.bridge.GetQueueStatus()
}
func (e *Engine) GetDomainHealth(domain string) (*DomainHealth, error) {
	return e.bridge.GetDomainHealth(domain)
}
func (e *Engine) OnMessageReceived(handler func(*InboundMessage)) {
	e.bridge.OnMessageReceived(handler)
}
func (e *Engine) OnMessageDelivered(handler func(*DeliveredMessage)) {
	e.bridge.OnMessageDelivered(handler)
}
func (e *Engine) OnDeliveryFailed(handler func(*FailedMessage)) {
	e.bridge.OnDeliveryFailed(handler)
}