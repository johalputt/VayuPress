// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
)

// One install served ONE website.
//
// customSiteDir() took no domain and the site mode came from install-wide
// settings keys, so a studio hosting client sites could host exactly one of
// them: every registered domain served the same uploaded bundle. These tests pin
// the split, and the primary's path staying put — an install that already
// deployed a bundle must keep serving it after upgrading, with nothing to
// redeploy.

func TestPrimaryKeepsTheHistoricBundlePath(t *testing.T) {
	root := customSiteRoot()
	if got := customSiteDirFor(""); got != root {
		t.Fatalf("primary bundle dir = %q, want the historic %q — an existing install "+
			"would stop serving the site it already deployed", got, root)
	}
}

func TestEachDomainGetsItsOwnBundleDirectory(t *testing.T) {
	a := customSiteDirFor("a1b2c3d4e5f6a1b2c3d4e5f6")
	b := customSiteDirFor("ffffffffffffffffffffffff")
	if a == b {
		t.Fatalf("two domains share a bundle directory (%q) — one client's site would "+
			"overwrite another's on deploy", a)
	}
	root := customSiteRoot()
	for _, d := range []string{a, b} {
		if !strings.HasPrefix(d, root+string(filepath.Separator)) {
			t.Errorf("bundle dir %q escaped the bundle root %q", d, root)
		}
		if d == root {
			t.Errorf("a secondary domain resolved to the PRIMARY's directory %q — it would "+
				"serve, and on deploy overwrite, the operator's own site", d)
		}
	}
}

// The scope becomes a path component. Ids are crypto/rand hex today, so this is
// defence in depth rather than the only barrier — but a path built from a value
// because of what that value happens to be right now is one refactor away from
// traversal, and the refactor will not come with a test unless this one exists.
func TestBundlePathRefusesAnythingThatIsNotAnID(t *testing.T) {
	root := customSiteRoot()
	for _, bad := range []string{
		"..", "../..", "a/../../etc", "/etc/passwd", "a/b",
		"..%2f..", "a1b2-c3d4", "A1B2C3D4E5F6A1B2C3D4E5F6", "id with space",
		strings.Repeat("a", 65),
	} {
		got := customSiteDirFor(bad)
		if got != root {
			t.Errorf("customSiteDirFor(%q) = %q; a non-id scope must fall back to the "+
				"primary directory, never become a path component", bad, got)
		}
		if !strings.HasPrefix(filepath.Clean(got), filepath.Clean(root)) {
			t.Errorf("customSiteDirFor(%q) escaped the bundle root: %q", bad, got)
		}
	}
}

// A secondary domain that has set no website of its own must serve ITS OWN blog,
// never inherit the primary's mode.
//
// Inheriting is the defect in its purest form: with the install set to "custom",
// every client domain served the studio's own uploaded bundle at the client's
// address. "Blog" means the domain's own scoped content, which is what ADR-0132
// Stage 2b already gives it everywhere else.
func TestSecondaryWithoutAnOverrideDoesNotInheritThePrimarysWebsite(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "handlers_bizsite.go"), "siteSourceFor")
	if body == "" {
		t.Fatal("siteSourceFor not found")
	}
	if !strings.Contains(body, `return "blog", "", ""`) {
		t.Error("a secondary domain with no site override does not fall back to its own blog. " +
			"If it falls through to the install-wide settings instead, an install in custom " +
			"mode serves the operator's bundle on every client domain")
	}
	if !strings.Contains(body, "IsPrimary") {
		t.Error("siteSourceFor does not distinguish the primary, so either the primary loses " +
			"its install-wide settings or secondaries inherit them")
	}
}

// The config_json envelope is shared. A writer that marshals a fresh envelope
// drops every sibling key, so saving one override silently erases another.
func TestConfigEnvelopeWritersPreserveSiblings(t *testing.T) {
	brand := domain.Brand{SiteName: "Client Ltd", AccentDark: "#0af"}
	site := domain.SiteConfig{Mode: "custom", Template: "cafe"}

	// Site written first, then a brand save on top of it.
	withSite, err := domain.EncodeSiteConfigInto("", site)
	if err != nil {
		t.Fatal(err)
	}
	both, err := domain.EncodeBrandConfigInto(withSite, brand)
	if err != nil {
		t.Fatal(err)
	}
	d := domain.Domain{ConfigJSON: both}
	gotSite, ok := d.Site()
	if !ok || gotSite.Mode != "custom" {
		t.Fatalf("saving a brand erased the website override: %+v (raw %q)", gotSite, both)
	}
	gotBrand, ok := d.Brand()
	if !ok || gotBrand.SiteName != "Client Ltd" {
		t.Fatalf("brand did not round-trip: %+v", gotBrand)
	}

	// ...and the reverse order, because the bug is symmetric.
	withBrand, err := domain.EncodeBrandConfigInto("", brand)
	if err != nil {
		t.Fatal(err)
	}
	both2, err := domain.EncodeSiteConfigInto(withBrand, site)
	if err != nil {
		t.Fatal(err)
	}
	d2 := domain.Domain{ConfigJSON: both2}
	if gb, ok := d2.Brand(); !ok || gb.SiteName != "Client Ltd" {
		t.Fatalf("saving a website override erased the brand: %+v (raw %q)", gb, both2)
	}

	// Clearing the brand must not clear the website, or a client tidying their
	// colours takes their own site offline.
	cleared, err := domain.EncodeBrandConfigInto(both, domain.Brand{})
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := (domain.Domain{ConfigJSON: cleared}).Site(); !ok || s.Mode != "custom" {
		t.Fatalf("clearing the brand erased the website override: %+v (raw %q)", s, cleared)
	}
}
