// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/bizsite"
	"github.com/johalputt/vayupress/internal/customsite"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
)

// ADR-0154 D2 — no install-wide link ever appears on a per-site page.
//
// The reported bug, verbatim from the page that carried it:
//
//	<p class="text-sm muted">The links below are the <b>install-wide</b> tools.
//	   They edit the primary site.</p>
//	<a class="btn" href="/os/theme">Theme Studio</a>
//	<a class="btn" href="/os/website">Website settings</a>
//	<a class="btn" href="/os/analytics">Analytics</a>
//	<a class="btn" href="/os/seo">SEO</a>
//
// Four buttons with the names of the four tools an operator wants, on the page
// about their client's site, opening the operator's own. The disclaimer above
// them is true and is not enough: a caveat in grey type does not survive contact
// with a button that looks like the thing you came for.
//
// The general rule, which has now cost two bugs: a control that acts on
// something other than the page it is on does not belong on that page.

// scopedSiteLinkAllowlist are the install-wide destinations a per-site page may
// still link, because each is genuinely about the whole install and cannot
// mislead: the site list, the DNS page (records are per-domain but the control
// that provisions them is one privileged helper), and the operator's own home.
var scopedSiteLinkAllowlist = map[string]bool{
	"/os/domains": true,
	"/os/dns":     true,
	"/os":         true,
	// VayuMCP is connector SETUP, not a tool that edits a site. Following it
	// pairs an assistant with this install; it changes no site's content, so it
	// cannot produce the outcome D2 exists to stop — an operator editing the
	// primary from a page titled with a client's domain. The pages that link it
	// say so in the sentence around it ("ask an assistant to build a site for
	// <this host>"), and the assistant then takes the host from list_sites.
	//
	// /os/seo was NOT admitted here and its link was removed instead. That one is
	// a report about the primary's cached artefacts, offered from a page titled
	// with a client's domain, under a caveat — which is exactly the "demoted, not
	// absent" shape D2 rules out. Its own sentence said "it is not shown here"
	// while being a link to it.
	"/os/vayumcp": true,
}

// scopedSiteLinkPrefixAllowlist are destinations addressed by a SLUG that this
// site owns. /os/editor/{slug} opens that post — a thing belonging to this
// site — so it cannot be the install-wide surface the rule is about. Bare
// /os/editor is NOT here on purpose: with no slug it creates on the primary,
// which is exactly the trap this test caught in the first draft of the content
// page (a "Write for this site" button pointing at /os/editor?domain={id},
// a parameter the editor does not read).
var scopedSiteLinkPrefixAllowlist = []string{"/os/editor/"}

var osLinkRe = regexp.MustCompile(`href="(/os/[^"?#]*)`)

// assertNoInstallWideLinks fails on any /os/… link that is neither scoped to
// this site (/os/d/{id}/…) nor explicitly allowed.
func assertNoInstallWideLinks(t *testing.T, label, id, page string) {
	t.Helper()
	scopedPrefix := "/os/d/" + id
	for _, m := range osLinkRe.FindAllStringSubmatch(page, -1) {
		href := strings.TrimSuffix(m[1], "/")
		if href == "" {
			href = "/os"
		}
		if strings.HasPrefix(href, scopedPrefix) || scopedSiteLinkAllowlist[href] {
			continue
		}
		allowed := false
		for _, p := range scopedSiteLinkPrefixAllowlist {
			if strings.HasPrefix(href, p) && len(href) > len(p) {
				allowed = true
				break
			}
		}
		if allowed {
			continue
		}
		t.Errorf("%s links %s — an install-wide tool on a page about one hosted site. "+
			"An operator following it edits the PRIMARY site from a page titled with their "+
			"client's domain, which is the whole of the reported bug", label, href)
	}
}

func isolationDomain() domain.Domain {
	return domain.Domain{
		ID: "s1", Host: "client.example", SiteType: domain.SiteBlog,
		Status: domain.StatusActive, MailEnabled: true, TLSState: domain.TLSActive,
	}
}

func TestTheSiteConsoleLinksNoInstallWideTool(t *testing.T) {
	d := isolationDomain()
	assertNoInstallWideLinks(t, "the site console", d.ID,
		scopedConsolePage(d, 3, 2, 1, true, nil, nil, nil, nil))
}

func TestEveryPerSiteToolLinksNoInstallWideTool(t *testing.T) {
	d := isolationDomain()
	// WITH rows. The first version of this test rendered an empty list and
	// therefore exercised none of the per-row links — it passed while proving
	// nothing about the markup that actually ships.
	items := []dbpkg.Article{
		{Title: "Live one", Slug: "live-one", Status: "published", UpdatedAt: time.Unix(1, 0).UTC()},
		{Title: "A draft", Slug: "a-draft", Status: "draft", UpdatedAt: time.Unix(2, 0).UTC()},
		{Title: "A page", Slug: "a-page", Status: "published", IsPage: true, UpdatedAt: time.Unix(3, 0).UTC()},
	}
	assertNoInstallWideLinks(t, "the content page", d.ID, scopedContentPage(d, items))
}

// ADR-0154 D2 says "A test enforces this: any href="/os/<tool>" in per-site
// markup fails the build." It named every per-site page and the test ran on two
// of them — so /os/d/{id}/seo shipped an <a href="/os/seo"> and the website page
// shipped two links to /os/vayumcp, none of which any assertion could see.
//
// A gate that covers a third of what it claims is the failure class this audit
// track keeps finding: the claim is the control, and nobody checks the claim.
// Every per-site renderer is now driven through the same helper, so adding a
// page and forgetting this test is the thing that has to be noticed, not the
// thing that silently passes.
func TestEveryPerSitePageIsCheckedForInstallWideLinks(t *testing.T) {
	d := isolationDomain()
	for _, c := range []struct{ label, page string }{
		{"the site console", scopedConsolePage(d, 3, 2, 1, true, nil, nil, nil, nil)},
		{"the content page", scopedContentPage(d, []dbpkg.Article{
			{Title: "Live one", Slug: "live-one", Status: "published", UpdatedAt: time.Unix(1, 0).UTC()},
		})},
		{"the SEO page", scopedSEOBody(d.ID, "https://"+d.Host, map[string]string{"canonical": "https://" + d.Host})},
		{"the settings page", scopedSettingsBody(d.ID, d.Host, map[string]string{}, presUnknown)},
		{"the website page", scopedWebsitePage(d, "", bizsite.Content{}, false, customsite.Manifest{})},
		{"the analytics page", scopedAnalyticsBody(10, 5, 25.0, 42.0, nil)},
	} {
		assertNoInstallWideLinks(t, c.label, d.ID, c.page)
	}
}

// FINDING (post-v3.17.48) — the seventh per-site page wrote the operator's site.
//
// The gate covered six renderers. The per-site block mounted a seventh:
// /os/d/{id}/theme, Theme Studio, the SAME handler as /os/theme. Its tile called
// it "Colours, typography and custom CSS — this site's own". It was none of
// those for a hosted site.
//
// The page linked /os/theme/store and /os/api/theme/export — absolute
// install-wide routes, and the export handler resolves osScope to the PRIMARY
// when reached that way. Worse, its script posts to absolute /os/api/theme/apply,
// /os/api/theme/code, /os/api/theme/import, /os/api/settings and the branding
// uploads, so pressing Apply on a client's page restyled the operator's own
// install. Beneath all of that, theme_tokens is CHECK(id=1) and applying a theme
// sets a process global — there is no per-site theme for a scoped route to write.
//
// So the mount is retired rather than repaired: the address redirects to the
// per-site settings page, and Theme Studio is named in sharedTools as
// install-wide. Removing it costs nobody anything they had — it never edited the
// site whose name was on the page.
func TestThePerSiteThemeStudioIsRetiredRatherThanServed(t *testing.T) {
	d := isolationDomain()

	// The old address must redirect, not 404: it is in bookmarks and in this
	// console's own history.
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/os/d/"+d.ID+"/theme", nil)
	(&App{}).handleOSScopedThemeRetired(rec, r.WithContext(context.WithValue(r.Context(), ctxScopedDomainKey, d)))
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("the retired per-site Theme Studio answered %d, want a permanent redirect — a "+
			"dead link teaches nothing and the URL is in circulation", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/os/d/"+d.ID+"/settings" {
		t.Errorf("it redirects to %q, not this site's own settings page", got)
	}

	// It must not be routed to the Theme Studio handler any more.
	routes := readSourceFile(t, "admin_os_ui.go")
	if strings.Contains(routes, `dr.Get("/theme", a.handleOSTheme)`) {
		t.Error("Theme Studio is mounted per-site again. Its script posts to absolute " +
			"/os/api/... paths, so every write from that page lands on the primary")
	}

	// The console must stop advertising it as this site's own, and must still
	// NAME it — an operator needs to know what a hosted site does not get.
	page := scopedConsolePage(d, 0, 0, 0, true, nil, nil, nil, nil)
	if strings.Contains(page, "/os/d/"+d.ID+"/theme") {
		t.Error("the console still links the retired per-site Theme Studio")
	}
	if !strings.Contains(page, "Theme Studio") {
		t.Error("the console no longer mentions Theme Studio at all, so an operator cannot tell " +
			"whether a hosted site has its own — silence is how the first version of this misled")
	}

	// And the install-wide Studio must be untouched.
	wide := httptest.NewRecorder()
	(&App{}).handleOSTheme(wide, httptest.NewRequest(http.MethodGet, "/os/theme", nil))
	for _, want := range []string{`href="/os/theme/store"`, `href="/os/api/theme/export"`} {
		if !strings.Contains(wide.Body.String(), want) {
			t.Errorf("the operator's own Theme Studio lost %s — the fix was to stop serving it "+
				"per-site, not to strip it", want)
		}
	}
}

