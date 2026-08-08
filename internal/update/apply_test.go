// SPDX-License-Identifier: Apache-2.0

package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withStubbedSignature substitutes the authenticity check so a test can reach
// the stages after it with a synthetic release. A fixture cannot carry a real
// Sigstore signature, and the alternative — dropping the check for tests — would
// mean nothing downstream of it were ever exercised.
//
// The real function is driven by TestAReleaseWithAnUnreadableSignatureIsRefused,
// so this substitution cannot conceal its removal.
func withStubbedSignature(t *testing.T) {
	t.Helper()
	prev := verifyReleaseSignature
	verifyReleaseSignature = func(_, _ []byte) error { return nil }
	t.Cleanup(func() { verifyReleaseSignature = prev })
}

func TestPreflightApply(t *testing.T) {
	if err := PreflightApply(false, "normal"); err == nil {
		t.Error("enabled=false should fail")
	}
	if err := PreflightApply(true, "read-only"); err == nil {
		t.Error("read-only mode should fail")
	}
	if err := PreflightApply(true, "quarantined"); err == nil {
		t.Error("quarantined mode should fail")
	}
	if err := PreflightApply(true, "maintenance"); err == nil {
		t.Error("maintenance mode should fail")
	}
	if err := PreflightApply(true, "normal"); err != nil {
		t.Errorf("all-good should pass: %v", err)
	}
}

func TestApplyVerifiedDryRun(t *testing.T) {
	withStubbedSignature(t)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	binary := []byte("\x7fELF this is a fake vayupress binary payload")
	sum := sha256.Sum256(binary)
	sumHex := hex.EncodeToString(sum[:])
	sig := ed25519.Sign(priv, sum[:])
	sigHex := hex.EncodeToString(sig)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/johalputt/vayupress/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		rel := map[string]any{
			"tag_name":     "v9.9.9",
			"body":         "notes",
			"html_url":     base + "/rel",
			"published_at": time.Now().Format(time.RFC3339),
			"assets": []map[string]any{
				{"name": "vayupress", "browser_download_url": base + "/bin", "size": len(binary)},
				{"name": "vayupress.sha256", "browser_download_url": base + "/sum", "size": len(sumHex)},
				{"name": "vayupress.sig", "browser_download_url": base + "/sig", "size": len(sigHex)},
				{"name": "vayupress.cosign.bundle", "browser_download_url": base + "/bundle"},
			},
		}
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) { w.Write(binary) })
	mux.HandleFunc("/sum", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sumHex + "  vayupress\n"))
	})
	mux.HandleFunc("/sig", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sigHex)) })
	mux.HandleFunc("/bundle", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{}")) })

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Rewrite GitHub API host to our test server.
	client := &http.Client{Timeout: 5 * time.Second, Transport: rewriteTransport{target: srv.URL}}

	opt := ApplyOptions{
		Current:    "v1.0.0",
		DryRun:     true,
		BinaryPath: "/should/not/be/touched",
	}
	newVersion, err := ApplyVerified(context.Background(), client, "johalputt", "vayupress", opt, nil)
	if err != nil {
		t.Fatalf("ApplyVerified dry-run: %v", err)
	}
	if newVersion != "v9.9.9" {
		t.Errorf("version = %q", newVersion)
	}
}
func TestPreflightMode(t *testing.T) {
	for _, m := range []string{"read-only", "readonly", "quarantined", "maintenance"} {
		if err := PreflightMode(m); err == nil {
			t.Errorf("mode %q should be refused", m)
		}
	}
	for _, m := range []string{"normal", "degraded", "recovery", ""} {
		if err := PreflightMode(m); err != nil {
			t.Errorf("mode %q should be allowed: %v", m, err)
		}
	}
}

// SECTION 5 AUDIT — this test previously asserted the defect.
//
// It was called TestApplyVerifiedUnsignedAllowed and it proved that an
// admin-initiated apply succeeded on checksum verification alone, with no
// signature anywhere in the release. That was the shipped behaviour and the
// panel called it "signed". The behaviour is gone, so the test asserting it is
// inverted rather than deleted: the same release that used to install must now
// be refused.
//
// A checksum published beside the binary it describes, and fetched over the same
// connection, is a self-certificate. Anyone who can publish one can publish the
// other.
func TestAReleaseWithNoSignatureIsRefused(t *testing.T) {
	binary := []byte("\x7fELF unsigned payload")
	sum := sha256.Sum256(binary)
	sumHex := hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		rel := map[string]any{
			"tag_name": "v3.0.0",
			"assets": []map[string]any{
				{"name": "b", "browser_download_url": base + "/bin"},
				{"name": "b.sha256", "browser_download_url": base + "/sum"},
			},
		}
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) { w.Write(binary) })
	mux.HandleFunc("/sum", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sumHex + "  b\n")) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second, Transport: rewriteTransport{target: srv.URL}}
	opt := ApplyOptions{Current: "v1.0.0", DryRun: true}
	_, err := ApplyVerified(context.Background(), client, "o", "r", opt, nil)
	if err == nil {
		t.Fatal("a release carrying no signature was accepted.\n\n" +
			"Its checksum matched, which proves only that the bytes I served are the " +
			"bytes I said I would serve. Anyone who can publish to the release channel " +
			"can publish both.")
	}
	if !strings.Contains(err.Error(), "carries no signature") {
		t.Errorf("refused for the wrong reason: %v", err)
	}
}

