// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayuos/mail"
)

// TestAnUnfinishedMailDomainIsRaisedFromTheStoredVerdict pins the badge's three
// states — never checked, failing, passing — and who may see it. The DNS tab is
// administrator-only, so an author must never be pointed at it.
func TestAnUnfinishedMailDomainIsRaisedFromTheStoredVerdict(t *testing.T) {
	a := appWithMailAccounts(t)
	ctx := context.Background()
	bell := func(level int) string {
		var out []string
		for _, n := range a.osNotifications(ctx, &osSettings{AccessLevel: level}) {
			out = append(out, n.Title+" | "+n.Detail+" | "+n.Href)
		}
		return strings.Join(out, "\n")
	}
	// The Mail sidebar's DNS section, as the console builds it for this install.
	tabs := func() string {
		s := a.getOSSettings(ctx)
		s.AccessLevel, s.Route = accessAdmin, "/os/vayumail"
		return stillAirShellHead("n", "Mail", "vayuos", s)
	}

	// Never checked: unknown is not a problem to raise.
	if strings.Contains(bell(accessAdmin), "Mail domain") || strings.Contains(tabs(), `aria-label="needs attention"`) {
		t.Fatal("an install that has never finished a check must not be told its DNS needs attention")
	}

	a.recordMailDNS(dnsHealth{Domains: []dnsDomainHealth{{Domain: "example.com", Health: &mail.DomainHealth{
		Records: []mail.RecordHealth{{Type: "SPF", Message: "no SPF record"}},
	}}}})
	if got := bell(accessAdmin); !strings.Contains(got, "Mail domain needs attention | SPF for example.com — no SPF record | /os/vayumail/dns") {
		t.Errorf("a failing check must reach an administrator's bell with its next step, got:\n%s", got)
	}
	if !strings.Contains(tabs(), `aria-label="needs attention"`) {
		t.Error("a failing check must mark the DNS tab")
	}
	if strings.Contains(bell(accessAuthor), "Mail domain") {
		t.Error("an author was pointed at the administrator-only DNS tab")
	}

	a.recordMailDNS(dnsHealth{AllOK: true})
	if strings.Contains(bell(accessAdmin), "Mail domain") || strings.Contains(tabs(), `aria-label="needs attention"`) {
		t.Error("a passing check must clear both")
	}
}

// TestALiveCheckStoresItsVerdict — the tab, its Re-check and the background
// watch all go through vayuDNSHealth, and the badge only ever reads what that
// stored. A cancelled context fails every lookup at once, without a network.
func TestALiveCheckStoresItsVerdict(t *testing.T) {
	a := appWithMailAccounts(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.vayuDNSHealth(ctx)
	if _, bad := a.mailDNSNeedsAttention(); !bad {
		t.Fatal("a check whose every lookup failed did not leave a failing verdict behind")
	}
}