// The drafts must be listed. The public listing excludes them by design, and an
// operator opening a client's site to see what is on it needs the unpublished
// ones most — those are the items waiting on somebody.
func TestTheContentPageListsDrafts(t *testing.T) {
	page := scopedContentPage(isolationDomain(), []dbpkg.Article{
		{Title: "Waiting on the client", Slug: "waiting", Status: "draft", UpdatedAt: time.Unix(1, 0).UTC()},
	})
	if !strings.Contains(page, "Waiting on the client") {
		t.Fatal("a draft owned by this site is not listed, so the console shows a client's site " +
			"as emptier than it is and the item nobody has finished is the one hidden")
	}
	if !strings.Contains(page, "draft") {
		t.Error("the draft is listed without being marked as one, so it reads as live")
	}
}

// Every tool the console advertises must carry the site in its own address.
// A scopedTools entry whose path forgot the %s would render a link to the
// operator's own tool with no warning at all.
func TestEveryScopedToolPathCarriesTheSite(t *testing.T) {
	for _, tool := range scopedTools {
		if !strings.HasPrefix(tool.Path, "/os/d/%s/") {
			t.Errorf("%s has path %q, which does not carry the site id — it would open the "+
				"install-wide tool from a per-site console", tool.Title, tool.Path)
		}
	}
}

// The install-wide tools that a hosted site does NOT yet have its own copy of
// must be named. Silence there is how an operator sells a client something the
// product does not do, and finds out in front of them.
func TestTheConsoleNamesWhatIsStillInstallWide(t *testing.T) {
	page := scopedConsolePage(isolationDomain(), 0, 0, 0, true, nil, nil, nil, nil)
	for _, want := range sharedTools {
		if !strings.Contains(page, want) {
			t.Errorf("the console never mentions that %s is still install-wide", want)
		}
	}
	if !strings.Contains(strings.ToLower(page), "row scoping is not a sandbox") {
		t.Error("the console does not state the isolation ceiling, so it reads as stronger " +
			"separation than one process on one machine can provide")
	}
}

// The old manage URL must redirect rather than 404: it is in operators'
// bookmarks and in this console's own history.
func TestTheOldManageURLIsNotSimplyDeleted(t *testing.T) {
	src := readSourceFile(t, "admin_os_domains.go")
	body := goFuncBody(src, "handleOSDomainManage")
	if !strings.Contains(body, "/os/d/") {
		t.Fatal("the retired per-site page does not redirect to the console, so every " +
			"bookmark and every in-product link to it is now broken")
	}
	if !strings.Contains(body, "StatusMovedPermanently") {
		t.Error("the redirect is not permanent, so the old URL stays in circulation")
	}
}

// A pending certificate is the state that stops a site serving. Reporting it in
// an amber tile and offering nothing to do about it is the same defect as not
// reporting it: the operator learns the tile means "wait", and then asks why the
// certificate is not automatic.
func TestAPendingCertificateCarriesTheControlThatFixesIt(t *testing.T) {
	d := isolationDomain()
	d.SyncState = domain.SyncApproved
	d.TLSState = domain.TLSPending
	page := scopedConsolePage(d, 0, 0, 0, true, nil, nil, nil, nil)

	if !strings.Contains(page, "data-site-provision") {
		t.Fatal("the console reports a pending certificate with no way to act on it — the " +
			"control lived on another page this one did not even link")
	}
	// It must also say WHY it is not instant. "Pending" alone reads as broken.
	for _, want := range []string{"root", "once a day"} {
		if !strings.Contains(strings.ToLower(page), want) {
			t.Errorf("the certificate notice never mentions %q, so an operator cannot tell "+
				"whether this is a design or a failure", want)
		}
	}

	// A live certificate must not carry the notice, or the page always shows a
	// problem and the notice stops meaning anything.
	ok := isolationDomain()
	ok.SyncState = domain.SyncApproved
	ok.TLSState = domain.TLSActive
	if strings.Contains(scopedConsolePage(ok, 0, 0, 0, true, nil, nil, nil, nil), "data-site-provision") {
		t.Error("a site with a live certificate is offered a provisioning run anyway")
	}

	// Nor must a HELD site: not provisioning it is what the hold does, and the
	// hold notice already explains that. Two competing explanations is worse
	// than one.
	held := isolationDomain()
	held.SyncState = domain.SyncHold
	held.TLSState = domain.TLSPending
	if strings.Contains(scopedConsolePage(held, 0, 0, 0, true, nil, nil, nil, nil), "data-site-provision") {
		t.Error("a site on manual hold is offered a provisioning run that would skip it")
	}
}

// A control that asks for a privileged step must report what the step DID, not
// merely that it was asked for. The first version of this button stopped at
// "Requested ✓" and left the operator to reload and guess — so a run that
// skipped this domain, or aborted because nginx's config was already invalid,
// looked exactly like success. That is the same defect as an amber tile with no
// button, one layer along: the page reports an action instead of an outcome.
func TestTheProvisionButtonReportsTheOutcome(t *testing.T) {
	script := domainManageScript("n1")
	i := strings.Index(script, "data-site-provision")
	if i < 0 {
		t.Fatal("the provisioning control is gone")
	}
	seg := script[i:]
	if !strings.Contains(seg, "/os/api/provision/status") {
		t.Fatal("the button never reads the run's result, so a run that provisioned nothing " +
			"is indistinguishable from one that worked")
	}
	// The three outcomes an operator must be able to tell apart.
	for want, why := range map[string]string{
		"nginx-config-broken": "a run aborted by an already-invalid nginx config",
		"res.failed":          "a run that reported problems",
		"res.ran===0":         "a run that provisioned nothing",
	} {
		if !strings.Contains(seg, want) {
			t.Errorf("the button does not distinguish %s", why)
		}
	}
}

// ADR-0154 D11 — the console diagnoses itself.
//
// Diagnosing one stuck certificate took four rounds of "run this and paste the
// output". Every fact involved is available to this process, so the page must
// determine and print them rather than describe where to go and look.
func TestAPendingCertificateShowsWhatTheConsoleChecked(t *testing.T) {
	checks := []diagCheck{
		{Label: "Root-side helper installed", OK: true, Detail: "present"},
		{Label: "Listed for provisioning", OK: false, Fatal: true, Detail: "on manual hold"},
		{Label: "DNS answers for client.example", OK: true, Detail: "resolves"},
	}
	body := scopedDiagnosticBody(checks, []string{"line one", "No sync-approved secondary domains"}, "client.example")

	// Each check, and its verdict, must be on the page.
	for _, c := range checks {
		if !strings.Contains(body, c.Label) {
			t.Errorf("the diagnostic never reports %q", c.Label)
		}
	}
	// The one that blocks must be distinguishable from the ones that merely
	// failed, or an operator reads six rows and learns nothing.
	if !strings.Contains(body, "badge--warn") {
		t.Error("a blocking check is not toned differently from a passing one")
	}
	// The log goes on the page verbatim. It is the artifact that actually
	// answered this on a real install.
	if !strings.Contains(body, "No sync-approved secondary domains") {
		t.Fatal("the provisioning log is not shown, so the operator is still being asked to " +
			"fetch it from a terminal — the thing this exists to stop")
	}
	if strings.Contains(body, "<script") {
		t.Error("log content reached the page unescaped")
	}
}

// The log is untrusted text from another process. It must be escaped.
func TestTheProvisioningLogIsEscaped(t *testing.T) {
	body := scopedDiagnosticBody(nil, []string{`<img src=x onerror="alert(1)">`}, "client.example")
	if strings.Contains(body, "<img src=x") {
		t.Fatal("a log line rendered as live markup; a root-side process writes that file and " +
			"its contents must never be trusted into the page")
	}
	if !strings.Contains(body, "&lt;img") {
		t.Error("the log line was neither escaped nor rendered — it vanished")
	}
}

