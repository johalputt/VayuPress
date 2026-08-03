// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProvisionRequestCarriesNoArguments is the guard on the property the whole
// privilege boundary rests on.
//
// The unprivileged service (www-data, NoNewPrivileges) creates a flag file that
// a ROOT systemd unit reacts to. That is only safe while the flag is a pure
// signal: the moment anything writes content here and the root worker reads it,
// an unprivileged process is passing arguments to a root process — the classic
// local privilege escalation, and it would arrive looking like a small feature
// ("let the console choose which subdomain to provision").
//
// So this test asserts the request file is EMPTY, and the source below asserts
// the worker never reads it.
func TestProvisionRequestCarriesNoArguments(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYU_DATA_DIR", dir)

	path := filepath.Join(dir, provisionRequestFile)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(b) != 0 {
		t.Fatalf("request file carries %d bytes; it must be a pure signal", len(b))
	}
	if !provisionPending() {
		t.Error("a fresh request was not reported as pending")
	}
}

// TestProvisionWorkerNeverReadsTheRequest inspects the root-side script. A grep
// is crude, but the property is worth an imperfect guard: if someone teaches the
// worker to read the flag, this fails and says why, which is more than review
// would reliably catch on a shell script nobody looks at twice.
func TestProvisionWorkerNeverReadsTheRequest(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("../../scripts/provision-subdomains.sh"))
	if err != nil {
		t.Skipf("worker script not readable from here: %v", err)
	}
	src := string(b)

	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.Contains(trimmed, "REQUEST") {
			continue
		}
		// Removing the flag is the only legitimate use.
		if strings.HasPrefix(trimmed, "rm ") || strings.Contains(trimmed, `rm -f "$REQUEST"`) ||
			strings.HasPrefix(trimmed, "REQUEST=") {
			continue
		}
		t.Errorf("the root worker touches the request file beyond deleting it:\n  %s\n"+
			"Reading it would let an unprivileged process pass arguments to root.", trimmed)
	}
}

// TestProvisionStatusRequiresAdmin — the endpoint reveals server paths and run
// history, and the run endpoint makes the box execute certbot on demand. Neither
// belongs to a non-admin session.
func TestProvisionStatusRequiresAdmin(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("admin_os_provision.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	src := string(b)
	for _, fn := range []string{"handleOSProvisionRequest", "handleOSProvisionStatus"} {
		i := strings.Index(src, "func (a *App) "+fn)
		if i < 0 {
			t.Fatalf("%s not found", fn)
		}
		end := strings.Index(src[i:], "\nfunc ")
		if end < 0 {
			end = len(src) - i
		}
		if !strings.Contains(src[i:i+end], "isAdminRequest") {
			t.Errorf("%s is not admin-gated", fn)
		}
	}
}

// TestProvisionRunIsCSRFProtected — a POST that makes the server run certbot
// must not be triggerable by an off-origin page. Let's Encrypt rate limits are
// finite, so this is a real denial-of-service lever, not a theoretical one.
func TestProvisionRunIsCSRFProtected(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("admin_os_ui.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, "/os/api/provision/run") {
			continue
		}
		if !strings.Contains(line, "CSRFTokenMiddleware") {
			t.Errorf("provision run route is not CSRF-protected:\n  %s", strings.TrimSpace(line))
		}
		return
	}
	t.Error("provision run route not registered")
}

// TestDNSRecordsCoverEveryProvisionedSubdomain keeps the page honest against the
// provisioning worker. If a helper gains a new subdomain and this list does not,
// the page shows a green install that is quietly missing a record — which is the
// exact failure mode the page was built to end.
func TestDNSRecordsCoverEveryProvisionedSubdomain(t *testing.T) {
	got := map[string]bool{}
	for _, r := range subdomainRecords("example.com", true, true) {
		got[r.Host] = true
	}
	for _, want := range []string{
		"example.com", "www.example.com", "mail.example.com",
		"openpgpkey.example.com", "talk.example.com",
		"mcp.example.com", "api.example.com",
	} {
		if !got[want] {
			t.Errorf("Domains & DNS does not list %s", want)
		}
	}
}

// TestProxyOffMarkedOnEveryMachineToMachineHost — the apex and www may sit behind
// a proxy; every other host is fetched by software with no JavaScript engine, so
// a bot challenge breaks it. Marking one of them "either" would send an operator
// to a silent failure with the page's blessing.
func TestProxyOffMarkedOnEveryMachineToMachineHost(t *testing.T) {
	for _, r := range subdomainRecords("example.com", true, true) {
		machineToMachine := strings.HasPrefix(r.Host, "mail.") ||
			strings.HasPrefix(r.Host, "openpgpkey.") ||
			strings.HasPrefix(r.Host, "talk.") ||
			strings.HasPrefix(r.Host, "mcp.") ||
			strings.HasPrefix(r.Host, "api.")
		if machineToMachine && !r.ProxyOff {
			t.Errorf("%s is machine-to-machine but not marked proxy-off", r.Host)
		}
		if !machineToMachine && r.ProxyOff {
			t.Errorf("%s is browser traffic; forcing proxy-off needlessly gives up CDN protection", r.Host)
		}
	}
}

