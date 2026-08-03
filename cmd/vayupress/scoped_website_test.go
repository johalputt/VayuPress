// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/bizsite"
	"github.com/johalputt/vayupress/internal/customsite"
	"github.com/johalputt/vayupress/internal/domain"
)

// ADR-0154 D9/D10 — a hosted domain can be a blog or a website, and either can
// be built by hand or by asking an assistant.
//
// The serving side has been per-domain since ADR-0132 Stage 2b. The admin side
// resolved by REQUEST HOST, and an operator's admin request carries no secondary
// host, so /os/website always edited the primary. A hosted domain's mode was
// reachable only from the CLI.

func websiteDomain(t *testing.T, mode, template, content string) domain.Domain {
	t.Helper()
	cfg, err := domain.EncodeSiteConfigInto("", domain.SiteConfig{Mode: mode, Template: template, Content: content})
	if err != nil {
		t.Fatalf("EncodeSiteConfigInto: %v", err)
	}
	return domain.Domain{ID: "s1", Host: "client.example", Status: domain.StatusActive,
		SyncState: domain.SyncApproved, TLSState: domain.TLSActive, ConfigJSON: cfg}
}

// A blank mode is not "unset" to a visitor — it serves the blog. Rendering the
// radio group with nothing selected would tell an operator their site serves
// nothing.
func TestABlankModeIsShownAsWhatItActuallyServes(t *testing.T) {
	if got := scopedSiteMode(domain.Domain{ID: "s1", Host: "client.example"}); got != "blog" {
		t.Errorf("a domain with no website override reports %q; it serves the blog", got)
	}
	page := scopedWebsitePage(domain.Domain{ID: "s1", Host: "client.example"}, "", bizsite.Content{}, false, customsite.Manifest{})
	if !strings.Contains(page, `value="blog" checked`) {
		t.Error("the mode picker has nothing selected for a site with no override, so it reads " +
			"as serving nothing while it actually serves the blog")
	}
}

// The catalogue of modes must match what the renderer understands. A mode the
// page can offer and bizRootActive does not know is a domain that serves a blank
// page, discovered by a visitor.
func TestEveryOfferedModeIsOneTheRendererUnderstands(t *testing.T) {
	for _, m := range scopedSiteModes {
		switch m.Value {
		case "blog", "business", "business_subpath":
		default:
			t.Errorf("the website page offers mode %q, which the renderer does not handle", m.Value)
		}
	}
	if _, err := scopedWebsiteConfig(domain.Domain{}, "custom", "", bizsite.Content{}); err == nil {
		t.Error("an arbitrary mode was accepted — a stored mode nothing renders serves a blank page")
	}
}

