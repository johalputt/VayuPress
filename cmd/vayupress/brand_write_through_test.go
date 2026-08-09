// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
)

// FINDING (S8) — the client's own branding editor wrote a store the public site
// no longer reads, under a heading promising the opposite.
//
// /os/mysite tells a client "Your website's name, description and colours.
// Changes go live immediately." The save called domains.SetBrand, which writes
// domains.config_json. But ADR-0153 Phase 4 moved the render path onto the
// per-domain settings scope: brandForRequest asks settings.ForDomain(d.ID) and
// returns as soon as GetAll succeeds — and GetAll on a scope with no rows
// succeeds, returning the compiled-in defaults. d.Brand() is reached only when
// the settings store ERRORS.
//
// So on every healthy install a client renamed their site, was told it was live,
// and nothing changed. ADR-0154 D3 had already prescribed the remedy for the
// operator's twin endpoint — "writing through to the scoped store so an
// operator's bookmark or script does not silently diverge" — and neither
// endpoint did it.
//
// SECOND FINDING, found while fixing the first: only ONE of the two endpoints
// validated what it stored. handleOSDomainBrand hex-checks the three colour
// fields and length-clips the three text fields; handleOSMySiteBrand — the one
// reachable by a principal the operator does not employ — did neither. That was
// close to harmless while the brand reached nothing, and making the brand reach
// the render path is exactly what would have armed it. A fix that activates a
// dormant hole is not a fix, so both endpoints now go through one path.

func brandFixture() domain.Brand {
	return domain.Brand{
		SiteName:    "Client Ltd",
		Tagline:     "We do the thing",
		Description: "A description of the thing.",
		AccentLight: "#2563eb",
		AccentDark:  "#60a5fa",
		ThemeColor:  "#0b1020",
	}
}

// The mapping is the defect's home: a brand field written under a key the render
// path does not read is the same silent no-op, one layer along. Asserting the
// ROUND TRIP through the production reader is what makes this real — a test
// listing the six keys by hand would agree with a wrong mapping.
func TestEveryBrandFieldSurvivesIntoWhatThePageActuallyReads(t *testing.T) {
	b := brandFixture()
	got := siteSettingsFromValues(brandSettingValues(b))

	for _, c := range []struct{ field, want, have string }{
		{"site name", b.SiteName, got.Name},
		{"tagline", b.Tagline, got.Tagline},
		{"description", b.Description, got.Description},
		{"accent (light)", b.AccentLight, got.AccentLight},
		{"accent (dark)", b.AccentDark, got.AccentDark},
		{"theme colour", b.ThemeColor, got.ThemeColor},
	} {
		if c.have != c.want {
			t.Errorf("%s saved as %q but the render path reads %q — the client edits this "+
				"field, is told it is live, and their site never changes", c.field, c.want, c.have)
		}
	}
}

// Clearing a field must clear it, not silently restore a default. A client who
// deletes their tagline and still sees one has the same complaint in reverse.
func TestClearingABrandFieldIsWrittenAsCleared(t *testing.T) {
	v := brandSettingValues(domain.Brand{SiteName: "Only the name"})
	for _, k := range []string{"site.tagline", "site.description"} {
		if _, ok := v[k]; !ok {
			t.Errorf("%s is absent from the write set, so an existing value survives a save "+
				"that cleared it and the field appears un-editable", k)
		}
	}
	if got := siteSettingsFromValues(v).Tagline; got != "" {
		t.Errorf("a cleared tagline reads back as %q", got)
	}
}

// The validation the operator's endpoint had and the client's did not.
func TestABrandCannotCarryAnythingButAHexColour(t *testing.T) {
	for _, bad := range []string{
		"red; --x: url(javascript:alert(1))",
		"#2563eb; }",
		"var(--anything)",
		"#12345",
	} {
		b := brandFixture()
		b.AccentLight = bad
		if _, err := normalizeBrand(b); err == nil {
			t.Errorf("accent %q was accepted; it lands in this domain's /theme.css verbatim", bad)
		}
	}
	// Empty is not invalid — it is how a field is cleared.
	b := brandFixture()
	b.AccentLight, b.AccentDark, b.ThemeColor = "", "", ""
	if _, err := normalizeBrand(b); err != nil {
		t.Errorf("clearing the colours was refused: %v", err)
	}
	// A three-digit hex is a real CSS colour and must not be refused.
	b = brandFixture()
	b.ThemeColor = "#abc"
	if _, err := normalizeBrand(b); err != nil {
		t.Errorf("#abc was refused: %v", err)
	}
}

// Text fields are length-capped so one client cannot write an unbounded string
// into a shared table through the field next to their colours.
func TestBrandTextIsCapped(t *testing.T) {
	b := brandFixture()
	b.SiteName = strings.Repeat("x", 500)
	b.Tagline = strings.Repeat("y", 500)
	b.Description = strings.Repeat("z", 900)
	n, err := normalizeBrand(b)
	if err != nil {
		t.Fatalf("normalizeBrand: %v", err)
	}
	for _, c := range []struct {
		field string
		got   string
		max   int
	}{
		{"site name", n.SiteName, 120},
		{"tagline", n.Tagline, 200},
		{"description", n.Description, 320},
	} {
		if len(c.got) > c.max {
			t.Errorf("%s stored %d bytes, cap is %d", c.field, len(c.got), c.max)
		}
	}
}

// BOTH endpoints must go through the one path. The whole second finding was two
// writers for one field where only one of them checked anything, so a test that
// only proves the helper is correct proves nothing about the endpoint that
// bypasses it.
func TestBothBrandEndpointsGoThroughTheOnePath(t *testing.T) {
	for _, c := range []struct{ file, fn, who string }{
		{"admin_os_mysite.go", "handleOSMySiteBrand", "the client's own editor"},
		{"admin_os_domains.go", "handleOSDomainBrand", "the operator's endpoint"},
	} {
		body := goFuncBody(readSourceFile(t, c.file), c.fn)
		if body == "" {
			t.Fatalf("%s is gone", c.fn)
		}
		if !strings.Contains(body, "a.saveBrand(") {
			t.Errorf("%s (%s) does not call saveBrand, so it writes a store the public site "+
				"does not read — or writes one without the validation the other has", c.fn, c.who)
		}
		if strings.Contains(body, "a.domains.SetBrand(") {
			t.Errorf("%s calls SetBrand directly, which is the half of the write that the "+
				"render path ignores. A second copy of this rule is the divergence again", c.fn)
		}
	}
}

// saveBrand must write BOTH stores. config_json alone was the bug; the scoped
// store alone would break the fallback brandForRequest uses when the settings
// store cannot answer — which is the one path that still reads d.Brand().
func TestSaveBrandWritesBothStores(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "admin_os_brand.go"), "saveBrand")
	if body == "" {
		t.Fatal("saveBrand is gone")
	}
	if !strings.Contains(body, "SetMany") {
		t.Fatal("saveBrand does not write the scoped settings store, so the public render path " +
			"still never sees a brand save — the finding is back")
	}
	if !strings.Contains(body, "SetBrand") {
		t.Error("saveBrand no longer writes the domain record, so brandForRequest's fallback " +
			"(used when the settings store errors) has nothing to fall back to")
	}
}
