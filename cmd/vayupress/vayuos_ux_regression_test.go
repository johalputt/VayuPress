// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_ux_regression_test.go — regression pins for the VayuMail/VayuTalk UX
// pass (docs/UX-AUDIT-2026-08-VAYUMAIL-VAYUTALK.md, Phase 0).
//
// Every test here guards a behaviour that was actively wrong and whose failure
// mode is invisible in normal use: a burned message that comes back, a "Copied"
// that copied nothing, a batch that half-failed and said nothing, a draft that
// quietly dropped its recipients. They assert on the source the way the rest of
// the console-contract tests do, because the failure each one describes is a
// property of the shipped client, not of a single handler call.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

func talkJS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "static", "js", "admin-os-talk.js"))
	if err != nil {
		t.Fatalf("read admin-os-talk.js: %v", err)
	}
	return string(b)
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestABurnedTalkMessageCannotComeBackOnReconnect pins the P0.
//
// The console never acks (the phone app is the authoritative reader), so the
// server re-flushes the queue on every SSE reconnect. If the client forgets an
// expired message's id, that flush re-adds a message the user just watched burn
// — with a fresh countdown. Dropping the id on expiry looked like tidy
// bookkeeping and was the single worst bug in the app.
func TestABurnedTalkMessageCannotComeBackOnReconnect(t *testing.T) {
	js := withoutComments(talkJS(t))
	if strings.Contains(js, "delete byId[m.id]") {
		t.Error("expiring a message must NOT delete its id from byId — the server re-flushes the queue on reconnect and the message resurrects")
	}
	if !strings.Contains(js, "m.expired = true") {
		t.Error("an expired message must be kept as an id tombstone so the reconnect flush is a no-op")
	}
	if !strings.Contains(js, "if (m.id && byId[m.id]) return") {
		t.Error("the dedupe-by-id guard is what makes the tombstone work; it must stay")
	}
}

// TestAFailedTalkSendKeepsTheTextAndOffersRetry pins the recovery path: a send
// that failed used to leave a one-line status and a cleared composer, so the
// message had to be retyped from memory.
func TestAFailedTalkSendKeepsTheTextAndOffersRetry(t *testing.T) {
	js := withoutComments(talkJS(t))
	for _, want := range []string{"function offerRetry(", "vtalk-bubble-retry", "function postSend("} {
		if !strings.Contains(js, want) {
			t.Errorf("the failed-send path is missing %q — a dropped connection must be one click from recovery", want)
		}
	}
	// Both failure branches (server refusal and network error) must offer it.
	if n := strings.Count(js, "offerRetry(m, p)"); n < 3 {
		t.Errorf("offerRetry is reached %d time(s); both the error and the network branch must attach it", n)
	}
}

// TestATalkSafetyNumberChangeIsLoud pins the one event an E2E product must never
// swallow: a peer's key changing under a contact the user already verified.
func TestATalkSafetyNumberChangeIsLoud(t *testing.T) {
	js := withoutComments(talkJS(t))
	if !strings.Contains(js, "keyChanged") {
		t.Error("a changed fingerprint must raise a warning — silently dropping the verification is how a MITM stays invisible")
	}
	if !strings.Contains(js, "vtalk-warn--key") {
		t.Error("the changed-key warning needs its own (danger-coloured) class, not the ordinary reachability warning")
	}
}

// TestTalkKeyWarningIsStyled keeps the CSS contract honest for the danger variant.
func TestTalkKeyWarningIsStyled(t *testing.T) {
	block := cssBlock(t, adminOSCSS(t), ".vp-os .vtalk-warn--key {")
	if !strings.Contains(block, "var(--danger)") {
		t.Errorf(".vtalk-warn--key must use the danger palette; block was:\n%s", block)
	}
}

// TestTalkConversationRowsAreKeyboardReachable — the rail was the only way to
// change conversation and it was a mouse-only <li>.
func TestTalkConversationRowsAreKeyboardReachable(t *testing.T) {
	js := withoutComments(talkJS(t))
	for _, want := range []string{"setAttribute('role', 'button')", "item.tabIndex = 0", "aria-current"} {
		if !strings.Contains(js, want) {
			t.Errorf("conversation rows are missing %q — keyboard users cannot switch conversations without it", want)
		}
	}
	if !strings.Contains(adminOSCSS(t), ".vp-os .vtalk-convo:focus-visible") {
		t.Error("a focusable row needs a visible focus ring, or the tab stop is invisible")
	}
}

// TestTalkRotateAsksBeforeDestroyingTheCode — rotating is irreversible (everyone
// holding the old code loses reach) and it used to happen on a single click.
func TestTalkRotateAsksBeforeDestroyingTheCode(t *testing.T) {
	page := withoutComments(readFileString(t, "vayuos_talk.go"))
	if !strings.Contains(page, "window.confirm(") || !strings.Contains(page, "cannot be undone") {
		t.Error("rotating the anonymous code must confirm first and say the change is irreversible")
	}
}

