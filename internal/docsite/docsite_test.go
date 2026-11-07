// SPDX-License-Identifier: Apache-2.0

package docsite

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/johalputt/vayupress"
)

var synced = time.Date(2026, 9, 27, 13, 0, 0, 0, time.UTC)

// fixture is a small repository laid out as VayuPress's is.
func fixture() fstest.MapFS {
	return fstest.MapFS{
		"CHANGELOG.md": {Data: []byte("# Changelog\n\n## [Unreleased]\n\n### Fixed\n\n- not shipped yet\n\n" +
			"## [3.17.85] — 2026-09-27\n\nA fix for backups.\n\n### Fixed\n\n- **Backups.** They work.\n\n" +
			"## [3.17.84] — 2026-09-26\n\n### Fixed\n\n- Keys.\n")},
		"docs/INSTALLATION.md":      {Data: []byte("# VayuPress Installation Guide\n\nRun one command.\n\n## Before you start\n\nSee [operations](OPERATIONS.md#backups), [the register](adr/INDEX.md), [a shot](screenshots/home.png), [the code](../cmd/vayupress/main.go), [outside](https://example.com/x) and [escape](../../etc/passwd).\n")},
		"docs/OPERATIONS.md":        {Data: []byte("# Operations Runbook — VayuPress\n\nKeep it running.\n")},
		"docs/screenshots/home.png": {Data: []byte("PNG")},
		"docs/adr/INDEX.md": {Data: []byte("| ADR | Title | Status | Owner | Date |\n|---|---|---|---|---|\n" +
			"| [ADR-0002](ADR-0002-two.md) | Two, as the register puts it | Accepted | Security | 2026-09-26 |\n")},
		"docs/adr/ADR-0001-one.md":                  {Data: []byte("# ADR-0001: The first\n\n**Status**: Proposed  \n**Date**: 2024-01-01\n\n## Context\n\nWhy.\n")},
		"docs/adr/ADR-0002-two.md":                  {Data: []byte("# ADR-0002 — The second\n\n- **Status:** Accepted\n- **Date:** 2026-09-26\n\n## 0. The question\n\nWhat.\n")},
		"docs/site/nav.json":                        {Data: []byte(`{"groups":[{"group":"Start","docs":["INSTALLATION","MISSING"]}],"hide":[]}`)},
		"docs/site/pages/about.md":                  {Data: []byte("---\npath: /about.html\ntitle: About\nkicker: About the Developer\n---\n# A person\n\n{decisions} decisions, {releases} releases.\n")},
		"docs/site/public/.well-known/security.txt": {Data: []byte("Contact: mailto:security@example.com\n")},
	}
}

