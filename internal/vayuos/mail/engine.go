package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Engine is the VayuMail runtime: DKIM signer + outbound queue + Maildir store,
// wired to VayuPress core through the Bridge.
type Engine struct {
	cfg    Config
	bridge Bridge
	db     *sql.DB
	dkim   *DKIM // primary-domain signer (cfg.Domain)
	// dkimByDomain caches per-domain signers for mail_enabled secondary senders
	// (VayuDomains Stage 3c). They share the primary's private key file (keyed by
	// selector) but carry the sender domain in the signature's d= tag, so each
	// domain's outbound aligns with the DKIM record published at
	// <selector>._domainkey.<sender-domain>.
	dkimByDomain map[string]*DKIM
	dkimMu       sync.Mutex
	queue        *Queue
	maildir      *Maildir
	accounts     *AccountStore
	smtpd        *SMTPServer
	imapd        *IMAPServer
	submitd      *SMTPServer  // authenticated submission (587)
	imapsd       *IMAPServer  // implicit-TLS IMAPS (993)
	pop3d        *POP3Server  // POP3 (110, STLS)
	pop3sd       *POP3Server  // implicit-TLS POP3S (995)
	uids         *UIDStore    // persistent IMAP UID/UIDVALIDITY
	tlsConf      *tls.Config  // shared STARTTLS / implicit-TLS config
	tlsProv      *tlsProvider // provenance/diagnostics for tlsConf
	acmeHTTP     *http.Server // ACME HTTP-01 challenge responder (ACME mode only)
	acmeErr      error        // ACME HTTP-01 listener bind error (e.g. :80 in use)
	decrypt      DecryptHook
	inboundErr   error
	// retentionAudit reports a completed retention sweep to the host's
	// audit log (nil-safe; see SetRetentionAudit).
	retentionAudit func(email string, count, days int)
	// queueRetention is the operator-settable auto-clear window (days) for
	// DELIVERED outbound-queue rows; 0 = keep forever. Runtime-updatable, so it is
	// an atomic rather than a copy of cfg.QueueRetentionDays.
	queueRetention atomic.Int64
	done           chan struct{}
}

// SetQueueRetentionDays sets the auto-clear window (days) for delivered
// outbound-queue rows; 0 disables pruning. Applied on the next retention sweep.
func (e *Engine) SetQueueRetentionDays(days int) {
	if days < 0 {
		days = 0
	}
	e.queueRetention.Store(int64(days))
}

// QueueRetentionDays returns the current auto-clear window (days); 0 = off.
func (e *Engine) QueueRetentionDays() int { return int(e.queueRetention.Load()) }

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

// mailboxKey resolves a mailbox identifier — which may be a bare local part (the
// historic form, resolving to the primary domain, byte-identical) or a full
// address (VayuDomains Stage 3d: a secondary mailbox reached via webmail) — into
// its Maildir (domain, local) key. An address with an empty/omitted domain falls
// back to the primary, so every existing local-part caller is unchanged.
func (e *Engine) mailboxKey(username string) (domain, local string) {
	if i := strings.IndexByte(username, '@'); i >= 0 {
		local = username[:i]
		if d := strings.ToLower(strings.TrimSpace(username[i+1:])); d != "" {
			return d, local
		}
		return e.cfg.Domain, local
	}
	return e.cfg.Domain, username
}

// ListFolder returns the messages in a folder for a local account.
func (e *Engine) ListFolder(username, folder string) ([]StoredMessage, error) {
	if e.maildir == nil {
		return nil, errors.New("vayumail: not started")
	}
	dom, local := e.mailboxKey(username)
	return e.maildir.ListFolder(dom, local, folder)
}

// ReadFolderMessage returns a message from a folder, PGP-decrypted if possible.
func (e *Engine) ReadFolderMessage(username, folder, id string) ([]byte, error) {
	if e.maildir == nil {
		return nil, errors.New("vayumail: not started")
	}
	dom, local := e.mailboxKey(username)
	raw, err := e.maildir.ReadRawFolder(dom, local, folder, id)
	if err != nil {
		return nil, err
	}
	if e.decrypt != nil {
		raw = e.decrypt(local+"@"+dom, raw)
	}
	return raw, nil
}

// Search runs a bounded, fully-local full-text search across an account's
// folders (no external index).
func (e *Engine) Search(username, q string, limit int) ([]SearchResult, error) {
	if e.maildir == nil {
		return nil, errors.New("vayumail: not started")
	}
	dom, local := e.mailboxKey(username)
	return e.maildir.Search(dom, local, q, limit)
}