// Saving from a form that does not render every field must not wipe the ones it
// omits. Losing a services grid nobody touched is worse than any layout bug.
func TestSavingKeepsTheFieldsThisPageDoesNotEdit(t *testing.T) {
	prev, err := json.Marshal(bizsite.Content{
		Name: "Old", Services: []bizsite.Service{{Title: "Haircut"}},
		Gallery: []string{"/a.jpg"}, SectionA: "What we do",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	d := websiteDomain(t, "business", "studio", string(prev))

	cfg, err := scopedWebsiteConfig(d, "business", "studio", bizsite.Content{Name: "New", Tagline: "Fresh"})
	if err != nil {
		t.Fatalf("scopedWebsiteConfig: %v", err)
	}
	got := bizsite.ParseContent(cfg.Content)
	if got.Name != "New" {
		t.Errorf("the edited field did not save: %q", got.Name)
	}
	if len(got.Services) != 1 || got.Services[0].Title != "Haircut" {
		t.Error("saving the website form destroyed the services grid, which this page does not edit")
	}
	if len(got.Gallery) != 1 {
		t.Error("saving the website form destroyed the gallery")
	}
	if got.SectionA != "What we do" {
		t.Error("saving the website form destroyed the services heading")
	}
}

// An unknown template key must resolve rather than be stored: a stored key
// nothing matches renders an unstyled page later, far from the save that caused
// it.
func TestAnUnknownTemplateResolvesRatherThanBeingStored(t *testing.T) {
	cfg, err := scopedWebsiteConfig(domain.Domain{}, "business", "no-such-template", bizsite.Content{})
	if err != nil {
		t.Fatalf("scopedWebsiteConfig: %v", err)
	}
	if cfg.Template == "no-such-template" {
		t.Fatal("an unknown template key was stored verbatim; the site renders unstyled later, " +
			"with nothing connecting it to this save")
	}
	if bizsite.ByKey(cfg.Template).Key != cfg.Template {
		t.Errorf("the stored template %q is not one the renderer knows", cfg.Template)
	}
}

// The MCP write path must share the console's validator. Two validators for one
// shape is how one of them comes to accept a mode the other refuses.
func TestTheAssistantAndTheConsoleShareOneValidator(t *testing.T) {
	src := readSourceFile(t, "mcp_sites.go")
	body := goFuncBody(src, "registerSiteTools")
	if !strings.Contains(body, "scopedWebsiteConfig(") {
		t.Error("the MCP site tools validate independently of the console, so the two surfaces " +
			"can disagree about what a valid mode or template is")
	}
	// And it must refuse the primary by name rather than letting SetSite fail
	// with something an assistant will just retry.
	look := goFuncBody(src, "mcpSiteByHost")
	if !strings.Contains(look, "IsPrimary") {
		t.Error("the host lookup does not refuse the primary, so an assistant is handed an " +
			"opaque failure from the registry instead of the reason")
	}
}

// An omitted field must be distinguishable from one set to empty, or an
// assistant editing one line blanks every other field on a live website.
func TestOmittedFieldsAreNotTreatedAsBlanking(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "mcp_sites.go"), "registerSiteTools")
	i := strings.Index(body, `Name: "update_site"`)
	if i < 0 {
		t.Fatal("update_site is gone")
	}
	seg := body[i:]
	if !strings.Contains(seg, "Tagline  *string") {
		t.Fatal("update_site decodes its content fields as plain strings, so an assistant that " +
			"sends only a new tagline blanks the name, about, phone and every other field on " +
			"somebody's live website")
	}
}

// ADR-0154 D12 — a whole authored site, not a filled-in template.
//
// The deploy path is customsite.Deploy for BOTH an uploaded zip and a site an
// assistant wrote. That is deliberate: it is the code that confines writes to an
// os.Root and refuses traversal in archive entries, and a second implementation
// of the part that must never be wrong is how one of them ends up without the
// confinement.

// An assistant supplies paths. They must not escape the site's directory, and
// the refusal must happen before anything is written.
//
// The first version of this test only complained "if the path normalises out",
// which made it conditional on the very logic it was checking — removing the
// cleaning step entirely left it green. A test a mutation survives is not a
// test. It now reads the ARCHIVE and asserts what is actually in it.
func TestAnAuthoredSiteCannotWriteOutsideItsOwnDirectory(t *testing.T) {
	for _, hostile := range []string{
		"../../../etc/cron.d/x", "..\\..\\windows\\system32\\a.txt",
		"/etc/passwd", "./../../escape.html", "../sibling/index.html",
	} {
		raw, err := zipFromFiles(map[string]string{"index.html": "<h1>ok</h1>", hostile: "x"})
		if err != nil {
			continue // refused outright: also correct
		}
		zr, zerr := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if zerr != nil {
			t.Fatalf("the archive built from %q is unreadable: %v", hostile, zerr)
		}
		for _, f := range zr.File {
			n := strings.ReplaceAll(f.Name, "\\", "/")
			if strings.HasPrefix(n, "/") || strings.HasPrefix(n, "../") || strings.Contains(n, "/../") {
				t.Errorf("the path %q was stored in the archive as %q, which escapes the site "+
					"directory — customsite.Deploy is the last line of defence and this is the first",
					hostile, f.Name)
			}
		}
	}
}

