// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
)

// The site list answers the question an operator opens it with, "are my
// sites up": a row per site with its state, and views for the two states that
// need them.

func listFixture() []domain.Domain {
	return []domain.Domain{
		{ID: "", Host: "example.test", IsPrimary: true, Status: domain.StatusActive, TLSState: domain.TLSPrimary},
		{ID: "s1", Host: "live.example", Status: domain.StatusActive, SyncState: domain.SyncApproved, TLSState: domain.TLSActive},
		{ID: "s2", Host: "waiting.example", Status: domain.StatusActive, SyncState: domain.SyncApproved, TLSState: domain.TLSPending},
		{ID: "s3", Host: "parked.example", Status: domain.StatusActive, SyncState: domain.SyncHold, TLSState: domain.TLSPending},
	}
}

func siteList(view, q string) string { return domainsList(listFixture(), siteFigures{}, view, q) }

// rowFor returns the list row naming host.
func rowFor(t *testing.T, page, host string) string {
	t.Helper()
	for _, row := range strings.Split(page, "<tr ")[1:] {
		if strings.Contains(row, `<span class="sa-site__host">`+host+`</span>`) {
			return row[:strings.Index(row, "</tr>")]
		}
	}
	t.Fatalf("no row for %s", host)
	return ""
}

// The views count what an operator came to check, and the numbers are the
// real ones: one held (parked), one approved and uncertified (waiting).
func TestTheSiteListCountsWhatAnOperatorCameToCheck(t *testing.T) {
	page := siteList("", "")
	if !strings.Contains(page, `data-page-kind="list"`) {
		t.Error("the site list does not say it is a list")
	}
	for _, want := range []string{`>All <span class="muted">4</span>`, `>On hold <span class="muted">1</span>`, `>No certificate <span class="muted">1</span>`} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	for host, state := range map[string]string{"example.test": "Serving", "live.example": "Serving", "waiting.example": "No certificate", "parked.example": "On hold"} {
		if !strings.Contains(rowFor(t, page, host), `</span>`+state+`</span></td>`) {
			t.Errorf("%s does not read %q", host, state)
		}
	}
	if strings.Contains(page, "stat-grid") {
		t.Error("the list still opens on a strip of figures")
	}
}

// A held site is not counted as missing a certificate: not issuing one is
// what the hold does, and counting it twice would make the hold look like a
// fault. Each view lists only its own.
func TestEachViewListsOnlyItsOwnSites(t *testing.T) {
	for view, want := range map[string]string{"held": "parked.example", "nocert": "waiting.example"} {
		page := siteList(view, "")
		if n := strings.Count(page, "data-list-row"); n != 1 || !strings.Contains(page, want) {
			t.Errorf("view %s lists %d rows, want only %s", view, n, want)
		}
	}
	page := siteList("", "LIVE")
	if n := strings.Count(page, "data-list-row"); n != 1 || !strings.Contains(page, "live.example") {
		t.Errorf("a search for LIVE lists %d rows, want live.example", n)
	}
	if !strings.Contains(siteList("", "nothing-here"), "No site matches that") {
		t.Error("a search that finds nothing does not say so")
	}
}

// A site with everything in place must look quiet. A page that always shows
// something amber is a page whose amber means nothing.
func TestAHealthyInstallShowsNothingToWorryAbout(t *testing.T) {
	page := domainsList([]domain.Domain{
		{ID: "", Host: "example.test", IsPrimary: true, Status: domain.StatusActive, TLSState: domain.TLSPrimary},
		{ID: "s1", Host: "live.example", Status: domain.StatusActive, SyncState: domain.SyncApproved, TLSState: domain.TLSActive},
	}, siteFigures{}, "", "")
	if strings.Contains(page, "sa-dot--warn") {
		t.Errorf("an install with nothing wrong still shows a warning")
	}
	if strings.Contains(page, "data-dom-sync-all") {
		t.Error("the bulk sync is offered with no site on hold")
	}
}

// The primary is managed from Website and cannot be removed from here; a
// hosted site opens its own console and is synced, switched and removed from
// the inspector.
func TestTheInspectorOffersWhatEachSiteCanTake(t *testing.T) {
	f := siteFigures{}
	primary := f.siteInspector(listFixture()[0])
	if !strings.Contains(primary, `href="/os/website"`) || strings.Contains(primary, "data-dom-") {
		t.Errorf("the primary's inspector:\n%s", primary)
	}
	parked := f.siteInspector(listFixture()[3])
	for _, want := range []string{`href="/os/d/s3"`, `data-dom-sync data-id="s3" data-sync="approved">Sync now`, "data-dom-toggle", `data-dom-delete data-id="s3"`, "On manual hold"} {
		if !strings.Contains(parked, want) {
			t.Errorf("a held site's inspector is missing %q", want)
		}
	}
	if n := strings.Count(siteList("", ""), "btn--primary"); n != 1 {
		t.Errorf("%d primary buttons on the page, want one", n)
	}
}

// The order of operations is still reachable, in the sheet where a site is
// added: this was a move, not a deletion. An operator who has never
// provisioned a site needs it.
func TestTheStagingDetailIsFoldedNotDeleted(t *testing.T) {
	add := domainsAddForm()
	for _, want := range []string{"Sync now", "Provision subdomains", "manual hold"} {
		if !strings.Contains(add, want) {
			t.Errorf("the how-it-works detail no longer mentions %q, so moving it lost it", want)
		}
	}
	if !strings.Contains(add, "<details") {
		t.Error("the detail is not folded, so it is back to being a wall of text")
	}
}

func TestTheSiteListIsCSPSafe(t *testing.T) {
	hostile := `hostile"><script>x`
	page := domainsList([]domain.Domain{{ID: `i"><script>`, Host: hostile, Status: domain.StatusActive}},
		siteFigures{marks: map[string]bool{`i"><script>`: true}, viewing: hostile}, "", hostile)
	if strings.Contains(page, `"><script>`) {
		t.Fatalf("a host broke out of its markup:\n%s", page)
	}
	if strings.Contains(page, `style="`) {
		t.Error("the list carries an inline style attribute, which the CSP forbids")
	}
}
