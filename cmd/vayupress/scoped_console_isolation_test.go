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
		scopedConsolePage(d, 3, 2, 1, true, nil, nil, nil))
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
	page := scopedConsolePage(isolationDomain(), 0, 0, 0, true, nil, nil, nil)
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
	page := scopedConsolePage(d, 0, 0, 0, true, nil, nil, nil)

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
	if strings.Contains(scopedConsolePage(ok, 0, 0, 0, true, nil, nil, nil), "data-site-provision") {
		t.Error("a site with a live certificate is offered a provisioning run anyway")
	}

	// Nor must a HELD site: not provisioning it is what the hold does, and the
	// hold notice already explains that. Two competing explanations is worse
	// than one.
	held := isolationDomain()
	held.SyncState = domain.SyncHold
	held.TLSState = domain.TLSPending
	if strings.Contains(scopedConsolePage(held, 0, 0, 0, true, nil, nil, nil), "data-site-provision") {
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
	page := scopedConsolePage(onion, 0, 0, 0, false, nil, nil, nil)
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

	elsewhere := dnsPointsHereCheck("client.example", []string{"185.199.108.153"}, true, local, front)
	if elsewhere.OK {
		t.Fatal("a domain resolving to a host unrelated to this install was reported as " +
			"reachable — the exact verdict that hid a domain burning the account's validation " +
			"budget on every run")
	}
	if !elsewhere.Fatal {
		t.Error("it is reported as a detail rather than as blocking, so it reads as background " +
			"noise beside the certificate that is not arriving")
	}
	for _, want := range []string{"185.199.108.153", "404", "Lifecycle"} {
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
		dnsPointsHereCheck("client.example", []string{"185.199.108.153"}, true, map[string]bool{}, front),
		dnsPointsHereCheck("client.example", []string{"185.199.108.153"}, true, local, nil),
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
	local := map[string]bool{"5.189.133.235": true}
	front := []string{"104.21.11.39"}

	split := dnsPointsHereCheck("test.example", []string{"5.189.133.235", "2a02:c207::1"}, true, local, front)
	if split.OK {
		t.Fatal("a host whose A record is correct and whose AAAA points somewhere else was " +
			"reported as pointing here — the issuance fails over IPv6 and the operator is " +
			"looking at a green check")
	}
	for _, want := range []string{"2a02:c207::1", "IPv6", "IPv4"} {
		if !strings.Contains(split.Detail, want) {
			t.Errorf("the finding never mentions %q, so it does not identify the record to fix", want)
		}
	}

	// A host answering correctly on BOTH families must stay quiet.
	both := map[string]bool{"5.189.133.235": true, "2a02:c207::1": true}
	if c := dnsPointsHereCheck("test.example", []string{"5.189.133.235", "2a02:c207::1"}, true, both, front); !c.OK {
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
	partial := map[string]bool{"5.189.133.235": true, "2a02:c207::1": true}
	if c := dnsPointsHereCheck("test.example",
		[]string{"5.189.133.235", "2a02:c207::1", "2a02:c207::99"}, true, partial, front); !c.OK {
		t.Errorf("a host answering on IPv6 was flagged because it publishes a SECOND IPv6 "+
			"address: %s. Only a family that is entirely unaccounted for is a finding", c.Detail)
	}

	// So must a single-family host — an absent AAAA is not a broken one.
	if c := dnsPointsHereCheck("test.example", []string{"5.189.133.235"}, true, local, front); !c.OK {
		t.Errorf("an IPv4-only host was flagged: %s", c.Detail)
	}
	// An address belonging to this install's OWN FRONT counts as reachable. The
	// case that matters is cross-family — origin over IPv4, front over IPv6 — and
	// it is the one a same-family fixture cannot distinguish at all: a mutation
	// dropping the front from the comparison survived a test written that way,
	// because the good IPv4 address alone satisfied the IPv4 family.
	frontDual := []string{"104.21.11.39", "2606:4700:3033::6815:b27"}
	if c := dnsPointsHereCheck("test.example",
		[]string{"5.189.133.235", "2606:4700:3033::6815:b27"}, true, local, frontDual); !c.OK {
		t.Errorf("an IPv6 address belonging to this install's own front was called a stray: %s. "+
			"That is every dual-stack site whose AAAA is on the proxy and whose A is direct", c.Detail)
	}

	// And the retired claim must not come back anywhere in the verdicts.
	stray := dnsPointsHereCheck("other.example", []string{"185.199.108.153"}, true, local, front)
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
		{Label: "DNS points somewhere else", OK: false, Fatal: true, Detail: "185.199.108.153"},
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
		{Label: "DNS points at a different host", OK: false, Fatal: true, Detail: "185.199.108.153"},
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
