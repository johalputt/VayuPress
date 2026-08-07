// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// The audit finding, in the attacker's voice:
//
//	Your CSRF token was b64url(nonce + "." + HMAC(nonce)), and validation
//	recomputed exactly that. No principal. No issued-at. So a token minted for my
//	own low-privilege mailbox session was byte-for-byte acceptable on a request
//	carrying the administrator's cookie — and it never expired, because MaxAge
//	lived in the browser and nothing server-side remembered when it was issued.
//	(Your middleware said the secret "rotates (every process restart)".
//	InitCSRFSecret has persisted it to disk for years.)
//
// The full chain does NOT stand in this codebase — it needs a cookie write on a
// sibling origin of the panel, and SameSite=Strict blocks every cross-site POST
// on its own. This is depth for the operator who later serves third-party
// content on a subdomain, or has a plain-HTTP sibling host: a network attacker
// can set a parent-domain cookie from there, and RFC 6265 §5.4 puts the longer
// Path first, so r.Cookie returns theirs.

func TestATokenMintedForOnePrincipalIsNotValidForAnother(t *testing.T) {
	InitCSRFSecret()

	mine := GenerateCSRFToken("session-hash-of-the-attacker")
	if mine == "" {
		t.Fatal("no token minted")
	}
	if !ValidateCSRFToken(mine, "session-hash-of-the-attacker") {
		t.Fatal("a token is not valid for the principal it was minted for, so the rest of " +
			"this test proves nothing")
	}

	if ValidateCSRFToken(mine, "session-hash-of-the-administrator") {
		t.Error("a token minted for one session validated against another.\n\n" +
			"Anyone who can obtain a token from a low-privilege session — signing in to " +
			"webmail is enough, the GET branch mints unconditionally — holds a value the " +
			"middleware accepts on the operator's own requests.")
	}
	if ValidateCSRFToken(mine, "") {
		t.Error("a session-bound token validated on a request with NO session")
	}
}

// The sign-in page has no session yet, so an empty binding has to work — but a
// token minted then must not survive into the session that follows it.
func TestASignedOutTokenDoesNotCarryIntoASession(t *testing.T) {
	InitCSRFSecret()

	anon := GenerateCSRFToken("")
	if !ValidateCSRFToken(anon, "") {
		t.Fatal("a token minted before sign-in is not valid before sign-in — the login form " +
			"cannot post")
	}
	if ValidateCSRFToken(anon, "session-hash-after-signing-in") {
		t.Error("the pre-login token stayed valid after signing in. Session fixation by " +
			"another name: an attacker who plants a token before you log in still holds a " +
			"live one afterwards.")
	}
}

// The token now expires server-side. MaxAge:3600 on the cookie was a request to
// the browser, not a rule — a value copied out of the jar was good forever.
func TestATokenExpires(t *testing.T) {
	InitCSRFSecret()

	fresh := GenerateCSRFToken("s")
	if !ValidateCSRFToken(fresh, "s") {
		t.Fatal("a freshly minted token was rejected")
	}
	if csrfTokenTTL > 24*time.Hour {
		t.Errorf("csrfTokenTTL is %v — a token that lives a day is not meaningfully bounded",
			csrfTokenTTL)
	}

	// Mint one stamped in the past by signing the same shape by hand. Nothing here
	// reaches into the clock, so this is the honest way to observe the boundary.
	stale := signCSRFPayload("deadbeef|s|" + itoa64(time.Now().Add(-csrfTokenTTL-time.Minute).Unix()))
	if ValidateCSRFToken(stale, "s") {
		t.Error("a token issued longer ago than csrfTokenTTL still validates; the lifetime " +
			"exists only in the cookie the browser was asked to drop")
	}

	// And one from the future, which is either a clock that moved or a value this
	// server did not mint. Neither earns extra life.
	future := signCSRFPayload("deadbeef|s|" + itoa64(time.Now().Add(2*time.Hour).Unix()))
	if ValidateCSRFToken(future, "s") {
		t.Error("a token stamped in the future validated, so a skewed or forged issued-at " +
			"buys an attacker an extended lifetime")
	}
}

// A token in the pre-binding format must be refused, not accepted for
// compatibility. Accepting it would leave every existing cookie exempt from the
// fix — the shape of "hardening" that changes nothing.
func TestALegacyUnboundTokenIsRefused(t *testing.T) {
	InitCSRFSecret()

	legacy := signCSRFPayload("deadbeefdeadbeef") // nonce only, the old shape
	if ValidateCSRFToken(legacy, "s") {
		t.Error("a token in the old unbound format still validates, so every cookie already " +
			"in a browser is exempt from the binding")
	}
	if ValidateCSRFToken(legacy, "") {
		t.Error("the old format validates whenever there is no session, which is most of the " +
			"public surface")
	}
}

// CSRFBinding must actually distinguish sessions, or every token would share one
// binding and the checks above would be vacuous.
func TestCSRFBindingFollowsTheSessionCookie(t *testing.T) {
	req := func(session string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/os/api/anything", nil)
		if session != "" {
			r.AddCookie(&http.Cookie{Name: SessionCookie, Value: session})
		}
		return r
	}

	a, b := CSRFBinding(req("token-a")), CSRFBinding(req("token-b"))
	if a == b {
		t.Fatalf("two different sessions both bind to %q — the binding is a constant", a)
	}
	if again := CSRFBinding(req("token-a")); again != a {
		t.Errorf("the same session bound to %q then %q; a token would stop matching itself "+
			"between requests and every POST would 403", a, again)
	}
	if CSRFBinding(req("")) != "" {
		t.Error("a request with no session produced a non-empty binding")
	}
	if a == "token-a" {
		t.Error("the binding is the raw session token. That value is HMAC-signed into a " +
			"cookie the page script is meant to read, so it must be hashed, not carried.")
	}
}

// signCSRFPayload mints a token over an arbitrary payload using the real server
// secret, so a test can construct shapes GenerateCSRFToken will not (a stale
// timestamp, the legacy format) and still have them pass the HMAC. Without this
// the tests above would only be observing signature failures.
func signCSRFPayload(payload string) string {
	mac := hmac.New(sha256.New, csrfSecret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(
		[]byte(payload + "." + hex.EncodeToString(mac.Sum(nil))))
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
