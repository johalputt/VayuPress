package pgp

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
)

// TestWKDDirectMethodServesKey proves the WKD "direct method" is served
// end-to-end: a key generated for alice@example.com is discoverable at
// /.well-known/openpgpkey/hu/<z-base-32-sha1(localpart)> (no domain segment),
// exactly the URL VayuMail-Mobile fetches, and the bytes returned parse back
// into the same OpenPGP entity. This is the same handler mounted on the public
// router via the /.well-known/openpgpkey/* wildcard route (routes.go), so a
// green result here means the app serves the key the mobile client imports.
func TestWKDDirectMethodServesKey(t *testing.T) {
	e := newTestEngine(t)
	const email = "alice@example.com"
	if _, err := e.GenerateKeypair(&PGPUser{UserID: "alice", Name: "Alice", Email: email}); err != nil {
		t.Fatalf("generate: %v", err)
	}

	srv := httptest.NewServer(e.ServeWKD("example.com"))
	defer srv.Close()

	// Direct method: NO domain path segment (advanced would insert /example.com).
	hash := wkdLocalHash("alice")
	directURL := srv.URL + "/.well-known/openpgpkey/hu/" + hash + "?l=alice"

	resp, err := http.Get(directURL)
	if err != nil {
		t.Fatalf("GET %s: %v", directURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("direct-method status = %d, want 200 (route not serving /hu/ without a domain segment)", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("content-type = %q, want application/octet-stream", ct)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing CORS header — cross-origin WKD clients would be blocked")
	}

	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Fatal("empty WKD body")
	}
	// The mobile client imports these raw bytes; confirm they are a valid key
	// carrying the expected identity.
	el, err := openpgp.ReadKeyRing(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("WKD body is not a valid binary key ring: %v", err)
	}
	if len(el) != 1 {
		t.Fatalf("expected exactly one entity, got %d", len(el))
	}
	foundID := false
	for id := range el[0].Identities {
		if strings.Contains(id, email) {
			foundID = true
		}
	}
	if !foundID {
		t.Errorf("served key does not carry identity for %s", email)
	}

	// The advanced method (with domain segment) must also resolve, so both
	// discovery strategies a client may try succeed against the same route.
	advURL := srv.URL + "/.well-known/openpgpkey/example.com/hu/" + hash + "?l=alice"
	ar, err := http.Get(advURL)
	if err != nil {
		t.Fatalf("GET %s: %v", advURL, err)
	}
	defer ar.Body.Close()
	if ar.StatusCode != http.StatusOK {
		t.Fatalf("advanced-method status = %d, want 200", ar.StatusCode)
	}

	// The policy file signals WKD support and must be present for the direct method.
	pr, err := http.Get(srv.URL + "/.well-known/openpgpkey/policy")
	if err != nil {
		t.Fatalf("GET policy: %v", err)
	}
	defer pr.Body.Close()
	if pr.StatusCode != http.StatusOK {
		t.Fatalf("direct-method policy status = %d, want 200", pr.StatusCode)
	}
}
