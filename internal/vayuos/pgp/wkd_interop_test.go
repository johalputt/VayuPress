// SPDX-License-Identifier: Apache-2.0

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

// TestWKDNeverServesAnotherDomainsKey is the regression test for a cross-domain
// key disclosure.
//
// ServeWKD took a domain argument and never read it. Lookups matched on the
// local-part hash alone, across every key in the keystore — so on an install
// serving several domains, a request for alice@one.example returned
// alice@two.example's key whenever the first address had no key of its own. The
// hash of "alice" is the same either way, and the domain segment of the request
// was discarded.
//
// The damage is not a 404 in the wrong place. A correspondent's client takes
// that key as the answer and encrypts to it, so mail intended for one person is
// encrypted to a different person's key: the intended recipient cannot read it,
// and someone else can. On a host serving unrelated domains those are unrelated
// people, and WKD's whole purpose is that this happens with no user involvement.
//
// A single-domain install cannot tell the difference, which is why it survived.
func TestWKDNeverServesAnotherDomainsKey(t *testing.T) {
	e := newTestEngine(t)
	// alice exists ONLY at two.example.
	if _, err := e.GenerateKeypair(&PGPUser{
		UserID: "alice2", Name: "Alice Two", Email: "alice@two.example",
	}); err != nil {
		t.Fatalf("generate: %v", err)
	}

	h := e.ServeWKD("one.example")
	hash := wkdLocalHash("alice")

	// Advanced method, asking one.example — where alice has no key at all.
	req := httptest.NewRequest(http.MethodGet,
		"http://openpgpkey.one.example/.well-known/openpgpkey/one.example/hu/"+hash+"?l=alice", nil)
	req.Host = "openpgpkey.one.example"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("a lookup for alice@one.example returned HTTP %d — it served another domain's key, "+
			"so a correspondent would encrypt to the wrong person", rec.Code)
	}

	// The same lookup against the domain that DOES hold the key must still work,
	// or the scoping has simply broken discovery instead of fixing it.
	req = httptest.NewRequest(http.MethodGet,
		"http://openpgpkey.two.example/.well-known/openpgpkey/two.example/hu/"+hash+"?l=alice", nil)
	req.Host = "openpgpkey.two.example"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("lookup for alice@two.example returned HTTP %d, want 200", rec.Code)
	}
	if len(rec.Body.Bytes()) == 0 {
		t.Error("empty key body for the domain that holds the key")
	}
}

// TestWKDDirectMethodScopesByHost — the direct method carries no domain in the
// path, so the host IS the scope. Serving the primary domain's keys on a
// secondary domain's hostname is the same cross-domain disclosure by another
// route.
func TestWKDDirectMethodScopesByHost(t *testing.T) {
	e := newTestEngine(t)
	if _, err := e.GenerateKeypair(&PGPUser{
		UserID: "bob", Name: "Bob", Email: "bob@primary.example",
	}); err != nil {
		t.Fatalf("generate: %v", err)
	}

	h := e.ServeWKD("primary.example")
	hash := wkdLocalHash("bob")

	req := httptest.NewRequest(http.MethodGet,
		"http://secondary.example/.well-known/openpgpkey/hu/"+hash+"?l=bob", nil)
	req.Host = "secondary.example"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("the direct method served primary.example's key on secondary.example's host (HTTP %d)", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet,
		"http://primary.example/.well-known/openpgpkey/hu/"+hash+"?l=bob", nil)
	req.Host = "primary.example"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("the direct method stopped serving its own domain's key (HTTP %d)", rec.Code)
	}
}
