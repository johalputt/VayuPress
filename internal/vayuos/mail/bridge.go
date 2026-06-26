package mail

import "time"

// MailUser is a resolved local mail account.
type MailUser struct {
	UserID   string
	Email    string
	Domain   string
	Username string
}

// Mailbox describes a delivery mailbox.
type Mailbox struct {
	Username string `json:"username"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
}

// MailboxStats summarises a mailbox.
type MailboxStats struct {
	Messages int   `json:"messages"`
	Bytes    int64 `json:"bytes"`
}

// MailDomain is a domain VayuMail serves.
type MailDomain struct {
	Domain string `json:"domain"`
	Active bool   `json:"active"`
}

// TransactionalMessage is a system email request (welcome mail, notices…).
type TransactionalMessage struct {
	To        []string
	Subject   string
	Body      string // HTML
	PlainBody string
}

// SMTPStats are outbound delivery counters for the panel.
type SMTPStats struct {
	Queued    int `json:"queued"`
	Delivered int `json:"delivered"`
	Failed    int `json:"failed"`
	Deferred  int `json:"deferred"`
}

// QueueStatus is a snapshot of the outbound queue.
type QueueStatus struct {
	Pending   int       `json:"pending"`
	Failed    int       `json:"failed"`
	OldestAge string    `json:"oldest_age"`
	CheckedAt time.Time `json:"checked_at"`
}

// Bridge is the only contract between VayuPress core and VayuMail.
type Bridge interface {
	// Auth — delegated to VayuPress core (never stores plaintext passwords).
	AuthUser(username, password string) (bool, error)
	GetUserByEmail(email string) (*MailUser, error)

	// IsLocalRecipient reports whether email belongs to a mailbox served by this
	// instance (a CMS user or an admin-managed mail account on the configured
	// domain). VayuMail uses it to short-circuit delivery: local recipients are
	// filed straight into their Maildir instead of being relayed out to an MX.
	IsLocalRecipient(email string) bool

	// Transactional sending.
	SendTransactional(msg *TransactionalMessage) error

	// PGP integration — VayuMail asks VayuPGP through core.
	EncryptForRecipient(plaintext []byte, recipientEmail string) ([]byte, bool)
	SignAs(plaintext []byte, senderUserID string) ([]byte, bool)
}
