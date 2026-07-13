package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayuos/mail"
)

// TestVayuMailHostSection pins the record set the old DNS tab never showed: the
// A/AAAA mail-host records, the reverse-DNS hint, the inbound ports, and the
// load-bearing "must not be proxied / DNS only" warning.
func TestVayuMailHostSection(t *testing.T) {
	out := vayuMailHostSection(mail.Config{Domain: "example.com", Hostname: "mail.example.com"})
	for _, want := range []string{"mail.example.com", "DNS only", "do not proxy", "AAAA", "PTR", "25"} {
		if !strings.Contains(out, want) {
			t.Errorf("mail host section missing %q", want)
		}
	}
}

// TestVayuDNSRecordsSection checks a records collapsible carries the record, a
// per-value copy control, and starts open when asked.
func TestVayuDNSRecordsSection(t *testing.T) {
	recs := []mail.DNSRecord{{Type: "MX", Name: "example.com.", Value: "mail.example.com.", Priority: 10}}
	out := vayuDNSRecordsSection("Records to publish — example.com", "sub", recs, true)
	for _, want := range []string{"vm-sec", "vm-tag", "mail.example.com.", `data-copy="mail.example.com."`, "<details", " open>"} {
		if !strings.Contains(out, want) {
			t.Errorf("records section missing %q\n%s", want, out)
		}
	}
}

// TestVayuDNSCollapsibleOpenState confirms the open flag is honoured so important
// sections start expanded and secondaries start collapsed.
func TestVayuDNSCollapsibleOpenState(t *testing.T) {
	if open := vayuDNSCollapsible("T", "", true, "x"); !strings.Contains(open, `<details class="card vm-sec" open>`) {
		t.Errorf("open collapsible not open: %s", open)
	}
	if closed := vayuDNSCollapsible("T", "", false, "x"); strings.Contains(closed, " open>") {
		t.Errorf("closed collapsible should not be open: %s", closed)
	}
}

// TestVayuDNSVerifyDomainTable pins the per-domain verification rendering: a
// misaligned MX surfaces its actionable message and an "action" badge, an aligned
// record reads ok, and the domain header reflects the overall state.
func TestVayuDNSVerifyDomainTable(t *testing.T) {
	hc := &mail.DomainHealth{Domain: "shop.example", AllOK: false, Records: []mail.RecordHealth{
		{Type: "MX", OK: false, Message: "MX does not point to mail.example.com — mail for this domain is delivered elsewhere"},
		{Type: "SPF", OK: true, Found: "v=spf1 a mx ~all"},
	}}
	out := vayuDNSVerifyDomainTable("shop.example", hc)
	for _, want := range []string{"shop.example", "check records", "delivered elsewhere", "v=spf1 a mx ~all", "action", "ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("verify table missing %q\n%s", want, out)
		}
	}
}
