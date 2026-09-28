// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
)

// Domains & DNS is a list of the domains this install hosts, each row carrying
// the one thing most in need of the operator, and the selected domain's
// records in the inspector.

func dnsView(host string, primary, approved bool, tls string, states ...dnsState) dnsDomainView {
	v := dnsDomainView{Host: host, IsPrimary: primary, SyncApproved: approved, TLSState: tls}
	for i, st := range states {
		h := host
		if i > 0 {
			h = "mail." + host
		}
		v.Checks = append(v.Checks, dnsCheck{dnsRecord: dnsRecord{Host: h, Required: i == 0, ProxyOff: i > 0}, State: st})
	}
	return v
}

// One seed per rule: each view below trips exactly one reason, so each row's
// word is decided by that reason alone.
func TestEachDomainsRowSaysWhatNeedsTheOperator(t *testing.T) {
	for _, c := range []struct {
		v    dnsDomainView
		want string
	}{
		{dnsView("ok.example", true, true, domain.TLSPrimary, dnsPointedHere, dnsUnverified), "Pointed"},
		{dnsView("held.example", false, false, domain.TLSPending, dnsNotPointed), "On hold"},
		{dnsView("nocert.example", false, true, domain.TLSPending, dnsPointedHere), "No certificate"},
		{dnsView("proxied.example", true, true, domain.TLSPrimary, dnsPointedHere, dnsProxied), "Behind the proxy"},
		{dnsView("missing.example", true, true, domain.TLSPrimary, dnsNotPointed), "Not pointed"},
		{dnsView("slow.example", true, true, domain.TLSPrimary, dnsPointedHere, dnsUnknown), "Not checked"},
		// A held domain is approved before anything else about it is fixed.
		{dnsView("heldproxied.example", false, false, domain.TLSPending, dnsPointedHere, dnsProxied), "On hold"},
	} {
		if got := string(c.v.state()); !strings.Contains(got, `</span>`+c.want+`</span>`) {
			t.Errorf("%s reads %s, want %q", c.v.Host, got, c.want)
		}
	}
	// An optional record not pointed is the operator's choice, not a fault.
	optional := dnsView("quiet.example", true, true, domain.TLSPrimary, dnsPointedHere, dnsNotPointed)
	if got := string(optional.state()); !strings.Contains(got, "Pointed</span>") || strings.Contains(got, "Not pointed") {
		t.Errorf("an optional record left unpointed reads %s", got)
	}
}

// An unfinished lookup is evidence of nothing: it is neither counted as
// resolving nor reported as unpointed.
func TestAnUnfinishedLookupClaimsNothing(t *testing.T) {
	v := dnsView("slow.example", true, true, domain.TLSPrimary, dnsUnknown, dnsPointedHere)
	if n := v.resolving(); n != 1 {
		t.Errorf("%d records resolve, want only the one that answered", n)
	}
	if strings.Contains(dnsInspector(v), "Not pointed") {
		t.Error("the record whose lookup did not finish is reported as not pointed")
	}
}

func TestTheDomainsViewsListOnlyTheirOwn(t *testing.T) {
	views := []dnsDomainView{
		dnsView("ok.example", true, true, domain.TLSPrimary, dnsPointedHere),
		dnsView("held.example", false, false, domain.TLSPending, dnsNotPointed),
	}
	page := dnsList(views, "ok.example", "", "")
	if !strings.Contains(page, `data-page-kind="list"`) || strings.Count(page, "data-list-row") != 2 {
		t.Error("the page is not a list of both domains")
	}
	for _, want := range []string{`>Needs attention <span class="muted">1</span>`, "1 of 1 resolve", "0 of 1 resolve", `data-sheet="dns-provision"`} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	if n := strings.Count(page, "btn--primary"); n != 1 {
		t.Errorf("%d primary buttons, want one", n)
	}
	attention := dnsList(views, "ok.example", "attention", "")
	if strings.Count(attention, "data-list-row") != 1 || !strings.Contains(attention, "held.example") {
		t.Error("Needs attention does not list only the held domain")
	}
	if found := dnsList(views, "ok.example", "", "HELD"); strings.Count(found, "data-list-row") != 1 {
		t.Error("a search for HELD does not find held.example")
	}
}

func TestTheDomainsListIsCSPSafe(t *testing.T) {
	hostile := `x"><script>y`
	v := dnsView(hostile, false, true, domain.TLSPending, dnsPointedHere)
	v.Checks[0].Addrs = []string{hostile}
	page := dnsList([]dnsDomainView{v}, hostile, "", hostile)
	if strings.Contains(page, `"><script>`) {
		t.Fatalf("a host broke out of its markup:\n%s", page)
	}
	if strings.Contains(page, `style="`) {
		t.Error("the list carries an inline style attribute, which the CSP forbids")
	}
}
