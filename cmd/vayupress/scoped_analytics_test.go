// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/domain"
)

// ADR-0153 Phases 5–7.

// The attribution rule, and the reason it is the whole of Phase 6's security.
//
// /api/v1/analytics/collect is public and unauthenticated. If the domain came
// from the beacon body, any visitor could name any domain and write traffic into
// any client's report on the install — inflating a figure the operator bills
// against, or poisoning a client's "busiest pages" with paths of their choosing.
func TestPageviewAttributionComesFromTheServerNeverTheBeacon(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "handlers_analytics.go"), "handleAnalyticsCollect")
	if !strings.Contains(body, "a.contentScope(r)") {
		t.Fatal("the collect handler does not resolve the domain server-side. If attribution " +
			"comes from the beacon, a visitor chooses which client's report their traffic " +
			"lands in")
	}
	if strings.Contains(body, "req.Hostname") {
		t.Error("the collect handler reads the beacon's own hostname field for attribution; " +
			"that value is client-supplied by construction")
	}
	// And the store must take it as a parameter rather than digging it out of req.
	sig := "func (s *Store) Collect(ctx context.Context, req CollectRequest, ip, ua, domainID string) error"
	if !strings.Contains(readAnalyticsSource(t), sig) {
		t.Error("Collect does not take a server-resolved domain id as an argument")
	}
}

// An empty scope is the PRIMARY, not "everything". A client's report that
// silently included every other site would be the cross-tenant leak with a
// chart around it that migration 080 already fixed once for the daily counters.
func TestAnEmptyAnalyticsScopeMeansThePrimaryNotEverything(t *testing.T) {
	src := readAnalyticsSource(t)
	body := goFuncBody(src, "domainClause")
	if body == "" {
		t.Fatal("domainClause not found")
	}
	if !strings.Contains(body, "ScopeAllDomains") {
		t.Fatal("there is no explicit all-domains value, so some other string must be doing " +
			"that job — and the obvious candidate is the empty string, which is the primary")
	}
	// The all-domains sentinel must be unreachable from a request value.
	if !strings.Contains(src, `ScopeAllDomains = "\x00`) {
		t.Error("the all-domains sentinel is an ordinary string, so a crafted domain id could " +
			"become it and disable filtering entirely")
	}
}

// The per-domain traffic page must read the scoped queries. Reading the
// unscoped ones would show every site's traffic under one client's name.
func TestTheScopedTrafficPageUsesTheScopedQueries(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "admin_os_scoped_analytics.go"), "renderSiteAnalyticsBody")
	calls := regexp.MustCompile(`\bst\.(\w+)\(`).FindAllStringSubmatch(body, -1)
	if len(calls) < 8 {
		t.Fatalf("the site's page reads %d things from the store; the test no longer sees its reads", len(calls))
	}
	for _, c := range calls {
		if !strings.HasSuffix(c[1], "Scoped") {
			t.Errorf("the site's page calls the store's %s, which is not given the site: it would report "+
				"every site on the install under one client's name", c[1])
		}
	}
	if !strings.Contains(goFuncBody(readSourceFile(t, "analytics_performance.go"), "readerVitals"), "WebVitalsP75Scoped(") {
		t.Error("the site's speed is not read for the site")
	}
}

// The page says what its visitors are: a daily hash, so a week's figure counts
// a person once for each day they came, and where each figure is defined.
// (It once warned that its visits and the install's visitors were different
// populations; since visitors are counted from each site's own rows, they
// are the same measure.) It is How this works, which closes every served
// page after the cached report (TestThePrimarysPageSaysWhereEverySitesAlertsAre).
func TestTheTrafficPageSaysWhatItsVisitorsAre(t *testing.T) {
	page := string(analyticsExplain(hostedView(domain.Domain{ID: "d1"}), "customer.example"))
	for _, want := range []string{"once for each day they came", `href="/docs/ANALYTICS"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the site's traffic page does not say %q", want)
		}
	}
}

// SEO: the scoped page must not report the primary's cached artefacts under a
// client's name. Mounting /os/seo would have done exactly that.
func TestTheScopedSEOPageDoesNotReportThePrimarysArtefacts(t *testing.T) {
	src := readSourceFile(t, "admin_os_scoped_seo.go")
	// The HANDLER BODY, not the whole file: the file comment explains why those
	// primary-only sources are avoided, and banning the substring everywhere
	// would fail on the sentence documenting the decision. Asserting on prose
	// instead of behaviour is how a test comes to enforce nothing useful.
	body := goFuncBody(src, "handleOSScopedSEO")
	if body == "" {
		t.Fatal("handleOSScopedSEO not found")
	}
	for _, banned := range []string{"config.Cfg.CacheDir", "evaluateSEOHealth("} {
		if strings.Contains(body, banned) {
			t.Errorf("the per-domain SEO page uses %s — those describe the PRIMARY's cached "+
				"files and canonical host, so the report would carry a client's heading and "+
				"another site's data", banned)
		}
	}
	if !strings.Contains(src, "install-level") {
		t.Error("the page does not say which parts of SEO reporting remain install-level")
	}
}

// Copy-from-primary is a COPY. If it linked, it would reintroduce the exact
// inheritance the operator rejected.
func TestCopyFromPrimaryCopiesAndExcludesIdentity(t *testing.T) {
	for _, k := range copyableFromPrimary {
		switch k {
		case "site.name", "site.tagline", "site.description", "site.author":
			t.Errorf("copy-from-primary would copy %q onto a client's site, publishing the "+
				"studio's own identity on their domain", k)
		}
	}
	if len(copyableFromPrimary) == 0 {
		t.Fatal("copy-from-primary copies nothing")
	}
	body := goFuncBody(readSourceFile(t, "admin_os_scoped_settings.go"), "handleOSScopedCopyFromPrimary")
	if !strings.Contains(body, "sc.IsPrimary()") {
		t.Error("copying onto the primary itself is not refused; it would be a no-op that " +
			"reports success")
	}
	if !strings.Contains(body, "settings.ForPrimary()") {
		t.Error("the copy does not read from the primary scope")
	}
	if !strings.Contains(body, "SetMany(r.Context(), sc,") {
		t.Error("the copy does not write to the request's own scope")
	}
}

func readAnalyticsSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../internal/analytics/extended.go")
	if err != nil {
		t.Fatalf("read analytics source: %v", err)
	}
	return string(b)
}

var _ = analytics.ScopeAllDomains
