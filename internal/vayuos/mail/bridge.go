// SPDX-License-Identifier: Apache-2.0

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
	// EncryptForRecipients encrypts plaintext to EVERY resolvable recipient in one
	// armored message (for RFC 3156 PGP/MIME, which — unlike inline PGP — carries
	// attachments and multiple recipients). It returns the ciphertext, the
	// addresses whose keys were not found, and ok=false when nothing could be
	// encrypted.
	//
	// known are the sending mailbox's keys for people outside the install
	// (contactkeys.go), and a signer that is set (a local address) signs the
	// message inside the encryption.
	EncryptForRecipients(plaintext []byte, recipientEmails []string, known map[string]string, signer string) ([]byte, []string, bool)
	// SignDetached is an armored detached signature over data by the local
	// address signer, for RFC 3156 multipart/signed; ok=false when it has no
	// key to sign with.
	SignDetached(data []byte, signer string) ([]byte, bool)
	// DescribePublicKey reads one armored public key: its fingerprint and the
	// addresses it names, or why it cannot be taken.
	DescribePublicKey(armored []byte) (fingerprint string, emails []string, err error)
}