// A healthy site must not pay for the diagnostic. It does a DNS lookup and reads
// a file; neither belongs on a page with nothing wrong.
func TestAHealthySiteRunsNoDiagnostic(t *testing.T) {
	src := readSourceFile(t, "admin_os_scoped_home.go")
	body := goFuncBody(src, "handleOSScopedHome")
	i := strings.Index(body, "diagnoseCertificate")
	if i < 0 {
		t.Fatal("the console never diagnoses a pending certificate")
	}
	guard := body[:i]
	if !strings.Contains(guard, "scopedNeedsCertificate") {
		t.Error("the diagnostic is not gated on whether this site is waiting for a certificate, " +
			"so every console page does a DNS lookup and a file read for nothing")
	}
	// The guard moved behind a name, so the condition itself is asserted where it
	// now lives. A gate whose only test is "some function is called" passes just
	// as happily when that function returns true unconditionally.
	pred := goFuncBody(src, "scopedNeedsCertificate")
	if !strings.Contains(pred, "TLSActive") {
		t.Error("the predicate does not look at the certificate state at all")
	}
}

// FINDING — every Tor site's console announced a certificate problem it does not
// have, and told the operator to fix it with DNS.
//
// A .onion is registered sync-approved, active and TLS-pending, and it stays
// TLS-pending for its whole life: an onion is served over http by design and no
// CA could issue for it. The console read that state literally and printed "no
// certificate has been issued … the browser refuses the page", with a diagnosis
// beneath it saying the name "does not resolve yet — point it at this server",
// for an address DNS does not resolve at all.
//
// Both statements are confident and false, which is the defect class this
// console was built to remove rather than commit.
func TestAnOnionSiteIsNotToldItsCertificateIsMissing(t *testing.T) {
	onion := testDomain("tor01", strings.Repeat("a", 56)+".onion")
	onion.TLSState = domain.TLSPending
	onion.SyncState = domain.SyncApproved

	if scopedNeedsCertificate(onion) {
		t.Fatal("an onion is treated as waiting for a CA certificate, so its console runs the " +
			"certificate diagnostic and tells the operator to point DNS at this server for a " +
			"name the DNS system does not resolve")
	}
	page := scopedConsolePage(onion, 0, 0, 0, false, nil, nil, nil, nil)
	for _, claim := range []string{"no certificate has been issued", "data-site-provision"} {
		if strings.Contains(page, claim) {
			t.Errorf("an onion site's console still carries %q", claim)
		}
	}
	// And the tile must not be amber either — an alarm on a site working exactly
	// as designed teaches the operator to stop reading the tile.
	if label, tone := scopedCertTile(onion); tone != "" || label == "Pending" {
		t.Errorf("the certificate tile reads %q/%q for an onion; nothing is wrong with it", label, tone)
	}

	// The clearnet site beside it must still be diagnosed, or this "fix" is just
	// the diagnostic switched off.
	web := testDomain("web01", "client.example")
	web.TLSState = domain.TLSPending
	web.SyncState = domain.SyncApproved
	if !scopedNeedsCertificate(web) {
		t.Fatal("a pending clearnet site is no longer treated as waiting for a certificate")
	}
}

// FINDING — a healthy run from YESTERDAY was reported as a pass while the
// request made thirty seconds ago sat unconsumed.
//
// The check called a run ok on Failed==0 && Ran>0 and never looked at WHEN it
// happened. An operator pressed Provision now, watched the page report four
// green checks including "Last run … 0 reported a problem", and concluded the
// fault was somewhere else. A stale success displayed as a current one is worse
// than no check.
func TestAStaleRunIsNotReportedAsAPass(t *testing.T) {
	old := provisionResult{
		FinishedAt: time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339),
		Ran:        5, Failed: 0, Details: "setup-vayudomain.sh=ok",
	}
	if runFinishedAfter(old, time.Now().Add(-time.Minute)) {
		t.Fatal("a run from two days ago was treated as having answered a request made a " +
			"minute ago, so the console reports the operator's own click as successful")
	}
	// An unreadable timestamp must not be read as "yes". Guessing that a request
	// was consumed, on the strength of a date we cannot parse, is the kind of
	// assertion this page exists to stop making.
	if runFinishedAfter(provisionResult{FinishedAt: "not-a-date"}, time.Now().Add(-time.Hour)) {
		t.Error("an unparseable finish time was treated as a run that answered the request")
	}
	// And a genuinely fresh run must still pass, or the check is a refusal.
	fresh := provisionResult{FinishedAt: time.Now().UTC().Format(time.RFC3339), Ran: 5}
	if !runFinishedAfter(fresh, time.Now().Add(-time.Minute)) {
		t.Error("a run that finished just now was not credited with answering the request")
	}
}

// FINDING — the in-app updater swaps the BINARY ONLY.
//
// It runs unprivileged and cannot write to /usr/local/lib/vayupress, so an
// install can be entirely up to date and still be running month-old shell
// helpers. Every version number on every page says current. That is exactly what
// happened: the binary carried the fix for the failure and the helper that trips
// over it did not, and nothing anywhere said the two could differ.
func TestTheConsoleNoticesStaleRootSideHelpers(t *testing.T) {
	// A driver with the fixes.
	if ok, _ := driverCarriesReportingFixes(
		`grep -qi "ALREADY invalid"` + "\n" + `grep -qiE "skipping|nothing to do|nothing to provision"`,
	); !ok {
		t.Error("a current driver was reported as stale")
	}
	// A driver without them — the state an install is in after updating the
	// binary alone, which is every install that has ever used the in-app updater.
	// Each fix is checked SEPARATELY. A fixture missing both moves together with
	// either check, so removing one of them changes no verdict — which is how a
	// mutation on the nginx classification survived the first version of this
	// test. One fixture per missing fix, or the checks are untested individually.
	if only, _ := driverCarriesReportingFixes(
		`grep -qiE "skipping|nothing to do|nothing to provision"`,
	); only {
		t.Error("a driver that cannot recognise a broken-nginx abort was reported as current; " +
			"that run aborts having done nothing and is counted as a helper that did work")
	}
	if only, _ := driverCarriesReportingFixes(`grep -qi "ALREADY invalid"`); only {
		t.Error("a driver that counts a no-op helper as one that did work was reported as current")
	}

	ok, why := driverCarriesReportingFixes(`grep -qi "skipping"`)
	if ok {
		t.Fatal("a driver that cannot tell a broken-nginx abort from a clean run was reported " +
			"as up to date, so the numbers on this page come from a report that cannot " +
			"distinguish success from silence and nothing says so")
	}
	if !strings.Contains(why, "unprivileged") {
		t.Error("the stale-helper message does not explain WHY updating from the console " +
			"cannot refresh them, so it reads as the updater being broken")
	}

	// UNREADABLE must be reported as not-current. A verdict of "up to date" for
	// a file that could not be opened is asserted on the absence of evidence —
	// the same unearned reassurance this whole diagnostic exists to remove.
	old := provisionDriverPath
	provisionDriverPath = filepath.Join(t.TempDir(), "does-not-exist.sh")
	t.Cleanup(func() { provisionDriverPath = old })
	if fresh, reason := provisionHelpersCurrent(); fresh {
		t.Fatalf("an unreadable driver was reported as up to date: %q", reason)
	}
}

// A check must not claim to block something it does not block.
//
// The stale-helper check shipped as Fatal, so the console painted "blocking"
// beside it and sent the operator to refresh shell scripts — while the thing
// actually stopping their certificate was elsewhere. Stale helpers degrade the
// run's REPORT; they do not stop certbot. Overstating that is the same defect as
// a panel row overstating what is enforcing, committed by the check written to
// find exactly that.
func TestStaleHelpersAreNotClaimedToBlockACertificate(t *testing.T) {
	src := readSourceFile(t, "admin_os_scoped_diagnose.go")
	body := goFuncBody(src, "diagnoseCertificate")
	i := strings.Index(body, `"Root-side helpers up to date"`)
	if i < 0 {
		t.Fatal("the stale-helper check is gone")
	}
	seg := body[i : i+400]
	if strings.Contains(seg, "Fatal: !fresh") {
		t.Fatal("stale helpers are marked as blocking the certificate. They make the run's " +
			"report unreliable; they do not stop certbot, and saying so sends the operator to " +
			"fix the thing that is not stopping them")
	}
	if !strings.Contains(seg, "does NOT stop a certificate") {
		t.Error("the stale-helper detail does not say what it does and does not affect, so it " +
			"reads as the cause")
	}
}

// The request must RE-ARM a stuck watcher.
//
// The systemd unit is `.path` with `PathExists=`, which fires when the file
// appears. A request never consumed — worker failed, start limit tripped —
// leaves the file in place, the condition never goes false, and no rewrite of
// the same path triggers anything again. The install reaches a state where the
// button is enabled, reports success, and can never cause a run.
func TestRequestingAProvisionClearsAStaleRequestFirst(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "admin_os_provision.go"), "handleOSProvisionRequest")
	rm := strings.Index(body, "os.Remove(path)")
	write := strings.Index(body, "os.WriteFile(path")
	if rm < 0 {
		t.Fatal("a stale request is never removed, so a watcher stuck on an unconsumed request " +
			"can never be re-armed from the console — the button stays enabled and does nothing")
	}
	if write < 0 {
		t.Fatal("the request is never written")
	}
	if rm > write {
		t.Error("the stale request is removed AFTER the new one is written, which deletes the " +
			"request that was just made")
	}
}

