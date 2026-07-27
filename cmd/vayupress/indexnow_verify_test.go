// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/safefetch"
)

const testINKey = "a1b2c3d4e5f60718293a4b5c6d7e8f90"

// newIndexNowTestApp returns an App whose IndexNow key resolves to key straight
// from config (a.secrets is nil, so indexNowKey falls through to the env value),
// with the resolution cache cleared so each case starts from a known state.
func newIndexNowTestApp(t *testing.T, key string) *App {
	t.Helper()
	prev := config.Cfg.IndexNowKey
	t.Cleanup(func() { config.Cfg.IndexNowKey = prev })
	config.Cfg.IndexNowKey = key
	a := &App{}
	a.invalidateIndexNowKey()
	return a
}

// TestIndexNowKeyPathMatchesOnlyTheKeyFile is the security half of the shield
// bypass. isIndexNowKeyPath disables bot protection for whatever it matches, so
// it must match the single live key file and nothing else — a looser rule (any
// "*.txt", say) would hand an unauthenticated caller a shield-free route into
// the themed 404 renderer, which is far more expensive than the 32-byte write
// this path exists to protect.
func TestIndexNowKeyPathMatchesOnlyTheKeyFile(t *testing.T) {
	a := newIndexNowTestApp(t, testINKey)

	if !a.isIndexNowKeyPath(httptest.NewRequest(http.MethodGet, "/"+testINKey+".txt", nil)) {
		t.Fatal("the live key file must be bypassed — a challenge there is a silent IndexNow outage")
	}
	// The first group is the one that matters: each of these has the exact shape
	// of the key file — single root segment, ".txt", long enough to clear the
	// cheap pre-filter — so only the key comparison itself can reject them. Widen
	// the match to "any root .txt" and every one of these becomes unshielded.
	for _, p := range []string{
		"/00000000000000000000000000000000.txt",      // same shape, different key
		"/" + testINKey[:len(testINKey)-1] + "1.txt", // the live key, last character changed
		"/a1b2c3d4e5f60718293a4b5c6d7e8f9.txt",       // the live key, one character short
		"/" + testINKey + "0.txt",                    // the live key with a character appended
		"/wp-config-backup-2026.txt",                 // the kind of probe this must never fast-path
		"/sub/" + testINKey + ".txt",                 // only the site root is the keyLocation
		"/" + testINKey + ".txt.bak",                 // suffix games
		"/" + testINKey,                              // the key without the extension
		"/" + testINKey + ".TXT",                     // case must match; the served name is exact
		"/other.txt",
		"/robots.txt",
		"/",
		"",
	} {
		if a.isIndexNowKeyPath(httptest.NewRequest(http.MethodGet, "http://x"+p, nil)) {
			t.Errorf("%q must NOT bypass the shield", p)
		}
	}
}

// TestIndexNowKeyPathDeclinesWhenNoKey guards the empty-key case: with no key
// configured there is no file to serve, so nothing may be bypassed. A naive
// `path == "/"+key+".txt"` with an empty key would match "/.txt".
func TestIndexNowKeyPathDeclinesWhenNoKey(t *testing.T) {
	a := newIndexNowTestApp(t, "")
	for _, p := range []string{"/.txt", "/anything.txt", "/" + testINKey + ".txt"} {
		if a.isIndexNowKeyPath(httptest.NewRequest(http.MethodGet, "http://x"+p, nil)) {
			t.Errorf("with no IndexNow key configured, %q must not bypass the shield", p)
		}
	}
}

// TestIndexNowKeyCacheInvalidates checks the cache cannot pin a rotated key.
// A stale key means the file serves the old value, engines fail validation, and
// every submission is voided — so a write must be visible immediately.
func TestIndexNowKeyCacheInvalidates(t *testing.T) {
	a := newIndexNowTestApp(t, testINKey)
	if got := a.cachedIndexNowKey(); got != testINKey {
		t.Fatalf("cachedIndexNowKey() = %q, want %q", got, testINKey)
	}
	rotated := "ffffffffffffffffffffffffffffffff"
	config.Cfg.IndexNowKey = rotated
	if got := a.cachedIndexNowKey(); got != testINKey {
		t.Fatalf("cache should hold the previous value until invalidated, got %q", got)
	}
	a.invalidateIndexNowKey()
	if got := a.cachedIndexNowKey(); got != rotated {
		t.Fatalf("after invalidation cachedIndexNowKey() = %q, want the rotated %q", got, rotated)
	}
	if a.isIndexNowKeyPath(httptest.NewRequest(http.MethodGet, "/"+testINKey+".txt", nil)) {
		t.Error("the retired key file must stop being bypassed once the key is rotated")
	}
}

