// SPDX-License-Identifier: Apache-2.0

package main

// sample_content_test.go — a template's demo text must not go live unnoticed.
//
// vayupress.johal.in served Bistro's "Maison Olive" — its menu, its opening
// hours, its "Reserve a table" — as a real business, and nothing on the panel
// said so. These tests pin where it is now said: the site console's Website
// row, the connector's get_site, and the attention strip every console page
// shows.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/bizsite"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
)

func siteServing(t *testing.T, mode string, c bizsite.Content) domain.Domain {
	t.Helper()
	raw := ""
	if c.Name != "" || c.Tagline != "" {
		b, _ := json.Marshal(c)
		raw = string(b)
	}
	cfg, err := domain.EncodeSiteConfigInto("", domain.SiteConfig{Mode: mode, Template: "bistro", Content: raw})
	if err != nil {
		t.Fatal(err)
	}
	return domain.Domain{ID: "d1", Host: "shop.example", Status: domain.StatusActive, ConfigJSON: cfg}
}

func TestSampleContentIsReportedOnlyForTemplateSites(t *testing.T) {
	demo := bizsite.ByKey("bistro").Defaults
	if got := siteSampleFields(context.Background(), siteServing(t, "business", demo)); len(got) == 0 {
		t.Fatal("a template site publishing Bistro's sample reported nothing")
	}
	// No content written at all renders the template's sample — the same page.
	if got := siteSampleFields(context.Background(), siteServing(t, "business", bizsite.Content{})); len(got) == 0 {
		t.Fatal("an empty template site renders the sample and reported nothing")
	}
	if got := siteSampleFields(context.Background(), siteServing(t, "business", bizsite.Content{Name: "Johal Studio", Tagline: "Websites"})); len(got) != 0 {
		t.Fatalf("real content reported as sample: %v", got)
	}
	if got := siteSampleFields(context.Background(), siteServing(t, "custom", demo)); got != nil {
		t.Fatalf("an uploaded bundle has no template content, yet reported %v", got)
	}
}

func TestTheSiteConsoleChipsSampleContent(t *testing.T) {
	a := &App{}
	r := httptest.NewRequest("GET", "/os/d/d1", nil)
	if got := a.scopedToolChips(r, siteServing(t, "business", bizsite.ByKey("bistro").Defaults), 0)["website"]; got.On || got.Text != "sample content" {
		t.Fatalf("Website row = %+v, want the off-toned \"sample content\" chip", got)
	}
	if got := a.scopedToolChips(r, siteServing(t, "business", bizsite.Content{Name: "Johal Studio", Tagline: "x"}), 0)["website"]; got.Text != "website" {
		t.Fatalf("a real site's Website row = %+v", got)
	}
}

func TestTheAttentionStripNamesTheSiteServingSampleContent(t *testing.T) {
	openMigratedDB(t)
	reg := domain.New(dbpkg.DB, dbpkg.RDB)
	d, err := reg.Create(context.Background(), "shop.example", domain.SiteBlog, false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(bizsite.ByKey("bistro").Defaults)
	if err := reg.SetSite(context.Background(), d.ID, domain.SiteConfig{Mode: "business", Template: "bistro", Content: string(b)}); err != nil {
		t.Fatal(err)
	}
	a := &App{domains: reg}
	var found *osNotification
	for _, n := range a.osNotifications(context.Background(), &osSettings{AccessLevel: accessAdmin}) {
		if strings.Contains(n.Title, "Sample content") {
			n := n
			found = &n
		}
	}
	if found == nil {
		t.Fatal("no attention item for a site publishing a template's sample content")
	}
	if found.Href != "/os/d/"+d.ID+"/website" || found.Severity != "warn" {
		t.Fatalf("attention item = %+v, want a warn linking to the site's website page", *found)
	}
}