// FINDING — the console had both files and compared neither.
//
// The worker appends to provision.log throughout and writes provision.result
// only at the very end. A log newer than the result therefore means a run
// STARTED and did not finish recording: died, was killed by its start timeout,
// or exited early. Without that comparison the page shows "Last run <days ago>"
// beside a worker that has been executing all along, and an operator pressing
// the button cannot tell "it never ran" from "it ran and told you nothing".
func TestARunThatNeverRecordedItsResultIsNamed(t *testing.T) {
	now := time.Now()

	// The reported state: a run that wrote to its log, never recorded a result,
	// and has been SILENT since. The silence is what makes it dead rather than
	// underway — see the sibling assertion below, which is the same shape of
	// evidence read the other way.
	c := workerTraceCheck(now.Add(-30*time.Minute), true, now.Add(-72*time.Hour), true)
	if c.OK {
		t.Fatal("a worker that has been writing its log for days while its recorded result " +
			"stands still was reported as healthy — this is the state an operator sees as " +
			"'nothing happens', and the page had both timestamps all along")
	}
	if !c.Fatal {
		t.Error("a run that never records a result is not marked as blocking, so it reads as " +
			"a detail beside the stale numbers it invalidates")
	}
	for _, want := range []string{"WROTE TO ITS LOG", "older run"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("the finding never says %q, so it does not distinguish a dead run from "+
				"an absent one", want)
		}
	}

	// A worker that logged and recorded moments apart is the normal case and
	// must stay quiet, or the check is noise on every healthy install.
	if ok := workerTraceCheck(now.Add(-30*time.Second), true, now.Add(-25*time.Second), true); !ok.OK {
		t.Errorf("a normal run was flagged: %s", ok.Detail)
	}

	// Never having run at all is a different statement from having run badly.
	none := workerTraceCheck(time.Time{}, false, time.Time{}, false)
	if none.OK || !strings.Contains(none.Detail, "never run") {
		t.Error("an install where the worker has never run does not say so distinctly")
	}
}

// FINDING — the check above, written to stop the console overstating things,
// overstated one itself.
//
// A worker still executing writes its log throughout and records a result only
// when it finishes. That is indistinguishable, on timestamps alone, from a run
// that died — except for one thing: whether the log is STILL MOVING. The first
// version ignored that and called every log-newer-than-result "running and
// dying", so an operator whose certbot was midway through several new domains —
// which genuinely takes minutes — was told their working install was broken, on
// the strength of a timestamp that said the opposite.
func TestARunStillUnderwayIsNotCalledADeadOne(t *testing.T) {
	now := time.Now()

	live := workerTraceCheck(now.Add(-time.Minute), true, now.Add(-72*time.Hour), true)
	if !live.OK || live.Fatal {
		t.Fatalf("a worker that wrote to its log a minute ago is reported as a failure: %q — %s",
			live.Label, live.Detail)
	}
	if !strings.Contains(live.Detail, "previous run") {
		t.Error("the in-progress verdict does not warn that the numbers below it are still the " +
			"previous run's, which is the one thing that could mislead while it finishes")
	}

	// The boundary is the point of the check: past the active window, silence
	// means dead, and it must go back to being blocking.
	dead := workerTraceCheck(now.Add(-provisionRunActiveFor-time.Minute), true, now.Add(-72*time.Hour), true)
	if dead.OK || !dead.Fatal {
		t.Error("a run that stopped writing its log long ago is no longer reported as dead, so " +
			"the in-progress branch now swallows the failure it was carved out of")
	}
}

// FINDING — a bare UTC timestamp read as "yesterday" for a run that had just
// happened, and it cost a real misdiagnosis.
//
// The page printed 2026-08-02T18:43:02Z. On the server, in IST, that same moment
// is 2026-08-03 00:13:02 — after midnight, TODAY. The 5½ hour offset crosses the
// date line, so a run eight milliseconds from its own result, on a completely
// healthy worker, was read as days-stale by everyone who looked at it and sent
// the investigation in the wrong direction twice.
//
// A timestamp the reader has to convert in their head is a number they have to
// trust, and this page exists to stop asking for trust.
func TestEveryTimestampCarriesHowLongAgoItWas(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		at   time.Time
		want string
	}{
		{now.Add(-30 * time.Second), "seconds ago"},
		{now.Add(-45 * time.Minute), "minutes ago"},
		{now.Add(-16 * time.Hour), "hours ago"},
		{now.Add(-72 * time.Hour), "days ago"},
	} {
		got := stamp(c.at)
		if !strings.Contains(got, c.want) {
			t.Errorf("stamp(%v) = %q, which never says %q — a reader in another timezone "+
				"cannot tell how old it is", c.at, got, c.want)
		}
		// The absolute time must survive too: it is what matches the server's own
		// files, and dropping it would trade one unverifiable number for another.
		if !strings.Contains(got, c.at.UTC().Format("2006-01-02")) {
			t.Errorf("stamp(%v) = %q dropped the absolute time, so it no longer matches what "+
				"the server's files show", c.at, got)
		}
	}

	// The exact case that misled everyone: a run 16 hours old, printed with a
	// date that reads as the previous day in IST.
	sixteen := stamp(now.Add(-16 * time.Hour))
	if !strings.Contains(sixteen, "16 hours ago") {
		t.Fatalf("a 16-hour-old run does not say so: %q. This is the reading that made a "+
			"healthy worker look days-stale", sixteen)
	}
}

// FINDING — "the name resolves, so the challenge can reach this server" does not
// follow, and the install this console was built on is the counterexample.
//
// A domain registered here pointed at a static-hosting provider. It resolved
// perfectly, and every provisioning run asked for a certificate whose challenge
// was answered by that provider with a 404. The check said the DNS was fine.
//
// It is fatal rather than a note because the failure is permanent and silent:
// every run reports a problem for that domain forever, which buries a NEW fault
// among the standing one. (An earlier version of this comment said it spent the
// whole account's validation budget. It does not — see
// TestAStaleAAAAIsNamedRatherThanBlamedOnSomebodyElse for why that was wrong and
// what the real cross-domain mechanism is.)
//
// SECOND FINDING, from attacking the first fix: "not an address this machine
// holds" is not the same statement as "not this install". On a CDN-fronted or
// NAT-ed install EVERY hosted name resolves to the front, and the first version
// called all of them blocking — contradicting this product's own DNS page, which
// says in as many words that this is "normal behind NAT, and not a fault". So
// the primary's addresses are the discriminator: shared with the primary means
// this install's own front; shared with nothing means a different host.
func TestADomainPointedElsewhereIsNotCalledReachable(t *testing.T) {
	local := map[string]bool{"203.0.113.7": true, "2001:db8::7": true}
	front := []string{"198.51.100.1"} // what the primary resolves to: a proxy

	elsewhere := dnsPointsHereCheck("client.example", []string{"203.0.113.10"}, true, local, front)
	if elsewhere.OK {
		t.Fatal("a domain resolving to a host unrelated to this install was reported as " +
			"reachable — the exact verdict that hid a domain burning the account's validation " +
			"budget on every run")
	}
	if !elsewhere.Fatal {
		t.Error("it is reported as a detail rather than as blocking, so it reads as background " +
			"noise beside the certificate that is not arriving")
	}
	for _, want := range []string{"203.0.113.10", "404", "Lifecycle"} {
		if !strings.Contains(elsewhere.Detail, want) {
			t.Errorf("the finding never mentions %q, so the operator cannot act on it", want)
		}
	}

	// Pointed here: reported as such, and NOT as a blocker.
	if c := dnsPointsHereCheck("client.example", []string{"203.0.113.7"}, true, local, front); !c.OK || c.Fatal {
		t.Errorf("a correctly-pointed domain was flagged: %q — %s", c.Label, c.Detail)
	}

	// THE REGRESSION THIS GUARDS. Behind a CDN or a NAT router every hosted name
	// resolves to the front, which is not a local address. Calling that blocking
	// cries wolf on the correct configuration — and this product's own DNS page
	// already tells the operator it is not a fault. One product must not return
	// two opposite verdicts on one fact.
	proxied := dnsPointsHereCheck("client.example", front, true, local, front)
	if !proxied.OK || proxied.Fatal {
		t.Errorf("a site behind the same front as the primary was called broken: %q — %s. "+
			"That is every CDN-fronted and every NAT-ed install", proxied.Label, proxied.Detail)
	}
	if !strings.Contains(proxied.Detail, "acme-challenge") {
		t.Error("the proxied verdict does not name the one thing that must be true of the front, " +
			"so it reads as an unconditional pass")
	}

	// Nothing to compare against — no local addresses, or a primary that would
	// not resolve — must claim NOTHING rather than guess. Asserting a fault we
	// cannot substantiate is the failure this page exists to correct.
	for _, blind := range []diagCheck{
		dnsPointsHereCheck("client.example", []string{"203.0.113.10"}, true, map[string]bool{}, front),
		dnsPointsHereCheck("client.example", []string{"203.0.113.10"}, true, local, nil),
	} {
		if !blind.OK || blind.Fatal {
			t.Error("with nothing trustworthy to compare against, the check still declared the " +
				"DNS wrong — a verdict asserted on the absence of evidence")
		}
		if !strings.Contains(blind.Detail, "nothing is claimed") {
			t.Error("the blind case does not say it is not claiming anything, so it reads as a pass")
		}
	}

	// A name that does not resolve at all is a different statement again.
	if c := dnsPointsHereCheck("client.example", nil, false, local, front); c.OK || !c.Fatal {
		t.Error("an unresolvable name is not reported as blocking")
	}
}

