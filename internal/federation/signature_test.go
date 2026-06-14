package federation

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// signedRequest builds an inbound POST /inbox request signed with priv over the
// (request-target) host date digest header set, mirroring a real Fediverse peer.
func signedRequest(t *testing.T, priv *rsa.PrivateKey, keyID string, body []byte, date time.Time) *http.Request {
	t.Helper()
	sum := sha256.Sum256(body)
	digest := "SHA-256=" + base64.StdEncoding.EncodeToString(sum[:])
	req := httptest.NewRequest(http.MethodPost, "/u/alice/inbox", bytes.NewReader(body))
	req.Host = "example.test"
	req.Header.Set("Date", date.UTC().Format(http.TimeFormat))
	req.Header.Set("Digest", digest)

	headers := []string{"(request-target)", "host", "date", "digest"}
	signing, err := buildSigningString(req, headers)
	if err != nil {
		t.Fatalf("buildSigningString: %v", err)
	}
	hashed := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, hashed[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	req.Header.Set("Signature", fmt.Sprintf(
		`keyId="%s",algorithm="rsa-sha256",headers="%s",signature="%s"`,
		keyID, strings.Join(headers, " "), base64.StdEncoding.EncodeToString(sig)))
	return req
}

func pubPEM(t *testing.T, priv *rsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal pub: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func TestVerifyRequest_ValidSignature(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	body := []byte(`{"type":"Create","actor":"https://peer.test/u/bob"}`)
	req := signedRequest(t, priv, "https://peer.test/u/bob#key", body, time.Now())

	if err := VerifyRequest(req, body, &priv.PublicKey); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
}

func TestVerifyRequest_TamperedBody(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	body := []byte(`{"type":"Create"}`)
	req := signedRequest(t, priv, "k", body, time.Now())
	// Body changed after signing → digest mismatch.
	if err := VerifyRequest(req, []byte(`{"type":"Delete"}`), &priv.PublicKey); err == nil {
		t.Fatal("tampered body accepted")
	}
}

func TestVerifyRequest_WrongKey(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	body := []byte(`{}`)
	req := signedRequest(t, priv, "k", body, time.Now())
	if err := VerifyRequest(req, body, &other.PublicKey); err == nil {
		t.Fatal("signature verified against wrong key")
	}
}

func TestVerifyRequest_ClockSkew(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	body := []byte(`{}`)
	req := signedRequest(t, priv, "k", body, time.Now().Add(-30*time.Minute))
	if err := VerifyRequest(req, body, &priv.PublicKey); err == nil {
		t.Fatal("stale request accepted")
	}
}

func TestVerifyRequest_MissingSignature(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	req := httptest.NewRequest(http.MethodPost, "/u/alice/inbox", strings.NewReader("{}"))
	if err := VerifyRequest(req, []byte("{}"), &priv.PublicKey); err == nil {
		t.Fatal("unsigned request accepted")
	}
}

func TestParseRSAPublicKeyPEM_RoundTrip(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	pub, err := ParseRSAPublicKeyPEM(pubPEM(t, priv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pub.N.Cmp(priv.PublicKey.N) != 0 {
		t.Fatal("round-tripped key differs")
	}
}

func TestInboxHandler_EnforcesSignature(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	srv := NewServer("https://example.test", "alice", "Alice")
	srv.SetKeyResolver(func(keyID string) (string, error) {
		if keyID == "https://peer.test/u/bob#key" {
			return pubPEM(t, priv), nil
		}
		return "", fmt.Errorf("unknown key %q", keyID)
	})

	body := []byte(`{"type":"Create","actor":"https://peer.test/u/bob"}`)

	// Signed request from a known key is admitted.
	good := signedRequest(t, priv, "https://peer.test/u/bob#key", body, time.Now())
	rec := httptest.NewRecorder()
	srv.InboxHandler(rec, good)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("signed request: want 202, got %d", rec.Code)
	}
	if srv.InboxCount() != 1 {
		t.Fatalf("activity not received: count=%d", srv.InboxCount())
	}

	// Unsigned request is rejected with 401.
	bad := httptest.NewRequest(http.MethodPost, "/u/alice/inbox", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	srv.InboxHandler(rec2, bad)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned request: want 401, got %d", rec2.Code)
	}
	if srv.InboxCount() != 1 {
		t.Fatalf("unsigned activity leaked into inbox: count=%d", srv.InboxCount())
	}
}

func TestInboxHandler_NoResolverStaysOpen(t *testing.T) {
	// Backward-compat: without a resolver, the inbox accepts unsigned requests.
	srv := NewServer("https://example.test", "alice", "Alice")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/u/alice/inbox",
		strings.NewReader(`{"type":"Create"}`))
	srv.InboxHandler(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202 without resolver, got %d", rec.Code)
	}
}
