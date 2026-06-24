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
}

// DefaultConfig returns constitutional defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:          false, // enabled by the first-boot wizard once a domain is set
		DKIMSelector:     "vayu",
		StorageDir:       "./vayudata/mail",
		QueueMaxAttempts: 12,
		QueueBaseBackoff: 2 * time.Minute,
		DeliveryTimeout:  30 * time.Second,
		DKIMEnabled:      true,
		SPFEnabled:       true,
		DMARCEnabled:     true,
	}
}
