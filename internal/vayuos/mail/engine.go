package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// Engine is the VayuMail runtime: DKIM signer + outbound queue + Maildir store,
// wired to VayuPress core through the Bridge.
type Engine struct {
	cfg        Config
	bridge     Bridge
	db         *sql.DB
	dkim       *DKIM
	queue      *Queue
	maildir    *Maildir
	accounts   *AccountStore
	smtpd      *SMTPServer
	imapd      *IMAPServer
	submitd    *SMTPServer  // authenticated submission (587)
	imapsd     *IMAPServer  // implicit-TLS IMAPS (993)
	pop3d      *POP3Server  // POP3 (110, STLS)
	pop3sd     *POP3Server  // implicit-TLS POP3S (995)
	uids       *UIDStore    // persistent IMAP UID/UIDVALIDITY
	tlsConf    *tls.Config  // shared STARTTLS / implicit-TLS config
	tlsProv    *tlsProvider // provenance/diagnostics for tlsConf
	acmeHTTP   *http.Server // ACME HTTP-01 challenge responder (ACME mode only)
	acmeErr    error        // ACME HTTP-01 listener bind error (e.g. :80 in use)
	decrypt    DecryptHook
	inboundErr error
	done       chan struct{}
}

// ACMEChallengeError returns the reason the ACME HTTP-01 challenge responder
// could not start (most often: port 80 is already held by a reverse proxy such
// as nginx), or "" when ACME is not in use or the responder is healthy. When
// non-empty in ACME mode it means a trusted certificate cannot be issued/renewed
// until the operator frees port 80 or forwards the challenge — so mail clients
// keep getting the self-signed fallback.
func (e *Engine) ACMEChallengeError() string {
	if e.acmeErr == nil {
		return ""
	}
	return e.acmeErr.Error()
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

// MarkRead flags a message as read (Maildir Seen) within a folder, returning
// its new id.
func (e *Engine) MarkRead(username, folder, id string) (string, error) {
	if e.maildir == nil {
		return id, errors.New("vayumail: not started")
	}
	return e.maildir.markSeenFolder(e.cfg.Domain, username, folder, id)
}

// MarkUnread clears the read flag, returning the message's new id.
func (e *Engine) MarkUnread(username, folder, id string) (string, error) {
	if e.maildir == nil {
		return id, errors.New("vayumail: not started")
	}
	return e.maildir.markUnseenFolder(e.cfg.Domain, username, folder, id)
}

// MailboxUsage returns the total bytes a mailbox occupies across all folders,
// for quota display in the admin panel.
func (e *Engine) MailboxUsage(email string) int64 {
	if e.maildir == nil {
		return 0
	}
	local, domain := splitAddress(email)
	if domain == "" {
		domain = e.cfg.Domain
	}
	if local == "" {
		return 0
	}
	return e.maildir.AccountSize(domain, local)
}

// MailboxQuota returns an account's storage limit in bytes (0 = unlimited).
func (e *Engine) MailboxQuota(email string) int64 {
	if e.accounts == nil {
		return 0
	}
	return e.accounts.QuotaFor(context.Background(), email)
}

// MailboxOverQuota reports whether a mailbox has reached or exceeded its storage
// quota — used to block sending/draft-saving (both file a copy into the
// mailbox) once it is full. Always false when no quota is set (0 = unlimited).
func (e *Engine) MailboxOverQuota(email string) bool {
	q := e.MailboxQuota(email)
	if q <= 0 {
		return false
	}
	return e.MailboxUsage(email) >= q
}

// SetPinned flags (or unflags) a message with the Maildir 'F' flag, surfaced in
// the panel as "pinned", returning the message's new id.
func (e *Engine) SetPinned(username, folder, id string, pinned bool) (string, error) {
	if e.maildir == nil {
		return id, errors.New("vayumail: not started")
	}
	return e.maildir.setFlagFolder(e.cfg.Domain, username, folder, id, 'F', pinned)
}

// SaveDraft files a composed message into the sender's Drafts folder and
// returns its id, so it can be reopened in the composer and finished later.
func (e *Engine) SaveDraft(from string, to []string, subject, body string) (string, error) {
	if e.maildir == nil {
		return "", errors.New("vayumail: not started")
	}
	local, _ := splitAddress(from)
	raw := "From: " + from + "\r\nTo: " + strings.Join(to, ", ") + "\r\nSubject: " + subject +
		"\r\nDate: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n\r\n" + body + "\r\n"
	return e.maildir.DeliverTo(e.cfg.Domain, local, "Drafts", []byte(raw))
}

// Deliverability runs the live spam-prevention self-checks (DKIM published-key
// vs signing-key, and reverse DNS/PTR vs hostname).
func (e *Engine) Deliverability(ctx context.Context) []RecordHealth {
	dkimName, dkimTXT := "", ""
	if e.dkim != nil {
		dkimName, dkimTXT = e.dkim.RecordName(), e.dkim.PublicTXT()
	}
	return Deliverability(ctx, e.cfg, dkimName, dkimTXT)
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
		// splitAddress tolerates a `"Name" <addr>` From, so the Sent copy is
		// filed under the sender's bare local part, not the display name.
		local, _ := splitAddress(from)
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
	// Outbound transport: an authenticated smarthost relay when configured
	// (the relay's IP reputation carries deliverability), otherwise sovereign
	// direct-to-MX. DKIM signing happens before the queue either way.
	var deliver DeliverFunc
	if e.cfg.RelayEnabled() {
		deliver = NewRelayDeliverer(e.cfg, e.cfg.Hostname, e.cfg.DeliveryTimeout)
	} else {
		deliver = NewMXDeliverer(e.cfg.Hostname, e.cfg.DeliveryTimeout)
	}
	q, err := NewQueue(e.db, e.cfg, deliver)
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

	// Persistent IMAP UID / UIDVALIDITY store (so real clients sync incrementally
	// instead of re-downloading on every reconnect).
	if us, uerr := NewUIDStore(e.db); uerr == nil {
		e.uids = us
	} else {
		return fmt.Errorf("vayumail: uid store init: %w", uerr)
	}

	// Inbound receive side. Enabled by default so a configured domain can
	// receive external mail; disabled with VAYUOS_MAIL_INBOUND=off. Binding the
	// mail ports is best-effort — a bind failure (e.g. :25 without privileges,
	// or a port already in use) is recorded and surfaced, but never fails engine
	// startup, so outbound delivery and local loopback delivery stay available.
	if e.cfg.InboundEnabled {
		// Best-effort TLS for STARTTLS (SMTP/submission/IMAP) + implicit IMAPS.
		// buildTLSProvider selects, in priority order: an operator-supplied
		// certificate, native ACME auto-provisioning, then a self-signed
		// fallback. Only the first two are trusted by real mail clients; the
		// engine surfaces the active mode so the panel can warn when mobile
		// apps (the Gmail app, Apple Mail) would reject the connection.
		if tp, terr := buildTLSProvider(e.cfg); terr == nil {
			e.tlsProv = tp
			e.tlsConf = tp.config
			// In ACME mode, serve the HTTP-01 challenge responder and kick off
			// issuance in the background so the trusted certificate is cached
			// before the first client connects.
			if tp.mode == tlsModeACME {
				e.startACMEChallengeServer(tp)
				tp.warmUp(ctx)
			}
		} else {
			e.inboundErr = fmt.Errorf("tls: %w", terr)
		}

		smtpd := NewSMTPServer(e.cfg, e.inboundDeliver).WithTLS(e.tlsConf)
		if err := smtpd.Start(ctx); err != nil {
			e.inboundErr = errors.Join(e.inboundErr, fmt.Errorf("smtp receive: %w", err))
		} else {
			e.smtpd = smtpd
		}

		imapd := NewIMAPServer(e.cfg, e.bridge, e.maildir, e.decrypt).WithTLS(e.tlsConf).WithUIDStore(e.uids)
		if err := imapd.Start(ctx); err != nil {
			e.inboundErr = errors.Join(e.inboundErr, fmt.Errorf("imap: %w", err))
		} else {
			e.imapd = imapd
		}

		// POP3 (110) with STLS when TLS is available. Best-effort, never fatal.
		pop3d := NewPOP3Server(e.cfg, e.bridge, e.maildir, e.decrypt).WithTLS(e.tlsConf)
		if err := pop3d.Start(ctx); err != nil {
			e.inboundErr = errors.Join(e.inboundErr, fmt.Errorf("pop3: %w", err))
		} else {
			e.pop3d = pop3d
		}

		// Implicit-TLS IMAPS (993) and authenticated submission (587) require a
		// TLS config; all three below are best-effort and never block startup,
		// but a failed bind is now recorded in inboundErr (rather than silently
		// dropped) so the panel and logs can explain why a client can't connect.
		if e.tlsConf != nil {
			imapsd := NewIMAPServer(e.cfg, e.bridge, e.maildir, e.decrypt).WithImplicitTLS(e.tlsConf, e.cfg.IMAPSListen).WithUIDStore(e.uids)
			if err := imapsd.Start(ctx); err != nil {
				e.inboundErr = errors.Join(e.inboundErr, fmt.Errorf("imaps (993): %w", err))
			} else {
				e.imapsd = imapsd
			}
			// Implicit-TLS POP3S (995).
			pop3sd := NewPOP3Server(e.cfg, e.bridge, e.maildir, e.decrypt).WithImplicitTLS(e.tlsConf, e.cfg.POP3SListen)
			if err := pop3sd.Start(ctx); err != nil {
				e.inboundErr = errors.Join(e.inboundErr, fmt.Errorf("pop3s (995): %w", err))
			} else {
				e.pop3sd = pop3sd
			}
			if e.bridge != nil {
				submitd := NewSubmissionServer(e.cfg, e.tlsConf, e.bridge.AuthUser, e.relayOutbound)
				if err := submitd.Start(ctx); err != nil {
					e.inboundErr = errors.Join(e.inboundErr, fmt.Errorf("submission (587): %w", err))
				} else {
					e.submitd = submitd
				}
			}
		}
	}
	return nil
}

// inboundDeliver files each recipient's copy of a received message locally.
func (e *Engine) inboundDeliver(_ string, rcpts []string, raw []byte) error {
	var firstErr error
	for _, rcpt := range rcpts {
		if _, derr := e.DeliverInbound(rcpt, raw); derr != nil && firstErr == nil {
			firstErr = derr
		}
	}
	return firstErr
}

// relayOutbound enqueues an authenticated submission for MX delivery. The
// envelope sender is reduced to a bare address.
func (e *Engine) relayOutbound(from string, rcpts []string, raw []byte) error {
	if e.queue == nil {
		return errors.New("vayumail: queue unavailable")
	}
	_, err := e.queue.Enqueue(context.Background(), envelopeAddress(from), rcpts, raw)
	return err
}

// InboundActive reports whether the inbound SMTP receive listener is running.
func (e *Engine) InboundActive() bool { return e.smtpd != nil }

// TLSActive reports whether STARTTLS/implicit-TLS is available to the listeners.
func (e *Engine) TLSActive() bool { return e.tlsConf != nil }

// TLSMode reports the provenance of the certificate the mail listeners present:
// "static" (operator-provided), "acme" (auto-provisioned), "selfsigned" (the
// in-memory fallback), or "none" when TLS is unavailable.
func (e *Engine) TLSMode() string {
	if e.tlsProv == nil {
		return string(tlsModeNone)
	}
	return string(e.tlsProv.mode)
}

// TLSTrusted reports whether the active certificate is one mainstream mail
// clients (the Gmail app, Apple Mail, Thunderbird, Outlook) will accept without
// a manual exception. A false value is the usual cause of a client's "Couldn't
// open connection to server" — the ports are up, but the certificate is the
// self-signed fallback that mobile apps reject.
func (e *Engine) TLSTrusted() bool { return e.tlsProv != nil && e.tlsProv.trusted() }

// TLSNote returns a short human-readable explanation of the active TLS mode,
// for display in the operator panel.
func (e *Engine) TLSNote() string {
	if e.tlsProv == nil {
		return "TLS not initialised"
	}
	return e.tlsProv.note
}

// TLSCertHosts returns the DNS names the served leaf certificate is valid for.
// Empty when TLS is unavailable or the certificate can't be parsed. Used to warn
// when the certificate doesn't cover the hostname clients are told to connect to
// — a silent failure on strict mobile apps (the Gmail app, which validates from
// Google's servers, and Thunderbird for Android), while desktop clients let the
// user click through the mismatch.
func (e *Engine) TLSCertHosts() []string {
	if e.tlsConf == nil {
		return nil
	}
	var cert *tls.Certificate
	if e.tlsConf.GetCertificate != nil {
		if c, err := e.tlsConf.GetCertificate(&tls.ClientHelloInfo{ServerName: e.cfg.Hostname}); err == nil {
			cert = c
		}
	}
	if cert == nil && len(e.tlsConf.Certificates) > 0 {
		cert = &e.tlsConf.Certificates[0]
	}
	if cert == nil || len(cert.Certificate) == 0 {
		return nil
	}
	leaf := cert.Leaf
	if leaf == nil {
		parsed, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return nil
		}
		leaf = parsed
	}
	return leaf.DNSNames
}