// MoveMessage moves a message between folders (e.g. mark as Junk, or Trash).
func (e *Engine) MoveMessage(username, id, from, to string) error {
	if e.maildir == nil {
		return errors.New("vayumail: not started")
	}
	dom, local := e.mailboxKey(username)
	return e.maildir.MoveBetween(dom, local, id, from, to)
}

// MarkRead flags a message as read (Maildir Seen) within a folder, returning
// its new id.
func (e *Engine) MarkRead(username, folder, id string) (string, error) {
	if e.maildir == nil {
		return id, errors.New("vayumail: not started")
	}
	dom, local := e.mailboxKey(username)
	return e.maildir.markSeenFolder(dom, local, folder, id)
}

// MarkUnread clears the read flag, returning the message's new id.
func (e *Engine) MarkUnread(username, folder, id string) (string, error) {
	if e.maildir == nil {
		return id, errors.New("vayumail: not started")
	}
	dom, local := e.mailboxKey(username)
	return e.maildir.markUnseenFolder(dom, local, folder, id)
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
	dom, local := e.mailboxKey(username)
	return e.maildir.setFlagFolder(dom, local, folder, id, 'F', pinned)
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
	return e.maildir.DeliverTo(e.senderDomain(from), local, "Drafts", []byte(raw))
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
	dom, local := e.mailboxKey(username)
	return e.maildir.deleteMessage(dom, local, folder, id)
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
		_, _ = e.maildir.DeliverTo(e.senderDomain(from), local, "Sent", []byte(sent))
	}
	return id, nil
}

// Attachment is a file attached to an outgoing message.
type Attachment struct {
	Filename    string
	ContentType string // defaults to application/octet-stream
	Data        []byte
}

// ComposeMessage carries the fields of a rich outgoing message.
type ComposeMessage struct {
	From        string
	To, CC, BCC []string
	ReplyTo     string
	Subject     string
	Body        string
	// HTML is an OPTIONAL alternative rendering of Body. When set, the message is
	// assembled as multipart/alternative with the plain text FIRST — the order every
	// mainstream client sends and spam filters expect, and the order that makes the
	// text part the fallback rather than an afterthought.
	//
	// Body stays authoritative: HTML is derived from it upstream, so the two cannot
	// describe different messages. Leave HTML empty and the assembly below is
	// byte-identical to before.
	HTML         string
	Attachments  []Attachment
	SenderUserID string
	// Encrypt opts this message into PGP encryption. Encryption is applied only
	// when the operator asks for it AND the message is a single-recipient,
	// no-attachment, no-Cc/Bcc message AND a recipient key is known. It is off by
	// default so a plain message is delivered as readable text (encrypting to a
	// recipient that cannot decrypt — e.g. a plain Gmail address — would arrive as
	// an unreadable ciphertext block and score as spam).
	Encrypt bool
}