// FINDING — this page claimed a mechanism that does not exist, and missed the
// one that does.
//
// It said a domain that cannot validate "spends the budget every other site on
// this install needs". The install's own provisioning script disproves it: every
// host is issued in its own certbot run under its own --cert-name, in a loop
// that continues past a failure, and the Failed Validation limit is scoped per
// account PER HOSTNAME. A stuck domain is loud, not contagious — and telling an
// operator otherwise sends them to remove a domain to unblock something that was
// never blocked by it.
//
// The real mechanism is the address family. Validation goes over IPv6 first
// whenever an AAAA exists. One that refuses the connection is retried over IPv4;
// one that ACCEPTS and answers as a different site is not — so a stale AAAA
// fails the issuance with a perfectly correct A record beside it, and nothing in
// the error mentions IPv6.
func TestAStaleAAAAIsNamedRatherThanBlamedOnSomebodyElse(t *testing.T) {
	local := map[string]bool{"198.51.100.10": true}
	front := []string{"198.51.100.20"}

	split := dnsPointsHereCheck("test.example", []string{"198.51.100.10", "2001:db8::1"}, true, local, front)
	if split.OK {
		t.Fatal("a host whose A record is correct and whose AAAA points somewhere else was " +
			"reported as pointing here — the issuance fails over IPv6 and the operator is " +
			"looking at a green check")
	}
	for _, want := range []string{"2001:db8::1", "IPv6", "IPv4"} {
		if !strings.Contains(split.Detail, want) {
			t.Errorf("the finding never mentions %q, so it does not identify the record to fix", want)
		}
	}

	// A host answering correctly on BOTH families must stay quiet.
	both := map[string]bool{"198.51.100.10": true, "2001:db8::1": true}
	if c := dnsPointsHereCheck("test.example", []string{"198.51.100.10", "2001:db8::1"}, true, both, front); !c.OK {
		t.Errorf("a dual-stack host answering on both families was flagged: %s", c.Detail)
	}
	// A family that is PARTLY accounted for stays quiet, and this is a deliberate
	// limit rather than an oversight. The machine demonstrably answers on IPv6; a
	// second AAAA is as likely to be a legitimate second interface as a stale
	// record, and only the authority knows which address it will pick. Flagging on
	// a guess is the cry-wolf failure that made the previous version of this check
	// wrong about every CDN. The residual risk is real and stated here rather than
	// papered over: if the authority happens to pick the address this machine does
	// not hold, issuance fails and this check will not have said so.
	partial := map[string]bool{"198.51.100.10": true, "2001:db8::1": true}
	if c := dnsPointsHereCheck("test.example",
		[]string{"198.51.100.10", "2001:db8::1", "2001:db8::99"}, true, partial, front); !c.OK {
		t.Errorf("a host answering on IPv6 was flagged because it publishes a SECOND IPv6 "+
			"address: %s. Only a family that is entirely unaccounted for is a finding", c.Detail)
	}

	// So must a single-family host — an absent AAAA is not a broken one.
	if c := dnsPointsHereCheck("test.example", []string{"198.51.100.10"}, true, local, front); !c.OK {
		t.Errorf("an IPv4-only host was flagged: %s", c.Detail)
	}
	// An address belonging to this install's OWN FRONT counts as reachable. The
	// case that matters is cross-family — origin over IPv4, front over IPv6 — and
	// it is the one a same-family fixture cannot distinguish at all: a mutation
	// dropping the front from the comparison survived a test written that way,
	// because the good IPv4 address alone satisfied the IPv4 family.
	frontDual := []string{"198.51.100.20", "2001:db8::b27"}
	if c := dnsPointsHereCheck("test.example",
		[]string{"198.51.100.10", "2001:db8::b27"}, true, local, frontDual); !c.OK {
		t.Errorf("an IPv6 address belonging to this install's own front was called a stray: %s. "+
			"That is every dual-stack site whose AAAA is on the proxy and whose A is direct", c.Detail)
	}

	// And the retired claim must not come back anywhere in the verdicts.
	stray := dnsPointsHereCheck("other.example", []string{"203.0.113.10"}, true, local, front)
	if strings.Contains(stray.Detail, "spends the budget") {
		t.Error("the per-account rate-limit claim is back; it is not how this install issues " +
			"certificates and it sends operators to fix an unrelated domain")
	}
	if !strings.Contains(stray.Detail, "does not block the other sites") {
		t.Error("the verdict does not say what it does NOT cause, which is the half that was wrong")
	}
}

// Folding the certificate panels into accordions must not HIDE anything.
//
// They were flat, permanently-expanded cards and between them most of the page,
// so collapsing them is right. But a collapsed panel quietly holding the reason
// the button will not work would be the same defect as the tile that said
// "pending" and offered nothing to do about it. Two guarantees, therefore: the
// verdict reads from the SUMMARY while collapsed, and a blocking check forces
// the diagnosis open by itself.
func TestTheCollapsedCertificatePanelsStillTellTheTruth(t *testing.T) {
	d := testDomain("abc123", "client.example")
	d.TLSState = domain.TLSPending
	d.SyncState = domain.SyncApproved

	blocked := scopedCertificateSection(d, []diagCheck{
		{Label: "Root-side helper installed", OK: true, Detail: "present"},
		{Label: "DNS points somewhere else", OK: false, Fatal: true, Detail: "203.0.113.10"},
	}, []string{"a log line"})

	// The chip must be in the summary, not the body: everything inside
	// mon-acc__body is invisible until someone clicks.
	sum, _, ok := strings.Cut(blocked, `<div class="mon-acc__body">`)
	if !ok {
		t.Fatal("the accordion has no body, so its markup is not what this asserts against")
	}
	if !strings.Contains(sum, "pending") {
		t.Error("the certificate accordion's collapsed summary does not say the certificate is " +
			"pending, so folding the panel hid the state it existed to show")
	}
	if !strings.Contains(blocked, "1 blocking") {
		t.Error("the diagnosis chip does not carry the blocking count, so a collapsed panel " +
			"reads as 'nothing to see' while holding the reason the button will not work")
	}

	// A blocking check must open the diagnosis without being clicked.
	i := strings.Index(blocked, "What this console checked")
	if i < 0 {
		t.Fatal("the diagnosis accordion is gone")
	}
	if !strings.Contains(blocked[strings.LastIndex(blocked[:i], "<details"):i], "open") {
		t.Error("a blocking check does not force the diagnosis open, so the operator has to " +
			"guess that the collapsed panel is where the answer is")
	}

	// With nothing blocking it stays shut, and says so — otherwise "open" carries
	// no information and the auto-open above is decoration.
	clean := scopedCertificateSection(d, []diagCheck{
		{Label: "Root-side helper installed", OK: true, Detail: "present"},
	}, nil)
	j := strings.Index(clean, "What this console checked")
	if j < 0 {
		t.Fatal("the diagnosis accordion is gone from the clean page")
	}
	if strings.Contains(clean[strings.LastIndex(clean[:j], "<details"):j], "open") {
		t.Error("the diagnosis opens itself when nothing is blocking, so opening means nothing")
	}
	if !strings.Contains(clean, "nothing blocking") {
		t.Error("a clean diagnosis does not say so from its summary")
	}
}

// FINDING — the collapsed summary promised the certificate was "one root-side
// step away" while the diagnosis three lines below it said the DNS points at
// somebody else's server.
//
// Both were on the same page, at the same moment, and the reassuring one was the
// one that stays visible when the panel is folded. Telling an operator to wait
// for something that can never arrive is the defect this console exists to
// remove, and folding the panels is what made a stale subtitle load-bearing.
func TestTheCertificateSummaryDoesNotPromiseWhatIsBlocked(t *testing.T) {
	d := testDomain("abc123", "client.example")
	d.TLSState = domain.TLSPending
	d.SyncState = domain.SyncApproved

	blocked := scopedCertificateSection(d, []diagCheck{
		{Label: "DNS points at a different host", OK: false, Fatal: true, Detail: "203.0.113.10"},
	}, nil)
	sum, _, _ := strings.Cut(blocked, `<div class="mon-acc__body">`)
	if strings.Contains(sum, "one root-side step away") {
		t.Fatal("the collapsed summary still says the certificate is one step away while a " +
			"blocking check says it cannot be issued at all — the reassuring half is the half " +
			"that stays on screen")
	}
	if !strings.Contains(sum, "Blocked") {
		t.Error("a blocked certificate does not say so from its summary")
	}

	// And the ordinary waiting case must keep its reassurance, or the fix is just
	// pessimism applied to every site.
	waiting := scopedCertificateSection(d, []diagCheck{
		{Label: "Root-side helper installed", OK: true, Detail: "present"},
	}, nil)
	if !strings.Contains(waiting, "one root-side step away") {
		t.Error("a site merely waiting on the sweep is now described as blocked")
	}
}

