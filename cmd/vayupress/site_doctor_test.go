// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/bizsite"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/sitedoc"
)

// A site that passed the gate when it was published can fail it later: here
// its picture is deleted from Media afterwards. Nothing about the site
// changed, and the doctor still says so — in the attention strip, on the
// site's console row and in get_site.
func TestTheDoctorFindsWhatBrokeAfterPublishing(t *testing.T) {
	a, _ := gateApp(t)
	list, _ := a.domains.List(context.Background())
	d := list[len(list)-1]
	ctx := context.Background()
	doc := gateDoc("#pics", mediaHave)
	if _, _, err := a.publishSite(ctx, d.ID, doc, "op"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if n := errorCount(a.hostedSiteChecks(ctx, d)); n != 0 {
		t.Fatalf("a freshly published clean site has %d problems", n)
	}
	strip := func() (int, string) {
		for _, n := range a.osNotifications(ctx, &osSettings{AccessLevel: accessAdmin}) {
			if n.Title == "Website problems" {
				return n.Count, n.Href
			}
		}
		return 0, ""
	}
	if n, _ := strip(); n != 0 {
		t.Fatalf("the strip reports problems on a clean site: %d", n)
	}

	if err := os.Remove(filepath.Join(config.Cfg.MediaDir, strings.TrimPrefix(mediaHave, "/media/"))); err != nil {
		t.Fatal(err)
	}
	checks := a.hostedSiteChecks(ctx, d)
	if errorCount(checks) != 1 || checks[0].Path != "pages[0].sections[1].images[0].src" {
		t.Fatalf("after the picture was deleted: %+v", checks)
	}
	if n, href := strip(); n != 1 || href != "/os/d/"+d.ID+"/website/editor" {
		t.Errorf("the strip says %d problems linking %q, want 1 linking the site's editor", n, href)
	}
	j, err := runTool(t, a, "get_site", `{"host":"harbour.example"}`)
	if err != nil || len(j["checks"].([]any)) == 0 {
		t.Errorf("get_site does not report the problem: %v %v", err, j["checks"])
	}
}

// Sample content is read from what a site serves. A published document is
// judged by its own text, not by the flat content it replaced.
func TestSampleContentIsReadFromThePublishedDocument(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example") // legacy content: the bistro sample
	ctx := context.Background()
	if len(siteSampleFields(ctx, d)) == 0 {
		t.Fatal("the untouched site should show as sample content")
	}
	publishRow(t, d.ID, docJSON(t, twoPageDoc("Harbour & Co")))
	if f := siteSampleFields(ctx, d); len(f) != 0 {
		t.Errorf("a site publishing its own document is flagged by its stale flat content: %v", f)
	}
	tpl := bizsite.ByKey("bistro")
	publishRow(t, d.ID, docJSON(t, sitedoc.FromLegacy(tpl, tpl.Defaults)))
	if f := siteSampleFields(ctx, d); len(f) == 0 {
		t.Error("a published document carrying the sample was not flagged")
	}
}
