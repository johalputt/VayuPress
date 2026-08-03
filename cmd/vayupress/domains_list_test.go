// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
)

// ADR-0154 D5 — the site list in the house style.
//
// The header it replaced carried one 150-word paragraph about rollout stages,
// manual holds and provisioning helpers, permanently, above the list. Every
// sentence in it was true and none of it answered the question an operator opens
// this page with, which is "are my sites up".

func listFixture() []domain.Domain {
	return []domain.Domain{
		{ID: "", Host: "example.test", IsPrimary: true, Status: domain.StatusActive, TLSState: domain.TLSPrimary},
		{ID: "s1", Host: "live.example", Status: domain.StatusActive, SyncState: domain.SyncApproved, TLSState: domain.TLSActive},
		{ID: "s2", Host: "waiting.example", Status: domain.StatusActive, SyncState: domain.SyncApproved, TLSState: domain.TLSPending},
		{ID: "s3", Host: "parked.example", Status: domain.StatusActive, SyncState: domain.SyncHold, TLSState: domain.TLSPending},
	}
}

// The tiles must answer the question, and the numbers must be the real ones.
func TestTheSiteListCountsWhatAnOperatorCameToCheck(t *testing.T) {
	head := domainsHeader(listFixture(), "")
	for _, want := range []string{"Sites", "Enabled", "On hold", "No certificate"} {
		if !strings.Contains(head, want) {
			t.Errorf("the site list has no %q tile", want)
		}
	}
	// One held (parked), one approved-but-uncertified (waiting). The held site is
	// NOT counted as missing a certificate: not provisioning it is the point of
	// the hold, and counting it twice would make the hold look like a fault.
	if !strings.Contains(head, `>1</span><span class="vm-stat__l">On hold`) {
		t.Errorf("the on-hold tile does not read 1:\n%s", head)
	}
	if !strings.Contains(head, `>1</span><span class="vm-stat__l">No certificate`) {
		t.Errorf("the certificate tile does not read 1 — a held site must not be counted as "+
			"missing a certificate, because not issuing one is what the hold does:\n%s", head)
	}
}

// A site with everything in place must produce four quiet tiles. A page that
// always shows something amber is a page whose amber means nothing.
func TestAHealthyInstallShowsNothingToWorryAbout(t *testing.T) {
	head := domainsHeader([]domain.Domain{
		{ID: "", Host: "example.test", IsPrimary: true, Status: domain.StatusActive, TLSState: domain.TLSPrimary},
		{ID: "s1", Host: "live.example", Status: domain.StatusActive, SyncState: domain.SyncApproved, TLSState: domain.TLSActive},
	}, "")
	if strings.Contains(head, "vm-stat--warn") {
		t.Errorf("an install with nothing wrong still shows a warning tile:\n%s", head)
	}
}

// The reference material must still be reachable — this was a fold, not a
// deletion. An operator who has never provisioned a site needs the order of
// operations, and losing it would trade one defect for another.
func TestTheStagingDetailIsFoldedNotDeleted(t *testing.T) {
	head := domainsHeader(listFixture(), "")
	for _, want := range []string{"Sync now", "Provision subdomains", "manual hold"} {
		if !strings.Contains(head, want) {
			t.Errorf("the how-it-works detail no longer mentions %q, so folding it away lost it", want)
		}
	}
	if !strings.Contains(head, "mon-acc") {
		t.Error("the detail is not in an accordion, so it is either missing or back to being a " +
			"wall of text above the list")
	}
}

func TestTheSiteListIsCSPSafe(t *testing.T) {
	head := domainsHeader(listFixture(), "hostile\"><script>x")
	if strings.Contains(head, `"><script>`) {
		t.Fatalf("the viewing host broke out of its markup:\n%s", head)
	}
	if strings.Contains(head, `style="`) {
		t.Error("the header carries an inline style attribute, which the CSP forbids")
	}
}