// FINDING, straight off a live console — the log box on a site's page showed a
// DIFFERENT site's failure, in full, with nothing saying so.
//
// A run covers five helpers across every hosted domain, and the box showed the
// last 25 lines of it. On the install this was found on those 25 lines were
// entirely one domain's certbot refusal, on the console page of a domain whose
// own output had scrolled away. The artifact included so the operator need not
// take a verdict on trust was itself misleading them.
func TestTheLogBoxShowsTHISHostsLinesNotTheTailOfSomebodyElses(t *testing.T) {
	log := []string{
		"Provisioning first.example…",
		"Waiting for verification...",
		"VayuDomain live: https://first.example",
		"Provisioning other.example…",
		"Challenge failed for domain other.example",
		"Detail: 203.0.113.10: Invalid response ...: 404",
		"certbot could not issue a cert for other.example.",
	}

	seg := hostLogSegment(log, "first.example")
	if len(seg) == 0 {
		t.Fatal("this host has no section at all, so the page falls back to the tail — which is " +
			"the other domain's failure")
	}
	for _, leak := range []string{"other.example", "203.0.113.10"} {
		if strings.Contains(strings.Join(seg, "\n"), leak) {
			t.Errorf("another domain's line (%q) is inside this host's section", leak)
		}
	}

	body := scopedDiagnosticBody(nil, log, "first.example")
	if strings.Contains(body, "203.0.113.10") {
		t.Fatal("the page still prints another domain's certbot error under this domain's " +
			"heading — the exact thing that sent an operator chasing the wrong host")
	}

	// When this host genuinely has no section, the fallback must SAY it is not
	// about this host. Silence there is how the bug read as correct.
	none := scopedDiagnosticBody(nil, log, "absent.example")
	if !strings.Contains(none, "no section for") {
		t.Error("with no lines for this host the page shows other output without saying it is " +
			"other output")
	}

	// A host whose name is a SUFFIX of another must neither capture nor be
	// captured by it. The ORDER matters: the shorter name has to come FIRST, so
	// that ending its section depends on recognising the longer name as a
	// different host. A fixture with them the other way round passes against a
	// broken end-condition and proves nothing — it did.
	nested := []string{
		"Provisioning example.test…", "line a",
		"Provisioning site.example.test…", "line b", "line c",
	}
	got := hostLogSegment(nested, "example.test")
	if len(got) != 2 {
		t.Errorf("example.test's section is %d lines (%v) — it swallowed site.example.test's, so a "+
			"subdomain's failure prints under the apex's heading", len(got), got)
	}
	if strings.Contains(strings.Join(got, "\n"), "line b") {
		t.Error("example.test's section contains site.example.test's output")
	}
	if sub := hostLogSegment(nested, "site.example.test"); len(sub) != 3 {
		t.Errorf("site.example.test's own section is %d lines (%v)", len(sub), sub)
	}
}

// FINDING — the panel said "0 reported a problem" beside a log that read
// "certbot could not issue a cert for …". Both from the same run.
//
// The summary counts HELPERS. setup-vayudomain.sh loops over hosts with
// `continue` on failure and exited 0 regardless, so a helper that failed to
// issue a certificate was indistinguishable from one that issued every
// certificate asked of it. Continuing past a failure and REPORTING success are
// two different decisions and only the first was intended.
//
// The driver now counts host failures — and that fix reaches nobody who already
// has the bug, because the in-app updater swaps the binary and cannot write to
// /usr/local/lib/vayupress. So the binary reads the log itself. That is the half
// that arrives through Update & Backup, which is the whole point.
func TestARefusedCertificateIsReportedEvenWhenTheRunSaysItIsClean(t *testing.T) {
	seg := []string{
		"Provisioning other.example…",
		"Type:   unauthorized",
		"Detail: 203.0.113.10: Invalid response ...: 404",
		"certbot could not issue a cert for other.example.",
	}
	v, ok := certbotHostVerdict(seg, "other.example")
	if !ok {
		t.Fatal("a run that was refused a certificate for this host produces no finding, so the " +
			"page keeps reporting the helper summary's '0 problems'")
	}
	if v.OK || !v.Fatal {
		t.Error("the refusal is not reported as blocking")
	}
	for _, want := range []string{"unauthorized", "404", "counts HELPERS"} {
		if !strings.Contains(v.Detail, want) {
			t.Errorf("the finding never mentions %q", want)
		}
	}

	// A host that succeeded must read as success, not as the absence of failure.
	if s, ok := certbotHostVerdict([]string{"VayuDomain live: https://ok.example"}, "ok.example"); !ok || !s.OK {
		t.Error("a host whose certificate was issued is not reported as issued")
	}
	// And a segment with no outcome yields no check at all, rather than a guess.
	if _, ok := certbotHostVerdict([]string{"Provisioning x.example…"}, "x.example"); ok {
		t.Error("a segment with no recorded outcome invented one")
	}

	// The shell half must be there too, or a fresh install keeps the old lie.
	//
	// Asserted on the GUARD's own body, not on the file containing "exit 1"
	// somewhere. The script has other non-zero exits, so a whole-file substring
	// check passes with this one deleted — which is exactly what it did.
	src := readSourceFile(t, "../../scripts/setup-vayudomain.sh")
	i := strings.Index(src, "HOST_FAILURES > 0")
	if i < 0 {
		t.Fatal("the provisioning helper does not count host failures, so a run that could not " +
			"issue a certificate is recorded by the driver as clean")
	}
	j := strings.Index(src[i:], "\nfi")
	if j < 0 || !strings.Contains(src[i:i+j], "exit 1") {
		t.Error("the host-failure guard does not exit non-zero, so the driver still classifies " +
			"the helper as ok and the panel still says 0 problems")
	}
	if !strings.Contains(src, "HOST_FAILURES=$((HOST_FAILURES + 1))") {
		t.Error("nothing ever increments the counter, so the guard can never fire")
	}
}

// Terminal colour escapes were being rendered on the page: an operator read
// "[1;33m  certbot could not issue …  [0m". Escaped, so harmless — and noise in
// the middle of the one artifact this page exists to show plainly.
func TestTerminalColourCodesDoNotReachThePage(t *testing.T) {
	got := stripANSI("\x1b[1;33m⚠  certbot could not issue a cert for x.example.\x1b[0m")
	if strings.Contains(got, "[1;33m") || strings.Contains(got, "[0m") || strings.ContainsRune(got, 0x1b) {
		t.Fatalf("colour escapes survived: %q", got)
	}
	if !strings.Contains(got, "certbot could not issue a cert for x.example.") {
		t.Fatalf("stripping the escapes took the message with it: %q", got)
	}
	if plain := "no escapes here"; stripANSI(plain) != plain {
		t.Error("a line with no escapes was altered")
	}
}

// FINDING, and the most expensive one in this whole investigation: the console
// ranked its own DEDUCTION above the authority's own MESSAGE.
//
// The certificate authority reported, for site.example.test, "Type: connection" at
// 198.51.100.10 — the IPv4 address, which is this server. The console's DNS
// check had separately deduced that the site's AAAA points elsewhere, and put
// that deduction in a row ABOVE the authority's error. The deduction was read as
// the cause for three releases while the actual message sat further down the
// same page, saying something different: nothing answered on port 80 at all.
//
// A page that has the answer and ranks a guess above it is not a diagnostic.
func TestTheAuthoritysOwnErrorOutranksTheConsolesDeduction(t *testing.T) {
	src := readSourceFile(t, "admin_os_scoped_diagnose.go")
	body := goFuncBody(src, "diagnoseCertificate")
	verdict := strings.Index(body, "certbotHostVerdict")
	deduction := strings.Index(body, "dnsPointsHereCheck")
	if verdict < 0 || deduction < 0 {
		t.Fatal("one of the two checks is gone")
	}
	if verdict > deduction {
		t.Fatal("the console still reports what it deduced about DNS ABOVE what the certificate " +
			"authority actually said. Direct evidence must lead; the ordering is the finding")
	}
}

// "Type: connection" and "Type: unauthorized" are entirely different faults with
// the same visible outcome, and the console printed them raw. One is a network
// path — nothing answered. The other is a webroot — something answered with the
// wrong thing. Telling them apart is most of the work.
func TestTheAuthoritysErrorTypeIsExplainedNotJustQuoted(t *testing.T) {
	conn := certbotErrorKind([]string{"Type:   connection", "Detail: 198.51.100.10: Fetching"})
	if !strings.Contains(conn, "CONNECTION") || !strings.Contains(conn, "nothing answered") {
		t.Errorf("a connection error is not explained as a network path: %q", conn)
	}
	if strings.Contains(conn, "wrong thing") {
		t.Error("a connection error is being described as a content problem")
	}

	un := certbotErrorKind([]string{"Type:   unauthorized", "Detail: 203.0.113.10: 404"})
	if !strings.Contains(un, "UNAUTHORIZED") || !strings.Contains(un, "wrong") {
		t.Errorf("an unauthorized error is not explained as a content problem: %q", un)
	}
	// The two must not read the same, or the distinction is decorative.
	if conn == un {
		t.Fatal("both error types produce identical prose")
	}
	if certbotErrorKind([]string{"no type line here"}) != "" {
		t.Error("an unrecognised error invented an explanation")
	}
}