// TestCopyButtonsNeverLie — the Tor world's primary "share my code" action used
// to print "Copied" when navigator.clipboard did not exist (plain-http .onion),
// so it silently copied nothing. A rejection must not be reported as success.
func TestCopyButtonsNeverLie(t *testing.T) {
	page := withoutComments(readFileString(t, "vayuos_talk.go"))
	if strings.Contains(page, ".then(d,d)") {
		t.Error("passing the success handler as the rejection handler reports a failed copy as success")
	}
	if !strings.Contains(page, "Copy failed") {
		t.Error("a failed clipboard write must say so; the user needs to know to copy by hand")
	}
}

// TestFederationToggleDoesNotReloadThePage — flipping a non-destructive setting
// used to throw away the page (and with it the in-memory conversations).
func TestFederationToggleDoesNotReloadThePage(t *testing.T) {
	page := readFileString(t, "vayuos_talk.go")
	start := strings.Index(page, "var fed=document.getElementById('vtalk-fed')")
	if start < 0 {
		t.Fatal("the federation toggle handler is missing")
	}
	end := start + 1200
	if end > len(page) {
		end = len(page)
	}
	if strings.Contains(page[start:end], "location.reload") {
		t.Error("the federation toggle must patch the switch in place — reloading discards every open chat for a settings flip")
	}
}

// TestMailAccountDeleteGoesThroughTheEngine pins the privacy fix: the store's
// Delete clears SQLite rows only, and mail left on disk is inherited whole by
// whoever is given the address next. The panel and the JSON endpoint must not
// disagree about that.
func TestMailAccountDeleteGoesThroughTheEngine(t *testing.T) {
	src := withoutComments(readFileString(t, "vayuos_mail_accounts.go"))
	if !strings.Contains(src, "a.vayuMail.DeleteMailbox(") {
		t.Error("the panel's Delete must call DeleteMailbox so the mail is set aside and a reissued address cannot inherit it")
	}
	if strings.Contains(src, "accts.Delete(r.Context(), email)") {
		t.Error("the store-only Delete leaves the messages on disk for the next holder of the address")
	}
	// And the confirm text has to describe what actually happens.
	if !strings.Contains(src, "kept aside") {
		t.Error("the delete confirmation must say the mail is kept aside rather than promising it is destroyed")
	}
}

// TestMailDraftKeepsCcAndBcc pins the silent-recipient-loss fix end to end: the
// handler accepts them, the engine writes them as headers, and reopening a draft
// reads them back into the composer.
func TestMailDraftKeepsCcAndBcc(t *testing.T) {
	src := readFileString(t, "vayuos_mail.go")
	if !strings.Contains(src, `json:"cc"`) {
		t.Error("the draft endpoint must accept cc")
	}
	if !strings.Contains(src, `json:"bcc"`) {
		t.Error("the draft endpoint must accept bcc")
	}
	if !strings.Contains(src, `msg.Header.Get("Cc")`) || !strings.Contains(src, `msg.Header.Get("Bcc")`) {
		t.Error("reopening a draft must restore Cc and Bcc, or the composer sends to fewer people than the draft described")
	}
	if !strings.Contains(src, `data-c-cc value="`) || !strings.Contains(src, `data-c-bcc value="`) {
		t.Error("the composer's hidden Cc/Bcc inputs must carry the draft's recipients")
	}
	// The draft message builder lives in drafts.go (it grew MIME attachment
	// handling); engine.go's SaveDraft delegates to it.
	engine := readFileString(t, filepath.Join("..", "..", "internal", "vayuos", "mail", "engine.go"))
	drafts := readFileString(t, filepath.Join("..", "..", "internal", "vayuos", "mail", "drafts.go"))
	if !strings.Contains(engine, "return e.SaveDraftWithAttachments(") {
		t.Error("SaveDraft must delegate to the attachment-aware builder")
	}
	if !strings.Contains(drafts, `"Cc: "`) || !strings.Contains(drafts, `"Bcc: "`) {
		t.Error("SaveDraftWithAttachments must write Cc/Bcc headers")
	}
	if !strings.Contains(withoutComments(readFileString(t, filepath.Join("..", "..", "static", "js", "admin-os-mail.js"))), "fieldWrap.hidden = false") {
		t.Error("a prefilled Cc/Bcc must un-hide its field, or the restored recipients are invisible")
	}
}

