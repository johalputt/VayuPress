// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/sitedoc"
)

// editHarbour is a hosted site whose saved draft is twoPageDoc.
func editHarbour(t *testing.T) (*App, string) {
	t.Helper()
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	if _, err := saveSiteDraft(context.Background(), d.ID, twoPageDoc("Harbour"), "test"); err != nil {
		t.Fatal(err)
	}
	return a, d.ID
}

func edit(t *testing.T, a *App, changes string) (map[string]any, error) {
	t.Helper()
	return runTool(t, a, "edit_site_document", `{"host":"harbour.example","changes":`+changes+`}`)
}

func harbourDraft(t *testing.T, id string) sitedoc.Document {
	t.Helper()
	doc, _, ok := siteDraft(context.Background(), id)
	if !ok {
		t.Fatal("no draft")
	}
	return doc
}

// Each kind of change does what it names and leaves the rest of the
// document as it was.
func TestEditSiteDocumentChangesOnlyWhatItNames(t *testing.T) {
	a, id := editHarbour(t)

	if _, err := edit(t, a, `[{"op":"set_section","section":"contact","fields":{"phone":"+44 1234"}}]`); err != nil {
		t.Fatal(err)
	}
	c := harbourDraft(t, id).Pages[0].Sections[1]
	if c.Phone != "+44 1234" || c.Email != "owner@harbour.example" || c.Heading != "Contact" {
		t.Errorf("set_section: phone %q, and the fields it did not name: email %q heading %q", c.Phone, c.Email, c.Heading)
	}

	if _, err := edit(t, a, `[{"op":"set_section","section":"contact","fields":{"email":null}}]`); err != nil {
		t.Fatal(err)
	}
	if c := harbourDraft(t, id).Pages[0].Sections[1]; c.Email != "" || c.Phone != "+44 1234" {
		t.Errorf("a field set to null: email %q phone %q", c.Email, c.Phone)
	}

	if _, err := edit(t, a, `[{"op":"set_page","page":"menu","fields":{"description":"What we cook"}}]`); err != nil {
		t.Fatal(err)
	}
	if p := harbourDraft(t, id).Pages[1]; p.Description != "What we cook" || p.Title != "Menu" || len(p.Sections) != 1 {
		t.Errorf("set_page: %+v", p)
	}

	if _, err := edit(t, a, `[{"op":"add_section","page":"menu","fields":{"id":"hours","kind":"text","body":"Late"}},`+
		`{"op":"add_section","page":"menu","after":"list","fields":{"id":"wine","kind":"text","body":"Wine"}}]`); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range harbourDraft(t, id).Pages[1].Sections {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "list,wine,hours" {
		t.Errorf("add_section at the end, then after list: %v", ids)
	}

	if _, err := edit(t, a, `[{"op":"remove_section","page":"menu","section":"wine"}]`); err != nil {
		t.Fatal(err)
	}
	if s := harbourDraft(t, id).Pages[1].Sections; len(s) != 2 || s[0].ID != "list" || s[1].ID != "hours" {
		t.Errorf("remove_section: %+v", s)
	}

	j, err := edit(t, a, `[{"op":"add_page","fields":{"slug":"about","title":"About","sections":[{"id":"story","kind":"text","body":"Since 1990"}]}}]`)
	if err != nil {
		t.Fatal(err)
	}
	if d := harbourDraft(t, id); len(d.Pages) != 3 || d.Pages[2].Slug != "about" || d.Pages[2].Sections[0].Body != "Since 1990" {
		t.Errorf("add_page: %+v", d.Pages)
	}
	if j["status"] != "draft saved" || len(j["outline"].([]any)) != 3 {
		t.Errorf("the reply has no outline of the result: %v", j)
	}
}

// A list that fails anywhere changes nothing, and says which entry failed
// and why — one seed for each way an entry can fail.
func TestEditSiteDocumentIsAllOrNothing(t *testing.T) {
	a, id := editHarbour(t)
	for _, c := range []struct{ name, changes, want string }{
		{"unknown section", `[{"op":"set_section","section":"contact","fields":{"phone":"1"}},{"op":"set_section","section":"nope","fields":{}}]`,
			`changes[1] (set_section): page "" has no section "nope" (its sections are top, contact)`},
		{"unknown page", `[{"op":"set_page","page":"wine","fields":{}}]`, `there is no page "wine"`},
		{"misspelt field", `[{"op":"set_section","section":"contact","fields":{"phon":"1"}}]`, `unknown field "phon"`},
		{"unknown op", `[{"op":"rename","section":"contact"}]`, `op "rename" is not one of`},
		{"unknown anchor", `[{"op":"add_section","after":"nope","fields":{"id":"x","kind":"text","body":"x"}}]`, `has no section "nope"`},
		{"invalid result", `[{"op":"set_section","section":"top","fields":{"items":[{"title":"x"}]}}]`, "pages[0].sections[0]"},
	} {
		_, err := edit(t, a, c.changes)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
		if d := harbourDraft(t, id); d.Pages[0].Sections[1].Phone != "" {
			t.Errorf("%s: an earlier entry was kept after a later one failed", c.name)
		}
	}
	if _, err := edit(t, a, `[]`); err == nil {
		t.Error("an empty list was accepted")
	}
}

// siteServer answers as the install does for a hosted site's pages: the
// domain from the Host, the home page, the site's stylesheet and icon, and
// the catch-all that serves a document's other pages.
func siteServer(a *App) http.HandlerFunc {
	r := chi.NewRouter()
	r.Use(a.domainMiddleware)
	r.Get("/", a.handleHome)
	r.Get("/site.css", a.handleBizSiteCSS)
	r.Get("/favicon.ico", a.serveFavicon(faviconDarkPNG))
	r.Get("/{slug}", a.handleNotFound)
	return r.ServeHTTP
}

// A publish is followed by a visit to every page, and the reply says what a
// visitor got: one seed per way a visit can fail.
func TestAPublishReportsWhatAVisitorGets(t *testing.T) {
	a, _ := editHarbour(t)
	publish := func() map[string]any {
		t.Helper()
		j, err := edit(t, a, `[{"op":"set_page","page":"menu","fields":{"description":"x"}}],"publish":true`)
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	problems := func(j map[string]any) string {
		var out []string
		for _, p := range j["pages"].([]any) {
			for _, m := range p.(map[string]any)["problems"].([]any) {
				out = append(out, m.(string))
			}
		}
		return strings.Join(out, " | ")
	}

	// Before the server is up there is nothing to visit.
	if j := publish(); j["verified"] != false || !strings.Contains(problems(j), "preview is unavailable") {
		t.Errorf("no server to visit: %v", j)
	}

	a.setRootHandler(siteServer(a))
	j := publish()
	if j["verified"] != true || len(j["pages"].([]any)) != 2 {
		t.Fatalf("a publish every page of which serves: %v", j)
	}

	a.setRootHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	if j := publish(); j["verified"] != false || !strings.Contains(problems(j), "a visitor gets HTTP 502") {
		t.Errorf("pages that answer 502: %v", j)
	}

	a.setRootHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Maison Olive</title></head></html>`))
	}))
	if j := publish(); j["verified"] != false || !strings.Contains(problems(j), `a visitor gets "Maison Olive", not the page just published ("Harbour — Fresh fish")`) {
		t.Errorf("an older page still served: %v", j)
	}

	// What preview_site finds wrong in a page is reported with it.
	inner := siteServer(a)
	a.setRootHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/site.css" {
			http.NotFound(w, r)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	if j := publish(); j["verified"] != false || !strings.Contains(problems(j), "/site.css") {
		t.Errorf("a page whose stylesheet is missing: %v", j)
	}
}
