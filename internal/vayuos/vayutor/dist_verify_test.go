package vayutor

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// gzTar builds a gzip'd tar containing the given files (name→content).
func gzTar(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o700, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("hdr: %v", err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestTarContainsTor(t *testing.T) {
	withTor := gzTar(t, map[string][]byte{"tor/tor": []byte("ELF"), "tor/libevent.so": []byte("x")})
	if !tarContainsTor(withTor) {
		t.Error("bundle with a tor binary should be accepted")
	}
	loneSo := gzTar(t, map[string][]byte{"tor/libevent.so.1": []byte("x"), "tor/libssl.so": []byte("y")})
	if tarContainsTor(loneSo) {
		t.Error("a lone-.so bundle (no tor binary) must be rejected")
	}
}

func TestVerifyBundleIntegrity(t *testing.T) {
	raw := gzTar(t, map[string][]byte{"tor/tor": []byte("ELF")})
	sum := sha256.Sum256(raw)
	sumHex := hex.EncodeToString(sum[:])

	// Pinned + matching → OK.
	t.Setenv("VAYUTOR_BUNDLE_SHA256", sumHex)
	if err := verifyBundleIntegrity("https://x/tor.tar.gz", sumHex); err != nil {
		t.Errorf("matching pin must verify: %v", err)
	}
	// Pinned + mismatch → refuse.
	t.Setenv("VAYUTOR_BUNDLE_SHA256", "deadbeef")
	if err := verifyBundleIntegrity("https://x/tor.tar.gz", sumHex); err == nil {
		t.Error("mismatched pin must be refused")
	}
	// Unpinned + strict → refuse.
	os.Unsetenv("VAYUTOR_BUNDLE_SHA256")
	t.Setenv("VAYUTOR_REQUIRE_VERIFIED_BUNDLE", "1")
	if err := verifyBundleIntegrity("https://x/tor.tar.gz", sumHex); err == nil {
		t.Error("unpinned bundle must be refused in strict mode")
	}
	// Unpinned + non-strict → allowed (logs the digest).
	os.Unsetenv("VAYUTOR_REQUIRE_VERIFIED_BUNDLE")
	if err := verifyBundleIntegrity("https://x/tor.tar.gz", sumHex); err != nil {
		t.Errorf("unpinned bundle in non-strict mode must be allowed: %v", err)
	}
}

func TestExtractTorFromTar(t *testing.T) {
	dir := t.TempDir()
	raw := gzTar(t, map[string][]byte{
		"tor/tor":          []byte("ELF-tor"),
		"tor/libevent.so":  []byte("lib1"),
		"tor/data/geoip":   []byte("ignored"), // not tor and not .so → skipped
		"tor/pluggable.so": []byte("lib2"),
	})
	if err := extractTorFromTar(raw, dir); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "tor")); err != nil || string(b) != "ELF-tor" {
		t.Fatalf("tor not extracted: %q err %v", b, err)
	}
	for _, lib := range []string{"libevent.so", "pluggable.so"} {
		if _, err := os.Stat(filepath.Join(dir, lib)); err != nil {
			t.Errorf("lib %s not extracted: %v", lib, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "geoip")); err == nil {
		t.Error("non-tor/non-.so file should not be extracted")
	}
}
