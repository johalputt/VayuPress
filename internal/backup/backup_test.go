package backup

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func makeTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "vayudata", "mail"), 0o750); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"vayupress.db":              "sqlite-bytes-here",
		"vayudata/mail/inbox.eml":   "From: a@b\r\n\r\nhello",
		"vayudata/mail/secret.key":  "PRIVATE",
		filepath.Join("static.txt"): "asset",
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRoundTrip(t *testing.T) {
	src := makeTree(t)
	var buf bytes.Buffer
	if err := Create(&buf, "correct horse battery staple", src); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Ciphertext must not leak filenames or content.
	for _, plain := range []string{"vayupress.db", "PRIVATE", "hello", "inbox.eml"} {
		if bytes.Contains(buf.Bytes(), []byte(plain)) {
			t.Errorf("plaintext %q visible in encrypted backup", plain)
		}
	}

	dest := t.TempDir()
	if err := Extract(bytes.NewReader(buf.Bytes()), "correct horse battery staple", dest); err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "vayudata", "mail", "secret.key"))
	if err != nil || string(got) != "PRIVATE" {
		t.Fatalf("restored content mismatch: %q err=%v", got, err)
	}
	if db, _ := os.ReadFile(filepath.Join(dest, "vayupress.db")); string(db) != "sqlite-bytes-here" {
		t.Fatal("db not restored")
	}
}

func TestWrongPassphraseFails(t *testing.T) {
	src := makeTree(t)
	var buf bytes.Buffer
	if err := Create(&buf, "right", src); err != nil {
		t.Fatal(err)
	}
	err := Extract(bytes.NewReader(buf.Bytes()), "wrong", t.TempDir())
	if !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("want ErrBadPassphrase, got %v", err)
	}
}

func TestTamperDetected(t *testing.T) {
	src := makeTree(t)
	var buf bytes.Buffer
	if err := Create(&buf, "pw", src); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	raw[len(raw)/2] ^= 0xFF // flip one ciphertext bit
	err := Extract(bytes.NewReader(raw), "pw", t.TempDir())
	if err == nil {
		t.Fatal("tampered backup extracted without error")
	}
}

func TestNotABackup(t *testing.T) {
	err := Extract(bytes.NewReader([]byte("hello world, not a backup")), "pw", t.TempDir())
	if !errors.Is(err, ErrNotBackup) {
		t.Fatalf("want ErrNotBackup, got %v", err)
	}
}

func TestEmptyPassphraseRefused(t *testing.T) {
	if err := Create(&bytes.Buffer{}, "  ", t.TempDir()); err == nil {
		t.Fatal("empty passphrase must be refused")
	}
}
