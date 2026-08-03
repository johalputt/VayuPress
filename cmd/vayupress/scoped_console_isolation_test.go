// SPDX-License-Identifier: Apache-2.0

package main

import (
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
		scopedConsolePage(d, 3, 2, 1, true, nil))
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
	page := scopedConsolePage(isolationDomain(), 0, 0, 0, true, nil)
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
	page := scopedConsolePage(d, 0, 0, 0, true, nil)

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
	if strings.Contains(scopedConsolePage(ok, 0, 0, 0, true, nil), "data-site-provision") {
		t.Error("a site with a live certificate is offered a provisioning run anyway")
	}

	// Nor must a HELD site: not provisioning it is what the hold does, and the
	// hold notice already explains that. Two competing explanations is worse
	// than one.
	held := isolationDomain()
	held.SyncState = domain.SyncHold
	held.TLSState = domain.TLSPending
	if strings.Contains(scopedConsolePage(held, 0, 0, 0, true, nil), "data-site-provision") {
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
