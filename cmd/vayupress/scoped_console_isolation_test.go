// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

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
		scopedConsolePage(d, 3, 2, 1, true, nil, ""))
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
	page := scopedConsolePage(isolationDomain(), 0, 0, 0, true, nil, "")
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
	page := scopedConsolePage(d, 0, 0, 0, true, nil, "")

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
	if strings.Contains(scopedConsolePage(ok, 0, 0, 0, true, nil, ""), "data-site-provision") {
		t.Error("a site with a live certificate is offered a provisioning run anyway")
	}

	// Nor must a HELD site: not provisioning it is what the hold does, and the
	// hold notice already explains that. Two competing explanations is worse
	// than one.
	held := isolationDomain()
	held.SyncState = domain.SyncHold
	held.TLSState = domain.TLSPending
	if strings.Contains(scopedConsolePage(held, 0, 0, 0, true, nil, ""), "data-site-provision") {
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
	body := scopedDiagnosticBody(checks, []string{"line one", "No sync-approved secondary domains"})

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
	body := scopedDiagnosticBody(nil, []string{`<img src=x onerror="alert(1)">`})
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
	if !strings.Contains(guard, "TLSActive") {
		t.Error("the diagnostic is not gated on the certificate state, so every console page " +
			"does a DNS lookup and a file read for nothing")
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
