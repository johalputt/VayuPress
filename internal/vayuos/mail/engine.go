package mail

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Engine is the VayuMail runtime: DKIM signer + outbound queue + Maildir store,
// wired to VayuPress core through the Bridge.
type Engine struct {
	cfg     Config
	bridge  Bridge
	db      *sql.DB
	dkim    *DKIM
	queue   *Queue
	maildir *Maildir
	smtpd   *SMTPServer
	imapd   *IMAPServer
	decrypt DecryptHook
	done    chan struct{}
}

// SetDecryptHook installs a transform applied to messages before they are
// served over IMAP (used for transparent PGP decryption). Call before Start.
func (e *Engine) SetDecryptHook(h DecryptHook) { e.decrypt = h }

// NewEngine constructs the engine; call Start to initialise I/O.
func NewEngine(cfg *Config, bridge Bridge, db *sql.DB) *Engine {
	if cfg == nil {
		c := DefaultConfig()
		cfg = &c
	}
	return &Engine{cfg: *cfg, bridge: bridge, db: db, done: make(chan struct{})}
}

// Name identifies the subsystem for the boot orchestrator.
func (e *Engine) Name() string { return "VayuMail" }

// Config returns a copy of the engine configuration.
func (e *Engine) Config() Config { return e.cfg }

// DKIM exposes the DKIM signer (for DNS record display).
func (e *Engine) DKIM() *DKIM { return e.dkim }

// Start initialises DKIM, Maildir and the queue, and launches the retry worker.
// When disabled it is a no-op so the binary still boots in degraded mode.
func (e *Engine) Start(ctx context.Context) error {
	if !e.cfg.Enabled {
		return errors.New("vayumail: disabled (no domain configured yet)")
	}
	if e.cfg.Domain == "" {
		return errors.New("vayumail: domain not set")
	}
	if e.db == nil {
		return errors.New("vayumail: storage not available")
	}
	dk, err := LoadOrCreateDKIM(e.cfg.StorageDir, e.cfg.DKIMSelector, e.cfg.Domain)
	if err != nil {
		return fmt.Errorf("vayumail: dkim init: %w", err)
	}
	e.dkim = dk
	e.maildir = NewMaildir(e.cfg.StorageDir + "/maildir")
	q, err := NewQueue(e.db, e.cfg, NewMXDeliverer(e.cfg.Hostname, e.cfg.DeliveryTimeout))
	if err != nil {
		return fmt.Errorf("vayumail: queue init: %w", err)
	}
	e.queue = q
	go e.worker()

	// Inbound receive side (opt-in). A listening mail daemon is started only
	// when the operator explicitly enables it (Operational Simplicity Doctrine).
	if e.cfg.InboundEnabled {
		e.smtpd = NewSMTPServer(e.cfg, func(from string, rcpts []string, raw []byte) error {
			var firstErr error
			for _, rcpt := range rcpts {
				if _, derr := e.DeliverInbound(rcpt, raw); derr != nil && firstErr == nil {
					firstErr = derr
				}
			}
			return firstErr
		})
		if err := e.smtpd.Start(ctx); err != nil {
			return fmt.Errorf("vayumail: smtp receive: %w", err)
		}
		e.imapd = NewIMAPServer(e.cfg, e.bridge, e.maildir, e.decrypt)
		if err := e.imapd.Start(ctx); err != nil {
			return fmt.Errorf("vayumail: imap: %w", err)
		}
	}
	return nil
}

// Stop halts the retry worker.
func (e *Engine) Stop(_ context.Context) error {
	if e.smtpd != nil {
		_ = e.smtpd.Stop(context.Background())
	}
	if e.imapd != nil {
		_ = e.imapd.Stop(context.Background())
	}
	select {
	case <-e.done:
	default:
		close(e.done)
	}
	return nil
}

func (e *Engine) worker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	// One pass shortly after boot to flush anything left from a restart.
	_, _, _ = e.queue.ProcessDue(context.Background(), time.Now())
	for {
		select {
		case <-e.done:
			return
		case <-ticker.C:
			_, _, _ = e.queue.ProcessDue(context.Background(), time.Now())
		}
	}
}