// ComposeRich sends a message with optional Cc/Bcc/Reply-To and file
// attachments, then files a copy in the sender's Sent folder. Bcc recipients
// receive the mail but never appear in the headers. When attachments are present
// the message is assembled as multipart/mixed, and when m.HTML is set the two
// bodies form a multipart/alternative (nested inside the mixed part when both
// apply); PGP auto-encryption is applied
// only for a single-recipient, no-attachment, no-Cc/Bcc message (encrypting a
// multipart body is out of scope for the composer). DKIM signing, local loopback
// delivery and MX queueing match the plain send path.
func (e *Engine) ComposeRich(ctx context.Context, m ComposeMessage) (int64, error) {
	if e.queue == nil || e.dkim == nil {
		return 0, errors.New("vayumail: not started")
	}
	all := make([]string, 0, len(m.To)+len(m.CC)+len(m.BCC))
	all = append(all, m.To...)
	all = append(all, m.CC...)
	all = append(all, m.BCC...)
	if len(all) == 0 {
		return 0, errors.New("vayumail: no recipients")
	}

	// Build the content entity — the MIME entity carrying the actual message: a
	// text/plain body, or a multipart/mixed of the text plus attachments. entHead
	// is its own Content-* header block (CRLF-separated, no trailing CRLF), entBody
	// the body bytes. This entity is sent as-is, or PGP/MIME-encrypted as a whole.
	var entHead string
	var entBody []byte
	withHTML := strings.TrimSpace(m.HTML) != ""
	switch {
	case len(m.Attachments) > 0:
		boundary := mimeBoundary()
		var b bytes.Buffer
		if withHTML {
			// mixed[ alternative[text, html], attachments... ] — the alternative
			// nests INSIDE the mixed part. Putting the two bodies as siblings of the
			// attachments would tell the client they are two separate documents, and
			// it would show both.
			b.WriteString("--" + boundary + "\r\n")
			b.Write(alternativeEntity(m.Body, m.HTML))
		} else {
			writeMIMEPart(&b, boundary, "text/plain; charset=utf-8", m.Body)
		}
		for _, at := range m.Attachments {
			writeAttachmentPart(&b, boundary, at)
		}
		b.WriteString("--" + boundary + "--\r\n")
		entHead = `Content-Type: multipart/mixed; boundary="` + boundary + `"`
		entBody = b.Bytes()
	case withHTML:
		alt := alternativeEntity(m.Body, m.HTML)
		// Split the entity's own header block off: at top level it continues the
		// message headers rather than being written as a nested part.
		if i := bytes.Index(alt, []byte("\r\n\r\n")); i > 0 {
			entHead = string(alt[:i])
			entBody = alt[i+4:]
		}
	default:
		entHead = "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit"
		entBody = []byte(normalizeCRLF(m.Body))
	}

	headers := []HeaderField{
		{Key: "From", Value: m.From},
		{Key: "To", Value: strings.Join(m.To, ", ")},
	}
	if len(m.CC) > 0 {
		headers = append(headers, HeaderField{Key: "Cc", Value: strings.Join(m.CC, ", ")})
	}
	if strings.TrimSpace(m.ReplyTo) != "" {
		headers = append(headers, HeaderField{Key: "Reply-To", Value: strings.TrimSpace(m.ReplyTo)})
	}
	headers = append(headers,
		HeaderField{Key: "Subject", Value: m.Subject},
		HeaderField{Key: "Date", Value: time.Now().UTC().Format(time.RFC1123Z)},
		HeaderField{Key: "Message-ID", Value: e.messageID(e.senderDomain(m.From))},
		HeaderField{Key: "MIME-Version", Value: "1.0"},
	)

	var bodyBuf bytes.Buffer
	// PGP/MIME (RFC 3156), opt-in via m.Encrypt. Unlike the old inline-PGP path this
	// encrypts the WHOLE content entity — body AND attachments — to EVERY recipient
	// (To/Cc/Bcc) plus the sender (so the Sent copy stays readable). We encrypt only
	// when every recipient has a resolvable key: a message a recipient can't read is
	// worse than an honestly-plaintext one, so otherwise we fall back to plaintext
	// (exactly as a missing key did before).
	encrypted := false
	if m.Encrypt && e.bridge != nil {
		var inner bytes.Buffer
		inner.WriteString(entHead)
		inner.WriteString("\r\n\r\n")
		inner.Write(entBody)
		recips := append(append([]string{}, all...), m.From)
		if ct, missing, ok := e.bridge.EncryptForRecipients(inner.Bytes(), recips); ok && !anyRecipientMissing(all, missing) {
			boundary := mimeBoundary()
			headers = append(headers,
				HeaderField{Key: "Content-Type", Value: `multipart/encrypted; protocol="application/pgp-encrypted"; boundary="` + boundary + `"`},
				HeaderField{Key: "X-VayuPGP", Value: "mime"},
			)
			bodyBuf.WriteString("--" + boundary + "\r\n")
			bodyBuf.WriteString("Content-Type: application/pgp-encrypted\r\n")
			bodyBuf.WriteString("Content-Description: PGP/MIME version identification\r\n\r\n")
			bodyBuf.WriteString("Version: 1\r\n\r\n")
			bodyBuf.WriteString("--" + boundary + "\r\n")
			bodyBuf.WriteString("Content-Type: application/octet-stream; name=\"encrypted.asc\"\r\n")
			bodyBuf.WriteString("Content-Description: OpenPGP encrypted message\r\n")
			bodyBuf.WriteString("Content-Disposition: inline; filename=\"encrypted.asc\"\r\n\r\n")
			bodyBuf.Write(ct)
			if !bytes.HasSuffix(ct, []byte("\n")) {
				bodyBuf.WriteString("\r\n")
			}
			bodyBuf.WriteString("--" + boundary + "--\r\n")
			encrypted = true
		}
	}
	if !encrypted {
		// Plaintext: the content entity's own headers continue the top-level block.
		for _, ln := range strings.Split(entHead, "\r\n") {
			if i := strings.Index(ln, ": "); i > 0 {
				headers = append(headers, HeaderField{Key: ln[:i], Value: ln[i+2:]})
			}
		}
		bodyBuf.Write(entBody)
	}

	var raw bytes.Buffer
	for _, h := range headers {
		raw.WriteString(h.Key)
		raw.WriteString(": ")
		raw.WriteString(h.Value)
		raw.WriteString("\r\n")
	}
	raw.WriteString("\r\n")
	raw.Write(bodyBuf.Bytes())

	rawMsg, err := e.dkimFor(e.senderDomain(m.From)).SignMessage(raw.Bytes())
	if err != nil {
		return 0, fmt.Errorf("vayumail: dkim sign: %w", err)
	}

	// File the exact assembled message in the sender's Sent folder (best-effort)
	// so attachments, Cc and the real body are preserved there — under the
	// sender's own domain so a secondary sender's Sent copy stays isolated.
	if e.maildir != nil {
		if local, _ := splitAddress(m.From); local != "" {
			_, _ = e.maildir.DeliverTo(e.senderDomain(m.From), local, "Sent", rawMsg)
		}
	}

	localRcpt, remoteRcpt := e.splitLocalRecipients(all)
	for _, rcpt := range localRcpt {
		if _, derr := e.DeliverInbound(rcpt, rawMsg); derr != nil {
			return 0, fmt.Errorf("vayumail: local delivery to %s: %w", rcpt, derr)
		}
	}
	if len(remoteRcpt) == 0 {
		return 0, nil
	}
	return e.queue.Enqueue(ctx, envelopeAddress(m.From), remoteRcpt, rawMsg)
}

