// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/sitedoc"
)

// hostedSite registers a hosted domain serving a template website, on a real
// migrated database.
func hostedSite(t *testing.T, a *App, host string) domain.Domain {
	t.Helper()
	ctx := context.Background()
	d, err := a.domains.Create(ctx, host, "blog", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.domains.SetSite(ctx, d.ID, domain.SiteConfig{Mode: "business", Template: "bistro"}); err != nil {
		t.Fatal(err)
	}
	if err := a.domains.SetSyncState(ctx, d.ID, domain.SyncApproved); err != nil {
		t.Fatal(err)
	}
	d, err = a.domains.ByID(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func siteApp(t *testing.T) *App {
	t.Helper()
	openMigratedDB(t)
	return &App{domains: domain.New(dbpkg.DB, dbpkg.Reader())}
}

// publishRow stores a revision the way a publish will, for serving tests.
func publishRow(t *testing.T, scope, raw string) {
	t.Helper()
	if _, err := dbpkg.WDB.Exec(`INSERT INTO site_revisions(domain_id,doc,published_at) VALUES(?,?,?)`,
		scope, raw, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func docJSON(t *testing.T, d sitedoc.Document) string {
	t.Helper()
	b, err := sitedoc.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func twoPageDoc(name string) sitedoc.Document {
	return sitedoc.Document{V: sitedoc.Version, Name: name, Pages: []sitedoc.Page{
		{Slug: "", Sections: []sitedoc.Section{{ID: "top", Kind: sitedoc.KindHero, Body: "Fresh fish"},
			{ID: "contact", Kind: sitedoc.KindContact, Heading: "Contact", Email: "owner@harbour.example"}}},
		{Slug: "menu", Title: "Menu", InNav: true, Sections: []sitedoc.Section{{ID: "list", Kind: sitedoc.KindText, Body: "Crab on toast"}}},
	}}
}

func siteGet(a *App, d domain.Domain, host, path string, h func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "http://"+host+path, nil)
	r = r.WithContext(context.WithValue(r.Context(), ctxKeyDomain{}, d))
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

// A published document is what the site serves — its home at /, its other
// pages at /<slug> — and a slug it does not have is still a 404.
func TestAPublishedDocumentIsWhatTheSiteServes(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	if rec := siteGet(a, d, d.Host, "/", a.handleHome); !strings.Contains(rec.Body.String(), "Maison Olive") {
		t.Fatalf("before any publish the site should serve its template sample; got %d", rec.Code)
	}
	publishRow(t, d.ID, docJSON(t, twoPageDoc("Harbour & Co")))

	home := siteGet(a, d, d.Host, "/", a.handleHome).Body.String()
	if !strings.Contains(home, "Harbour &amp; Co") || strings.Contains(home, "Maison Olive") {
		t.Errorf("home does not serve the published document:\n%s", home)
	}
	menu := siteGet(a, d, d.Host, "/menu", a.handleNotFound)
	if menu.Code != http.StatusOK || !strings.Contains(menu.Body.String(), "Crab on toast") {
		t.Errorf("/menu: %d, want the document's Menu page", menu.Code)
	}
	if rec := siteGet(a, d, d.Host, "/wine", a.handleNotFound); rec.Code != http.StatusNotFound {
		t.Errorf("/wine, which the document does not have: %d, want 404", rec.Code)
	}
}

// A site switched back to its blog keeps its published document for later,
// but serves none of its pages meanwhile: /menu is the blog's to answer.
func TestADocumentIsServedOnlyWhileTheSiteServesItsWebsite(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	publishRow(t, d.ID, docJSON(t, twoPageDoc("Harbour & Co")))
	if err := a.domains.SetSite(context.Background(), d.ID, domain.SiteConfig{Mode: "blog"}); err != nil {
		t.Fatal(err)
	}
	d, _ = a.domains.ByID(context.Background(), d.ID)
	if rec := siteGet(a, d, d.Host, "/menu", a.handleNotFound); rec.Code != http.StatusNotFound {
		t.Errorf("a site serving its blog answered /menu from its document: %d", rec.Code)
	}
}

// Each site serves its own document. One published for site A must not
// appear on site B, nor on the primary.
func TestADocumentBelongsToItsOwnSite(t *testing.T) {
	a := siteApp(t)
	siteA := hostedSite(t, a, "a.example")
	siteB := hostedSite(t, a, "b.example")
	publishRow(t, siteA.ID, docJSON(t, twoPageDoc("Only On A")))
	if body := siteGet(a, siteB, siteB.Host, "/", a.handleHome).Body.String(); strings.Contains(body, "Only On A") {
		t.Error("site A's document was served on site B")
	}
	if rec := siteGet(a, siteB, siteB.Host, "/menu", a.handleNotFound); rec.Code != http.StatusNotFound {
		t.Errorf("site A's /menu page answered on site B: %d", rec.Code)
	}
	if _, ok := publishedSiteDoc(context.Background(), ""); ok {
		t.Error("the primary found a document although only site A published one")
	}
}

// A revision that no longer parses must not take the site down: it serves
// its legacy content, which is at least the site as it was.
func TestAnUnreadableRevisionServesTheLegacySite(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	publishRow(t, d.ID, `{"v":1,"name":"x","pages":[`)
	rec := siteGet(a, d, d.Host, "/", a.handleHome)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Maison Olive") {
		t.Errorf("an unparseable revision gave %d without the legacy site", rec.Code)
	}
}

// The canonical address and social cards come from the registered domain. A
// Host header is the client's to choose, and a canonical built from it is a
// canonical anyone can point at their own site.
func TestTheCanonicalAddressIsTheRegisteredDomain(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	body := siteGet(a, d, "evil.example", "/", a.handleHome).Body.String()
	if !strings.Contains(body, `<link rel="canonical" href="https://harbour.example/">`) {
		t.Errorf("canonical is not the registered domain:\n%s", body)
	}
	if strings.Contains(body, "evil.example") {
		t.Error("the request's Host header reached the page")
	}
}

// A message from a hosted site's form records the site, and is emailed to the
// address that site publishes, signed with its own name.
func TestAHostedSitesContactMessagesAreItsOwn(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	publishRow(t, d.ID, docJSON(t, twoPageDoc("Harbour & Co")))
	r := httptest.NewRequest(http.MethodPost, "http://harbour.example/api/v1/contact",
		strings.NewReader(`{"name":"Ann","email":"ann@visitor.example","message":"Table for two?","page":"/"}`))
	r = r.WithContext(context.WithValue(r.Context(), ctxKeyDomain{}, d))
	r.RemoteAddr = "203.0.113.9:4000"
	rec := httptest.NewRecorder()
	a.handleContactSubmit(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit: %d %s", rec.Code, rec.Body)
	}
	var got string
	if err := dbpkg.DB.QueryRow(`SELECT domain_id FROM contact_messages WHERE email='ann@visitor.example'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != d.ID {
		t.Errorf("the message was stored for site %q, want %q", got, d.ID)
	}
	to, name := a.contactRecipient(r, d.ID, d.Host)
	if to != "owner@harbour.example" || name != "Harbour & Co" {
		t.Errorf("hosted site's messages go to %q signed %q, want its own address and name", to, name)
	}
	if to, _ := a.contactRecipient(r, "", ""); to == "owner@harbour.example" {
		t.Error("the primary's messages went to a hosted site's address")
	}

	inbox := httptest.NewRecorder()
	a.handleOSMessages(inbox, httptest.NewRequest(http.MethodGet, "/os/messages", nil))
	body := inbox.Body.String()
	if !strings.Contains(body, `<div class="row-meta">harbour.example</div>`) {
		t.Error("the inbox does not say which site the message came from")
	}
	if !strings.Contains(body, `href="https://harbour.example/"`) {
		t.Error("the inbox links the message's page on the console's host instead of its site")
	}
}

// A published brand reaches the site's stylesheet, and the page's link to it
// changes when the brand does — a browser holding the old stylesheet must not
// keep showing the old colours for five minutes.
func TestTheBrandReachesTheStylesheetAndBustsItsCache(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	sheetOf := func() string {
		m := regexp.MustCompile(`href="(/site\.css\?v=[^"]+)"`).FindStringSubmatch(siteGet(a, d, d.Host, "/", a.handleHome).Body.String())
		if m == nil {
			t.Fatal("the page links no stylesheet")
		}
		return m[1]
	}
	before := sheetOf()
	doc := twoPageDoc("Harbour")
	doc.Style = &sitedoc.Style{Accent: "#1d4ed8", Font: "serif"}
	publishRow(t, d.ID, docJSON(t, doc))
	after := sheetOf()
	if after == before || !strings.HasPrefix(after, "/site.css?v=bistro.") {
		t.Errorf("stylesheet link %q → %q: a brand change must change it, and keep the design key first", before, after)
	}
	css := siteGet(a, d, d.Host, after, a.handleBizSiteCSS).Body.String()
	if !strings.Contains(css, "body.vb--bistro") || !strings.Contains(css, "--vb-accent:#1d4ed8") {
		t.Error("the site stylesheet lacks the design or the published brand")
	}
	if strings.Index(css, "--vb-accent:#1d4ed8") < strings.Index(css, "body.vb--bistro") {
		t.Error("the brand comes before the design, so the design would win")
	}
}
