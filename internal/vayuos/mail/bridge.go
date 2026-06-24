// Package mail provides the VayuMail subsystem — a self-hosted mail server
// (SMTP + IMAP + DKIM/SPF/DMARC) based on Mox (MIT license).
//
// VayuMail compiles into the VayuPress binary. All authentication is delegated
// to VayuPress's user store. Mail is stored in Maildir format under VayuPress
// storage paths. No separate process or config file is needed.
package mail

import (
	"time"
)

// ── Core types ───────────────────────────────────────────────────────────────

// MailUser is a VayuMail user linked to a VayuPress account.
type MailUser struct {
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	Domain   string `json:"domain"`
	Username string `json:"username"` // local part of email
}

// Mailbox represents a user's mailbox.
type Mailbox struct {
	Username string `json:"username"`
	Domain   string `json:"domain"`
	SizeBytes int64 `json:"size_bytes"`
	Messages int   `json:"messages"`
}

// MailboxStats holds aggregate mailbox statistics.
type MailboxStats struct {
	TotalMessages int   `json:"total_messages"`
	Unread        int   `json:"unread"`
	SizeBytes     int64 `json:"size_bytes"`
	LastActivity  time.Time `json:"last_activity"`
}

// MailDomain represents a configured mail domain.
type MailDomain struct {
	Domain      string `json:"domain"`
	DKIMEnabled bool   `json:"dkim_enabled"`
	SPFVerified bool   `json:"spf_verified"`
	DMARCSetup  bool   `json:"dmarc_setup"`
	Active      bool   `json:"active"`
}

// ── Message types ────────────────────────────────────────────────────────────

// RawMessage is a raw MIME email message.
type RawMessage struct {
	From    string
	To      []string
	Data    []byte
	Size    int
	Headers map[string]string
}

// TransactionalMessage is a system-generated email.
type TransactionalMessage struct {
	To          []string
	Subject     string
	Body        string // HTML body
	PlainBody   string // Plain text fallback
	FromName    string
	FromAddress string
}

// InboundMessage is a received email.
type InboundMessage struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        []string  `json:"to"`
	Subject   string    `json:"subject"`
	Size      int       `json:"size"`
	ReceivedAt time.Time `json:"received_at"`
	HasPGP    bool      `json:"has_pgp"`
}

// DeliveredMessage represents a successfully delivered message.
type DeliveredMessage struct {
	ID          string    `json:"id"`
	From        string    `json:"from"`
	To          string    `json:"to"`
	DeliveredAt time.Time `json:"delivered_at"`
}

// FailedMessage represents a delivery failure.
type FailedMessage struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Error     string    `json:"error"`
	FailedAt  time.Time `json:"failed_at"`
}

// ── Stats types ──────────────────────────────────────────────────────────────

// SMTPStats holds SMTP server statistics.
type SMTPStats struct {
	ConnectionsAccepted int64 `json:"connections_accepted"`
	MessagesReceived    int64 `json:"messages_received"`
	MessagesDelivered   int64 `json:"messages_delivered"`
	MessagesRejected    int64 `json:"messages_rejected"`
	ActiveConnections   int64 `json:"active_connections"`
}

// QueueStatus holds mail queue statistics.
type QueueStatus struct {
	Queued    int `json:"queued"`
	Processing int `json:"processing"`
	Deferred  int `json:"deferred"`
	Failed    int `json:"failed"`
}

// DomainHealth holds health information for a domain.
type DomainHealth struct {
	Domain    string              `json:"domain"`
	MX        *DNSRecordHealth    `json:"mx"`
	SPF       *DNSRecordHealth    `json:"spf"`
	DKIM      *DNSRecordHealth    `json:"dkim"`
	DMARC     *DNSRecordHealth    `json:"dmarc"`
	TLS       *TLSHealth          `json:"tls"`
}

// DNSRecordHealth represents the status of a DNS record.
type DNSRecordHealth struct {
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Healthy  bool   `json:"healthy"`
	Message  string `json:"message"`
}

// TLSHealth represents TLS certificate status.
type TLSHealth struct {
	Issued    bool      `json:"issued"`
	ExpiresAt time.Time `json:"expires_at"`
	DaysLeft  int       `json:"days_left"`
	AutoRenew bool      `json:"auto_renew"`
}

// ── Config ───────────────────────────────────────────────────────────────────

// Config holds VayuMail configuration.
type Config struct {
	Enabled        bool   `json:"enabled"`
	Domain         string `json:"domain"`
	Hostname       string `json:"hostname"`
	SMTPPort       int    `json:"smtp_port"`
	SubmissionPort int    `json:"submission_port"`
	IMAPPort       int    `json:"imap_port"`
	StoragePath    string `json:"storage_path"`
	DKIMEnabled    bool   `json:"dkim_enabled"`
	SPFEnabled     bool   `json:"spf_enabled"`
	DMARCEnabled   bool   `json:"dmarc_enabled"`
	DANEEnabled    bool   `json:"dane_enabled"`
}

// DefaultConfig returns a Config with safe defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:        true,
		SMTPPort:       25,
		SubmissionPort: 587,
		IMAPPort:       993,
		DKIMEnabled:    true,
		SPFEnabled:     true,
		DMARCEnabled:   true,
		DANEEnabled:    false,
	}
}

// ── Bridge interface ─────────────────────────────────────────────────────────

// Bridge is the ONLY way VayuPress core talks to VayuMail and vice versa.
// No direct package imports between them — only through this interface.
type Bridge interface {
	// Auth — VayuMail delegates auth to VayuPress
	AuthUser(username, password string) (bool, error)
	GetUserByEmail(email string) (*MailUser, error)

	// Mailbox lifecycle
	CreateMailbox(domain, username string) error
	DeleteMailbox(domain, username string) error
	ListMailboxes(domain string) ([]Mailbox, error)
	GetMailboxStats(username string) (*MailboxStats, error)

	// Transactional sending (VayuPress sends system email)
	SendTransactional(msg *TransactionalMessage) error

	// Domain management
	AddDomain(domain string) error
	RemoveDomain(domain string) error
	ListDomains() ([]MailDomain, error)

	// Health and monitoring
	GetSMTPStats() (*SMTPStats, error)
	GetQueueStatus() (*QueueStatus, error)
	GetDomainHealth(domain string) (*DomainHealth, error)

	// Events (VayuMail notifies VayuPress)
	OnMessageReceived(handler func(*InboundMessage))
	OnMessageDelivered(handler func(*DeliveredMessage))
	OnDeliveryFailed(handler func(*FailedMessage))
}