// anyRecipientMissing reports whether any address in required appears in the
// missing list (case-insensitively) — i.e. a real recipient has no PGP key, so
// the message must not be encrypted (they couldn't read it).
func anyRecipientMissing(required, missing []string) bool {
	if len(missing) == 0 {
		return false
	}
	ms := make(map[string]bool, len(missing))
	for _, m := range missing {
		ms[strings.ToLower(strings.TrimSpace(m))] = true
	}
	for _, r := range required {
		if ms[strings.ToLower(strings.TrimSpace(r))] {
			return true
		}
	}
	return false
}

// writeAttachmentPart appends one base64 MIME attachment part (RFC 2045).
func writeAttachmentPart(buf *bytes.Buffer, boundary string, at Attachment) {
	ct := strings.TrimSpace(at.ContentType)
	if ct == "" {
		ct = "application/octet-stream"
	}
	name := mimeSanitizeFilename(at.Filename)
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: " + ct + "; name=\"" + name + "\"\r\n")
	buf.WriteString("Content-Transfer-Encoding: base64\r\n")
	buf.WriteString("Content-Disposition: attachment; filename=\"" + name + "\"\r\n\r\n")
	enc := base64.StdEncoding.EncodeToString(at.Data)
	for i := 0; i < len(enc); i += 76 { // wrap at 76 columns
		end := i + 76
		if end > len(enc) {
			end = len(enc)
		}
		buf.WriteString(enc[i:end])
		buf.WriteString("\r\n")
	}
}

