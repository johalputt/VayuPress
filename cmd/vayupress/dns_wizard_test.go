// SPDX-License-Identifier: Apache-2.0

package main

// dns_wizard_test.go — the guided Domain-health checklist (Phase 3.4).
//
// The card's whole job is to answer "am I finished?" in one glance and to name the
// single next thing to fix, so those two behaviours are what is pinned here.

import (
	"strings"
	"testing"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

func okDNSHealth() dnsHealth {
	return dnsHealth{
		AllOK: true,
		Domains: []dnsDomainHealth{{
			Domain: "example.com",
			Health: &vmail.DomainHealth{
				AllOK: true,
				Records: []vmail.RecordHealth{
					{Type: "MX", OK: true, Found: "10 mail.example.com"},
					{Type: "SPF", OK: true, Found: "v=spf1 ..."},
					{Type: "DKIM", OK: true, Found: "selector._domainkey"},
					{Type: "DMARC", OK: true, Found: "v=DMARC1; p=quarantine"},
				},
			},
		}},
		Deliverability: []vmail.RecordHealth{{Type: "PTR", OK: true, Message: "mail.example.com"}},
	}
}

func TestTheWizardSaysWhenThereIsNothingToDo(t *testing.T) {
	out := vayuDNSWizard(okDNSHealth())
	if !strings.Contains(out, ">All checks pass<") {
		t.Error("an aligned install must say so plainly")
	}
	if strings.Contains(out, "Next:") {
		t.Error("there is no next step when every check passes")
	}
	if strings.Contains(out, "Action needed") {
		t.Error("a passing install must not be labelled as needing action")
	}
	// Every check is still shown, so the operator can see WHAT passed.
	for _, want := range []string{"MX", "SPF", "DKIM", "DMARC", "PTR"} {
		if !strings.Contains(out, want) {
			t.Errorf("the checklist should still list %s", want)
		}
	}
}

func TestTheWizardNamesTheNextThingToFix(t *testing.T) {
	h := okDNSHealth()
	h.AllOK = false
	h.Domains[0].Health.AllOK = false
	h.Domains[0].Health.Records[2] = vmail.RecordHealth{Type: "DKIM", OK: false, Message: "no key found at selector._domainkey.example.com"}

	out := vayuDNSWizard(h)
	if !strings.Contains(out, ">Action needed<") {
		t.Error("a failing check must be labelled as needing action")
	}
	if !strings.Contains(out, "Next:") {
		t.Fatal("the card must end with an instruction, not just a verdict")
	}
	if !strings.Contains(out, "DKIM for example.com") {
		t.Error("the next step must name the failing check and its domain")
	}
	if !strings.Contains(out, "vm-wizard-item--todo") {
		t.Error("the failing row must be marked as outstanding")
	}
	if !strings.Contains(out, "vm-wizard-item--ok") {
		t.Error("the passing rows must still read as ok")
	}
}

func TestTheWizardPrefersADomainFailureOverADeliverabilityOne(t *testing.T) {
	h := okDNSHealth()
	h.AllOK = false
	h.Domains[0].Health.AllOK = false
	h.Domains[0].Health.Records[0] = vmail.RecordHealth{Type: "MX", OK: false, Message: "no MX record"}
	h.Deliverability = []vmail.RecordHealth{{Type: "PTR", OK: false, Message: "missing reverse DNS"}}

	out := vayuDNSWizard(h)
	next := out[strings.Index(out, "Next:"):]
	if !strings.Contains(next, "MX") {
		t.Errorf("the next step should be the earliest failing check (MX), got:\n%s", next)
	}
}

func TestTheWizardHandlesADomainWithNoHealth(t *testing.T) {
	h := dnsHealth{AllOK: true, Domains: []dnsDomainHealth{{Domain: "broken.example"}}}
	out := vayuDNSWizard(h)
	if !strings.Contains(out, "Domain health") {
		t.Error("a domain with no health report must not take the card down with it")
	}
}

func TestTheWizardRefreshIsOutOfBand(t *testing.T) {
	h := okDNSHealth()
	oob := vayuDNSWizardOOB(h)
	if !strings.Contains(oob, `hx-swap-oob="true"`) {
		t.Error("the Re-check response must swap the checklist too, or it keeps showing the previous verdict")
	}
	if strings.Count(oob, `id="vm-dns-wizard"`) != 1 {
		t.Error("the out-of-band copy must carry exactly one swap id")
	}
	if strings.Contains(vayuDNSWizard(h), "hx-swap-oob") {
		t.Error("the page-rendered card must not swap itself out of band")
	}
}
