package mail

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestAccountStoreCRUD(t *testing.T) {
	t.Parallel()
	db, _ := sql.Open("sqlite3", ":memory:")
	defer db.Close()
	s, err := NewAccountStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ctx := context.Background()
	if err := s.Create(ctx, "Alice@Example.com", "hash123", "Alice", "author"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Create(ctx, "alice@example.com", "h", "dup", "author"); err == nil {
		t.Fatalf("expected duplicate rejection")
	}
	if h := s.HashFor(ctx, "alice@example.com"); h != "hash123" {
		t.Fatalf("hash mismatch: %q", h)
	}
	list, _ := s.List(ctx)
	if len(list) != 1 || list[0].Email != "alice@example.com" {
		t.Fatalf("list wrong: %+v", list)
	}
	if err := s.SetPasswordHash(ctx, "alice@example.com", "newhash"); err != nil {
		t.Fatalf("setpw: %v", err)
	}
	if s.HashFor(ctx, "alice@example.com") != "newhash" {
		t.Fatalf("password not updated")
	}
	if err := s.Delete(ctx, "alice@example.com"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if h := s.HashFor(ctx, "alice@example.com"); h != "" {
		t.Fatalf("account should be gone")
	}
}

func TestMaildirFolders(t *testing.T) {
	t.Parallel()
	md := NewMaildir(t.TempDir())
	if err := md.CreateAll("example.com", "bob"); err != nil {
		t.Fatalf("create folders: %v", err)
	}
	// Deliver to Inbox, then move to Junk.
	id, err := md.DeliverTo("example.com", "bob", "Inbox", []byte("Subject: Spammy\r\n\r\nbuy now"))
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	inbox, _ := md.ListFolder("example.com", "bob", "Inbox")
	if len(inbox) != 1 {
		t.Fatalf("inbox should have 1, got %d", len(inbox))
	}
	if err := md.MoveBetween("example.com", "bob", id, "Inbox", "Junk"); err != nil {
		t.Fatalf("move: %v", err)
	}
	inbox, _ = md.ListFolder("example.com", "bob", "Inbox")
	junk, _ := md.ListFolder("example.com", "bob", "Junk")
	if len(inbox) != 0 || len(junk) != 1 {
		t.Fatalf("after move: inbox=%d junk=%d (want 0/1)", len(inbox), len(junk))
	}
	// Read the junk message back.
	raw, err := md.ReadRawFolder("example.com", "bob", "Junk", junk[0].ID)
	if err != nil || len(raw) == 0 {
		t.Fatalf("read junk: %v", err)
	}
}

func TestAccountSetActive(t *testing.T) {
	t.Parallel()
	db, _ := sql.Open("sqlite3", ":memory:")
	defer db.Close()
	s, err := NewAccountStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ctx := context.Background()
	if err := s.Create(ctx, "carol@example.com", "hash", "Carol", "author"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Active by default → HashFor returns the hash.
	if s.HashFor(ctx, "carol@example.com") != "hash" {
		t.Fatalf("active account should expose hash")
	}
	// Disable → HashFor returns "" (cannot authenticate).
	if err := s.SetActive(ctx, "carol@example.com", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if s.HashFor(ctx, "carol@example.com") != "" {
		t.Fatalf("disabled account must not authenticate")
	}
	list, _ := s.List(ctx)
	if len(list) != 1 || list[0].Active {
		t.Fatalf("account should be listed as inactive: %+v", list)
	}
	// Re-enable.
	if err := s.SetActive(ctx, "carol@example.com", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if s.HashFor(ctx, "carol@example.com") != "hash" {
		t.Fatalf("re-enabled account should authenticate again")
	}
	// Updating a missing account errors.
	if err := s.SetActive(ctx, "nobody@example.com", false); err == nil {
		t.Fatalf("expected error for unknown account")
	}
	if err := s.SetPasswordHash(ctx, "carol@example.com", ""); err == nil {
		t.Fatalf("empty password hash must be rejected")
	}
}

func TestScoreSpam(t *testing.T) {
	t.Parallel()
	ham := []byte("From: Friend <friend@example.com>\r\n" +
		"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\n" +
		"Subject: Lunch tomorrow?\r\n\r\n" +
		"Hey, are you free for lunch tomorrow around noon?")
	if v := ScoreSpam(ham); v.IsSpam {
		t.Fatalf("legitimate message flagged as spam: score=%d reasons=%v", v.Score, v.Reasons)
	}

	spam := []byte("Subject: WINNER WINNER CONGRATULATIONS!!!\r\n\r\n" +
		"You have won the lottery! Click here to claim your free money now. " +
		"Act now, this is a limited time offer. $$$ 100% guaranteed.")
	v := ScoreSpam(spam)
	if !v.IsSpam {
		t.Fatalf("obvious spam not flagged: score=%d reasons=%v", v.Score, v.Reasons)
	}
	if v.Score < SpamThreshold {
		t.Fatalf("spam score %d below threshold %d", v.Score, SpamThreshold)
	}
}

func TestDeliverInboundFilesSpamToJunk(t *testing.T) {
	t.Parallel()
	db, _ := sql.Open("sqlite3", ":memory:")
	defer db.Close()
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Domain = "example.com"
	cfg.Hostname = "mail.example.com"
	cfg.StorageDir = t.TempDir()
	e := NewEngine(&cfg, nil, db)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer e.Stop(context.Background())

	spam := []byte("Subject: FREE MONEY WINNER!!!\r\n\r\n" +
		"You won the lottery, click here, act now, $$$ 100% guaranteed free money.")
	if _, err := e.DeliverInbound("dave@example.com", spam); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	inbox, _ := e.ListFolder("dave", "Inbox")
	junk, _ := e.ListFolder("dave", "Junk")
	if len(inbox) != 0 {
		t.Fatalf("spam should not land in inbox, got %d", len(inbox))
	}
	if len(junk) != 1 {
		t.Fatalf("spam should be filed in Junk, got %d", len(junk))
	}

	ham := []byte("From: a@b.com\r\nDate: Mon, 01 Jan 2024 10:00:00 +0000\r\nSubject: Hi\r\n\r\nNormal note.")
	if _, err := e.DeliverInbound("dave@example.com", ham); err != nil {
		t.Fatalf("deliver ham: %v", err)
	}
	inbox, _ = e.ListFolder("dave", "Inbox")
	if len(inbox) != 1 {
		t.Fatalf("ham should land in inbox, got %d", len(inbox))
	}
}