// TestMailBulkActionReportsPartialFailure — every per-message error used to be
// discarded, so a batch that half-applied looked exactly like one that worked.
// Driven for real: one message that exists and one id that does not.
func TestMailBulkActionReportsPartialFailure(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	raw := []byte("From: a@example.org\r\nTo: dana@example.com\r\nSubject: hi\r\n\r\nx\r\n")
	if _, err := a.vayuMail.DeliverInbound("a@example.org", "dana@example.com", raw); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	msgs, _ := a.vayuMail.Inbox("example.com", "dana")
	if len(msgs) != 1 {
		t.Fatalf("seed: %d messages, want 1", len(msgs))
	}
	post := func(ids ...string) *httptest.ResponseRecorder {
		vals := url.Values{"user": {"dana"}, "folder": {"Inbox"}, "action": {"mark"}, "mark": {"read"}, "id": ids}
		req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/inbox/action", strings.NewReader(vals.Encode())), admin)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		a.handleVayuOSInboxAction(rec, req)
		return rec
	}

	if got := post(msgs[0].ID).Header().Get("HX-Trigger"); got != "" {
		t.Errorf("a batch that fully applied must not warn, got HX-Trigger %q", got)
	}
	got := post(msgs[0].ID, "1700000000.gone.host").Header().Get("HX-Trigger")
	if got != `{"vm-inbox-result":{"done":1,"failed":1}}` {
		t.Errorf("a half-applied batch must say so, got HX-Trigger %q", got)
	}

	js := withoutComments(mailJS(t))
	if !strings.Contains(js, "addEventListener('vm-inbox-result'") {
		t.Error("the console must turn that event into a visible warning, or counting the failures changes nothing")
	}
}

// TestMailContactsExplainARefusedSave — an invalid address used to re-render the
// panel unchanged with the typed value gone: indistinguishable from a dead button.
// The fix echoes what was typed back into value="…", which makes the echo an
// injection point, so each field gets its own seed that breaks out of the
// attribute if, and only if, that field is left unescaped.
func TestMailContactsExplainARefusedSave(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	vals := url.Values{"user": {"dana"}, "email": {`x"onfocus=alert(1)`}, "name": {`"autofocus x="`}}
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/contacts/add", strings.NewReader(vals.Encode())), admin)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.handleVayuOSContactAdd(rec, req)
	body := rec.Body.String()

	if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, "valid email address") {
		t.Errorf("a refused save must say why, got: %s", body)
	}
	if !strings.Contains(body, "x&#34;onfocus=alert(1)") {
		t.Error("the typed address must come back, so the operator can correct it rather than retype it")
	}
	if strings.Contains(body, `x"onfocus`) {
		t.Error("the typed address broke out of its value attribute")
	}
	if strings.Contains(body, `"autofocus`) {
		t.Error("the typed name broke out of its value attribute")
	}
}

// TestContactErrTextSpeaksToTheOperator keeps store wording out of the panel.
func TestContactErrTextSpeaksToTheOperator(t *testing.T) {
	bad := contactErrText(vmail.ErrBadContact)
	if !strings.Contains(bad, "valid email address") {
		t.Errorf("a bad address should say so plainly, got %q", bad)
	}
	other := contactErrText(errors.New("vayumail: no storage"))
	if strings.Contains(other, "vayumail:") {
		t.Errorf("the operator must not be shown the store's internal wording, got %q", other)
	}
	if strings.Contains(bad, "vayumail:") || strings.Contains(other, "vayumail:") {
		t.Error("no contact error should leak the package prefix")
	}
}

// TestMailPasswordIsSetThroughAMaskedModal — the old control was window.prompt,
// which showed the password in cleartext with no confirmation.
func TestMailPasswordIsSetThroughAMaskedModal(t *testing.T) {
	js := withoutComments(mailJS(t))
	if strings.Contains(js, "window.prompt('New password") {
		t.Error("setting a mailbox password must not use window.prompt")
	}
	if !strings.Contains(js, "function openMailboxPasswordModal(") {
		t.Error("the masked password modal is missing")
	}
	if !strings.Contains(js, "i.type = 'password'") {
		t.Error("the password field must be masked by default")
	}
	if !strings.Contains(js, "'mb-pass-confirm'") {
		t.Error("the modal must confirm the password, or a typo locks the holder out")
	}
	if !strings.Contains(js, "ev.key === 'Escape'") {
		t.Error("the modal must close on Escape like the 2FA modal does")
	}
}

// TestMailSaveAsDraftNavigatesOnSuccessOnly — the old fixed 700ms timer sent the
// operator to an empty Drafts folder whenever the save was slower than that, or
// had failed outright.
func TestMailSaveAsDraftNavigatesOnSuccessOnly(t *testing.T) {
	js := withoutComments(mailJS(t))
	if strings.Contains(js, "}, 700);") {
		t.Error("the draft button must not navigate on a fixed timer")
	}
	if !strings.Contains(js, "saveDraft(false, function (ok, nothing)") {
		t.Error("navigation must hang off the save result")
	}
}

// TestMailToastKindsAreReal — vpToast only styles ok/error/info/warn, so the
// 'success' kind this module passed rendered an unstyled toast.
func TestMailToastKindsAreReal(t *testing.T) {
	js := withoutComments(mailJS(t))
	if strings.Contains(js, "'success'") {
		t.Error("vpToast has no 'success' kind — use 'ok'")
	}
}
