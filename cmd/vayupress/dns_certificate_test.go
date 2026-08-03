// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
)

// An operator pointed a subdomain at the server, opened Domains & DNS, and read:
// 3 domains hosted, 12 resolving, 0 behind the proxy, 0 not pointed, the record
// itself badged "pointed here". Every number on the page said finished.
//
// The site served a browser interstitial —  ERR_CERT_COMMON_NAME_INVALID. No
// certificate had ever been issued for that host, so nginx fell through to the
// default vhost and presented the primary's certificate.
//
// The page HAD that fact the whole time: dnsDomainView carries TLSState, loaded
// from the registry for every domain, and rendered it nowhere. This is the page
// whose own header says each of these things "fails QUIETLY when its record is
// missing, so this page checks rather than assumes" — and it was assuming.

// A domain that resolves here but has no certificate is the specific state that
// looks finished and is not. It must be surfaced.
func TestADomainThatResolvesWithoutACertificateIsSurfaced(t *testing.T) {
	pointed := func(host string) []dnsCheck {
		return []dnsCheck{{dnsRecord: dnsRecord{Host: host, Required: true}, State: dnsPointedHere}}
	}
	cases := []struct {
		name string
		v    dnsDomainView
		want bool
	}{
		{"DNS pointed, certificate never issued — the whole reason this exists",
			dnsDomainView{Host: "test.example.com", SyncApproved: true,
				TLSState: domain.TLSPending, Checks: pointed("test.example.com")}, true},
		{"DNS pointed, certbot reported a failure",
			dnsDomainView{Host: "test.example.com", SyncApproved: true,
				TLSState: domain.TLSFailed, Checks: pointed("test.example.com")}, true},
		{"DNS pointed and the certificate is live — nothing to say",
			dnsDomainView{Host: "test.example.com", SyncApproved: true,
				TLSState: domain.TLSActive, Checks: pointed("test.example.com")}, false},
		{"the primary carries its certificate outside the registry",
			dnsDomainView{Host: "example.com", IsPrimary: true, SyncApproved: true,
				TLSState: domain.TLSPrimary, Checks: pointed("example.com")}, false},
		{"DNS not pointed yet — the record row already says so, do not complain twice",
			dnsDomainView{Host: "test.example.com", SyncApproved: true, TLSState: domain.TLSPending,
				Checks: []dnsCheck{{dnsRecord: dnsRecord{Host: "test.example.com", Required: true},
					State: dnsNotPointed}}}, false},
		{"the lookup did not finish — asserting a missing certificate would be a guess",
			dnsDomainView{Host: "test.example.com", SyncApproved: true, TLSState: domain.TLSPending,
				Checks: []dnsCheck{{dnsRecord: dnsRecord{Host: "test.example.com", Required: true},
					State: dnsUnknown}}}, false},
		{"a domain on manual hold is not provisioned BY DESIGN, and the hold notice says so",
			dnsDomainView{Host: "test.example.com", SyncApproved: false,
				TLSState: domain.TLSPending, Checks: pointed("test.example.com")}, false},
	}
	for _, c := range cases {
		if got := c.v.NeedsCertificate(); got != c.want {
			t.Errorf("%s: NeedsCertificate() = %v, want %v", c.name, got, c.want)
		}
	}
}

// Surfacing it means the section opens itself. A fact folded behind a click is
// how this went unnoticed: the operator's eye goes to the tiles, and the tiles
// were all green.
func TestAMissingCertificateOpensTheDomainSection(t *testing.T) {
	v := dnsDomainView{Host: "test.example.com", SyncApproved: true, TLSState: domain.TLSPending,
		Checks: []dnsCheck{{dnsRecord: dnsRecord{Host: "test.example.com", Required: true},
			State: dnsPointedHere}}}
	if !v.NeedsAttention() {
		t.Fatal("a domain whose DNS is pointed and whose certificate was never issued is folded " +
			"away as healthy. Its site serves a browser security interstitial and this page — " +
			"the one an operator opens to check exactly this — shows nothing but green")
	}
}

// And the rendered section must say it, in words that name the next action.
// "TLS: pending" is a status; it is not something an operator can act on.
func TestTheRenderedSectionNamesTheMissingCertificateAndWhatToDo(t *testing.T) {
	v := dnsDomainView{Host: "test.example.com", SyncApproved: true, TLSState: domain.TLSPending,
		Checks: []dnsCheck{{dnsRecord: dnsRecord{Host: "test.example.com", Required: true},
			State: dnsPointedHere}}}
	html := dnsDomainSection(v)
	// Not assertCSPSafe: this page's own table header reads "CDN proxy", which is
	// the correct user-facing word here and trips that helper's asset-host scan.
	// The part of the rule that applies to rendered markup is the inline style.
	if strings.Contains(html, `style="`) {
		t.Error("the section carries an inline style attribute, which the CSP forbids")
	}

	low := strings.ToLower(html)
	if !strings.Contains(low, "no certificate") {
		t.Error("the section never says the certificate is missing")
	}
	if !strings.Contains(low, "provision subdomains") {
		t.Error("the section does not name the control that fixes it, so the operator is told " +
			"something is wrong and left to find the button")
	}

	// A healthy domain must stay quiet — a warning that is always present is one
	// nobody reads, which is the failure mode this page has already had twice.
	healthy := v
	healthy.TLSState = domain.TLSActive
	if strings.Contains(strings.ToLower(dnsDomainSection(healthy)), "no certificate") {
		t.Error("a domain with a live certificate is warned about anyway")
	}
}

// The provisioning result the panel reports must not call a run that did nothing
// a clean run. The driver already knows this — its own comment says "an exit
// status of 0 is not the same as work done" — and catches exactly one of the
// three ways a helper can exit 0 having done nothing.
//
// The one it misses is the worst of them. Every helper aborts early, printing
// "nginx configuration is ALREADY invalid", when a single stale vhost anywhere
// under sites-enabled breaks `nginx -t`. That path prints no "skipping", so all
// five helpers are counted as having run, and the panel reports "5 provisioned,
// 0 skipped, 0 reported a problem" for an install where nothing was provisioned
// and nothing ever will be until someone finds the bad file.
func TestAProvisioningRunThatDidNothingIsNotReportedAsClean(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("../../scripts/provision-subdomains.sh"))
	if err != nil {
		t.Skipf("worker script not readable from here: %v", err)
	}
	src := string(b)

	if !strings.Contains(src, "ALREADY invalid") {
		t.Error("a helper that aborted because nginx was already broken is counted as a " +
			"successful run. The panel then reports the install as clean while nothing was " +
			"provisioned and nothing can be until the stale vhost is found")
	}
	if !strings.Contains(src, "nothing to do") {
		t.Error(`a helper that printed "nothing to do" is counted as work done`)
	}
	// The nginx case is a PROBLEM, not a no-op: it needs the operator, and
	// filing it under "skipped" would put it in the same column as a subdomain
	// whose DNS is simply not pointed yet — which is fine and needs nobody.
	if !strings.Contains(src, `failed=$((failed + 1))`) {
		t.Error("the driver has no failure path left for a broken-nginx abort")
	}
}

// The tile strip is what an operator actually reads. It counted records and said
// nothing about certificates, which is how "0 not pointed" came to mean "done".
func TestTheTileStripCountsDomainsWithoutACertificate(t *testing.T) {
	src := readSourceFile(t, "admin_os_dns.go")
	body := goFuncBody(src, "handleOSDNS")
	if !strings.Contains(body, "NeedsCertificate()") {
		t.Error("the page's summary never counts domains missing a certificate, so every tile " +
			"can read green while a hosted site serves a certificate error")
	}
}