// TLSCertCovers reports whether the served certificate is valid for host,
// honouring a single leading "*." wildcard (RFC 6125), case-insensitively.
func (e *Engine) TLSCertCovers(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return false
	}
	for _, n := range e.TLSCertHosts() {
		n = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(n), "."))
		if n == host {
			return true
		}
		if strings.HasPrefix(n, "*.") {
			// "*.example.com" matches one label to the left (foo.example.com).
			if suffix := n[1:]; strings.HasSuffix(host, suffix) {
				label := host[:len(host)-len(suffix)]
				if label != "" && !strings.Contains(label, ".") {
					return true
				}
			}
		}
	}
	return false
}

// startACMEChallengeServer serves the ACME HTTP-01 challenge responder on
// cfg.ACMEHTTPAddr (default :80). Issuance and renewal depend on it being
// reachable from the public internet on port 80 for the mail hostname. Binding
// is best-effort: a failure (e.g. :80 already held by a reverse proxy) is
// recorded in inboundErr with remediation guidance, never fatal.
func (e *Engine) startACMEChallengeServer(tp *tlsProvider) {
	if tp == nil || tp.httpHandler == nil {
		return
	}
	addr := e.cfg.ACMEHTTPAddr
	if addr == "" {
		addr = ":80"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		e.acmeErr = fmt.Errorf("could not bind %s for the ACME HTTP-01 challenge: %v", addr, err)
		e.inboundErr = errors.Join(e.inboundErr, fmt.Errorf(
			"acme http-01 listener %s (a trusted certificate cannot be issued until port 80 is reachable for %s — free the port, point a reverse proxy's /.well-known/acme-challenge/ at it, or set VAYUOS_MAIL_ACME_HTTP_ADDR): %w",
			addr, e.cfg.Hostname, err))
		return
	}
	srv := &http.Server{Handler: tp.httpHandler, ReadHeaderTimeout: 10 * time.Second}
	e.acmeHTTP = srv
	go func() { _ = srv.Serve(ln) }()
}

