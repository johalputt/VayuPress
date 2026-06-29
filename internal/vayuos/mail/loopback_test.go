package mail

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// loopbackBridge is a test bridge that treats a fixed set of addresses as local
// mailboxes. Only IsLocalRecipient is exercised here; the other methods are
// inert so the engine can run its send path.
type loopbackBridge struct{ localSet map[string]bool }

func (b loopbackBridge) AuthUser(string, string) (bool, error)         { return false, nil }
func (b loopbackBridge) GetUserByEmail(string) (*MailUser, error)      { return nil, nil }
func (b loopbackBridge) IsLocalRecipient(email string) bool            { return b.localSet[email] }
func (b loopbackBridge) SendTransactional(*TransactionalMessage) error { return nil }
func (b loopbackBridge) EncryptForRecipient([]byte, string) ([]byte, bool) {
	return nil, false
}
func (b loopbackBridge) SignAs([]byte, string) ([]byte, bool) { return nil, false }

func newLoopbackEngine(t *testing.T, bridge Bridge) *Engine {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// A :memory: database is per-connection; pin the pool to one connection so
	// the queue table created during Start is visible to later queue operations.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Domain = "example.com"
	cfg.Hostname = "mail.example.com"
	cfg.StorageDir = t.TempDir()
	cfg.InboundEnabled = false // these tests exercise outbound/local delivery, not the listener
	e := NewEngine(&cfg, bridge, db)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { e.Stop(context.Background()) })
	return e
}

// A message addressed to a local mailbox must be filed straight into that
// account's Inbox (the loopback delivery), not silently left in the outbound
// queue for external relay.
func TestSendMailLocalDeliveryLandsInInbox(t *testing.T) {
	t.Parallel()
	bridge := loopbackBridge{localSet: map[string]bool{"bob@example.com": true}}
	e := newLoopbackEngine(t, bridge)

	id, err := e.SendMail(context.Background(), "alice@example.com",
		[]string{"bob@example.com"}, "Hello Bob", "", "Local body text", "")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	// Purely local delivery: no queue id is produced.
	if id != 0 {
		t.Fatalf("expected no queue id for local-only send, got %d", id)
	}
	inbox, _ := e.ListFolder("bob", "Inbox")
	if len(inbox) != 1 {
		t.Fatalf("expected 1 message in bob's Inbox, got %d", len(inbox))
	}
	if inbox[0].Subject != "Hello Bob" {
		t.Errorf("subject mismatch: %q", inbox[0].Subject)
	}
	// Nothing should have been queued for external relay.
	qs, _, err := e.QueueStatus(context.Background())
	if err != nil {
		t.Fatalf("queue status: %v", err)
	}
	if qs.Pending != 0 {
		t.Fatalf("expected empty outbound queue, got %d pending", qs.Pending)
	}
}

// A remote recipient must still be enqueued for MX relay (the existing,
// working outbound path) and must NOT be delivered into any local mailbox.
func TestSendMailRemoteStillQueued(t *testing.T) {
	t.Parallel()
	bridge := loopbackBridge{localSet: map[string]bool{}}
	e := newLoopbackEngine(t, bridge)

	id, err := e.SendMail(context.Background(), "alice@example.com",
		[]string{"stranger@partner.test"}, "Hi", "", "body", "")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if id == 0 {
		t.Fatalf("expected a queue id for remote send")
	}
	qs, _, _ := e.QueueStatus(context.Background())
	if qs.Pending != 1 {
		t.Fatalf("expected 1 pending queued message, got %d", qs.Pending)
	}
}

// A mixed send (one local + one remote recipient) must do both: deliver locally
// AND enqueue the remote copy.
func TestSendMailMixedLocalAndRemote(t *testing.T) {
	t.Parallel()
	bridge := loopbackBridge{localSet: map[string]bool{"carol@example.com": true}}
	e := newLoopbackEngine(t, bridge)

	id, err := e.SendMail(context.Background(), "alice@example.com",
		[]string{"carol@example.com", "ext@partner.test"}, "Team update", "", "body", "")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if id == 0 {
		t.Fatalf("expected a queue id because there is a remote recipient")
	}
	inbox, _ := e.ListFolder("carol", "Inbox")
	if len(inbox) != 1 {
		t.Fatalf("expected carol to receive 1 local message, got %d", len(inbox))
	}
	qs, _, _ := e.QueueStatus(context.Background())
	if qs.Pending != 1 {
		t.Fatalf("expected 1 queued (remote) message, got %d", qs.Pending)
	}
}