// mimeSanitizeFilename strips characters that would break a quoted MIME filename
// or enable header injection.
func mimeSanitizeFilename(s string) string {
	s = strings.NewReplacer("\"", "", "\r", "", "\n", "", "\\", "").Replace(s)
	s = strings.TrimSpace(s)
	if s == "" {
		return "attachment"
	}
	return s
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
	e := &Engine{cfg: *cfg, bridge: bridge, db: db, done: make(chan struct{})}
	e.queueRetention.Store(int64(cfg.QueueRetentionDays))
	return e
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
	switch {
	case e.cfg.LocalOnly:
		// Tor Space: never dial out; external recipients are bounced (ADR-0141).
		deliver = NewLocalOnlyDeliverer()
	case e.cfg.RelayEnabled():
		deliver = NewRelayDeliverer(e.cfg, e.cfg.Hostname, e.cfg.DeliveryTimeout)
	default:
		deliver = NewMXDeliverer(e.cfg.Hostname, e.cfg.DeliveryTimeout)
	}
	q, err := NewQueue(e.db, e.cfg, deliver)
	if err != nil {
		return fmt.Errorf("vayumail: queue init: %w", err)
	}
	e.queue = q
	go e.worker()
	go e.retentionLoop()
	// Snooze wake loop: resurfaces due snoozed messages (see snooze.go).
	go e.snoozeSweeper()

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

		smtpd := NewSMTPServer(e.cfg, e.inboundDeliver).WithTLS(e.tlsConf).WithRecipientCheck(e.isLocalRecipient)
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
				submitd := NewSubmissionServer(e.cfg, e.tlsConf, e.bridge.AuthUser, e.relayOutbound).WithSenderCheck(e.submissionSenderAllowed)
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
	// DKIM-sign submitted mail (audit L7): 587/client-submitted messages (the
	// mobile app + Thunderbird path) were enqueued verbatim and unsigned, so DMARC
	// alignment rested on SPF alone and broke on any forwarding hop. Sign with the
	// sender-domain key, mirroring the webmail Compose path. Best-effort: a signing
	// failure relays the message unsigned rather than bouncing it.
	msg := raw
	if signer := e.dkimFor(e.senderDomain(from)); signer != nil {
		if out, err := signer.SignMessage(raw); err == nil && len(out) > 0 {
			msg = out
		}
		// On a signing error, fall through and relay the message unsigned rather
		// than bounce it (best-effort, matching the prior behaviour).
	}
	_, err := e.queue.Enqueue(context.Background(), envelopeAddress(from), rcpts, msg)
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

// PlannedRecordsForDomain lists the DNS records to publish for a mail_enabled
// secondary domain (VayuDomains Stage 3d). The MX points at the primary mail host
// (which serves every domain's mailboxes), while the SPF/DKIM/DMARC records are
// scoped to the secondary domain. The DKIM public key is shared across domains, so
// the secondary publishes the *same* key TXT at its own <selector>._domainkey.
func (e *Engine) PlannedRecordsForDomain(domain string) []DNSRecord {
	if domain == "" || strings.EqualFold(domain, e.cfg.Domain) {
		return e.PlannedRecords()
	}
	cfg := e.cfg // copy; keep Hostname = the primary mail host
	cfg.Domain = strings.ToLower(domain)
	dkimName, dkimTXT := "", ""
	if dk := e.dkimFor(domain); dk != nil {
		dkimName, dkimTXT = dk.RecordName(), dk.PublicTXT()
	}
	return PlannedRecords(cfg, dkimName, dkimTXT)
}

// HealthForDomain runs live DNS checks for any mail domain this install serves —
// the primary or a mail_enabled secondary — so the operator can confirm every
// domain's MX/SPF/DKIM/DMARC is aligned to this install. The DKIM key is shared
// across domains and published at each domain's own <selector>._domainkey name,
// so the check compares the domain's published key against this install's key.
func (e *Engine) HealthForDomain(ctx context.Context, domain string) *DomainHealth {
	if strings.TrimSpace(domain) == "" {
		domain = e.cfg.Domain
	}
	dkimName := e.cfg.DKIMSelector + "._domainkey." + strings.ToLower(strings.TrimSuffix(domain, "."))
	dkimTXT := ""
	if dk := e.dkimFor(domain); dk != nil {
		dkimName, dkimTXT = dk.RecordName(), dk.PublicTXT()
	}
	return CheckHealthForDomain(ctx, e.cfg, domain, dkimName, dkimTXT)
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
		{Key: "Message-ID", Value: e.messageID(e.senderDomain(from))},
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

	rawMsg, err := e.dkimFor(e.senderDomain(from)).SignMessage(raw.Bytes())
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

// submissionSenderAllowed enforces sender-login binding on the 587 submission
// path (audit M5): an authenticated user may send with their own address, an
// external (non-local) From, or an alias that delivers to their own mailbox — but
// NOT as ANOTHER local mailbox they do not own (the intra-server spoofing vector,
// e.g. an intern sending as the CEO). It fails OPEN whenever it cannot positively
// prove the From belongs to a different local user (empty/null sender, missing
// account data, or an ambiguous localpart-only login) so legitimate delivery is
// never broken — the guard only ever blocks a confidently-foreign local sender.
func (e *Engine) submissionSenderAllowed(authUser, fromAddr string) bool {
	authUser = strings.TrimSpace(authUser)
	fromAddr = strings.TrimSpace(fromAddr)
	if authUser == "" || fromAddr == "" {
		return true
	}
	if strings.EqualFold(authUser, fromAddr) {
		return true
	}
	// Only a LOCAL mailbox owned by someone else is forbidden; an external From is
	// out of scope for this intra-server guard (recipient-side SPF/DKIM covers it).
	if !e.isLocalRecipient(fromAddr) {
		return true
	}
	// Tolerate a localpart-only or differently-cased login that still names the
	// same identity (localparts equal, and any domain the login carried matches).
	al, ad := splitAddress(authUser)
	fl, fd := splitAddress(fromAddr)
	if strings.EqualFold(al, fl) && (ad == "" || strings.EqualFold(ad, fd)) {
		return true
	}
	// Allow an alias/identity that resolves to the authenticated mailbox.
	if e.accounts != nil {
		if target := e.accounts.ResolveAlias(context.Background(), fromAddr); target != "" &&
			(strings.EqualFold(target, authUser) || strings.EqualFold(target, al+"@"+fd)) {
			return true
		}
	}
	return false
}

// isLocalRecipient reports whether addr is a mailbox on this instance. The
// recipient domain must match the configured domain; the address is then an
// alias (delivering into its target mailbox) or an account confirmed through
// the bridge (CMS user or admin-managed mail account).
func (e *Engine) isLocalRecipient(addr string) bool {
	_, domain := splitAddress(addr)
	// The recipient must be on a domain this install serves — the primary, or a
	// mail_enabled secondary (VayuDomains Stage 3b). Byte-identical to the historic
	// primary-only check when no secondary mail domain is configured.
	if !e.cfg.AcceptsMailDomain(domain) {
		return false
	}
	if e.accounts != nil && e.accounts.ResolveAlias(context.Background(), addr) != "" {
		return true
	}
	if e.bridge != nil {
		return e.bridge.IsLocalRecipient(addr)
	}
	return true
}

// messageID mints a unique Message-ID whose domain part matches the sender's
// domain (VayuDomains Stage 3c) so it aligns with the From/DKIM domain. An empty
// domain falls back to the primary — byte-identical for single-domain sends.
func (e *Engine) messageID(domain string) string {
	if domain == "" {
		domain = e.cfg.Domain
	}
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "<" + hex.EncodeToString(b) + "@" + domain + ">"
}

// senderDomain extracts the outbound sender's domain from a From value, falling
// back to the primary domain (so a bare/relative From, or a primary sender,
// behaves exactly as before).
func (e *Engine) senderDomain(from string) string {
	if _, d := splitAddress(from); d != "" {
		return d
	}
	return e.cfg.Domain
}

// dkimFor returns the DKIM signer for a sender domain. The primary domain uses
// the signer loaded at Start (byte-identical); a mail_enabled secondary gets a
// signer that reuses the shared private key but carries its own domain in the d=
// tag, so its mail validates against the record published at
// <selector>._domainkey.<domain>. Falls back to the primary signer if a
// per-domain signer cannot be built, so a send is never blocked.
func (e *Engine) dkimFor(domain string) *DKIM {
	if e.dkim == nil || domain == "" || strings.EqualFold(domain, e.cfg.Domain) {
		return e.dkim
	}
	dom := strings.ToLower(domain)
	e.dkimMu.Lock()
	defer e.dkimMu.Unlock()
	if d, ok := e.dkimByDomain[dom]; ok {
		return d
	}
	d, err := LoadOrCreateDKIM(e.cfg.StorageDir, e.cfg.DKIMSelector, dom)
	if err != nil || d == nil {
		return e.dkim
	}
	if e.dkimByDomain == nil {
		e.dkimByDomain = make(map[string]*DKIM)
	}
	e.dkimByDomain[dom] = d
	return d
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

// alternativeEntity builds a complete multipart/alternative MIME entity — its own
// Content-Type header line, a blank line, then the two parts — carrying the plain
// text first and the HTML second.
//
// It returns a whole entity rather than writing into a parent buffer because it is
// used in two places: nested inside a multipart/mixed when there are attachments,
// and promoted to the top level when there are not. A nested multipart declares no
// Content-Transfer-Encoding: RFC 2045 §6.4 restricts multipart to 7bit/8bit/binary,
// and the parts carry their own.
func alternativeEntity(text, html string) []byte {
	boundary := mimeBoundary()
	var b bytes.Buffer
	b.WriteString(`Content-Type: multipart/alternative; boundary="` + boundary + `"` + "\r\n\r\n")
	writeMIMEPart(&b, boundary, "text/plain; charset=utf-8", text)
	writeMIMEPart(&b, boundary, "text/html; charset=utf-8", html)
	b.WriteString("--" + boundary + "--\r\n")
	return b.Bytes()
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
