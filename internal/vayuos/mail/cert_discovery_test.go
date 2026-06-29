package mail

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPickReadableCert proves discovery picks the first candidate whose cert+key
// are both readable, skips a candidate missing the key, and returns empty when
// nothing is usable.
func TestPickReadableCert(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	_ = os.MkdirAll(good, 0o755)
	cert := filepath.Join(good, "fullchain.pem")
	key := filepath.Join(good, "privkey.pem")
	if err := os.WriteFile(cert, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}

	missingKey := [2]string{cert, filepath.Join(dir, "nope.pem")}
	if c, k := pickReadableCert([][2]string{missingKey, {cert, key}}); c != cert || k != key {
		t.Errorf("expected to skip missing-key candidate and pick the good pair, got (%q,%q)", c, k)
	}
	if c, _ := pickReadableCert([][2]string{{filepath.Join(dir, "a"), filepath.Join(dir, "b")}}); c != "" {
		t.Errorf("expected empty when nothing readable, got %q", c)
	}
}

// TestMailCertCandidatesIncludesServiceCopy proves the service-readable copy is
// probed first (so it is preferred over the root-only Let's Encrypt key).
func TestMailCertCandidatesIncludesServiceCopy(t *testing.T) {
	cands := mailCertCandidates("mail.example.com")
	if len(cands) < 2 {
		t.Fatalf("want both the service copy and the letsencrypt path, got %d", len(cands))
	}
	if cands[0][0] != "/var/lib/vayupress/mailcert/fullchain.pem" {
		t.Errorf("service-readable copy must be probed first, got %q", cands[0][0])
	}
}
