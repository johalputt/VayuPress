// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/newsletter"
	"github.com/johalputt/vayupress/internal/settings"
)

// The newsletter's page answers "how many will this reach": the count and who
// is still to confirm beside the title, the month's growth beside what was
// sent, then the subscribers. Composing rises in a sheet, and without a relay
// the page's action is the setting that fixes it.
func TestTheNewsletterIsAnOverviewThatSaysWhetherItCanSend(t *testing.T) {
	openMigratedDB(t)
	store := newsletter.New(dbpkg.DB)
	a := &App{newsletterStore: store, siteSettings: settings.New(dbpkg.DB), mailer: email.New(email.Config{})}

	if setup := getPage(t, a.handleOSNewsletter, "/os/newsletter"); !strings.Contains(setup, `data-page-kind="setup"`) {
		t.Fatal("with no relay and nobody yet, the newsletter is not its Setup page")
	}

	ctx := context.Background()
	if err := store.SubscribeConfirmed(ctx, "ann@readers.example"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Subscribe(ctx, "bo@readers.example"); err != nil {
		t.Fatal(err)
	}
	off := getPage(t, a.handleOSNewsletter, "/os/newsletter")
	for _, want := range []string{`data-page-kind="overview"`, "1 subscriber, 1 waiting to confirm. Sending is off", `href="/os/settings/email">Set up sending`,
		">+2<", "Waiting to confirm", "Nothing sent yet."} {
		if !strings.Contains(off, want) {
			t.Errorf("no relay: missing %q", want)
		}
	}
	if strings.Contains(off, `data-sheet="nl-compose"`) {
		t.Error("without a relay the page still offers a composer that cannot send")
	}
	_, own, _ := strings.Cut(off, `data-page-kind="overview"`)
	own, _, _ = strings.Cut(own, "<dialog")
	for _, not := range []string{`class="badge`, "style=", "<textarea", `type="email"`, "Have left"} {
		if strings.Contains(own, not) {
			t.Errorf("the page itself carries %q", not)
		}
	}

	a.mailer = email.New(email.Config{Host: "relay.example"})
	on := getPage(t, a.handleOSNewsletter, "/os/newsletter")
	if !strings.Contains(on, `data-sheet="nl-compose"`) || !strings.Contains(on, `<dialog class="sa-sheet" id="nl-compose"`) || strings.Contains(on, "Sending is off") {
		t.Error("with a relay the broadcast is not composed in its sheet")
	}
}