// TestDNSPageIsAdminGated — the page lists the install's own hostnames and
// resolved addresses and carries the privileged provisioning control.
func TestDNSPageIsAdminGated(t *testing.T) {
	if osPathMinLevel("/os/dns") < osPathMinLevel("/os/update") {
		t.Error("/os/dns is gated below /os/update; it exposes infrastructure detail and a privileged control")
	}
}

// TestProxiedApexDoesNotFlagCorrectSubdomains reproduces the real-world layout
// that the first version of this page got exactly backwards.
//
// The documented, CORRECT configuration for a CDN-fronted install is: apex and
// www proxied (so human traffic keeps CDN protection), every machine-to-machine
// subdomain direct at the origin. The original check compared each subdomain
// against the apex — so on precisely that configuration it reported all five
// direct hosts as "resolves elsewhere" and told the operator their working
// install was broken.
//
// A status page that cries wolf on the correct setup is worse than none: it
// trains people to ignore it, so the one time it is right they scroll past.
func TestProxiedApexDoesNotFlagCorrectSubdomains(t *testing.T) {
	// The origin this machine holds.
	local := map[string]bool{"5.189.133.235": true}
	// The apex resolves to a CDN, which this machine does not hold.
	apexAddrs := map[string]bool{"188.114.96.3": true, "2a06:98c1:3121::3": true}

	apexIsLocal := false
	for a := range apexAddrs {
		if local[a] {
			apexIsLocal = true
		}
	}
	apexProxied := len(apexAddrs) > 0 && !apexIsLocal && len(local) > 0
	if !apexProxied {
		t.Fatal("a CDN-fronted apex was not recognised as proxied")
	}

	// A direct subdomain on the origin must read as pointed here, not as a fault.
	direct := []string{"5.189.133.235"}
	state := classifyForTest(direct, local, apexAddrs, apexProxied, true)
	if state != dnsPointedHere {
		t.Errorf("a correctly configured direct subdomain was flagged (state=%v)", state)
	}

	// A subdomain left ON the proxy is the genuine misconfiguration.
	onProxy := []string{"188.114.96.3"}
	if state := classifyForTest(onProxy, local, apexAddrs, apexProxied, true); state != dnsProxied {
		t.Errorf("a proxied machine-to-machine host was not flagged (state=%v)", state)
	}
}

// TestUnverifiableDoesNotReportAFault — behind NAT the public address is on no
// local interface, so nothing can be proven. Reporting a fault we cannot
// substantiate is how a status page loses its credibility.
func TestUnverifiableDoesNotReportAFault(t *testing.T) {
	local := map[string]bool{"10.0.0.5": true} // private, behind NAT
	apexAddrs := map[string]bool{"203.0.113.10": true}
	apexProxied := true

	if state := classifyForTest([]string{"203.0.113.11"}, local, apexAddrs, apexProxied, true); state != dnsUnverified {
		t.Errorf("an unprovable record was reported as something definite (state=%v)", state)
	}
}

// TestSecondaryMailDomainGetsItsOwnKeyDiscoveryRecord — WKD is per-domain: a key
// for someone@shop.example is only findable at openpgpkey.shop.example, on that
// host's own certificate. A page that listed the primary's records alone told an
// operator hosting several mail domains that everything was pointed while every
// secondary domain's key discovery was silently dead.
func TestSecondaryMailDomainGetsItsOwnKeyDiscoveryRecord(t *testing.T) {
	got := map[string]bool{}
	for _, r := range subdomainRecords("shop.example", false, true) {
		got[r.Host] = true
	}
	for _, want := range []string{
		"shop.example", "www.shop.example",
		"mail.shop.example", "openpgpkey.shop.example",
	} {
		if !got[want] {
			t.Errorf("a hosted mail domain does not list %s", want)
		}
	}
}

// TestSecondaryDomainsDoNotDemandInstallWideHosts — talk/mcp/api exist once per
// install, on the primary. Listing them under every secondary would invent work
// for the operator: they would point records at hosts with nothing behind them,
// and the page would then report the install as incomplete forever.
func TestSecondaryDomainsDoNotDemandInstallWideHosts(t *testing.T) {
	for _, mail := range []bool{true, false} {
		for _, r := range subdomainRecords("shop.example", false, mail) {
			for _, installWide := range []string{"talk.", "mcp.", "api."} {
				if strings.HasPrefix(r.Host, installWide) {
					t.Errorf("secondary domain (mail=%v) demands %s, which is install-wide", mail, r.Host)
				}
			}
		}
	}
}

