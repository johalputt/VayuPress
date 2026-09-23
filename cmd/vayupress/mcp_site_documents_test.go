// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/bizsite"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/users"
)

// runTool calls one registered connector tool's handler with JSON arguments.
func runTool(t *testing.T, a *App, name, args string) (map[string]any, error) {
	t.Helper()
	for _, tl := range a.buildMCPServer().Tools() {
		if tl.Name != name {
			continue
		}
		out, err := tl.Handler(context.Background(), json.RawMessage(args))
		if err != nil {
			return nil, err
		}
		var j map[string]any
		if err := json.Unmarshal([]byte(out), &j); err != nil {
			t.Fatalf("%s returned non-JSON: %s", name, out)
		}
		return j, nil
	}
	t.Fatalf("no tool named %s", name)
	return nil, nil
}

func docArg(t *testing.T, name string) string {
	t.Helper()
	b, err := json.Marshal(twoPageDoc(name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// An assistant reads the site as a document, saves a draft (refused with the
// field that failed when wrong), publishes it, and restores an earlier one —
// through the same validator and publish path as the editor.
func TestTheConnectorEditsASiteAsADocument(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")

	j, err := runTool(t, a, "get_site_document", `{"host":"harbour.example"}`)
	if err != nil || j["source"] != "legacy" || j["doc"].(map[string]any)["name"] != "Maison Olive" {
		t.Fatalf("get_site_document on an untouched site: %v %v", err, j)
	}

	bad := `{"host":"harbour.example","doc":{"v":1,"name":"x","pages":[{"slug":"","sections":[` +
		`{"id":"g","kind":"gallery","images":[{"src":"/media/a.webp"}]}]}]}}`
	if _, err := runTool(t, a, "save_site_draft", bad); err == nil || !strings.Contains(err.Error(), "pages[0].sections[0].images[0].alt") {
		t.Errorf("a gallery picture without alt text: %v", err)
	}
	if _, err := runTool(t, a, "save_site_draft", `{"host":"harbour.example","doc":`+docArg(t, "Drafted")+`}`); err != nil {
		t.Fatalf("save_site_draft: %v", err)
	}
	j, _ = runTool(t, a, "get_site_document", `{"host":"harbour.example"}`)
	if j["source"] != "draft" {
		t.Errorf("after a draft, get_site_document reads %v", j["source"])
	}

	// Publishing with no doc publishes the saved draft.
	first, err := runTool(t, a, "publish_site_document", `{"host":"harbour.example"}`)
	if err != nil || first["status"] != "published" {
		t.Fatalf("publish the draft: %v %v", err, first)
	}
	if body := siteGet(a, d, d.Host, "/", a.handleHome).Body.String(); !strings.Contains(body, "Drafted") {
		t.Error("the published draft is not what the site serves")
	}
	if _, err := runTool(t, a, "publish_site_document", `{"host":"harbour.example"}`); err == nil || !strings.Contains(err.Error(), "no saved draft") {
		t.Errorf("publishing again with no draft and no doc: %v", err)
	}
	if _, err := runTool(t, a, "publish_site_document", `{"host":"harbour.example","doc":`+docArg(t, "Second")+`}`); err != nil {
		t.Fatal(err)
	}
	rev := strconv.Itoa(int(first["revision"].(float64)))
	if _, err := runTool(t, a, "restore_site_revision", `{"host":"harbour.example","revision":`+rev+`}`); err != nil {
		t.Fatalf("restore_site_revision: %v", err)
	}
	if body := siteGet(a, d, d.Host, "/", a.handleHome).Body.String(); !strings.Contains(body, "Drafted") {
		t.Error("the restored revision is not what the site serves")
	}
}

// Publishing a document to a domain serving its blog switches it to serve
// the website — a publish no visitor can see is a success reported for
// nothing. The blog moves to /blog, not away.
func TestPublishingADocumentMakesTheDomainServeIt(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	if err := a.domains.SetSite(context.Background(), d.ID, domain.SiteConfig{Mode: "blog", Template: "studio"}); err != nil {
		t.Fatal(err)
	}
	j, err := runTool(t, a, "publish_site_document", `{"host":"harbour.example","doc":`+docArg(t, "Now A Site")+`}`)
	if err != nil || j["serves"] != "business_subpath" {
		t.Fatalf("publish to a blog-mode domain: %v %v", err, j)
	}
	d, _ = a.domains.ByID(context.Background(), d.ID)
	site, _ := d.Site()
	if site.Mode != "business_subpath" || site.Template != "studio" {
		t.Errorf("after publish the domain serves %q in %q, want business_subpath keeping its design", site.Mode, site.Template)
	}
	if body := siteGet(a, d, d.Host, "/", a.handleHome).Body.String(); !strings.Contains(body, "Now A Site") {
		t.Error("the domain does not serve the document it was switched to")
	}
}

// Once a site is a document, the flat content fields no longer reach its
// page. update_site refuses them rather than store what no visitor sees, and
// still changes what the domain serves.
func TestUpdateSiteRefusesContentForADocumentSite(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	if _, err := runTool(t, a, "update_site", `{"host":"harbour.example","name":"Before"}`); err != nil {
		t.Fatalf("update_site before any document: %v", err)
	}
	publishRow(t, d.ID, docJSON(t, twoPageDoc("Doc")))
	if _, err := runTool(t, a, "update_site", `{"host":"harbour.example","name":"After"}`); err == nil ||
		!strings.Contains(err.Error(), "publish_site_document") {
		t.Errorf("update_site wrote content to a document site: %v", err)
	}
	if _, err := runTool(t, a, "update_site", `{"host":"harbour.example","template":"studio"}`); err != nil {
		t.Errorf("changing the design of a document site was refused: %v", err)
	}
}

// The Website page stops offering fields that no longer render, and its save
// keeps the stored content rather than overwriting it with the empty fields
// a page without that form sends.
func TestTheWebsitePageHandsADocumentSiteToTheEditor(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	page := func() string {
		r := httptest.NewRequest(http.MethodGet, "/os/d/"+d.ID+"/website", nil)
		r = r.WithContext(context.WithValue(r.Context(), ctxScopedDomainKey, d))
		rec := httptest.NewRecorder()
		a.handleOSScopedWebsite(rec, r)
		return rec.Body.String()
	}
	if body := page(); !strings.Contains(body, `id="web-name"`) || !strings.Contains(body, "/website/editor") {
		t.Fatal("before a document, the page should offer its form and the editor")
	}
	if _, err := runTool(t, a, "update_site", `{"host":"harbour.example","name":"Kept Name"}`); err != nil {
		t.Fatal(err)
	}
	publishRow(t, d.ID, docJSON(t, twoPageDoc("Doc")))
	// As the scoping middleware does on every request: the domain as stored now.
	d, _ = a.domains.ByID(context.Background(), d.ID)
	if body := page(); strings.Contains(body, `id="web-name"`) || !strings.Contains(body, "Edited as pages and sections") {
		t.Error("a document site's Website page still offers the flat content form")
	}

	save := httptest.NewRequest(http.MethodPost, "/os/d/"+d.ID+"/api/website",
		strings.NewReader(`{"mode":"business","template":"studio","content":{"name":""}}`))
	save = save.WithContext(context.WithValue(context.WithValue(save.Context(), ctxScopedDomainKey, d),
		ctxUserKey, &users.User{Role: users.RoleAdmin}))
	rec := httptest.NewRecorder()
	a.handleOSScopedWebsiteSave(rec, save)
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	d, _ = a.domains.ByID(context.Background(), d.ID)
	site, _ := d.Site()
	if c := bizsite.ParseContent(site.Content); c.Name != "Kept Name" || site.Template != "studio" {
		t.Errorf("after saving the design, content name is %q and template %q — the stored content must be kept", c.Name, site.Template)
	}
}

// The primary's Website save keeps its stored content once the site is a
// document, for the same reason as a hosted site's.
func TestThePrimarysSaveKeepsContentOnceItIsADocument(t *testing.T) {
	openMigratedDB(t)
	a := &App{siteSettings: settings.New(dbpkg.DB)}
	admin := func(body string) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/os/api/website/save", strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), ctxUserKey, &users.User{Role: users.RoleAdmin}))
		rec := httptest.NewRecorder()
		a.handleOSWebsiteSave(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("save: %d %s", rec.Code, rec.Body)
		}
	}
	admin(`{"mode":"business","template":"bistro","content":{"name":"Kept Name"}}`)
	publishRow(t, "", docJSON(t, twoPageDoc("Doc")))
	admin(`{"mode":"business","template":"studio","content":{}}`)
	c := bizsite.ParseContent(a.siteSettings.Get(context.Background(), settings.ForPrimary(), settings.KeyBizContent))
	if c.Name != "Kept Name" {
		t.Errorf("saving the design of a document site overwrote its content: name %q", c.Name)
	}
	if got := a.siteSettings.Get(context.Background(), settings.ForPrimary(), settings.KeyBizTemplate); got != "studio" {
		t.Errorf("the design change itself was not saved: %q", got)
	}
}
