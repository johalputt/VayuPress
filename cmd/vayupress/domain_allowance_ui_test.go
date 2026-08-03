// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
)

// The allowance shipped enforcing and unsettable.
//
// `POST /os/api/domains/{id}/allowance` refused mailbox creation at the cap from
// the day it landed, and no page rendered an input for it. The only way to grant
// a client their mailboxes was a hand-written API call — which, for an operator
// who was promised the whole product works from the panel, is indistinguishable
// from "mailbox creation is broken and I cannot fix it".
//
// These tests are written from that operator's position.

func allowanceDomain(t *testing.T, granted int) domain.Domain {
	t.Helper()
	cfg, err := domain.EncodeLimitsInto("", domain.Limits{Mailboxes: granted})
	if err != nil {
		t.Fatalf("EncodeLimitsInto: %v", err)
	}
	return domain.Domain{
		ID: "s1", Host: "client.example", SiteType: domain.SiteBlog,
		Status: domain.StatusActive, ConfigJSON: cfg,
	}
}

func TestTheOperatorCanSetAMailboxAllowanceFromThePanel(t *testing.T) {
	page := scopedConsolePage(allowanceDomain(t, 5), 0, 0, 2, true, nil, "")
	assertCSPSafe(t, "scopedConsolePage", page)

	if !strings.Contains(page, "data-site-allowance-save") {
		t.Fatal("the manage page has no control for the mailbox allowance. The limit still " +
			"enforces, so every domain sits at whatever it was last set to by hand and the " +
			"operator has no way to change it without leaving the panel")
	}
	// The current value must be prefilled, or saving is a guess that silently
	// overwrites a number the operator could not see.
	if !strings.Contains(page, `id="site-allowance"`) || !strings.Contains(page, `value="5"`) {
		t.Errorf("the allowance input is not prefilled with the granted figure:\n%s", page)
	}
	// Used and granted must appear together. "How many more can they have?" is
	// the question being answered, and the allowance alone cannot answer it.
	if !strings.Contains(page, "2 of 5 used") {
		t.Errorf("the card does not show usage against the allowance:\n%s", page)
	}

	script := domainManageScript("n1")
	if !strings.Contains(script, "[data-site-allowance-save]") {
		t.Error("the button is rendered but nothing is wired to it")
	}
	if !strings.Contains(script, "/allowance") {
		t.Error("the script never calls the allowance endpoint")
	}
}

// Zero is the ambiguous one, and the wrong reading is the expensive one: an
// operator who reads 0 as "no limit configured, so unlimited" will not
// understand why creation refuses. The card has to say it in words.
func TestAZeroAllowanceReadsAsNoneNotUnlimited(t *testing.T) {
	page := scopedConsolePage(allowanceDomain(t, 0), 0, 0, 0, true, nil, "")
	if !strings.Contains(page, "No mailboxes granted") {
		t.Error("a zero allowance does not announce itself as zero, so the operator reads the " +
			"blank as 'unlimited' and files a bug when the next mailbox is refused")
	}
	// "unlimited" may appear ONLY as the thing being denied. Banning the token
	// outright would fail the sentence that does the clarifying — this asserts the
	// claim, not the vocabulary, which is the distinction that makes the check
	// worth having at all.
	low := strings.ToLower(page)
	for i := 0; ; {
		j := strings.Index(low[i:], "unlimited")
		if j < 0 {
			break
		}
		at := i + j
		if !strings.HasSuffix(strings.TrimSpace(low[:at]), "never") {
			t.Errorf("the card offers 'unlimited' as a state rather than denying it, at:\n%s",
				low[max(0, at-90):min(len(low), at+40)])
		}
		i = at + len("unlimited")
	}
}

// A full allowance must say so before the operator hits the refusal, not after.
func TestAFullAllowanceSaysSoBeforeTheRefusal(t *testing.T) {
	page := scopedConsolePage(allowanceDomain(t, 3), 0, 0, 3, true, nil, "")
	if !strings.Contains(page, "the allowance is full") {
		t.Errorf("a domain at its cap does not warn, so the operator discovers it by having "+
			"mailbox creation fail in front of a client:\n%s", page)
	}
}

// Mail being switched off install-wide is a different situation from an
// allowance of zero, and conflating them sends the operator to the wrong screen.
func TestMailBeingOffIsNotTheSameAsNoAllowance(t *testing.T) {
	page := scopedConsolePage(allowanceDomain(t, 4), 0, 0, 0, false, nil, "")
	if !strings.Contains(page, "Mail is switched off") {
		t.Error("with mail disabled the card still talks about the allowance, sending the " +
			"operator to raise a number that is not what is stopping them")
	}
}

// An empty field must not be sent. A number input hands back "" when cleared,
// which parses to NaN — and a NaN serialised into the payload lands as 0, which
// silently revokes every mailbox the operator granted while reporting success.
func TestAnEmptyAllowanceFieldIsRefusedRatherThanSentAsZero(t *testing.T) {
	script := domainManageScript("n1")
	body := script[strings.Index(script, "data-site-allowance-save"):]
	for _, want := range []string{"isNaN", "raw===''"} {
		if !strings.Contains(body, want) {
			t.Errorf("the allowance handler does not guard %q, so clearing the field revokes "+
				"the grant and reports 'Saved'", want)
		}
	}
}
