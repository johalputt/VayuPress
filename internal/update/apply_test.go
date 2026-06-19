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
	"strings"
	"testing"
	"time"
)

func TestPreflightApply(t *testing.T) {
	const pk = "deadbeef"

	if err := PreflightApply(false, "normal", pk); err == nil {
		t.Error("enabled=false should fail")
	}
	if err := PreflightApply(true, "read-only", pk); err == nil {
		t.Error("read-only mode should fail")
	}
	if err := PreflightApply(true, "quarantined", pk); err == nil {
		t.Error("quarantined mode should fail")
	}
	if err := PreflightApply(true, "maintenance", pk); err == nil {
		t.Error("maintenance mode should fail")
	}
	if err := PreflightApply(true, "normal", ""); err == nil {
		t.Error("empty pubkey should fail")
	}
	if err := PreflightApply(true, "normal", pk); err != nil {
		t.Errorf("all-good should pass: %v", err)
	}
}

func TestApplyVerifiedDryRun(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
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
				{"name": "vayupress.tar.gz", "browser_download_url": base + "/bin", "size": len(binary)},
				{"name": "vayupress.sha256", "browser_download_url": base + "/sum", "size": len(sumHex)},
				{"name": "vayupress.sig", "browser_download_url": base + "/sig", "size": len(sigHex)},
			},
		}
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) { w.Write(binary) })
	mux.HandleFunc("/sum", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sumHex + "  vayupress.tar.gz\n"))
	})
	mux.HandleFunc("/sig", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sigHex)) })

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Rewrite GitHub API host to our test server.
	client := &http.Client{Timeout: 5 * time.Second, Transport: rewriteTransport{target: srv.URL}}

	opt := ApplyOptions{
		Current:    "v1.0.0",
		DryRun:     true,
		PubKeyHex:  hex.EncodeToString(pub),
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

func TestApplyVerifiedBadSignature(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	_, priv2, _ := ed25519.GenerateKey(rand.Reader) // wrong key
	binary := []byte("payload")
	sum := sha256.Sum256(binary)
	sumHex := hex.EncodeToString(sum[:])
	badSig := hex.EncodeToString(ed25519.Sign(priv2, sum[:]))

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		rel := map[string]any{
			"tag_name": "v2.0.0",
			"assets": []map[string]any{
				{"name": "b.tar.gz", "browser_download_url": base + "/bin"},
				{"name": "b.sha256", "browser_download_url": base + "/sum"},
				{"name": "b.sig", "browser_download_url": base + "/sig"},
			},
		}
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) { w.Write(binary) })
	mux.HandleFunc("/sum", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sumHex)) })
	mux.HandleFunc("/sig", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(badSig)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second, Transport: rewriteTransport{target: srv.URL}}
	opt := ApplyOptions{Current: "v1.0.0", DryRun: true, PubKeyHex: hex.EncodeToString(pub)}
	_, err := ApplyVerified(context.Background(), client, "o", "r", opt, nil)
	if err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("expected signature failure, got %v", err)
	}
}
