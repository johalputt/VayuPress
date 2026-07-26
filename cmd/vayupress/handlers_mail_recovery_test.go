package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The public recovery surface is unauthenticated and exists to bypass a password,
// so these tests are about what it must NOT do: name an account, mint a session,
// or hand a working link to a preloader.

// TestRecoveryNeverRevealsWhetherAMailboxExists is the enumeration guarantee. Mail
// addresses are public, so knowing one must prove nothing — otherwise this
// endpoint is a free directory of every account on the server.
func TestRecoveryNeverRevealsWhetherAMailboxExists(t *testing.T) {
	t.Parallel()
	a := &App{} // no VayuMail: every address is "unknown"

	bodies := map[string]string{}
	for _, addr := range []string{"real@example.com", "nobody-at-all@example.com", "", "not-an-address"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/mail/recover",
			strings.NewReader(url.Values{"email": {addr}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "203.0.113.9:1234"
		a.handleMailRecoverRequest(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("address %q returned HTTP %d; every request must answer identically", addr, rec.Code)
		}
		bodies[addr] = rec.Body.String()
	}
	var first string
	for addr, b := range bodies {
		if first == "" {
			first = b
			continue
		}
		if b != first {
			t.Errorf("the response for %q differs from another address — that is an enumeration oracle", addr)
		}
	}
	// And the words themselves must be conditional.
	if !strings.Contains(first, "If that mailbox exists") {
		t.Errorf("the confirmation asserts a mail was sent instead of hedging:\n%s", first)
	}
}

// TestRecoveryPagesAreNeverCachedOrIndexed. A reset form carries a bearer token
// in its URL: a cached copy is a credential left on a shelf, and an indexed one
// is a credential in a search engine.
func TestRecoveryPagesAreNeverCachedOrIndexed(t *testing.T) {
	t.Parallel()
	a := &App{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/mail/recover", nil)
	a.handleMailRecoverRequest(rec, req)

	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if got := rec.Header().Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
		t.Errorf("X-Robots-Tag = %q, want noindex", got)
	}
	// The token travels in the URL, so it must not leak through Referer.
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
}

// TestResetLinkIsNotConsumedByAGet. Mail clients, antivirus scanners and link
// preloaders all fetch URLs in messages. Burning the token on GET would let them
// destroy the holder's one chance before a human ever saw the page.
func TestResetLinkIsNotConsumedByAGet(t *testing.T) {
	t.Parallel()
	a := &App{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/mail/recover/reset?token=abc123", nil)
	a.handleMailRecoverReset(rec, req)

	body := rec.Body.String()
	// With no VayuMail this renders the unavailable notice, which is fine — the
	// point is that the GET path never reaches ConsumeRecoveryToken. That is
	// enforced structurally below.
	if strings.Contains(body, "no longer valid") {
		t.Error("a GET consumed the token")
	}
}

// TestRecoveryNeverMintsASession. A reset link that signs you in is a
// session-stealing link: anyone who intercepts it gets the account without ever
// knowing the password. The holder must finish by signing in normally.
func TestRecoveryNeverMintsASession(t *testing.T) {
	t.Parallel()
	a := &App{}
	for _, tc := range []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		path string
	}{
		{"request", a.handleMailRecoverRequest, "/mail/recover"},
		{"reset", a.handleMailRecoverReset, "/mail/recover/reset"},
		{"code", a.handleMailRecoverCode, "/mail/recover/code"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, tc.path,
			strings.NewReader("email=a@b.co&password=abcdefgh&confirm=abcdefgh&code=X&token=t"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "203.0.113.9:1234"
		tc.fn(rec, req)
		if cookies := rec.Result().Cookies(); len(cookies) > 0 {
			t.Errorf("%s set a cookie (%v) — recovery must never mint a session", tc.name, cookies)
		}
	}
}

// TestRecoveryFlowIsStructurallySound reads the handler source. Two of the
// guarantees above are about code that only runs with a live database, and a
// runtime test with no VayuMail cannot reach them — but a regression would be
// silent, so they are pinned here instead of left unchecked.
func TestRecoveryFlowIsStructurallySound(t *testing.T) {
	t.Parallel()
	src := repoFile(t, "cmd/vayupress/handlers_mail_recovery.go")
	code := withoutComments(src)

	// The token is consumed only in the POST branch.
	getBranch := code[strings.Index(code, "func (a *App) handleMailRecoverReset"):]
	getBranch = getBranch[:strings.Index(getBranch, "token := strings.TrimSpace")]
	if strings.Contains(getBranch, "ConsumeRecoveryToken") {
		t.Error("the GET branch consumes the reset token — a link preloader would burn it")
	}

	// Every completion path goes through the pipeline; a direct SetPasswordHash
	// here would skip app-password revocation.
	if strings.Contains(code, "SetPasswordHash(") {
		t.Error("recovery calls SetPasswordHash directly, bypassing applyMailPasswordReset")
	}
	if n := strings.Count(code, "applyMailPasswordReset("); n != 2 {
		t.Errorf("expected both completion paths (link, code) to call the pipeline, found %d", n)
	}

	// The request path must audit even when it does nothing, or an attack that
	// only probes leaves no trace.
	if !strings.Contains(code, "vayumail.recovery.requested") {
		t.Error("recovery requests are not audited — a burst against one mailbox is the attack signal")
	}
	// Tor mode has no clearnet egress; sending must be refused rather than
	// silently failing somewhere deep in the relay.
	if !strings.Contains(code, "safefetch.ClearnetBlocked()") {
		t.Error("the link path does not check the Tor-mode egress kill-switch")
	}
}

// TestRecoveryIsNotExemptFromBotProtection. shieldBypassPrefixes exists for
// callers that CANNOT solve a challenge — the WebAPK minting server, MCP clients.
// A locked-out person has a real browser, so a challenge is an inconvenience, not
// an outage, and bot protection in front of a credential-reset endpoint is
// exactly where it belongs. Adding /mail here would be a mistake that looks like
// a convenience.
func TestRecoveryIsNotExemptFromBotProtection(t *testing.T) {
	t.Parallel()
	for _, p := range shieldBypassPrefixes {
		if p == "/mail" || strings.HasPrefix(p, "/mail/") {
			t.Errorf("%q exempts recovery from VayuShield; a human can solve a challenge, so this only helps attackers", p)
		}
	}
}

// TestRecoveryRateLimitsAreTight. This endpoint sends mail to a third party on an
// unauthenticated request, so it is a harassment vector as much as a guessing
// one. The budgets must stay small.
func TestRecoveryRateLimitsAreTight(t *testing.T) {
	t.Parallel()
	if recoveryByAddress.limit > 5 {
		t.Errorf("per-address budget is %d/hour — too generous for an endpoint that mails a third party",
			recoveryByAddress.limit)
	}
	if recoveryByIP.limit > 20 {
		t.Errorf("per-IP budget is %d/hour — too generous for a sweep across addresses", recoveryByIP.limit)
	}
	if recoveryByAddress.window < recoveryByIP.window/2 {
		t.Error("the per-address window is much shorter than the per-IP one; the tighter budget is the address")
	}
}

// TestRecoveryPagesEscapeUntrustedInput. The address is echoed back into the code
// form so a retry does not force retyping, and it comes straight from the
// request.
func TestRecoveryPagesEscapeUntrustedInput(t *testing.T) {
	t.Parallel()
	// The check is for LIVE markup, not for the scary words: "onerror=alert(1)"
	// sitting inside &lt;img …&gt; is inert text and perfectly safe. What must never
	// appear is an unescaped tag, or an unescaped quote that would break out of the
	// value attribute the address is echoed into.
	page := recoveryCodeFormPage(`"><script>alert(1)</script>`, `"><img src=x onerror=alert(1)>`)
	if strings.Contains(page, "<script>") || strings.Contains(page, "<img ") {
		t.Errorf("recovery form reflected a live tag:\n%s", page)
	}
	if !strings.Contains(page, "&lt;script&gt;") || !strings.Contains(page, "&#34;") {
		t.Errorf("recovery form did not escape the input at all:\n%s", page)
	}
	done := recoveryDonePage(`<script>alert(1)</script>@x.test`, mailResetOutcome{})
	if strings.Contains(done, "<script>") {
		t.Errorf("completion page reflected an unescaped address:\n%s", done)
	}
	notice := recoveryNoticePage("<b>t</b>", "<i>m</i>")
	if strings.Contains(notice, "<b>") || strings.Contains(notice, "<i>") {
		t.Errorf("notice page reflected unescaped markup:\n%s", notice)
	}
}

// TestCompletionPageReportsWhatWasRevoked. The counts are the holder's only way
// to notice devices they do not recognise — the signal that someone else was in
// the account.
func TestCompletionPageReportsWhatWasRevoked(t *testing.T) {
	t.Parallel()
	page := recoveryDonePage("user@example.com", mailResetOutcome{
		AppPasswordsRevoked: 4, SessionsRevoked: 1, QueueHeld: 2,
	})
	for _, want := range []string{"4 connected apps", "1 web session", "2 unsent messages"} {
		if !strings.Contains(page, want) {
			t.Errorf("completion page is missing %q:\n%s", want, page)
		}
	}
	// A partial failure must never hide behind a success page.
	warned := recoveryDonePage("user@example.com", mailResetOutcome{Problems: []string{"revoke app passwords: boom"}})
	if !strings.Contains(warned, "did not complete") {
		t.Errorf("a partially failed reset rendered as a clean success:\n%s", warned)
	}
}