// CreateMailbox provisions a Maildir for a local account.
func (e *Engine) CreateMailbox(domain, username string) error {
	if e.maildir == nil {
		return errors.New("vayumail: not started")
	}
	if domain == "" {
		domain = e.cfg.Domain
	}
	return e.maildir.Create(domain, username)
}

// MailboxStats returns message counts for a local account.
func (e *Engine) MailboxStats(domain, username string) (MailboxStats, error) {
	if e.maildir == nil {
		return MailboxStats{}, errors.New("vayumail: not started")
	}
	if domain == "" {
		domain = e.cfg.Domain
	}
	return e.maildir.Stats(domain, username)
}

// PlannedRecords lists the DNS records the operator should publish.
func (e *Engine) PlannedRecords() []DNSRecord {
	dkimName, dkimTXT := "", ""
	if e.dkim != nil {
		dkimName, dkimTXT = e.dkim.RecordName(), e.dkim.PublicTXT()
	}
	return PlannedRecords(e.cfg, dkimName, dkimTXT)
}

// Health runs live DNS health checks for the configured domain.
func (e *Engine) Health(ctx context.Context) *DomainHealth {
	dkimName := e.cfg.DKIMSelector + "._domainkey." + e.cfg.Domain
	return CheckHealth(ctx, e.cfg, dkimName)
}

// QueueStatus returns outbound queue counters.
func (e *Engine) QueueStatus(ctx context.Context) (*QueueStatus, *SMTPStats, error) {
	if e.queue == nil {
		return &QueueStatus{CheckedAt: time.Now().UTC()}, &SMTPStats{}, nil
	}
	return e.queue.Status(ctx)
}

// SendMail assembles an RFC 5322 message, applies PGP (encrypt when a recipient
// key is known), DKIM-signs it, and enqueues it for delivery. senderUserID is
// used for PGP signing/encryption context; pass "" to skip PGP.
func (e *Engine) SendMail(ctx context.Context, from string, to []string, subject, htmlBody, textBody, senderUserID string) (int64, error) {
	if e.queue == nil || e.dkim == nil {
		return 0, errors.New("vayumail: not started")
	}
	if len(to) == 0 {
		return 0, errors.New("vayumail: no recipients")
	}

	body := textBody
	contentType := "text/plain; charset=utf-8"
	pgpApplied := false

	// PGP: encrypt to a single known recipient when possible (privacy by default).
	if e.bridge != nil && len(to) == 1 {
		if ct, ok := e.bridge.EncryptForRecipient([]byte(textBody), to[0]); ok && len(ct) > 0 {
			body = string(ct)
			contentType = "text/plain; charset=utf-8" // inline PGP/ASCII-armored
			pgpApplied = true
		}
	}
	if !pgpApplied && htmlBody != "" {
		body = htmlBody
		contentType = "text/html; charset=utf-8"
	}

	headers := []HeaderField{
		{Key: "From", Value: from},
		{Key: "To", Value: strings.Join(to, ", ")},
		{Key: "Subject", Value: subject},
		{Key: "Date", Value: time.Now().UTC().Format(time.RFC1123Z)},
		{Key: "Message-ID", Value: e.messageID()},
		{Key: "MIME-Version", Value: "1.0"},
		{Key: "Content-Type", Value: contentType},
	}
	if pgpApplied {
		headers = append(headers, HeaderField{Key: "X-VayuPGP", Value: "encrypted"})
	}

	dkimHeader, err := e.dkim.Sign(headers, []byte(body))
	if err != nil {
		return 0, fmt.Errorf("vayumail: dkim sign: %w", err)
	}

	var raw strings.Builder
	raw.WriteString(dkimHeader)
	raw.WriteString("\r\n")
	for _, h := range headers {
		raw.WriteString(h.Key)
		raw.WriteString(": ")
		raw.WriteString(h.Value)
		raw.WriteString("\r\n")
	}
	raw.WriteString("\r\n")
	raw.WriteString(body)

	return e.queue.Enqueue(ctx, from, to, []byte(raw.String()))
}

func (e *Engine) messageID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "<" + hex.EncodeToString(b) + "@" + e.cfg.Domain + ">"
}
