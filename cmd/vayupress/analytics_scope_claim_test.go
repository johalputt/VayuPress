// SPDX-License-Identifier: Apache-2.0

package main

import (
	"regexp"
	"strings"
	"testing"
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
// operators learn to skip — including the ones that matter.
func TestTheScopeNoteAppearsOnlyWhereItIsTrue(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "admin_os_intel.go"), "analyticsScopeNote")
	if body == "" {
		t.Fatal("analyticsScopeNote is gone, so the install-wide page says nothing about its " +
			"own scope again")
	}
	if !strings.Contains(body, "len(list) < 2") {
		t.Error("the note is not gated on there being more than one hosted domain, so every " +
			"single-domain install now carries a caveat that cannot apply to it")
	}
	if !strings.Contains(body, "/os/domains") {
		t.Error("the note does not point at where a per-site figure lives, so it tells an " +
			"operator their number is wrong for the question and not where the right one is")
	}
	// And it must be reached, or it is a function nobody calls.
	if !strings.Contains(goFuncBody(readSourceFile(t, "admin_os_intel.go"), "renderAnalyticsBody"),
		"a.analyticsScopeNote(ctx)") {
		t.Fatal("the analytics page never renders the scope note")
	}
}
