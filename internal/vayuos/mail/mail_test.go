package mail

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestDKIMSignVerifies(t *testing.T) {
	t.Parallel()
	dk, err := LoadOrCreateDKIM(t.TempDir(), "vayu", "example.com")
	if err != nil {
		t.Fatalf("dkim: %v", err)
	}
	if !strings.HasPrefix(dk.PublicTXT(), "v=DKIM1; k=rsa; p=") {
		t.Fatalf("bad TXT: %s", dk.PublicTXT())
	}

	raw := "From: Alice <alice@example.com>\r\n" +
		"To: bob@example.net\r\n" +
		"Subject: Hello sovereignty\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"This is the body.\r\n"

	signed, err := dk.SignMessage([]byte(raw))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// A DKIM-Signature header is prepended, carrying our domain and selector.
	head := string(signed)
	if !strings.HasPrefix(head, "DKIM-Signature:") {
		t.Fatalf("signed message must start with DKIM-Signature, got: %.40q", head)
	}
	if !strings.Contains(head, "d=example.com") || !strings.Contains(head, "s=vayu") {
		t.Fatalf("DKIM-Signature missing d=/s= tags: %.200q", head)
	}
	if !strings.Contains(head, "a=rsa-sha256") || !strings.Contains(head, "c=relaxed/relaxed") {
		t.Fatalf("DKIM-Signature missing algorithm/canonicalization: %.200q", head)
	}
	// The original message must be preserved verbatim after the new header.
	if !strings.Contains(head, raw) {
		t.Fatalf("original message not preserved under signature")
	}
}

func TestDKIMPersistsKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, _ := LoadOrCreateDKIM(dir, "vayu", "example.com")
	b, _ := LoadOrCreateDKIM(dir, "vayu", "example.com")
	if a.PublicTXT() != b.PublicTXT() {
		t.Fatalf("DKIM key not persisted across loads")
	}
}

func TestMaildirDeliver(t *testing.T) {
	t.Parallel()
	md := NewMaildir(t.TempDir())
	if _, err := md.Deliver("example.com", "alice", []byte("Subject: hi\r\n\r\nbody")); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	st, err := md.Stats("example.com", "alice")
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.Messages != 1 || st.Bytes == 0 {
		t.Fatalf("unexpected stats: %+v", st)
	}
}

func TestQueueRetryAndDeliver(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	cfg := DefaultConfig()
	cfg.QueueMaxAttempts = 3
	cfg.QueueBaseBackoff = time.Millisecond

	attempts := 0
	deliver := func(_ context.Context, _ string, _ []string, _ []byte) error {
		attempts++
		if attempts < 2 {
			return errors.New("temp failure")
		}
		return nil
	}
	q, err := NewQueue(db, cfg, deliver)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	id, err := q.Enqueue(context.Background(), "from@example.com", []string{"to@example.net"}, []byte("raw"))
	if err != nil || id == 0 {
		t.Fatalf("enqueue: id=%d err=%v", id, err)
	}
	// First pass: transient failure, message deferred.
	d, f, _ := q.ProcessDue(context.Background(), time.Now())
	if d != 0 || f != 1 {
		t.Fatalf("pass1: delivered=%d failed=%d", d, f)
	}
	// Second pass after backoff: delivered.
	d, f, _ = q.ProcessDue(context.Background(), time.Now().Add(time.Second))
	if d != 1 || f != 0 {
		t.Fatalf("pass2: delivered=%d failed=%d", d, f)
	}
	st, stats, _ := q.Status(context.Background())
	if st.Pending != 0 || stats.Delivered != 1 {
		t.Fatalf("status: pending=%d delivered=%d", st.Pending, stats.Delivered)
	}
}

func TestQueuePermanentFailure(t *testing.T) {
	t.Parallel()
	db, _ := sql.Open("sqlite3", ":memory:")
	defer db.Close()
	cfg := DefaultConfig()
	cfg.QueueMaxAttempts = 1
	cfg.QueueBaseBackoff = time.Millisecond
	q, _ := NewQueue(db, cfg, func(_ context.Context, _ string, _ []string, _ []byte) error {
		return errors.New("hard fail")
	})
	_, _ = q.Enqueue(context.Background(), "a@b.com", []string{"c@d.com"}, []byte("x"))
	_, _, _ = q.ProcessDue(context.Background(), time.Now())
	st, _, _ := q.Status(context.Background())
	if st.Failed != 1 {
		t.Fatalf("expected 1 failed, got %d", st.Failed)
	}
}

func TestPlannedRecords(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.Hostname = "mail.example.com"
	recs := PlannedRecords(cfg, "vayu._domainkey.example.com", "v=DKIM1; k=rsa; p=AAAA")
	var haveMX, haveSPF, haveDKIM, haveDMARC bool
	for _, r := range recs {
		switch {
		case r.Type == "MX" && r.Priority == 10:
			haveMX = true
		case r.Type == "TXT" && strings.Contains(r.Value, "v=spf1"):
			haveSPF = true
		case r.Type == "TXT" && strings.Contains(r.Value, "v=DKIM1"):
			haveDKIM = true
		case r.Type == "TXT" && strings.Contains(r.Value, "v=DMARC1"):
			haveDMARC = true
		}
	}
	if !haveMX || !haveSPF || !haveDKIM || !haveDMARC {
		t.Fatalf("missing records: mx=%v spf=%v dkim=%v dmarc=%v", haveMX, haveSPF, haveDKIM, haveDMARC)
	}
}
