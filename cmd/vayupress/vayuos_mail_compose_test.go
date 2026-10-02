// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
)

// The compose sheet (Mail plan §5): a reply's quote is folded away under it,
// not typed into the message; a forward's history is what is sent, so it
// stays in the body. The sheet minimises, expands and closes; the page has
// only its way back to the mailbox.
func TestTheComposeSheetFoldsAReplysQuote(t *testing.T) {
	a, holder, id := reviewerApp(t, vmail.RoleMailbox)
	get := func(h http.HandlerFunc, target string) string {
		rec := httptest.NewRecorder()
		h(rec, withUser(httptest.NewRequest(http.MethodGet, target, nil), holder))
		return rec.Body.String()
	}
	q := "reply=1&user=dana&folder=Inbox&id=" + id
	sheet := get(a.handleVayuOSComposeSheet, "/os/vayumail/compose/sheet?"+q)
	for _, want := range []string{
		`<h2 class="mx-compose__title" id="mx-compose-title" data-c-title>Re: Keep me</h2>`,
		`<input type="hidden" data-c-to value="x@example.net">`,
		`data-c-body placeholder="Write your message…" aria-label="Message"></textarea>`,
		"Show the quoted message</summary><pre class=\"mx-quoted__text\" data-c-quote>On ",
		"x@example.net wrote:\r\n&gt; body\r\n",
		`data-c-min`, `data-c-max`, `data-c-close`,
		`<input type="checkbox" data-c-encrypt checked hidden>`,
	} {
		if !strings.Contains(sheet, want) {
			t.Errorf("the reply sheet is missing %q:\n%s", want, sheet)
		}
	}
	if strings.Contains(sheet, "mx-compose--page") || strings.Contains(sheet, "<html") {
		t.Error("the sheet is not a bare fragment")
	}

	page := get(a.handleVayuOSCompose, "/os/vayumail/compose?"+q)
	if !strings.Contains(page, `class="mx-compose mx-compose--page"`) || !strings.Contains(page, `href="/os/vayumail/inbox?user=dana" aria-label="Close"`) || strings.Contains(page, "data-c-min") {
		t.Error("the compose page is not the sheet, full width, closing back to the mailbox")
	}

	fwd := get(a.handleVayuOSComposeSheet, "/os/vayumail/compose/sheet?forward=1&user=dana&folder=Inbox&id="+id)
	if strings.Contains(fwd, "data-c-quote") || !strings.Contains(fwd, "---------- Forwarded message ----------") {
		t.Error("a forward's history must stay in the body, which is what it sends")
	}
}

// The recipient check answers as the send would: a key on file can be
// encrypted to, an address with none cannot; and without VayuPGP nothing can.
func TestTheRecipientCheckAnswersAsTheSendWould(t *testing.T) {
	a := &App{}
	ask := func(addr string) map[string]interface{} {
		rec := httptest.NewRecorder()
		a.handleVayuOSComposeRecipient(rec, httptest.NewRequest(http.MethodGet, "/os/vayumail/compose/recipient?addr="+strings.NewReplacer(" ", "%20", "<", "%3C", ">", "%3E", "@", "%40").Replace(addr), nil))
		var out map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %v (%s)", addr, err, rec.Body.String())
		}
		return out
	}
	if got := ask("snd@example.com"); got["key"] != false {
		t.Errorf("without VayuPGP a recipient was said to have a key: %v", got)
	}
	cfg := vpgp.DefaultConfig()
	cfg.StorageDir = t.TempDir()
	cfg.MasterSecret = []byte("test-master-secret-do-not-use-in-prod")
	a.vayuPGP = vpgp.NewEngine(&cfg)
	if err := a.vayuPGP.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.vayuPGP.GenerateKeypair(&vpgp.PGPUser{UserID: "snd", Name: "Snd", Email: "snd@example.com"}); err != nil {
		t.Fatal(err)
	}
	if got := ask("Sandra Nair <snd@example.com>"); got["key"] != true || got["initials"] != "SN" {
		t.Errorf("a recipient with a key on file: %v", got)
	}
	if got := ask("nobody@localhost"); got["key"] != false {
		t.Errorf("a recipient with no key anywhere: %v", got)
	}
}
