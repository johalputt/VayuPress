// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/settings"
)

// The Website page is a document: the site itself, framed and sandboxed, with
// what the domain shows, the design, the content, an uploaded build and the
// install checks in the inspector beside it. None of it is a figure strip, an
// accordion or a chip.
func TestTheWebsiteIsADocument(t *testing.T) {
	a := membersApp(t)
	seoConfig(t, "example.com", "", false)
	page := getPage(t, a.handleOSWebsite, "/os/website")
	for _, want := range []string{`data-page-kind="document"`, `<h1 class="sa-doc__title">Website</h1>`, "example.com shows the blog",
		`<iframe class="web-doc__frame" data-biz-frame title="The website" sandbox="" src="/os/api/site-doc/preview?page=&amp;preview=`,
		">Hosting</h2>", ">Design</h2>", ">Content</h2>", ">Your own build</h2>", ">Installs as an app</h2>",
		`<dialog class="sa-sheet" id="web-designs"`, `<dialog class="sa-sheet" id="web-bundle"`, `data-sheet="web-designs"`, `data-sheet="web-bundle"`} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	_, own, _ := strings.Cut(page, `data-page-kind="document"`)
	for _, not := range []string{"stat-card", "mon-acc", "mon-chip", "section-head", "page-sub"} {
		if strings.Contains(own, not) {
			t.Errorf("the page carries %q", not)
		}
	}
	// Line-oriented content is edited only where a value can hold a line
	// break: an <input> strips them, and a save then stores them run together.
	for _, k := range []string{"about", "address", "hours", "services", "gallery"} {
		if !regexp.MustCompile(`<textarea [^>]*data-biz-f="` + k + `"`).MatchString(page) {
			t.Errorf("%s is not edited in a textarea", k)
		}
	}
	// Every element the page's script looks up is on the page: a lost hook
	// turns its control off without an error. Read from the script.
	src, err := os.ReadFile("../../static/js/admin-os-website.js")
	if err != nil {
		t.Fatal(err)
	}
	hooks := regexp.MustCompile(`querySelector(?:All)?\('\[(data-biz-[a-z-]+)\]'\)`).FindAllStringSubmatch(string(src), -1)
	if len(hooks) < 8 {
		t.Fatalf("read %d hooks from the script; the pattern no longer matches it", len(hooks))
	}
	for _, h := range hooks {
		// Roll back is drawn only when there is an earlier upload to go back to.
		if h[1] == "data-biz-rollback" {
			continue
		}
		if !strings.Contains(page, " "+h[1]) {
			t.Errorf("the script looks up [%s] and the page has none", h[1])
		}
	}
	if !strings.Contains(page, `name="biz-mode"`) {
		t.Error("the hosting choice the script reads is not on the page")
	}

	// The site you uploaded is not this server's to frame, so the page says
	// what is served instead of previewing a design that is not.
	if err := a.siteSettings.SetMany(context.Background(), settings.ForPrimary(), map[string]string{settings.KeySiteMode: "custom"}); err != nil {
		t.Fatal(err)
	}
	page = getPage(t, a.handleOSWebsite, "/os/website")
	if strings.Contains(page, "data-biz-frame") || !strings.Contains(page, "example.com serves the site you uploaded") {
		t.Error("in the uploaded mode the page still previews a design")
	}
}
