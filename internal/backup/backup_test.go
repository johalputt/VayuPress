package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
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

// sealTar builds a valid encrypted backup whose archive contains exactly the
// given entries (name→content). It reuses the production encryption path, so a
// crafted entry name lets us drive Extract's Zip-Slip guard end-to-end.
func sealTar(t *testing.T, passphrase string, entries map[string]string) []byte {
	t.Helper()
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(deriveKey(passphrase, salt))
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.WriteString(magic)
	buf.Write(salt)
	enc := &encWriter{w: &buf, gcm: gcm, buf: make([]byte, 0, chunkSize)}
	gz := gzip.NewWriter(enc)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := enc.flush(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestExtractRejectsTraversal drives the real Extract path with archives whose
// entry names try to escape the destination via "..", and confirms nothing is
// written outside it (Zip-Slip / CodeQL go/zipslip).
func TestExtractRejectsTraversal(t *testing.T) {
	const pw = "pw"
	for _, bad := range []string{"../escape.txt", "../../etc/evil", "a/../../escape", "sub/../../../escape"} {
		dest := t.TempDir()
		archive := sealTar(t, pw, map[string]string{bad: "OWNED"})
		err := Extract(bytes.NewReader(archive), pw, dest)
		if err == nil {
			t.Errorf("Extract accepted traversal entry %q", bad)
		}
		// Belt-and-braces: assert nothing landed outside dest either way.
		parent := filepath.Dir(dest)
		for _, leaked := range []string{"escape.txt", "evil", "escape"} {
			if _, statErr := os.Stat(filepath.Join(parent, leaked)); statErr == nil {
				t.Errorf("entry %q escaped the destination: %s written", bad, filepath.Join(parent, leaked))
			}
		}
	}
}

// TestExtractAllowsContainedAbsoluteNames confirms an absolute-looking entry is
// safely contained under dest (filepath.Join treats it as relative), not rejected.
func TestExtractAllowsContainedAbsoluteNames(t *testing.T) {
	const pw = "pw"
	dest := t.TempDir()
	archive := sealTar(t, pw, map[string]string{"/etc/passwd": "contained", "a/b.txt": "ok"})
	if err := Extract(bytes.NewReader(archive), pw, dest); err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "etc", "passwd"))
	if err != nil || string(got) != "contained" {
		t.Fatalf("absolute-looking entry not contained under dest: %q err=%v", got, err)
	}
}

func TestEmptyPassphraseRefused(t *testing.T) {
	if err := Create(&bytes.Buffer{}, "  ", t.TempDir()); err == nil {
		t.Fatal("empty passphrase must be refused")
	}
}
