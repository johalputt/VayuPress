package mail

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestDKIMBodyCanonicalization(t *testing.T) {
	t.Parallel()
	// Trailing whitespace and empty lines are normalised away.
	got := canonicalizeBodyRelaxed([]byte("Hello  World \r\n\r\n\r\n"))
	if string(got) != "Hello World\r\n" {
		t.Fatalf("relaxed body canon = %q", got)
	}
	if len(canonicalizeBodyRelaxed([]byte(""))) != 0 {
		t.Fatalf("empty body must canon to empty")
	}
}

func TestDKIMSignVerifies(t *testing.T) {
	t.Parallel()
	dk, err := LoadOrCreateDKIM(t.TempDir(), "vayu", "example.com")
	if err != nil {
		t.Fatalf("dkim: %v", err)
	}
	if !strings.HasPrefix(dk.PublicTXT(), "v=DKIM1; k=rsa; p=") {
		t.Fatalf("bad TXT: %s", dk.PublicTXT())
	}
	headers := []HeaderField{
		{Key: "From", Value: "Alice <alice@example.com>"},
		{Key: "To", Value: "bob@example.net"},
		{Key: "Subject", Value: "Hello   sovereignty"},
	}
	body := []byte("This is the body.\r\n")
	full, err := dk.Sign(headers, body)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	value := strings.TrimPrefix(full, "DKIM-Signature: ")

	// Verify body hash tag.
	bh := tagValue(value, "bh")
	wantBH := base64.StdEncoding.EncodeToString(sha256OfBytes(canonicalizeBodyRelaxed(body)))
	if bh != wantBH {
		t.Fatalf("bh mismatch: %s != %s", bh, wantBH)
	}

	// Reconstruct the signed data exactly as Sign does and verify b=.
	idx := strings.LastIndex(value, "b=")
	if idx < 0 {
		t.Fatalf("no b= tag")
	}
	sigB64 := value[idx+2:]
	emptyB := value[:idx] + "b="
	var sb strings.Builder
	for _, h := range headers {
		sb.WriteString(canonicalizeHeaderRelaxed(h.Key, h.Value))
		sb.WriteString("\r\n")
	}
	sb.WriteString(canonicalizeHeaderRelaxed("DKIM-Signature", emptyB))
	digest := sha256.Sum256([]byte(sb.String()))
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	if err := rsa.VerifyPKCS1v15(&dk.priv.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("DKIM signature does not verify: %v", err)
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

func tagValue(s, key string) string {
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, key+"=") {
			return part[len(key)+1:]
		}
	}
	return ""
}

func sha256OfBytes(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
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