// A path that merely LOOKS awkward but stays inside must still be kept, or the
// fix is a refusal to serve nested assets rather than a traversal guard.
func TestAnAuthoredSiteKeepsLegitimateNestedPaths(t *testing.T) {
	raw, err := zipFromFiles(map[string]string{
		"index.html": "<h1>hi</h1>", "assets/css/site.css": "body{}", "./img/logo.svg": "<svg/>",
	})
	if err != nil {
		t.Fatalf("zipFromFiles: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("unreadable archive: %v", err)
	}
	got := map[string]bool{}
	for _, f := range zr.File {
		got[f.Name] = true
	}
	for _, want := range []string{"index.html", "assets/css/site.css", "img/logo.svg"} {
		if !got[want] {
			t.Errorf("the archive is missing %q — a real site's nested assets were dropped", want)
		}
	}
}

// index.html is required rather than defaulted. A bundle with no entry point
// deploys cleanly and serves 404 at the root — a site that looks published and
// is not.
func TestAnAuthoredSiteMustCarryAnEntryPoint(t *testing.T) {
	if hasIndexHTML(map[string]string{"about.html": "x", "assets/a.css": "y"}) {
		t.Error("a file set with no index.html was accepted as a complete site")
	}
	for _, ok := range []string{"index.html", "INDEX.HTML", "./index.html", "index.htm"} {
		if !hasIndexHTML(map[string]string{ok: "<h1>hi</h1>"}) {
			t.Errorf("%q was not recognised as the entry point", ok)
		}
	}
}

// The limits are checked before the archive is built, so a caller cannot make
// the server allocate its way out of memory constructing something Deploy would
// have rejected anyway.
func TestAnAuthoredSiteIsBoundedBeforeItIsBuilt(t *testing.T) {
	if _, err := zipFromFiles(nil); err == nil {
		t.Error("an empty file set was accepted")
	}
	huge := map[string]string{"index.html": strings.Repeat("x", int(customsite.MaxTotalBytes)+1)}
	if _, err := zipFromFiles(huge); err == nil {
		t.Error("a file set larger than the install's limit was zipped anyway")
	}
	many := map[string]string{"index.html": "x"}
	for i := 0; i < customsite.MaxFiles+10; i++ {
		many["f"+strconv.Itoa(i)+".html"] = "x"
	}
	if _, err := zipFromFiles(many); err == nil {
		t.Error("a file set beyond the file-count limit was zipped anyway")
	}
}

// Identical input must produce an identical archive, or "did anything change"
// becomes unanswerable between two deploys of the same site.
func TestAnAuthoredSiteDeploysDeterministically(t *testing.T) {
	files := map[string]string{"index.html": "<h1>a</h1>", "assets/b.css": "body{}", "a/c.js": "1"}
	first, err := zipFromFiles(files)
	if err != nil {
		t.Fatalf("zipFromFiles: %v", err)
	}
	second, err := zipFromFiles(files)
	if err != nil {
		t.Fatalf("zipFromFiles: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("the same files produced two different archives, so an unchanged site looks " +
			"changed on every deploy")
	}
}

// "custom" must be selectable ONLY when something is actually deployed.
// Selecting a mode with nothing behind it publishes a domain serving a 404 at
// its root, which reads as a broken site rather than an unfinished choice.
func TestCustomModeIsRefusedUntilSomethingIsDeployed(t *testing.T) {
	d := domain.Domain{ID: "deadbeefdeadbeefdeadbeef", Host: "client.example"}
	_, err := scopedWebsiteConfig(d, "custom", "", bizsite.Content{})
	if err == nil {
		t.Fatal("a domain with no uploaded bundle was switched to serve one, so its root " +
			"serves 404 while the console reports the change as published")
	}
	// And the refusal must say WHICH problem it is: an unsupported mode and an
	// empty upload slot are different situations with different next steps.
	if !strings.Contains(err.Error(), "upload") {
		t.Errorf("the refusal reads as an unknown mode rather than a missing upload: %v", err)
	}
}