// withTheme gives repo this repository's own design and product list, which
// a build needs: the binary carries no design of its own.
func withTheme(t *testing.T, repo fstest.MapFS) fstest.MapFS {
	t.Helper()
	real := os.DirFS("../..")
	err := fs.WalkDir(real, themeDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(real, p)
		repo[p] = &fstest.MapFile{Data: data}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if data, err := fs.ReadFile(real, productsFile); err == nil {
		repo[productsFile] = &fstest.MapFile{Data: data}
	}
	return repo
}

func build(t *testing.T, repo fs.FS) Bundle {
	t.Helper()
	if m, ok := repo.(fstest.MapFS); ok {
		if _, has := m[themeDir+"/templates/layout.html"]; !has {
			withTheme(t, m)
		}
	}
	assets, _ := fs.Sub(vayupress.StaticFS, "static")
	b, err := Build(Input{Repo: repo, Commit: "0123456789abcdef", Source: "https://github.com/o/r", Synced: synced, Assets: assets,
		Install: Install{Host: "johal.in", Posts: 234615, Sites: 13}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func file(t *testing.T, b Bundle, name string) string {
	t.Helper()
	data, ok := b[name]
	if !ok {
		t.Fatalf("the bundle has no %s", name)
	}
	return string(data)
}

// One seed per way a link is resolved.
func TestLinksWrittenForGitHubWorkOnTheSite(t *testing.T) {
	b := build(t, fixture())
	doc := file(t, b, "docs/INSTALLATION/index.html")
	for what, want := range map[string]string{
		"a document":                   `href="/docs/OPERATIONS/#backups"`,
		"the register":                 `href="/decisions/"`,
		"a picture":                    `href="/docs/screenshots/home.png"`,
		"a file outside docs":          `href="https://github.com/o/r/blob/0123456789abcdef/cmd/vayupress/main.go"`,
		"somewhere else, as written":   `href="https://example.com/x" rel="nofollow"`,
		"a link leaving the repo, cut": `>escape</a>`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s: missing %s in\n%s", what, want, doc)
		}
	}
	if strings.Contains(doc, "etc/passwd") {
		t.Error("a link leaving the repository kept its target")
	}
	if strings.Contains(doc, `/decisions/" rel="nofollow"`) || strings.Contains(doc, `OPERATIONS/#backups" rel="nofollow"`) {
		t.Error("a link to the site's own page is marked nofollow")
	}
	if _, ok := b["docs/screenshots/home.png"]; !ok {
		t.Error("a picture a document shows is not in the bundle")
	}
}

func TestADocumentsTitleIsTheSitesNotTheForges(t *testing.T) {
	for in, want := range map[string]string{
		"VayuPress Installation Guide":               "Installation Guide",
		"Operations Runbook — VayuPress":             "Operations Runbook",
		"VayuPress — Required Mailboxes":             "Required Mailboxes",
		"Trust Model — VayuPress Plugin Confinement": "Trust Model",
		"Buzz — connect a Buzz agent to your site":   "Buzz — connect a Buzz agent to your site",
		"": "slug name",
	} {
		if got := guideTitle(in, "x/slug-name"); got != want {
			t.Errorf("guideTitle(%q) = %q, want %q", in, got, want)
		}
	}
	b := build(t, fixture())
	if doc := file(t, b, "docs/INSTALLATION/index.html"); strings.Count(doc, "<h1") != 1 || !strings.Contains(doc, "<h1>Installation Guide</h1>") {
		t.Errorf("the page does not have exactly one title, its own:\n%s", doc)
	}
}

// An ADR's own header block goes (the page shows its facts), in either form
// the records are written in; the register's row wins where it has one, and
// nothing is invented where neither says.
func TestADecisionShowsTheRegistersFactsOnce(t *testing.T) {
	b := build(t, fixture())
	two := file(t, b, "docs/adr/ADR-0002-two/index.html")
	if strings.Contains(two, "Status:") {
		t.Errorf("the list-form header block stayed in the body:\n%s", two)
	}
	for _, want := range []string{"<b>Accepted</b>", "26 September 2026", "Two, as the register puts it", "› Security"} {
		if !strings.Contains(two, want) {
			t.Errorf("ADR-0002 is missing %q", want)
		}
	}
	one := file(t, b, "docs/adr/ADR-0001-one/index.html")
	if strings.Contains(one, "Status</strong>") {
		t.Errorf("the paragraph-form header block stayed in the body:\n%s", one)
	}
	if !strings.Contains(one, "<b>Proposed</b>") || !strings.Contains(one, "1 January 2024") {
		t.Error("a decision the register does not list lost its own header's facts")
	}
	if strings.Contains(one, "› Core") {
		t.Error("a decision with no owner was given one")
	}
}

func TestChangesAreWhatShipped(t *testing.T) {
	b := build(t, fixture())
	list := file(t, b, "changes/index.html")
	if strings.Contains(list, "Unreleased") || strings.Contains(list, "not shipped yet") {
		t.Error("the unreleased section is on the site")
	}
	if !strings.Contains(list, "A fix for backups.") || !strings.Contains(list, "/changes/3.17.84/") {
		t.Errorf("the releases or their summaries are missing:\n%s", list)
	}
	if !strings.Contains(file(t, b, "changes/3.17.85/index.html"), "They work.") {
		t.Error("a release's own page does not carry its notes")
	}
}

func TestTheSitesOwnPagesKeepTheirAddressesAndFigures(t *testing.T) {
	b := build(t, fixture())
	about := file(t, b, "about.html")
	if !strings.Contains(about, "2 decisions, 2 releases.") {
		t.Errorf("the page's figures were not filled from the build:\n%s", about)
	}
	if !strings.Contains(about, "<title>About · VayuPress</title>") || !strings.Contains(about, "About the Developer") {
		t.Error("the page lost its title or its kicker")
	}
	if string(b[".well-known/security.txt"]) != "Contact: mailto:security@example.com\n" {
		t.Error("a file the site serves as it is was changed or dropped")
	}
	// The footer links the pages this repository has, and only those.
	home := file(t, b, "index.html")
	if !strings.Contains(home, `href="/about.html"`) || strings.Contains(home, `href="/sponsors/"`) {
		t.Error("the footer does not follow the pages the repository has: About must be linked, Sponsor (absent) must not")
	}
}

// A document the navigation does not name still has its page, under More; a
// name the navigation lists that has no document is left out, not linked.
func TestNavigationNeverLosesADocumentNorLinksToNone(t *testing.T) {
	b := build(t, fixture())
	home := file(t, b, "docs/index.html")
	if !strings.Contains(home, `id="g-more"`) || !strings.Contains(home, `href="/docs/OPERATIONS/"`) {
		t.Errorf("a document the navigation does not name has no place:\n%s", home)
	}
	if strings.Contains(home, "MISSING") {
		t.Error("the navigation links a document that does not exist")
	}
}

func TestTheBundleDeploysAndPacksTheSameEveryTime(t *testing.T) {
	b := build(t, fixture())
	one, err := b.Zip()
	if err != nil {
		t.Fatal(err)
	}
	two, _ := b.Zip()
	if !bytes.Equal(one, two) {
		t.Error("the same site packed to different bytes")
	}
	if _, err := zip.NewReader(bytes.NewReader(one), int64(len(one))); err != nil {
		t.Fatal(err)
	}
	repo := withTheme(t, fixture())
	repo["docs/site/public/notes.md"] = &fstest.MapFile{Data: []byte("x")}
	assets, _ := fs.Sub(vayupress.StaticFS, "static")
	if _, err := Build(Input{Repo: repo, Synced: synced, Assets: assets}); err == nil || !strings.Contains(err.Error(), "notes.md") {
		t.Errorf("a file the deploy would refuse was let through: %v", err)
	}
}

// The real repository, built as the install builds it: every address a page
// names is in the bundle, every asset is the site's own, and search finds a
// decision by its title. A nav.json slug that no longer exists, a template
// that links a page nobody renders, a stylesheet from somewhere else: each
// fails here, by name.
func TestTheRealSiteHasNoBrokenLinkAndNothingFromElsewhere(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the whole site")
	}
	b := build(t, os.DirFS("../.."))
	ref := regexp.MustCompile(`(?:href|src|srcset)="(/[^"#?]*)`)
	asset := regexp.MustCompile(`<(?:script|link)[^>]+(?:src|href)="([^"]+)"`)
	var broken []string
	for name, data := range b {
		if !strings.HasSuffix(name, ".html") {
			continue
		}
		for _, m := range ref.FindAllStringSubmatch(string(data), -1) {
			target := strings.TrimPrefix(m[1], "/")
			if target == "" || strings.HasSuffix(target, "/") {
				target += "index.html"
			}
			if _, ok := b[target]; !ok {
				broken = append(broken, name+" → "+m[1])
			}
		}
		for _, m := range asset.FindAllStringSubmatch(string(data), -1) {
			if !strings.HasPrefix(m[1], "/") {
				broken = append(broken, name+" loads "+m[1]+" from elsewhere")
			}
		}
	}
	if len(broken) > 0 {
		if len(broken) > 20 {
			broken = append(broken[:20], "…")
		}
		t.Errorf("%d broken:\n%s", len(broken), strings.Join(broken, "\n"))
	}
	var index []struct{ T, U, K string }
	if err := json.Unmarshal(b["search.json"], &index); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range index {
		if e.K == "Decision" && strings.Contains(e.T, "ADR-0164") && path.Clean(e.U) == "/docs/adr/ADR-0164-outside-services-in-a-sites-csp" {
			found = true
		}
	}
	if !found {
		t.Error("search does not find ADR-0164 by its title")
	}
}

// A repository's docs/site/theme draws the site instead of the design compiled
// in, so a new look is a push. It replaces the design whole: one it is missing
// a file of is an error naming the file, not half of each.
func TestARepositoryThemeDrawsTheSite(t *testing.T) {
	repo := withTheme(t, fixture())
	for p, f := range repo {
		switch p {
		case themeDir + "/templates/layout.html":
			f.Data = []byte(strings.Replace(string(f.Data), "<body>", `<body data-theme="from-the-repository">`, 1))
		case themeDir + "/assets/site.css":
			f.Data = append(f.Data, "\n/* the repository's */\n"...)
		}
	}
	b := build(t, repo)
	if !strings.Contains(string(b["index.html"]), `data-theme="from-the-repository"`) {
		t.Error("the pages are not drawn with the repository's templates")
	}
	css := ""
	for p, data := range b {
		if strings.HasPrefix(p, "assets/site.") && strings.HasSuffix(p, ".css") {
			css += string(data)
		}
	}
	if !strings.Contains(css, "/* the repository's */") {
		t.Error("the site does not carry the repository's stylesheet")
	}

	delete(repo, themeDir+"/assets/site.js")
	static, _ := fs.Sub(vayupress.StaticFS, "static")
	if _, err := Build(Input{Repo: repo, Synced: synced, Assets: static}); err == nil || !strings.Contains(err.Error(), "the design has no assets/site.js") {
		t.Errorf("a theme missing its script = %v; it must be refused by name, not drawn half from the built-in design", err)
	}
}

// TestTheHomePageCarriesItsSections — docs/site/home is drawn between the
// products and Still Air, in the files' numbered order, each with its kicker
// and figures; a section's h3s become one grid of cards, ended by the next
// h2; and a line that is only a pictogram is left out. One seed per rule.
func TestTheHomePageCarriesItsSections(t *testing.T) {
	repo := fixture()
	repo["docs/site/home/01-zeta.md"] = &fstest.MapFile{Data: []byte("---\nkicker: Why it exists\n---\n## Own it\n\nIntro with {decisions} decisions.\n\n💳\n\n### Card one\n\nFirst.\n\n### Card two\n\nSecond.\n\n## After the cards\n\nLater.\n")}
	repo["docs/site/home/02-alpha.md"] = &fstest.MapFile{Data: []byte("---\nkicker: Second\n---\n## The second section\n")}
	home := file(t, build(t, repo), "index.html")
	first, second := strings.Index(home, `id="zeta"`), strings.Index(home, `id="alpha"`)
	if first < 0 || second < 0 || first > second {
		t.Fatalf("the sections are missing or out of their numbered order (zeta at %d, alpha at %d)", first, second)
	}
	if !strings.Contains(home, "Why it exists") || !strings.Contains(home, "Intro with 2 decisions.") {
		t.Error("a section lost its kicker or its figures")
	}
	if !strings.Contains(home, `<div class="cards"><div class="card"><h3`) || strings.Count(home, `<div class="card">`) != 2 {
		t.Errorf("the section's h3s are not one grid of two cards:\n%s", home[first:second])
	}
	if strings.Index(home, "After the cards") < strings.LastIndex(home[:second], `</div></div>`) {
		t.Error("the next h2 did not end the grid of cards")
	}
	if strings.Contains(home, "💳") {
		t.Error("a pictogram-only line reached the page")
	}
	if a, s := strings.Index(home, `id="h-products"`), strings.Index(home, `id="still-air"`); a > first || s < second {
		t.Error("the sections are not between the products and Still Air")
	}
}

// TestHomeNamesEveryProductAndOffersBothWorlds — the ten products, and the
// switch between Clearnet and Tor Space with Clearnet pressed.
func TestHomeNamesEveryProductAndOffersBothWorlds(t *testing.T) {
	home := file(t, build(t, fixture()), "index.html")
	for _, p := range []string{"Websites", "Blog", "VayuMail", "VayuTalk", "Tor Space", "VayuShield", "VayuAnalytics", "VayuAPI", "VayuMCP", "VayuOS"} {
		if !strings.Contains(home, "<h3>"+p+"</h3>") {
			t.Errorf("Home does not name %s", p)
		}
	}
	if !strings.Contains(home, `data-world="clearnet" aria-pressed="true"`) || !strings.Contains(home, `data-world="tor" aria-pressed="false"`) {
		t.Error("Home has no world switch with Clearnet pressed")
	}
}

// TestARepositoryWithNoDesignIsRefused — the binary carries no site design:
// one project's site must not be what every install draws. A repository with
// no docs/site/theme is refused with the reason, and builds nothing.
func TestARepositoryWithNoDesignIsRefused(t *testing.T) {
	assets, _ := fs.Sub(vayupress.StaticFS, "static")
	b, err := Build(Input{Repo: fixture(), Commit: "0123456789abcdef", Source: "https://github.com/o/r", Synced: synced, Assets: assets})
	if !errors.Is(err, errNoTheme) || b != nil {
		t.Fatalf("a repository with no theme gave %d files and %v; want errNoTheme", len(b), err)
	}
}

// TestProductsAreTheRepositorysOwn — the home page names the products the
// repository lists, and a repository that lists none names none.
func TestProductsAreTheRepositorysOwn(t *testing.T) {
	repo := withTheme(t, fixture())
	repo[productsFile] = &fstest.MapFile{Data: []byte(`[{"name":"Only this","text":"t","icon":"site","adr":"ADR-0002"}]`)}
	home := file(t, build(t, repo), "index.html")
	if !strings.Contains(home, "<h3>Only this</h3>") || strings.Contains(home, "<h3>VayuMail</h3>") {
		t.Error("the home page does not name exactly the repository's own products")
	}
	if !strings.Contains(home, "ADR-0002") {
		t.Error("a product's decision is not linked")
	}
	delete(repo, productsFile)
	if home := file(t, build(t, repo), "index.html"); strings.Contains(home, `<div class="product">`) {
		t.Error("a repository with no product list still names products")
	}
}

// TestTheEngineEmbedsNoSite — the package ships in every install's binary,
// so it holds no design and no site's files: nothing in it is embedded.
// One project's site belongs to its repository (docs/site/theme).
func TestTheEngineEmbedsNoSite(t *testing.T) {
	files, err := fs.Glob(os.DirFS("."), "*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "//go:embed") {
			t.Errorf("%s embeds files into the binary; a site's design belongs to its repository", f)
		}
	}
}
