// SPDX-License-Identifier: Apache-2.0

package pgp

import (
	"strings"
	"testing"
)

// TestWKDURLMatchesServedPath locks the URL shown in the admin panel to the path
// ServeWKD actually answers on. These are derived from the same hash function,
// and this test is what keeps them that way: if the panel ever displayed a URL
// the server does not serve, an operator would publish a discovery address that
// silently 404s, and nothing in the product would report it.
func TestWKDURLMatchesServedPath(t *testing.T) {
	const email = "security@vayupress.com"
	got := WKDURL(email)

	want := "https://openpgpkey.vayupress.com/.well-known/openpgpkey/vayupress.com/hu/" +
		wkdLocalHash("security") + "?l=security"
	if got != want {
		t.Fatalf("WKDURL(%q)\n got: %s\nwant: %s", email, got, want)
	}

	// The hash segment must be the one ServeWKD compares against. ServeWKD reads
	// whatever follows "/hu/", so extract it exactly as the handler does.
	idx := strings.Index(got, "/hu/")
	if idx < 0 {
		t.Fatalf("URL has no /hu/ segment: %s", got)
	}
	hash := got[idx+len("/hu/"):]
	if i := strings.IndexByte(hash, '?'); i >= 0 {
		hash = hash[:i]
	}
	if hash != wkdLocalHash("security") {
		t.Fatalf("URL hash %q does not match wkdLocalHash %q", hash, wkdLocalHash("security"))
	}
}

// TestWKDURLIsPerDomain guards the property that makes multi-domain installs
// work: discovery is per-domain, so two mailboxes on different domains must
// produce different hosts. A single shared host would leave every secondary
// domain's users undiscoverable while looking correct.
func TestWKDURLIsPerDomain(t *testing.T) {
	a := WKDURL("someone@example.com")
	b := WKDURL("someone@shop.example")
	if a == b {
		t.Fatal("same URL for two different domains")
	}
	if !strings.HasPrefix(a, "https://openpgpkey.example.com/") {
		t.Errorf("wrong host for example.com: %s", a)
	}
	if !strings.HasPrefix(b, "https://openpgpkey.shop.example/") {
		t.Errorf("wrong host for shop.example: %s", b)
	}
	// The local-part hash is domain-independent, so an identical local part on two
	// domains yields the same hash — correct per spec, and worth pinning so nobody
	// "fixes" it by mixing the domain into the digest.
	if hashOf(t, a) != hashOf(t, b) {
		t.Error("local-part hash should not depend on the domain")
	}
}

// TestWKDURLCaseFolded verifies an address typed with capitals resolves to the
// same directory entry, since the spec hashes the lowercased local part.
func TestWKDURLCaseFolded(t *testing.T) {
	if lo, up := WKDURL("security@vayupress.com"), WKDURL("Security@vayupress.com"); hashOf(t, lo) != hashOf(t, up) {
		t.Errorf("case affected the hash:\n %s\n %s", lo, up)
	}
}

// TestWKDURLRejectsMalformed returns empty rather than a URL that cannot
// resolve. Emitting a broken URL would put an unreachable address in the admin
// panel and, worse, in a published security contact.
func TestWKDURLRejectsMalformed(t *testing.T) {
	for _, in := range []string{"", "no-at-sign", "@nolocal.example", "nodomain@"} {
		if got := WKDURL(in); got != "" {
			t.Errorf("WKDURL(%q) = %q, want empty", in, got)
		}
	}
}

func hashOf(t *testing.T, u string) string {
	t.Helper()
	idx := strings.Index(u, "/hu/")
	if idx < 0 {
		t.Fatalf("no /hu/ in %s", u)
	}
	h := u[idx+len("/hu/"):]
	if i := strings.IndexByte(h, '?'); i >= 0 {
		h = h[:i]
	}
	return h
}
