// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/newsletter"
	"github.com/johalputt/vayupress/internal/settings"
)

// The Newsletter is drawn in the language its reader chose, in both its
// forms, while what belongs to its subscribers is not translated: an address
// is marked as one.
func TestTheNewsletterIsDrawnInThePagesLanguage(t *testing.T) {
	openMigratedDB(t)
	store := newsletter.New(dbpkg.DB)
	a := &App{newsletterStore: store, siteSettings: settings.New(dbpkg.DB), mailer: email.New(email.Config{})}
	h := consoleLanguage(http.HandlerFunc(a.handleOSNewsletter))
	german := func() string {
		r := httptest.NewRequest(http.MethodGet, "/os/newsletter", nil)
		r.Header.Set("Accept-Language", "de")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Body.String()
	}

	if setup := german(); !strings.Contains(setup, "Der Newsletter ist noch nicht eingerichtet") || strings.Contains(setup, "A mail relay for broadcasts") {
		t.Error("the German setup page is not in German")
	}

	ctx := context.Background()
	if err := store.SubscribeConfirmed(ctx, "ann@readers.example"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Subscribe(ctx, "bo@readers.example"); err != nil {
		t.Fatal(err)
	}
	off := german()
	for _, want := range []string{"1 Abonnent, 1 warten auf Bestätigung. Der Versand ist aus", ">Versand einrichten<", "Wartet auf Bestätigung", "Noch nichts gesendet.",
		`<td class="post-row__name" translate="no">ann@readers.example</td>`} {
		if !strings.Contains(off, want) {
			t.Errorf("the German page lacks %q", want)
		}
	}
	a.mailer = email.New(email.Config{Host: "relay.example"})
	on := german()
	for _, want := range []string{"Einen Rundbrief schreiben", "An 1 Abonnenten senden", `aria-label="Schließen"`, "Abonnenten suchen"} {
		if !strings.Contains(on, want) {
			t.Errorf("the German page with a relay lacks %q", want)
		}
	}
	for _, english := range []string{"Compose a broadcast", "Send the test", "Search subscribers", ">Subscribers<", "Waiting to confirm"} {
		if strings.Contains(on, english) {
			t.Errorf("the German page still says %q", english)
		}
	}
}

// A refusal from the Newsletter's endpoints is in the language of the page
// that asked.
func TestTheNewsletterRepliesInThePagesLanguage(t *testing.T) {
	h := consoleLanguage(http.HandlerFunc((&App{}).handleOSNewsletterSendTest))
	r := httptest.NewRequest(http.MethodPost, "/os/api/newsletter/test", strings.NewReader(`{}`))
	r.Header.Set(pageLangHeader, "de")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error.Message != "Der Newsletter ist auf dieser Installation aus." {
		t.Errorf("the refusal to a German page reads %q", rec.Body.String())
	}
}