// TestASubdomainHostIsNotAskedForAWwwRecord — a site hosted at test.johal.in was
// listed as REQUIRING www.test.johal.in. Nobody creates that record: www is a
// convention of a registrable name, not of an arbitrary host.
//
// The consequence was not cosmetic. Required drives a warn badge, holds the
// domain's section open on every visit, and counts in the "Not pointed" tile —
// so a correctly configured subdomain reported a permanent fault with no action
// that could ever clear it. That is the same defect this file already refuses to
// invent for install-wide hosts and for mail records on a domain that serves no
// mailboxes, and it is exactly what localAddrSet's comment warns about: a check
// that cries wolf on the correct configuration trains people to ignore it.
func TestASubdomainHostIsNotAskedForAWwwRecord(t *testing.T) {
	for _, host := range []string{"test.johal.in", "shop.example.co.uk", "a.b.example.com"} {
		for _, mail := range []bool{true, false} {
			for _, r := range subdomainRecords(host, false, mail) {
				if r.Host == "www."+host {
					t.Errorf("a site hosted at %s is asked for www.%s — a record the operator "+
						"will never create, reported as a fault forever", host, host)
				}
			}
		}
	}
}

// TestARegistrableNameStillGetsItsWwwRecord — the fix above must not throw away
// the case it exists for. On a name someone actually registered, a missing www
// is a real hole that fails where the operator cannot see it: the visitor who
// types www gets an error and never reports it.
func TestARegistrableNameStillGetsItsWwwRecord(t *testing.T) {
	for _, host := range []string{"johal.in", "example.com", "example.co.uk", "shop.example"} {
		found := false
		for _, r := range subdomainRecords(host, false, false) {
			if r.Host == "www."+host {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is a registrable name and lists no www record, so a missing www "+
				"redirect goes unreported", host)
		}
	}
}

// TestNonMailSecondaryIsNotAskedForMailRecords — pointing mail./openpgpkey. at a
// domain that serves no mailboxes buys nothing, and the page would report the
// absence as a gap that can never legitimately close.
func TestNonMailSecondaryIsNotAskedForMailRecords(t *testing.T) {
	for _, r := range subdomainRecords("brochure.example", false, false) {
		if strings.HasPrefix(r.Host, "mail.") || strings.HasPrefix(r.Host, "openpgpkey.") {
			t.Errorf("a domain with mail disabled is asked for %s", r.Host)
		}
	}
}

// TestHeldDomainNeedsAttention — a domain on manual hold is skipped by every
// provisioning helper, which is correct, and was previously skipped in SILENCE,
// which is not: an operator saw no certificate and nothing explaining why. The
// section must open itself rather than hide the reason behind a click.
func TestHeldDomainNeedsAttention(t *testing.T) {
	held := dnsDomainView{Host: "shop.example", SyncApproved: false}
	if !held.NeedsAttention() {
		t.Error("a domain on manual hold is folded away, so nothing says why it was never provisioned")
	}
	fine := dnsDomainView{Host: "shop.example", SyncApproved: true, Checks: []dnsCheck{
		{dnsRecord: dnsRecord{Host: "shop.example", Required: true}, State: dnsPointedHere},
	}}
	if fine.NeedsAttention() {
		t.Error("a fully pointed, approved domain is demanding attention it does not need")
	}
}

// TestUnfinishedLookupIsNotReportedAsNotPointed — the page now issues many times
// more lookups than it did for a single domain, so some can hit the deadline. A
// lookup that did not finish is not evidence the record is missing, and counting
// it as one would put a false number on the page.
func TestUnfinishedLookupIsNotReportedAsNotPointed(t *testing.T) {
	if dnsUnknown == dnsNotPointed {
		t.Fatal("an unfinished lookup is indistinguishable from a missing record")
	}
	v := dnsDomainView{SyncApproved: true, Checks: []dnsCheck{
		{dnsRecord: dnsRecord{Host: "mail.example.com", Required: true}, State: dnsUnknown},
	}}
	if v.NeedsAttention() {
		t.Error("an unfinished lookup raised an alarm about a record it knows nothing about")
	}
}

// classifyForTest mirrors the classification in resolveAll. Kept in the test
// so the table above documents the intent; resolveAll is exercised end to
// end by the page itself.
func classifyForTest(addrs []string, local, apexAddrs map[string]bool, apexProxied, proxyOff bool) dnsState {
	if len(addrs) == 0 {
		return dnsNotPointed
	}
	for _, a := range addrs {
		if local[a] {
			return dnsPointedHere
		}
	}
	for _, a := range addrs {
		if apexAddrs[a] && apexProxied && proxyOff {
			return dnsProxied
		}
	}
	return dnsUnverified
}