// The probe is what actually splits a connection error in two, so it must be
// wired in, must carry this site's Host header, and must not follow redirects.
//
// The Host header selects this site's vhost rather than the default one — which
// is exactly what the authority's request does, and without it the probe tests
// the wrong server block entirely. Following a redirect would hide the failure
// it exists to catch: a 301 means the catch-all matched instead of the challenge
// location, which no certificate can survive.
func TestTheChallengeProbeAsksAsTheAuthorityWould(t *testing.T) {
	src := readSourceFile(t, "admin_os_scoped_diagnose.go")
	body := goFuncBody(src, "challengeProbe")
	// The request itself is built one function down, since the probe now tries
	// several addresses. The assertions follow the code rather than being
	// loosened: a gate that stops looking where the logic moved is a gate that
	// has quietly stopped gating.
	attempt := goFuncBody(src, "probeChallengeAt")
	if !strings.Contains(attempt, "req.Host = host") {
		t.Fatal("the probe does not set the Host header, so it exercises the default vhost " +
			"rather than this site's — a pass would prove nothing about the site being diagnosed")
	}
	if !strings.Contains(attempt, "ErrUseLastResponse") {
		t.Error("the probe follows redirects, so a catch-all 301 — the exact misconfiguration " +
			"that breaks issuance — would be reported as success")
	}
	if !strings.Contains(body, "os.Remove") {
		t.Error("the probe leaves its token in the public webroot")
	}
	// And it must be reached from the diagnosis, or none of the above matters.
	if !strings.Contains(goFuncBody(src, "diagnoseCertificate"), "challengeProbe") {
		t.Fatal("the probe is never called")
	}
}

// The vhost check is the one that ends the guessing, so its matcher must be
// exact. "example.test" is a substring of "site.example.test": a substring test would
// report the apex's server block as covering every subdomain of it, call a
// MISSING vhost present, and turn the single decisive check on the page into a
// confident wrong answer — which is worse than the silence it replaced.
func TestAVhostIsMatchedByWholeNameNotSubstring(t *testing.T) {
	apex := "server {\n  listen 80;\n  server_name example.test www.example.test;\n}\n"

	if serverNameClaims(apex, "site.example.test") {
		t.Fatal("the apex's server block was reported as claiming site.example.test — the console " +
			"would say a vhost exists for a host that falls through to the default server, " +
			"which is the exact failure it was written to find")
	}
	if !serverNameClaims(apex, "example.test") {
		t.Error("a host its own server block does name was not matched")
	}
	if !serverNameClaims(apex, "EXAMPLE.TEST") {
		t.Error("server_name matching is case-sensitive; DNS names are not")
	}
	// A trailing dot is the same name.
	if !serverNameClaims("server_name site.example.test.;", "site.example.test") {
		t.Error("a fully-qualified server_name with a trailing dot was not matched")
	}
	// And the reverse direction: a subdomain's block must not claim the apex.
	if serverNameClaims("server_name site.example.test;", "example.test") {
		t.Error("a subdomain's server block was reported as claiming the apex")
	}
	// A line that merely mentions the host is not a server_name directive.
	if serverNameClaims("# example.test is the primary\nlocation / { }", "example.test") {
		t.Error("a comment mentioning the host counted as a server block claiming it")
	}
}

// The console must not claim anything when it cannot read nginx's config —
// the same rule every other check on this page follows.
func TestAnUnreadableNginxDirClaimsNothing(t *testing.T) {
	saved := nginxSitesEnabled
	t.Cleanup(func() { nginxSitesEnabled = saved })
	nginxSitesEnabled = filepath.Join(t.TempDir(), "does-not-exist")

	c := vhostCheck("client.example")
	if !c.OK || c.Fatal {
		t.Fatalf("with nginx's config unreadable the console declared the vhost missing: %s", c.Detail)
	}
	if !strings.Contains(c.Detail, "nothing is claimed") {
		t.Error("the unreadable case does not say it is claiming nothing, so it reads as a pass")
	}
}

// And when the directory IS readable and this host is absent, that is the
// finding — stated as blocking, with the consequence named.
func TestAMissingVhostIsReportedAsTheCause(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("other", "server {\n listen 80;\n server_name other.example;\n"+
		" location ^~ /.well-known/acme-challenge/ { root /var/cache/vayupress; }\n}\n")
	saved := nginxSitesEnabled
	t.Cleanup(func() { nginxSitesEnabled = saved })
	nginxSitesEnabled = dir

	c := vhostCheck("client.example")
	if c.OK || !c.Fatal {
		t.Fatal("a host with no enabled server block was not reported as blocking")
	}
	if !strings.Contains(c.Detail, "default") {
		t.Error("the finding does not explain that the default server answers instead")
	}
	// And a host that IS fully configured must come back clean, or the check is
	// just noise.
	if ok := vhostCheck("other.example"); !ok.OK {
		t.Errorf("a correctly configured host was flagged: %s", ok.Detail)
	}
}

