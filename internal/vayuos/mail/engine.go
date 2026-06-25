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
	cfg      Config
	bridge   Bridge
	db       *sql.DB
	dkim     *DKIM
	queue    *Queue
	maildir  *Maildir
	accounts *AccountStore
	smtpd    *SMTPServer
	imapd    *IMAPServer
	decrypt  DecryptHook
	done     chan struct{}
}

// Accounts returns the admin-managed mail account store (nil until Start).
func (e *Engine) Accounts() *AccountStore { return e.accounts }

// Folders returns the standard mailbox folder names.
func (e *Engine) Folders() []string { return StandardFolders }

// ListFolder returns the messages in a folder for a local account.
func (e *Engine) ListFolder(username, folder string) ([]StoredMessage, error) {
	if e.maildir == nil {
		return nil, errors.New("vayumail: not started")
	}
	return e.maildir.ListFolder(e.cfg.Domain, username, folder)
}

// ReadFolderMessage returns a message from a folder, PGP-decrypted if possible.
func (e *Engine) ReadFolderMessage(username, folder, id string) ([]byte, error) {
	if e.maildir == nil {
		return nil, errors.New("vayumail: not started")
	}
	raw, err := e.maildir.ReadRawFolder(e.cfg.Domain, username, folder, id)
	if err != nil {
		return nil, err
	}
	if e.decrypt != nil {
		raw = e.decrypt(username+"@"+e.cfg.Domain, raw)
	}
	return raw, nil
}

// Search runs a bounded, fully-local full-text search across an account's
// folders (no external index).
func (e *Engine) Search(username, q string, limit int) ([]SearchResult, error) {
	if e.maildir == nil {
		return nil, errors.New("vayumail: not started")
	}
	return e.maildir.Search(e.cfg.Domain, username, q, limit)
}

// MoveMessage moves a message between folders (e.g. mark as Junk, or Trash).
func (e *Engine) MoveMessage(username, id, from, to string) error {
	if e.maildir == nil {
		return errors.New("vayumail: not started")
	}
	return e.maildir.MoveBetween(e.cfg.Domain, username, id, from, to)
}

// DeleteMessage permanently removes a message from a folder.
func (e *Engine) DeleteMessage(username, folder, id string) error {
	if e.maildir == nil {
		return errors.New("vayumail: not started")
	}
	return e.maildir.deleteMessage(e.cfg.Domain, username, folder, id)
}

// Compose assembles, DKIM-signs, queues an outgoing message and files a copy in
// the sender's Sent folder. senderUserID is the PGP context (may be "").
func (e *Engine) Compose(ctx context.Context, from string, to []string, subject, body, senderUserID string) (int64, error) {
	id, err := e.SendMail(ctx, from, to, subject, "", body, senderUserID)
	if err != nil {
		return 0, err
	}
	// File a plain copy in the sender's Sent folder (best-effort).
	if e.maildir != nil {
		local := from
		if i := strings.Index(local, "@"); i >= 0 {
			local = local[:i]
		}
		sent := "From: " + from + "\r\nTo: " + strings.Join(to, ", ") + "\r\nSubject: " + subject +
			"\r\nDate: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n\r\n" + body + "\r\n"
		_, _ = e.maildir.DeliverTo(e.cfg.Domain, local, "Sent", []byte(sent))
	}
	return id, nil
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

	// Admin-managed mail accounts (email + password).
	if as, aerr := NewAccountStore(e.db); aerr == nil {
		e.accounts = as
	} else {
		return fmt.Errorf("vayumail: accounts init: %w", aerr)
	}

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
	rawMsg := []byte(raw.String())

	// Split recipients into local mailboxes (delivered straight into the
	// Maildir, so they appear in the recipient's Inbox) and remote addresses
	// (relayed out via the MX queue). Without this loopback, mail sent to a
	// local account would only ever be queued for external delivery and would
	// never land in the recipient's Inbox on this instance.
	local, remote := e.splitLocalRecipients(to)
	for _, rcpt := range local {
		if _, derr := e.DeliverInbound(rcpt, rawMsg); derr != nil {
			return 0, fmt.Errorf("vayumail: local delivery to %s: %w", rcpt, derr)
		}
	}
	if len(remote) == 0 {
		// Purely local delivery — nothing to relay. Report success with no
		// queue id (the message is already in the recipient's Maildir).
		return 0, nil
	}
	return e.queue.Enqueue(ctx, from, remote, rawMsg)
}

// splitLocalRecipients partitions recipients into those served by this instance
// (delivered locally) and those that must be relayed out. When no bridge is
// wired it falls back to a domain-only check against the configured domain,
// matching the inbound SMTP server's relay policy.
func (e *Engine) splitLocalRecipients(to []string) (local, remote []string) {
	for _, addr := range to {
		if e.isLocalRecipient(addr) {
			local = append(local, addr)
		} else {
			remote = append(remote, addr)
		}
	}
	return local, remote
}

// isLocalRecipient reports whether addr is a mailbox on this instance. The
// recipient domain must match the configured domain; account existence is then
// confirmed through the bridge (CMS user or admin-managed mail account).
func (e *Engine) isLocalRecipient(addr string) bool {
	_, domain := splitAddress(addr)
	if domain == "" || !strings.EqualFold(domain, e.cfg.Domain) {
		return false
	}
	if e.bridge != nil {
		return e.bridge.IsLocalRecipient(addr)
	}
	return true
}

func (e *Engine) messageID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "<" + hex.EncodeToString(b) + "@" + e.cfg.Domain + ">"
}