// The end-to-end proof that the REAL verifier runs on the apply path.
//
// Other tests substitute verifyReleaseSignature so they can exercise the stages
// after it; this one does not, so a regression that removed the call, or wired
// it to something permissive, fails here.
func TestAReleaseWithAnUnreadableSignatureIsRefused(t *testing.T) {
	binary := []byte("\x7fELF payload")
	sum := sha256.Sum256(binary)
	sumHex := hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		rel := map[string]any{
			"tag_name": "v3.0.0",
			"assets": []map[string]any{
				{"name": "b", "browser_download_url": base + "/bin"},
				{"name": "b.sha256", "browser_download_url": base + "/sum"},
				{"name": "b.cosign.bundle", "browser_download_url": base + "/bundle"},
			},
		}
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) { w.Write(binary) })
	mux.HandleFunc("/sum", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sumHex + "  b\n")) })
	// An attacker who must attach SOMETHING named like a signature.
	mux.HandleFunc("/bundle", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"not":"a sigstore bundle"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second, Transport: rewriteTransport{target: srv.URL}}
	opt := ApplyOptions{Current: "v1.0.0", DryRun: true}
	if _, err := ApplyVerified(context.Background(), client, "o", "r", opt, nil); err == nil {
		t.Fatal("a release whose signature is not a signature was installed.\n\n" +
			"Attaching a file with the right NAME is free. The apply path has to read it.")
	}
}

// The refusal must land even when the release serves no binary at all, so the
// operator is told what is wrong rather than getting a download error.
func TestAReleaseMissingItsAssetsIsRefused(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		rel := map[string]any{
			"tag_name": "v3.0.0",
			"assets": []map[string]any{
				{"name": "b", "browser_download_url": base + "/bin"},
				{"name": "b.sha256", "browser_download_url": base + "/sum"},
			},
		}
		_ = json.NewEncoder(w).Encode(rel)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second, Transport: rewriteTransport{target: srv.URL}}
	opt := ApplyOptions{Current: "v1.0.0", DryRun: true}
	if _, err := ApplyVerified(context.Background(), client, "o", "r", opt, nil); err == nil {
		t.Fatal("expected refusal for a release with no verifiable signature")
	}
}

func TestResolveInstallPath(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "vayupress")
	if err := os.WriteFile(real, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "vayupress-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	// A symlink resolves to the real file (so the atomic swap targets the
	// writable file, not the symlink).
	if got := ResolveInstallPath(link); got != real {
		t.Errorf("ResolveInstallPath(symlink) = %q, want %q", got, real)
	}
	// A plain real path is returned unchanged.
	if got := ResolveInstallPath(real); got != real {
		t.Errorf("ResolveInstallPath(real) = %q, want %q", got, real)
	}
	// A "(deleted)" marker (left after a prior swap) is stripped before resolving.
	if got := ResolveInstallPath(real + " (deleted)"); got != real {
		t.Errorf("ResolveInstallPath(deleted) = %q, want %q", got, real)
	}
	// A non-existent path can't be resolved and is returned as-is.
	missing := filepath.Join(dir, "does-not-exist")
	if got := ResolveInstallPath(missing); got != missing {
		t.Errorf("ResolveInstallPath(missing) = %q, want %q", got, missing)
	}
	// Empty input is returned unchanged.
	if got := ResolveInstallPath(""); got != "" {
		t.Errorf("ResolveInstallPath(\"\") = %q, want \"\"", got)
	}
}

// SECTION 5 PRE-RELEASE PASS — the same defect, still live on the other path.
//
// Enforcement was wired into ApplyVerified and the panel was fixed, and the CLI
// was left exactly as it was. In an operator's voice:
//
//	I read UPGRADING.md, exported VAYU_SELFUPDATE_ENABLED=true, and ran
//	`vayupress update apply`. It told me to pin a release key. I pinned one.
//	Now it tells me the release is missing a .sig asset. There is no value of
//	VAYU_RELEASE_PUBKEY that makes this command work, including not setting it.
//
// PreflightApply demanded a pinned key, and the pinned key then demanded a .sig
// asset the pipeline has never produced. Both refusals, in both directions.
//
// The Ed25519 path is removed rather than repaired. It has never verified a
// single release, cannot without pipeline changes nobody has made, and its only
// effect on a live install is to break the updater of whoever followed the
// documentation. Keeping it as an "optional extra" would have kept that landmine
// armed while the release notes called it optional.
func TestTheCLIPathCanActuallyApplyAnUpdate(t *testing.T) {
	// No key pinned: the CLI opt-in and a sane mode are all it may require.
	if err := PreflightApply(true, "normal"); err != nil {
		t.Errorf("PreflightApply refuses without a pinned key: %v\n\n"+
			"There is then no way to run `vayupress update apply` at all, because "+
			"pinning a key made ApplyVerified demand a .sig asset that has never been "+
			"published. Both directions refused.", err)
	}
	// The opt-in and the mode gate are the CLI's own guards and must survive.
	if err := PreflightApply(false, "normal"); err == nil {
		t.Error("PreflightApply no longer requires VAYU_SELFUPDATE_ENABLED — that is the " +
			"CLI's deliberate opt-in and removing it was not the point")
	}
	if err := PreflightApply(true, "read-only"); err == nil {
		t.Error("PreflightApply no longer refuses read-only mode")
	}
}
