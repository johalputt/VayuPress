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
// Scope note: a full inbound MX listener + IMAP server is intentionally a
// future milestone. A long-running mail daemon listening on ports 25/143/993
// is a significant resource and attack-surface commitment that is governed by
// the VayuPress Operational Simplicity Doctrine, so it is delivered separately
// rather than bundled half-finished.
package mail

import "time"

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

	// InboundEnabled gates the receive side (SMTP listener + IMAP). It is an
	// explicit opt-in per the Operational Simplicity Doctrine: a long-running
	// mail daemon is only started when the operator asks for it.
	InboundEnabled bool
	// SMTPListen / IMAPListen are the bind addresses for the receive servers.
	SMTPListen string
	IMAPListen string
	// MaxMessageBytes caps an inbound message size.
	MaxMessageBytes int64

	// JunkFilterEnabled turns on the built-in, fully-local heuristic spam
	// filter. High-scoring inbound mail is filed into the recipient's Junk
	// folder instead of the inbox. No external services or network calls are
	// involved (privacy by default).
	JunkFilterEnabled bool
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
		InboundEnabled:    false, // opt-in (VAYUOS_MAIL_INBOUND=on)
		SMTPListen:        ":25",
		IMAPListen:        ":143",
		MaxMessageBytes:   25 * 1024 * 1024, // 25 MiB
		JunkFilterEnabled: true,             // local heuristic, no external services
	}
}
