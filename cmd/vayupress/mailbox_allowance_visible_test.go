// SPDX-License-Identifier: Apache-2.0

package main

// mailbox_allowance_visible_test.go — the state every new customer domain
// passes through, and how it looks.
//
// Mailboxes.Limits defaults to 0 on purpose: an allowance nobody chose is not an
// allowance. The consequence is that EVERY newly hosted domain starts unable to
// create a single mailbox — and the console rendered that as "0 in use" on a
// tile and a collapsed card, which reads like a site nobody has got round to
// setting up rather than one that will refuse its owner the moment they try.
//
// Attacked from the position of the operator who has just sold hosting to
// somebody: what does the page tell me is wrong, before my customer tells me?

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/users"
)

func domainWithAllowance(t *testing.T, mailEnabled bool, granted int) domain.Domain {
	t.Helper()
	d := domain.Domain{ID: "d1", Host: "customer.example", Status: domain.StatusActive, MailEnabled: mailEnabled}
	raw, err := domain.EncodeLimitsInto("", domain.Limits{Mailboxes: granted})
	if err != nil {
		t.Fatal(err)
	}
	d.ConfigJSON = raw
	return d
}

func renderScoped(d domain.Domain, mailOn bool) string {
	return scopedConsolePage(d, 0, 0, 0, mailOn, nil, nil, nil)
}

// The state that costs a customer their first hour.
func TestASiteWithMailOnAndNoAllowanceSaysSoWithoutBeingOpened(t *testing.T) {
	page := renderScoped(domainWithAllowance(t, true, 0), true)

	tile := statCardIn(t, page, "Mailboxes")
	if !strings.Contains(tile, "0 granted") {
		t.Errorf("the mailbox tile does not say the allowance is zero; \"0\" alone reads as "+
			"\"nobody has made a mailbox yet\", not \"nobody can\". Tile: %s", tile)
	}
	if !strings.Contains(tile, "stat-card--warn") {
		t.Errorf("the mailbox tile is not toned as a problem, so nothing draws the eye to it: %s", tile)
	}
	if !strings.Contains(page, "none granted") {
		t.Error("the allowance card's chip does not flag the state while collapsed")
	}
	// The card explains exactly this and is useless shut.
	i := strings.Index(page, "Mailbox allowance")
	if i < 0 {
		t.Fatal("the allowance section is missing entirely")
	}
	if !strings.Contains(page[:i], "<details open") && !strings.Contains(page[i-400:i+400], "open") {
		t.Error("the section stays collapsed in the one state where its advice matters")
	}
}

// It must not cry wolf. A site with a real allowance is fine, and an operator
// who sees a warning on a healthy site stops reading warnings.
func TestASiteWithAnAllowanceIsNotFlagged(t *testing.T) {
	page := renderScoped(domainWithAllowance(t, true, 5), true)
	if strings.Contains(page, "0 granted") || strings.Contains(page, "none granted") {
		t.Fatal("a site with mailboxes granted was flagged as having none")
	}
	if strings.Contains(statCardIn(t, page, "Mailboxes"), "stat-card--warn") {
		t.Error("a site with mailboxes granted has a warning on its mailbox tile")
	}
}

// Mail switched off install-wide is a DIFFERENT situation, and the existing copy
// distinguishes them deliberately. Warning here would tell an operator to grant
// an allowance that would change nothing.
func TestMailBeingOffIsNotReportedAsAMissingAllowance(t *testing.T) {
	for _, c := range []struct {
		name             string
		mailOn, domainOn bool
	}{
		{"mail off install-wide", false, true},
		{"mail off for this domain", true, false},
		{"off both ways", false, false},
	} {
		page := renderScoped(domainWithAllowance(t, c.domainOn, 0), c.mailOn)
		if strings.Contains(page, "0 granted") || strings.Contains(page, "none granted") {
			t.Errorf("%s: reported as a missing allowance, which sends the operator to grant "+
				"mailboxes that still could not be created", c.name)
		}
	}
}

var _ = users.RoleClient