// SubmissionActive reports whether the authenticated submission (587) listener
// is running.
func (e *Engine) SubmissionActive() bool { return e.submitd != nil }

// IMAPSActive reports whether the implicit-TLS IMAPS (993) listener is running.
func (e *Engine) IMAPSActive() bool { return e.imapsd != nil }

// IMAPActive reports whether the plaintext/STARTTLS IMAP (143) listener is running.
func (e *Engine) IMAPActive() bool { return e.imapd != nil }

// POP3Active reports whether the POP3 (110) listener is running.
func (e *Engine) POP3Active() bool { return e.pop3d != nil }

// POP3SActive reports whether the implicit-TLS POP3S (995) listener is running.
func (e *Engine) POP3SActive() bool { return e.pop3sd != nil }

// InboundError returns the reason the inbound listeners could not start, or nil
// when inbound is disabled or running. It lets the panel explain a failed bind
// (e.g. ":25 without privileges") without taking down outbound mail.
func (e *Engine) InboundError() error { return e.inboundErr }

// Stop halts the retry worker.
func (e *Engine) Stop(_ context.Context) error {
	if e.acmeHTTP != nil {
		_ = e.acmeHTTP.Close()
	}
	if e.smtpd != nil {
		_ = e.smtpd.Stop(context.Background())
	}
	if e.submitd != nil {
		_ = e.submitd.Stop(context.Background())
	}
	if e.imapd != nil {
		_ = e.imapd.Stop(context.Background())
	}
	if e.imapsd != nil {
		_ = e.imapsd.Stop(context.Background())
	}
	if e.pop3d != nil {
		_ = e.pop3d.Stop(context.Background())
	}
	if e.pop3sd != nil {
		_ = e.pop3sd.Stop(context.Background())
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
	return e.sendMail(ctx, from, to, subject, htmlBody, textBody, senderUserID, true)
}

// SendSystemMail sends transactional / system mail — sign-in (magic) links,
// welcome, newsletter confirmations, comment and payment notices. Unlike
// SendMail it is NEVER PGP-encrypted: the recipient must be able to READ the
// link even when they have a published PGP key (otherwise the message arrives
// as an unreadable "-----BEGIN PGP MESSAGE-----" blob). It is still DKIM-signed,
// queued for durable delivery, and local-loopback aware.
func (e *Engine) SendSystemMail(ctx context.Context, from string, to []string, subject, htmlBody, textBody string) (int64, error) {
	return e.sendMail(ctx, from, to, subject, htmlBody, textBody, "", false)
}

func (e *Engine) sendMail(ctx context.Context, from string, to []string, subject, htmlBody, textBody, senderUserID string, allowPGP bool) (int64, error) {
	_ = senderUserID // reserved for PGP signing context
	if e.queue == nil || e.dkim == nil {
		return 0, errors.New("vayumail: not started")
	}
	if len(to) == 0 {
		return 0, errors.New("vayumail: no recipients")
	}

	text := textBody
	html := htmlBody
	pgpApplied := false

	// PGP: encrypt to a single known recipient when possible (privacy by
	// default) — but ONLY for person-to-person mail. System/transactional mail
	// (allowPGP=false) is never encrypted so its links stay readable.
	if allowPGP && e.bridge != nil && len(to) == 1 {
		if ct, ok := e.bridge.EncryptForRecipient([]byte(textBody), to[0]); ok && len(ct) > 0 {
			text = string(ct)
			html = ""
			pgpApplied = true
		}
	}

	// Ordered RFC 5322 headers. Date and Message-ID are mandatory for inbox
	// placement; mailbox providers penalise their absence heavily.
	headers := []HeaderField{
		{Key: "From", Value: from},
		{Key: "To", Value: strings.Join(to, ", ")},
		{Key: "Subject", Value: subject},
		{Key: "Date", Value: time.Now().UTC().Format(time.RFC1123Z)},
		{Key: "Message-ID", Value: e.messageID()},
		{Key: "MIME-Version", Value: "1.0"},
	}

	var bodyBuf bytes.Buffer
	switch {
	case pgpApplied:
		headers = append(headers,
			HeaderField{Key: "Content-Type", Value: "text/plain; charset=utf-8"},
			HeaderField{Key: "Content-Transfer-Encoding", Value: "8bit"},
			HeaderField{Key: "X-VayuPGP", Value: "encrypted"},
		)
		bodyBuf.WriteString(normalizeCRLF(text))
	case html != "" && text != "":
		// Well-formed multipart/alternative (text first, HTML second) — the
		// shape every mainstream MUA sends and that spam filters expect.
		boundary := mimeBoundary()
		headers = append(headers, HeaderField{Key: "Content-Type", Value: `multipart/alternative; boundary="` + boundary + `"`})
		writeMIMEPart(&bodyBuf, boundary, "text/plain; charset=utf-8", text)
		writeMIMEPart(&bodyBuf, boundary, "text/html; charset=utf-8", html)
		bodyBuf.WriteString("--" + boundary + "--\r\n")
	case html != "":
		headers = append(headers,
			HeaderField{Key: "Content-Type", Value: "text/html; charset=utf-8"},
			HeaderField{Key: "Content-Transfer-Encoding", Value: "8bit"},
		)
		bodyBuf.WriteString(normalizeCRLF(html))
	default:
		headers = append(headers,
			HeaderField{Key: "Content-Type", Value: "text/plain; charset=utf-8"},
			HeaderField{Key: "Content-Transfer-Encoding", Value: "8bit"},
		)
		bodyBuf.WriteString(normalizeCRLF(text))
	}

	// Assemble the complete message (CRLF throughout), then DKIM-sign it as a
	// whole so the signed bytes are exactly the bytes we transmit.
	var raw bytes.Buffer
	for _, h := range headers {
		raw.WriteString(h.Key)
		raw.WriteString(": ")
		raw.WriteString(h.Value)
		raw.WriteString("\r\n")
	}
	raw.WriteString("\r\n")
	raw.Write(bodyBuf.Bytes())

	rawMsg, err := e.dkim.SignMessage(raw.Bytes())
	if err != nil {
		return 0, fmt.Errorf("vayumail: dkim sign: %w", err)
	}

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
	// The envelope sender (MAIL FROM) must be a bare address even when the
	// From: header carries a display name like `"Ankush" <a@b>`.
	return e.queue.Enqueue(ctx, envelopeAddress(from), remote, rawMsg)
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

// mimeBoundary returns a unique multipart boundary token.
func mimeBoundary() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "vayu-" + hex.EncodeToString(b)
}

// normalizeCRLF rewrites bare LF and lone CR line endings to canonical CRLF so
// the transmitted bytes match what DKIM canonicalization expects.
func normalizeCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// writeMIMEPart appends one multipart/alternative body part (CRLF-terminated).
func writeMIMEPart(buf *bytes.Buffer, boundary, contentType, content string) {
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: " + contentType + "\r\n")
	buf.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	body := normalizeCRLF(content)
	buf.WriteString(body)
	if !strings.HasSuffix(body, "\r\n") {
		buf.WriteString("\r\n")
	}
}
