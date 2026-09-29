// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
)

// FINDING (S8) — the install-wide Analytics page never said it was install-wide.
//
// Every figure on /os/analytics comes from the UNSCOPED readers (Since,
// OverviewSince, TopPages …), so on a multi-domain install each one sums every
// hosted domain. The page made no false claim — its subtitle promised only
// "computed on your own server" — and that is exactly the failure: an operator
// hosting thirty clients reads "Analytics", sees a number, and cannot tell
// whether it is one client's or everyone's. Its sibling page IS scoped and says
// so, which makes the silence here read as "scoped too".
//
// The MCP tools were the same, one layer further from anyone who could check:
// analytics_summary, analytics_audience and analytics_referrers take no host
// parameter, aggregate the whole install, and said nothing either way while
// analytics_referrers described "the site's own domain" in the singular.

// goConcatRe matches Go's multi-line string concatenation — `" +\n\t\t\t"` — so a
// description split across source lines reads as the one sentence a caller sees.
//
// Without this the assertions match against source layout rather than content: a
// phrase broken over two literals ("Covers EVERY " + "domain this install
// serves") contains neither half of what it says, and the test fails on correct
// copy while passing on copy that happens to fit one line.
var goConcatRe = regexp.MustCompile(`"\s*\+\s*"`)

// mcpToolDescription returns a named tool's Description as the assembled string,
// read out of the source so the assertions are against what actually ships.
func mcpToolDescription(t *testing.T, file, tool string) string {
	t.Helper()
	src := readSourceFile(t, file)
	i := strings.Index(src, `Name:        "`+tool+`"`)
	if i < 0 {
		i = strings.Index(src, `Name: "`+tool+`"`)
	}
	if i < 0 {
		t.Fatalf("tool %s not found in %s", tool, file)
	}
	rest := src[i:]
	j := strings.Index(rest, "InputSchema:")
	if j < 0 {
		t.Fatalf("tool %s has no InputSchema, so its description cannot be delimited", tool)
	}
	return goConcatRe.ReplaceAllString(rest[:j], "")
}

// Every unscoped analytics reader exposed to a caller has to say whose traffic
// it is counting. A tool that sums thirty clients and describes itself as "the
// site's" is a number that means something other than it looks like.
func TestTheUnscopedAnalyticsToolsSayTheyCoverTheWholeInstall(t *testing.T) {
	for _, c := range []struct{ file, tool string }{
		{"mcp_server.go", "analytics_summary"},
		{"mcp_server.go", "analytics_audience"},
		{"mcp_shield.go", "analytics_referrers"},
	} {
		desc := mcpToolDescription(t, c.file, c.tool)
		low := strings.ToLower(desc)
		if !strings.Contains(low, "every domain this install serves") {
			t.Errorf("%s does not say it covers every domain this install serves. It takes no "+
				"host parameter and sums them all, so a caller asking about one client's site "+
				"gets thirty clients' traffic and no warning", c.tool)
		}
		if !strings.Contains(low, "not scoped") {
			t.Errorf("%s does not state that it is unscoped, so the omission reads as an "+
				"oversight rather than a fact about the tool", c.tool)
		}
	}
}

// analytics_referrers claimed "the site's own domain … excluded" in the
// singular, while the exclusion is now built from every registered host. The
// description has to follow the mechanism, or it is a second copy of a rule that
// has already diverged once.
func TestTheReferrerToolDescribesTheExclusionItActuallyApplies(t *testing.T) {
	desc := strings.ToLower(mcpToolDescription(t, "mcp_shield.go", "analytics_referrers"))
	if strings.Contains(desc, "the site's own domain and subdomains are excluded") {
		t.Error("the description still says 'the site's own domain' in the singular; the " +
			"exclusion covers every host this install serves")
	}
	if !strings.Contains(desc, "every host this install serves") {
		t.Error("the description does not say which hosts are excluded")
	}
}

// The page's note has to be conditional. A caveat printed on a single-domain
// install can never apply there, and a notice that is always wrong is a notice
// operators learn to skip — including the ones that matter. Where it does
// apply it says how many sites the figures add up and where one site's own
// figures are.
func TestTheScopeNoteAppearsOnlyWhereItIsTrue(t *testing.T) {
	openMigratedDB(t)
	reg := domain.New(dbpkg.DB, dbpkg.RDB)
	if err := reg.EnsurePrimary(context.Background(), "example.test", domain.SiteBlog); err != nil {
		t.Fatal(err)
	}
	a := &App{analytics: analytics.New(dbpkg.DB), domains: reg}
	one := a.renderAnalyticsBody(context.Background(), 30, "30 days")
	if one == "" {
		t.Fatal("the report did not render")
	}
	if strings.Contains(one, `href="/os/domains"`) || strings.Contains(one, "across all") {
		t.Error("a single-site install carries a caveat that cannot apply to it")
	}
	if _, err := dbpkg.DB.Exec(`INSERT INTO domains(id,host,site_type,is_primary,status,created_at,updated_at) VALUES('c','client.example','blog',0,'active',datetime('now'),datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	two := a.renderAnalyticsBody(context.Background(), 30, "30 days")
	for _, want := range []string{"These figures add up all 2 sites", `<a href="/os/domains">Sites</a>`} {
		if !strings.Contains(two, want) {
			t.Errorf("an install serving two sites: missing %q", want)
		}
	}
}