// FINDING — the console printed "an enabled config names this host, so a request
// carrying it reaches this site" in a row DIRECTLY ABOVE a probe reporting that
// nothing answered. Both were on screen at once and they cannot both be true.
//
// The gap was the PORT. A config can name a host and listen only on 443 — which
// is exactly what this helper writes once a certificate exists, and what it
// leaves behind if one is later removed. HTTP-01 arrives on port 80, finds no
// block for that name there, and falls through to the default server, which
// closes the connection. The name was present; the thing that mattered was not.
func TestAHostServedOnlyOn443IsNotCalledRouted(t *testing.T) {
	dir := t.TempDir()
	saved := nginxSitesEnabled
	t.Cleanup(func() { nginxSitesEnabled = saved })
	nginxSitesEnabled = dir

	// Names the host, listens only on 443.
	if err := os.WriteFile(filepath.Join(dir, "https-only"),
		[]byte("server {\n listen 443 ssl http2;\n listen [::]:443 ssl http2;\n"+
			" server_name client.example www.client.example;\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := vhostCheck("client.example")
	if c.OK || !c.Fatal {
		t.Fatalf("a host whose only server block listens on 443 was reported as routed: %q — %s. "+
			"That is the row that contradicted the probe directly beneath it", c.Label, c.Detail)
	}
	if !strings.Contains(c.Detail, "port 80") {
		t.Error("the finding does not name the port, which is the entire difference")
	}
}

// A port-80 block that redirects everything to https answers the challenge with
// a 301. A redirect is not a token: validation fails while the site looks
// entirely healthy in a browser, which is the hardest version of this to spot.
func TestAPort80BlockWithoutTheChallengeLocationIsReported(t *testing.T) {
	dir := t.TempDir()
	saved := nginxSitesEnabled
	t.Cleanup(func() { nginxSitesEnabled = saved })
	nginxSitesEnabled = dir

	if err := os.WriteFile(filepath.Join(dir, "redirect-only"),
		[]byte("server {\n listen 80;\n server_name client.example;\n"+
			" location / { return 301 https://$host$request_uri; }\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := vhostCheck("client.example")
	if c.OK || !c.Fatal {
		t.Fatalf("a port-80 block that only redirects was reported as serving the challenge: %s", c.Detail)
	}
	if !strings.Contains(c.Detail, "acme-challenge") {
		t.Error("the finding does not name the missing location")
	}
}

// The port is parsed, not substring-matched. "listen 8080" contains "80", and
// "listen 443 ssl" must never count — either mistake turns this check into the
// confident wrong answer it was written to replace.
func TestPort80IsParsedNotSubstringMatched(t *testing.T) {
	for _, tc := range []struct {
		block string
		want  bool
	}{
		{"listen 80;", true},
		{"listen [::]:80;", true},
		{"listen 0.0.0.0:80 default_server;", true},
		{"listen 80 default_server;", true},
		{"listen 8080;", false},
		{"listen 443 ssl http2;", false},
		{"listen [::]:443 ssl;", false},
		{"server_name eighty.example;", false},
	} {
		if got := listensOnPort80(tc.block); got != tc.want {
			t.Errorf("listensOnPort80(%q) = %v, want %v", tc.block, got, tc.want)
		}
	}
}

// One file routinely holds two blocks: an HTTP one carrying the challenge and an
// HTTPS one that does not. Asking "does this FILE mention port 80" answers yes
// for a file whose only port-80 block belongs to a DIFFERENT hostname.
func TestServerBlocksAreSplitSoOneHostsPortsAreNotAnothers(t *testing.T) {
	cfg := "server {\n listen 80;\n server_name a.example;\n}\n" +
		"server {\n listen 443 ssl;\n server_name b.example;\n}\n"
	blocks := serverBlocks(cfg)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 server blocks, got %d: %q", len(blocks), blocks)
	}
	for _, b := range blocks {
		if serverNameClaims(b, "b.example") && listensOnPort80(b) {
			t.Fatal("b.example was credited with a.example's port-80 listener — the two blocks " +
				"were not separated, so one host's config vouches for another's")
		}
	}
	// server_name and server_tokens must not be mistaken for a block opener.
	if got := serverBlocks("server_tokens off;\nserver_name x;\n"); len(got) != 0 {
		t.Errorf("a directive starting with \"server\" was parsed as a block: %q", got)
	}
}

// FINDING — the console had both halves of the answer and printed them as
// unrelated rows.
//
// "The config on disk is correct" and "nothing answered on port 80" can only
// both be true for one reason: nginx is running configuration older than the
// file. Leaving a reader to derive that from two rows is the same failure as not
// showing them — and it is what the page did while the operator was told, three
// times, to look at DNS.
func TestTwoDisagreeingChecksProduceTheConclusionTheyImply(t *testing.T) {
	goodVhost := diagCheck{Label: "nginx routes this host's challenge on port 80", OK: true}
	failedProbe := diagCheck{Label: "This server can answer its own challenge", OK: false, Fatal: true}

	c, ok := staleConfigCheck(goodVhost, failedProbe)
	if !ok {
		t.Fatal("a correct config beside a failed probe produced no conclusion, so the page " +
			"still shows two rows and leaves the reader to join them")
	}
	if c.OK || !c.Fatal {
		t.Error("the conclusion is not reported as blocking")
	}
	for _, want := range []string{"reload", "never seen"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("the conclusion never mentions %q, so it does not say what to do", want)
		}
	}
	// It must NOT fire when there is no contradiction — otherwise it is noise on
	// every page and says nothing.
	if _, ok := staleConfigCheck(goodVhost, diagCheck{OK: true}); ok {
		t.Error("the conclusion fires when both checks agree that things are fine")
	}
	badVhost := diagCheck{Label: "nginx has NO server block", OK: false, Fatal: true}
	if _, ok := staleConfigCheck(badVhost, failedProbe); ok {
		t.Fatal("a MISSING vhost was reported as a stale-reload problem — the file is absent, " +
			"reloading changes nothing, and this would send the operator past the real cause")
	}
}

// FINDING — the two facts that together explain everything were printed as
// separate rows, several apart, neither saying what to do.
//
// "A request is waiting and no run has started since" and "the shell helpers are
// OLDER than this binary" are not two problems. The same step installs the
// systemd watcher that consumes the request and the helpers that do the work, so
// while it is stale every repair this console offers runs through a worker that
// never starts — the vhost is never rewritten, nginx is never reloaded, and no
// certificate can be issued however correct the DNS is.
func TestAStalledRootSideIsReportedAsOneActionNotTwoSymptoms(t *testing.T) {
	c, ok := rootSideStalledCheck(true, false)
	if !ok {
		t.Fatal("an unconsumed request beside stale helpers produced no combined finding, so the " +
			"page still shows two rows and never names the single step that clears both")
	}
	if c.OK || !c.Fatal {
		t.Error("the combined finding is not reported as blocking")
	}
	for _, want := range []string{"watcher", "never starts", "Domains & DNS"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("the finding never mentions %q, so it does not say why or where to act", want)
		}
	}
	// It must NOT fire on either fact alone. Stale helpers with a healthy watcher
	// degrade reporting only — saying "reinstall the root side" for that would be
	// the overstatement this page keeps having to correct.
	if _, ok := rootSideStalledCheck(false, false); ok {
		t.Error("stale helpers ALONE were reported as a stalled root side, which sends the " +
			"operator to reinstall for something that is not blocking them")
	}
	if _, ok := rootSideStalledCheck(true, true); ok {
		t.Error("an unconsumed request with CURRENT helpers was blamed on the helpers")
	}
}

// FINDING that indicts this page's own probe: it dialled 127.0.0.1 alone and
// reported a BLOCKING row when nothing answered there.
//
// If nginx is bound to specific public addresses rather than a wildcard,
// loopback is not where it listens — and the console would be reporting a fault
// that exists only in the way it looked. A diagnostic that can manufacture a
// false blocking row about the thing it is diagnosing is worse than none.
func TestTheProbeDoesNotAssumeNginxListensOnLoopback(t *testing.T) {
	if listensEverywhere([]string{"198.51.100.10"}) {
		t.Fatal("a listener bound to one public address was treated as a wildcard, so the probe " +
			"would test only loopback and call a working server broken")
	}
	for _, w := range [][]string{{"0.0.0.0"}, {"[::]"}, {"127.0.0.1", "0.0.0.0"}} {
		if !listensEverywhere(w) {
			t.Errorf("%v was not recognised as a wildcard bind", w)
		}
	}
	// The probe must consult the listener table rather than hard-coding loopback.
	body := goFuncBody(readSourceFile(t, "admin_os_scoped_diagnose.go"), "challengeProbe")
	if !strings.Contains(body, "listensEverywhere(port80Listeners())") {
		t.Fatal("the probe does not check where port 80 is actually bound before deciding that " +
			"loopback is a valid test")
	}
	if !strings.Contains(body, "localAddrSet()") {
		t.Error("the probe never tries this machine's own addresses, so a non-wildcard bind is " +
			"still reported as a failure")
	}
}

// /proc/net's addresses are little-endian hex. Decoding them wrongly would print
// a plausible but incorrect address in a row an operator is meant to act on.
func TestProcAddressesAreDecodedNotGuessed(t *testing.T) {
	// 127.0.0.1 little-endian = 0100007F
	if got := decodeProcAddr("0100007F"); got != "127.0.0.1" {
		t.Errorf("decodeProcAddr(0100007F) = %q, want 127.0.0.1", got)
	}
	if got := decodeProcAddr("00000000"); got != "0.0.0.0" {
		t.Errorf("decodeProcAddr(00000000) = %q, want 0.0.0.0", got)
	}
	if got := decodeProcAddr(strings.Repeat("0", 32)); got != "[::]" {
		t.Errorf("the IPv6 wildcard decoded as %q", got)
	}
	// Anything unrecognised must yield nothing rather than a fabricated address.
	for _, bad := range []string{"", "ZZ", "12345"} {
		if got := decodeProcAddr(bad); got != "" {
			t.Errorf("decodeProcAddr(%q) invented %q", bad, got)
		}
	}
}

// THE ROOT CAUSE, found by reading our own helper rather than reasoning about
// the symptom:
//
//	reload_ok() { nginx_ok && { systemctl reload nginx 2>/dev/null || true; return 0; }; ... }
//
// `|| true` discarded the reload's exit status and the function returned success
// regardless, so `nginx -t` passing was treated as "the new vhost is live". When
// the reload did not happen, the script went on to certbot against a server
// still running configuration that had never contained the vhost — and the
// authority reported an unexplained "Type: connection", because the running
// nginx had no server block for that name and the default server closes the
// connection.
//
// Every layer above was correct: the file, the symlink, the DNS, the webroot.
// The single step that makes any of them take effect was the only one whose
// failure was thrown away.
func TestAFailedNginxReloadIsNeverReportedAsSuccess(t *testing.T) {
	// The first draft of this test RETURNED when it found the bug — passing on
	// exactly the input it existed to catch — and shadowed `t` with a string
	// while doing it. Recorded because it is the same defect class as the code
	// under test: a check that reports success on the failing case.
	for _, f := range provisioningHelpers {
		src := readSourceFile(t, f)
		if !strings.Contains(src, "RELOADING it failed") {
			t.Errorf("%s reloads nginx without reporting a reload failure. A reload that does "+
				"not happen leaves a correct file on disk that the running server has never "+
				"read, and the certificate authority reports that as an unexplained connection "+
				"error", f)
		}
	}
}

// provisioningHelpers are every root-side script that writes a vhost and
// reloads nginx. Listed once: a new helper added without this list is a new
// place for the same bug to live.
var provisioningHelpers = []string{
	"../../scripts/setup-vayudomain.sh",
	"../../scripts/setup-api-subdomain.sh",
	"../../scripts/setup-mcp-subdomain.sh",
	"../../scripts/setup-talk-subdomain.sh",
	"../../scripts/setup-openpgpkey-subdomain.sh",
}

// A live `|| true` on the reload must fail this test outright, in any helper.
func TestNoHelperDiscardsTheReloadExitStatus(t *testing.T) {
	for _, f := range provisioningHelpers {
		for i, line := range strings.Split(readSourceFile(t, f), "\n") {
			s := strings.TrimSpace(line)
			if strings.HasPrefix(s, "#") {
				continue // the retired form is quoted in a comment on purpose
			}
			if strings.Contains(s, "systemctl reload nginx") && strings.Contains(s, "|| true") {
				t.Errorf("%s:%d discards the reload's exit status: %s", f, i+1, s)
			}
		}
	}
}
