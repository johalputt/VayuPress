// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/sitedoc"
)

const (
	mediaHave = "/media/0123456789abcdef0123456789abcdef.jpg"
	mediaGone = "/media/fedcba9876543210fedcba9876543210.jpg"
)

// gateApp is a hosted site on a migrated database with a media library and
// a router that answers /search as a route and everything else as a post.
func gateApp(t *testing.T) (*App, string) {
	t.Helper()
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	oldMedia := config.Cfg.MediaDir
	config.Cfg.MediaDir = t.TempDir()
	t.Cleanup(func() { config.Cfg.MediaDir = oldMedia })
	if err := os.WriteFile(filepath.Join(config.Cfg.MediaDir, strings.TrimPrefix(mediaHave, "/media/")), []byte("jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Get("/search", func(http.ResponseWriter, *http.Request) {})
	router.Get("/{slug}", func(http.ResponseWriter, *http.Request) {})
	a.setRootHandler(router)
	return a, d.ID
}

// gateDoc is a site whose home links with the given button link and shows
// the given picture, described so no warning fires unless a case adds one.
func gateDoc(link, picture string) sitedoc.Document {
	d := sitedoc.Document{V: sitedoc.Version, Name: "Harbour", Pages: []sitedoc.Page{
		{Slug: "", Description: "A harbour restaurant.", Sections: []sitedoc.Section{
			{ID: "top", Kind: sitedoc.KindHero, Body: "Fresh fish", CTA: "Go", CTALink: link},
			{ID: "pics", Kind: sitedoc.KindGallery, Images: []sitedoc.Image{{Src: picture, Alt: "The quay"}}},
		}},
		{Slug: "menu", Description: "What we cook.", Sections: []sitedoc.Section{{ID: "food", Kind: sitedoc.KindText, Body: "Crab"}}},
	}}
	return d
}

func findings(checks []siteCheck, level string) []string {
	var out []string
	for _, c := range checks {
		if c.Level == level {
			out = append(out, c.Path+": "+c.Message)
		}
	}
	return out
}

// Each error, one seed each, asserting the field and the reason — and the
// links that do reach something, which must not be flagged.
func TestThePublishGateFindsDeadEnds(t *testing.T) {
	a, scope := gateApp(t)
	ctx := context.Background()
	if _, err := dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,tags,status,created_at,updated_at,domain_id) VALUES
		('p1','Wine','wine','x','','published',?,?,?), ('p2','Draft','soon','x','','draft',?,?,?), ('p3','Theirs','theirs','x','','published',?,?,'elsewhere')`,
		time.Now(), time.Now(), scope, time.Now(), time.Now(), scope, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ link, picture, path, reason string }{
		{"#nope", mediaHave, "pages[0].sections[0].cta_link", "no section #nope on this page"},
		{"/menu#nope", mediaHave, "pages[0].sections[0].cta_link", "the page /menu has no section #nope"},
		{"/nowhere", mediaHave, "pages[0].sections[0].cta_link", "nothing on this site answers at /nowhere"},
		{"/soon", mediaHave, "pages[0].sections[0].cta_link", "nothing on this site answers at /soon"},     // an unpublished post
		{"/theirs", mediaHave, "pages[0].sections[0].cta_link", "nothing on this site answers at /theirs"}, // another site's post
		{"#pics", mediaGone, "pages[0].sections[1].images[0].src", "not in the media library"},
	} {
		errs := findings(a.checkSite(ctx, scope, gateDoc(c.link, c.picture)), "error")
		if len(errs) != 1 || !strings.HasPrefix(errs[0], c.path+": ") || !strings.Contains(errs[0], c.reason) {
			t.Errorf("link %q picture %q: errors %q, want one at %s saying %q", c.link, c.picture, errs, c.path, c.reason)
		}
	}
	for _, ok := range []string{"#pics", "/menu", "/menu#food", "/search", "/wine", "https://other.example/", "tel:+441"} {
		if errs := findings(a.checkSite(ctx, scope, gateDoc(ok, mediaHave)), "error"); len(errs) != 0 {
			t.Errorf("link %q reaches something but was flagged: %q", ok, errs)
		}
	}
}

// Each warning, one seed each; a clean document has none.
func TestThePublishGateWarns(t *testing.T) {
	a, scope := gateApp(t)
	ctx := context.Background()
	if w := findings(a.checkSite(ctx, scope, gateDoc("#pics", mediaHave)), "warning"); len(w) != 0 {
		t.Fatalf("the clean fixture already warns: %q", w)
	}
	warnOnly := func(name string, d sitedoc.Document, path, reason string) {
		t.Helper()
		w := findings(a.checkSite(ctx, scope, d), "warning")
		if len(w) != 1 || !strings.HasPrefix(w[0], path+": ") || !strings.Contains(w[0], reason) {
			t.Errorf("%s: warnings %q, want one at %s saying %q", name, w, path, reason)
		}
	}

	d := gateDoc("#pics", mediaHave)
	d.Pages[1].Description = ""
	warnOnly("no description", d, "pages[1].description", "no description")
	// A header tagline is the page's description when none is written.
	d = gateDoc("#pics", mediaHave)
	d.Pages[0].Description = ""
	if w := findings(a.checkSite(ctx, scope, d), "warning"); len(w) != 0 {
		t.Errorf("a page described by its tagline was flagged: %q", w)
	}

	d = gateDoc("#pics", mediaHave)
	d.Pages[1].Sections = append(d.Pages[1].Sections, sitedoc.Section{ID: "c", Kind: sitedoc.KindContact, Form: true})
	warnOnly("a form with nowhere to send", d, "pages[1].sections[1].form", "reach only the console's inbox")
	d.Pages[1].Sections[1].Email = "owner@harbour.example"
	if w := findings(a.checkSite(ctx, scope, d), "warning"); len(w) != 0 {
		t.Errorf("a form with an address still warns: %q", w)
	}

	big := "/media/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.jpg"
	if err := os.WriteFile(filepath.Join(config.Cfg.MediaDir, strings.TrimPrefix(big, "/media/")), make([]byte, pageWeightBudget+1), 0o600); err != nil {
		t.Fatal(err)
	}
	warnOnly("a heavy page", gateDoc("#pics", big), "pages[0]", "slow to appear on a phone")

	prev := config.Cfg.OnionMode
	config.Cfg.OnionMode = true
	defer func() { config.Cfg.OnionMode = prev }()
	warnOnly("another site's picture on Tor", gateDoc("#pics", "https://cdn.example/a.jpg"), "pages[0].sections[1].images[0].src", "does not load on the Tor site")
}

// An error stops a publish with the field it is about; warnings publish and
// are returned; a draft is saved whatever the checks find, and reports them.
func TestTheGateStopsAPublishButNotADraft(t *testing.T) {
	a, _ := gateApp(t)
	d0, err := a.domains.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var d = d0[len(d0)-1]
	h := editorRouter(a, d, true)

	rec, j := editorCall(t, h, http.MethodPost, "/sd/draft", withDoc(gateDoc("/nowhere", mediaHave)))
	if rec.Code != http.StatusOK || len(j["checks"].([]any)) != 1 {
		t.Fatalf("a draft with a dead link: %d %s — it should save and report the check", rec.Code, rec.Body)
	}
	rec, j = editorCall(t, h, http.MethodPost, "/sd/publish", withDoc(gateDoc("/nowhere", mediaHave)))
	if rec.Code != http.StatusUnprocessableEntity || j["error"].(map[string]any)["path"] != "pages[0].sections[0].cta_link" {
		t.Fatalf("publish with a dead link: %d %s", rec.Code, rec.Body)
	}
	if _, ok := publishedSiteDoc(context.Background(), d.ID); ok {
		t.Error("the refused publish went live")
	}
	warned := gateDoc("#pics", mediaHave)
	warned.Pages[1].Description = ""
	rec, j = editorCall(t, h, http.MethodPost, "/sd/publish", withDoc(warned))
	if rec.Code != http.StatusOK || len(j["checks"].([]any)) != 1 {
		t.Errorf("publish with only a warning: %d %s — it should publish and report it", rec.Code, rec.Body)
	}
}
