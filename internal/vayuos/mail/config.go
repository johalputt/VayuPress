// Package mail implements VayuMail — VayuPress's native mail sovereignty layer.
//
// VayuMail is a first-class subsystem of VayuPress (single binary, single
// process). v1.8.0 delivers the outbound sovereignty path:
//
//   - DKIM signing (RFC 6376, relaxed/relaxed, RSA-SHA256)
//   - Direct-to-MX SMTP delivery with opportunistic STARTTLS
//   - A durable, SQLite-backed retry queue
//   - Maildir message storage
//   - Automatic DNS record generation (MX / SPF / DKIM / DMARC) and live health
//     checks surfaced in the VayuOS panel
//   - PGP auto-sign / auto-encrypt of outgoing mail via the VayuPGP bridge
//
// Authentication and storage are delegated to VayuPress core through the
// Bridge interface; VayuMail never stores plaintext passwords.
//
// Inbound: a minimal RFC 5321 SMTP-receive listener + RFC 3501 IMAP read
// server provide the receive side. They are enabled by default when a domain
// is configured (set VAYUOS_MAIL_INBOUND=off to run outbound-only). Binding the
// mail ports is best-effort: if the ports cannot be opened the engine records
// the condition and continues with outbound + local delivery intact.
package mail

import (
	"net"
	"strconv"
	"time"
)

// Config controls the VayuMail engine.
type Config struct {
	Enabled  bool
	Domain   string // primary mail domain (e.g. example.com)
	Hostname string // mail host (e.g. mail.example.com)

	// DKIMSelector is the DKIM selector published at <selector>._domainkey.<domain>.
	DKIMSelector string

	// StorageDir is the base directory for Maildir + DKIM key storage.
	StorageDir string

	// QueueRetry controls retry backoff for the outbound queue.
	QueueMaxAttempts int
	QueueBaseBackoff time.Duration

	// DeliveryTimeout bounds a single SMTP delivery attempt.
	DeliveryTimeout time.Duration

	// DKIMEnabled / SPFEnabled / DMARCEnabled toggle DNS guidance + signing.
	DKIMEnabled  bool
	SPFEnabled   bool
	DMARCEnabled bool

	// InboundEnabled gates the receive side (SMTP listener + IMAP). It is on by
	// default so a configured domain can actually receive external mail; set
	// VAYUOS_MAIL_INBOUND=off to run outbound-only. Binding the mail ports is
	// best-effort — if the process cannot bind them (e.g. :25 without
	// privileges) the engine records the condition and continues serving
	// outbound and local delivery rather than failing to start.
	InboundEnabled bool
	// SMTPListen / IMAPListen are the bind addresses for the receive servers.
	SMTPListen string
	IMAPListen string

	// TLS. When TLSCertFile/TLSKeyFile are set they are used for STARTTLS
	// (SMTP :25, submission :587, IMAP :143) and implicit TLS (IMAPS :993).
	// When empty, an in-memory self-signed certificate is generated for
	// Hostname so opportunistic STARTTLS still works out of the box (sending
	// MTAs use opportunistic TLS and do not verify the certificate).
	TLSCertFile string
	TLSKeyFile  string
	// SubmissionListen is the authenticated mail-submission bind address (587).
	SubmissionListen string
	// IMAPSListen is the implicit-TLS IMAP bind address (993).
	IMAPSListen string
	// MaxMessageBytes caps an inbound message size.
	MaxMessageBytes int64

	// JunkFilterEnabled turns on the built-in, fully-local heuristic spam
	// filter. High-scoring inbound mail is filed into the recipient's Junk
	// folder instead of the inbox. No external services or network calls are
	// involved (privacy by default).
	JunkFilterEnabled bool

	// Outbound smarthost relay (optional). When RelayHost is set, the outbound
	// queue delivers through this authenticated SMTP relay instead of direct-to-
	// MX. Everything else stays sovereign (inbound receive, IMAP, local
	// delivery, DKIM signing). This is the pragmatic answer to a fresh self-
	// hosted IP that lacks sending reputation: the relay's established IP
	// reputation carries deliverability while the domain, DKIM and storage
	// remain self-owned. Credentials are never persisted by VayuMail; they come
	// from the environment at boot.
	RelayHost     string // relay hostname (e.g. smtp.provider.com)
	RelayPort     int    // relay port (587 submission / 465 implicit TLS / 25)
	RelayUsername string // SMTP AUTH username (empty = no auth)
	RelayPassword string // SMTP AUTH password
	// RelayRequireTLS requires an encrypted channel (STARTTLS, or implicit TLS
	// on :465) before AUTH/DATA. On by default; only disable for a trusted relay
	// on a private network.
	RelayRequireTLS bool
}

// RelayEnabled reports whether outbound mail should be sent through a configured
// smarthost relay rather than direct-to-MX.
func (c Config) RelayEnabled() bool { return c.RelayHost != "" }

// RelayAddr returns the host:port of the configured relay (defaulting the port
// to 587 — the standard authenticated submission port).
func (c Config) RelayAddr() string {
	port := c.RelayPort
	if port == 0 {
		port = 587
	}
	return net.JoinHostPort(c.RelayHost, strconv.Itoa(port))
}

// DefaultConfig returns constitutional defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:           false, // enabled by the first-boot wizard once a domain is set
		DKIMSelector:      "vayu",
		StorageDir:        "./vayudata/mail",
		QueueMaxAttempts:  12,
		QueueBaseBackoff:  2 * time.Minute,
		DeliveryTimeout:   30 * time.Second,
		DKIMEnabled:       true,
		SPFEnabled:        true,
		DMARCEnabled:      true,
		InboundEnabled:    true, // on by default; disable with VAYUOS_MAIL_INBOUND=off
		SMTPListen:        ":25",
		IMAPListen:        ":143",
		SubmissionListen:  ":587",
		IMAPSListen:       ":993",
		MaxMessageBytes:   25 * 1024 * 1024, // 25 MiB
		JunkFilterEnabled: true,             // local heuristic, no external services
		RelayPort:         587,              // standard authenticated submission port
		RelayRequireTLS:   true,             // never AUTH over plaintext by default
	}
}