// A From header with a display name must be preserved verbatim in the delivered
// message (so recipients see the name), while the envelope uses the bare
// address. This is the local-delivery half; envelopeAddress covers the relay.
func TestSendMailPreservesDisplayName(t *testing.T) {
	t.Parallel()
	bridge := loopbackBridge{localSet: map[string]bool{"bob@example.com": true}}
	e := newLoopbackEngine(t, bridge)

	from := `"Alice Example" <alice@example.com>`
	if _, err := e.SendMail(context.Background(), from,
		[]string{"bob@example.com"}, "Hi Bob", "", "body", ""); err != nil {
		t.Fatalf("send: %v", err)
	}
	msgs, _ := e.ListFolder("bob", "Inbox")
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].From, "Alice Example") {
		t.Errorf("display name not preserved in From: %q", msgs[0].From)
	}
}
func TestSplitLocalRecipientsDomainGate(t *testing.T) {
	t.Parallel()
	// localSet would match on local-part alone, but the domain differs.
	bridge := loopbackBridge{localSet: map[string]bool{"bob@other.test": true}}
	e := newLoopbackEngine(t, bridge)

	local, remote := e.splitLocalRecipients([]string{"bob@other.test", "bob@example.com"})
	if len(local) != 0 {
		t.Fatalf("address on a foreign domain must be remote, got local=%v", local)
	}
	if len(remote) != 2 {
		t.Fatalf("expected both addresses remote, got remote=%v", remote)
	}
}

// Without a bridge, locality falls back to a domain-only check (matching the
// inbound SMTP relay policy).
func TestIsLocalRecipientNoBridgeDomainFallback(t *testing.T) {
	t.Parallel()
	e := newLoopbackEngine(t, nil)
	if !e.isLocalRecipient("anyone@example.com") {
		t.Fatalf("configured-domain address should be local without a bridge")
	}
	if e.isLocalRecipient("anyone@elsewhere.test") {
		t.Fatalf("foreign-domain address must not be local")
	}
}

// encryptingBridge is a loopbackBridge whose recipients all have a "PGP key", so
// EncryptForRecipient always returns ciphertext. It lets the test prove that
// SendMail encrypts (person-to-person) while SendSystemMail never does.
type encryptingBridge struct{ loopbackBridge }

func (encryptingBridge) EncryptForRecipient([]byte, string) ([]byte, bool) {
	return []byte("-----BEGIN PGP MESSAGE-----\nCIPHERTEXT\n-----END PGP MESSAGE-----"), true
}

func readMaildirRaw(t *testing.T, root string) string {
	t.Helper()
	var sb strings.Builder
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if b, e := os.ReadFile(p); e == nil {
				sb.Write(b)
				sb.WriteString("\n")
			}
		}
		return nil
	})
	return sb.String()
}

// SendSystemMail must never PGP-encrypt, even when the recipient has a key, so a
// sign-in link stays readable. SendMail (person-to-person) must still encrypt.
func TestSendSystemMailNeverEncrypted(t *testing.T) {
	t.Parallel()
	bridge := encryptingBridge{loopbackBridge{localSet: map[string]bool{"bob@example.com": true}}}
	e := newLoopbackEngine(t, bridge)

	// Person-to-person mail to a keyed recipient → encrypted (control case).
	if _, err := e.SendMail(context.Background(), "alice@example.com",
		[]string{"bob@example.com"}, "Secret", "", "secret body", ""); err != nil {
		t.Fatalf("SendMail: %v", err)
	}
	// Transactional sign-in mail to the SAME recipient → must stay readable.
	const link = "https://example.com/members/verify?token=abc123"
	if _, err := e.SendSystemMail(context.Background(), "Acme <noreply@example.com>",
		[]string{"bob@example.com"}, "Your sign-in link",
		`<a href="`+link+`">Sign in</a>`, "Sign in: "+link); err != nil {
		t.Fatalf("SendSystemMail: %v", err)
	}

	raw := readMaildirRaw(t, e.cfg.StorageDir)
	// The encrypted control message is present (proves the bridge encrypts).
	if !strings.Contains(raw, "X-VayuPGP") {
		t.Fatal("expected the person-to-person SendMail to be PGP-encrypted")
	}
	// The system mail's link must be present in cleartext, and it must not have
	// been turned into a PGP blob.
	if !strings.Contains(raw, link) {
		t.Error("system mail sign-in link should be delivered in readable text")
	}
}