// TestIndexNowKeyFileVerdict is the check that has to be able to fail. Every
// real-world way the verification file breaks — missing, challenged at the
// edge, or answered with an interstitial that is not the key — must produce a
// non-empty explanation, and only a correctly served file may return "".
func TestIndexNowKeyFileVerdict(t *testing.T) {
	const u = "https://example.com/" + testINKey + ".txt"

	ok := []struct {
		name string
		body string
	}{
		{"exact key", testINKey},
		{"key with the trailing newline a text file normally carries", testINKey + "\n"},
		{"key with surrounding whitespace", "  " + testINKey + "\r\n"},
	}
	for _, c := range ok {
		if got := indexNowKeyFileVerdict(u, testINKey, http.StatusOK, []byte(c.body), nil); got != "" {
			t.Errorf("%s: want pass, got %q", c.name, got)
		}
	}

	bad := []struct {
		name   string
		status int
		body   string
		err    error
		expect string // substring the operator needs to see
	}{
		{"not served", http.StatusNotFound, "not found", nil, "404"},
		{"edge bot challenge", http.StatusForbidden, "<html>Attention Required</html>", nil, "skip/allow rule"},
		{"rate limited", http.StatusTooManyRequests, "", nil, "challenging machine clients"},
		{"shed under load", http.StatusServiceUnavailable, "", nil, "challenging machine clients"},
		{"redirected to a login", http.StatusUnauthorized, "", nil, "instead of 200"},
		{"200 but an interstitial body", http.StatusOK, "<html><body>Just a moment…</body></html>", nil, "not the key"},
		{"200 but the wrong key", http.StatusOK, "00000000000000000000000000000000", nil, "not the key"},
		{"200 but empty", http.StatusOK, "", nil, "not the key"},
		{"domain not publicly resolvable", 0, "", safefetch.ErrBlockedAddress, "public address"},
		{"transport failure", 0, "", errors.New("dial tcp: i/o timeout"), "could not be fetched"},
	}
	for _, c := range bad {
		got := indexNowKeyFileVerdict(u, testINKey, c.status, []byte(c.body), c.err)
		if got == "" {
			t.Errorf("%s: the key file is broken here and the check reported success", c.name)
			continue
		}
		if !strings.Contains(got, c.expect) {
			t.Errorf("%s: verdict %q does not tell the operator about %q", c.name, got, c.expect)
		}
	}
}

// TestIndexNowPendingIsNotShownAsSubmitted covers the reporting half of the
// same bug. HTTP 202 means "received — key validation pending"; if that
// validation then fails the URL is dropped with no error anywhere, so a 202 must
// never render the same confirmed tick as a 200.
func TestIndexNowPendingIsNotShownAsSubmitted(t *testing.T) {
	pending := dbpkg.IndexNowStatus{State: dbpkg.IndexNowPending, HTTPCode: http.StatusAccepted, Detail: "validating your key file"}
	badge := osIndexNowBadge("hello-world", pending, true, false)
	if strings.Contains(badge, "✓ IndexNow") {
		t.Errorf("a pending (HTTP 202) submission must not render the confirmed tick: %s", badge)
	}
	if !strings.Contains(badge, "pending") {
		t.Errorf("a pending submission must say so: %s", badge)
	}

	submitted := dbpkg.IndexNowStatus{State: dbpkg.IndexNowSubmitted, HTTPCode: http.StatusOK}
	if !strings.Contains(osIndexNowBadge("hello-world", submitted, true, false), "✓ IndexNow") {
		t.Error("a confirmed (HTTP 200) submission must still render the tick")
	}
	if dbpkg.IndexNowPending == dbpkg.IndexNowSubmitted {
		t.Fatal("pending and submitted must be distinct states")
	}
}

// TestIndexNowStatusHintSeparates200From202 keeps the operator-facing wording
// honest: 202 is not an acceptance, and describing it as one is what made a
// broken install look healthy.
func TestIndexNowStatusHintSeparates200From202(t *testing.T) {
	got202 := indexNowStatusHint(http.StatusAccepted)
	if got202 == indexNowStatusHint(http.StatusOK) {
		t.Fatalf("202 and 200 must not read the same (both %q)", got202)
	}
	if !strings.Contains(got202, "pending") {
		t.Errorf("the 202 hint must say validation is pending, got %q", got202)
	}
}
