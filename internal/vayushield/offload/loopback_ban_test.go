// SPDX-License-Identifier: Apache-2.0

package offload

// loopback_ban_test.go — the shield must never ban the machine it protects.
//
// THE INCIDENT.
//
// A live install returned 502 for roughly ten minutes at a time, repeatedly,
// always shortly after a certificate provisioning run, and always recovered on
// its own. The application was healthy throughout — /health, which touches no
// database, answered in 1 ms. What gave it away was this, run ON the server:
//
//	$ curl -m 8 http://127.0.0.1:8080/
//	curl: (28) Failed to connect to 127.0.0.1 port 8080: Connection timed out
//
// A loopback connection cannot time out. There is no network between the two
// ends: it connects, or it is refused, and both are instant. A TIMEOUT means a
// packet filter is DROPPING the packets — a `reject` would have said "refused".
//
// The filter was this product's own. VayuShield's kernel offload exports jailed
// addresses to a root agent, which drops them by source address in a chain that
// runs ahead of everything else on the machine. Nothing excluded loopback. So
// once 127.0.0.1 entered the ban set, nginx could not reach this application and
// every visitor got 502 until the ban's TTL expired.
//
// How the loopback address gets jailed at all is the part worth keeping in mind:
// when a reverse proxy's real-IP layer is not configured, EVERY visitor arrives
// as 127.0.0.1, so one bad actor convicts the whole audience — and the machine.

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newExporter(t *testing.T) (*Exporter, string) {
	t.Helper()
	dir := t.TempDir()
	return New(dir), dir
}

// THE test. Banning loopback must be refused outright.
func TestLoopbackIsNeverExportedToTheKernel(t *testing.T) {
	e, dir := newExporter(t)

	for _, ip := range []string{"127.0.0.1", "127.0.0.53", "::1", "0.0.0.0", "::"} {
		e.Ban(ip, time.Hour)
	}
	e.Flush()

	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		if os.IsNotExist(err) {
			return // nothing written at all is the correct outcome
		}
		t.Fatal(err)
	}
	body := string(b)
	for _, ip := range []string{"127.0.0.1", "127.0.0.53", "::1", "0.0.0.0"} {
		if strings.Contains(body, ip) {
			t.Errorf("%q reached the kernel ban file. The root agent drops by source address in a "+
				"chain ahead of everything else, so this takes nginx away from the application and "+
				"every visitor gets 502 until the ban expires — with the process healthy and nothing "+
				"in its log.\nfile:\n%s", ip, body)
		}
	}
}

// A refusal must be counted, because it means the shield decided this machine
// was its own attacker — almost always a real-IP misconfiguration where the
// whole audience shares one address.
func TestARefusedLoopbackBanIsCountedNotSwallowed(t *testing.T) {
	e, _ := newExporter(t)
	e.Ban("127.0.0.1", time.Hour)
	e.Ban("::1", time.Hour)
	e.Ban("0.0.0.0", time.Hour)

	got := e.RefusedBans()
	if got["loopback"] != 2 {
		t.Errorf("loopback refusals = %d, want 2; a silent refusal hides the misconfiguration that "+
			"caused it", got["loopback"])
	}
	if got["unspecified"] != 1 {
		t.Errorf("unspecified refusals = %d, want 1", got["unspecified"])
	}
	if e.Count() != 0 {
		t.Errorf("%d ban(s) live after only refusable addresses were submitted", e.Count())
	}
}

// The guard must not have closed the feature to close the hole. A real attacker
// address still gets banned.
func TestARealAddressIsStillBanned(t *testing.T) {
	e, dir := newExporter(t)
	e.Ban("203.0.113.9", time.Hour)
	e.Ban("2001:db8::1", time.Hour)
	e.Flush()

	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("nothing was written; the guard broke banning entirely: %v", err)
	}
	for _, ip := range []string{"203.0.113.9", "2001:db8::1"} {
		if !strings.Contains(string(b), ip) {
			t.Errorf("%q was not exported; the loopback guard is refusing real addresses", ip)
		}
	}
	if e.Count() != 2 {
		t.Errorf("Count() = %d, want 2", e.Count())
	}
}

// Private ranges stay bannable on purpose. An operator behind a LAN-facing
// proxy may have a real reason to ban 10.x, and refusing it would be this
// product overriding a decision that belongs to them. Loopback is different in
// kind — banning it can never be what anybody wanted.
func TestPrivateRangesAreStillBannable(t *testing.T) {
	e, _ := newExporter(t)
	e.Ban("10.1.2.3", time.Hour)
	e.Ban("192.168.1.50", time.Hour)
	if e.Count() != 2 {
		t.Errorf("Count() = %d, want 2 — private addresses are the operator's call, not ours", e.Count())
	}
}

// The predicate itself, named cases, so a future edit cannot quietly widen or
// narrow it without a test saying so.
func TestNeverBannableCoversLoopbackAndUnspecifiedOnly(t *testing.T) {
	cases := []struct {
		ip     string
		refuse bool
		why    string
	}{
		{"127.0.0.1", true, "loopback"},
		{"127.255.255.254", true, "loopback"},
		{"::1", true, "loopback"},
		{"0.0.0.0", true, "unspecified"},
		{"::", true, "unspecified"},
		{"10.0.0.1", false, ""},
		{"192.168.0.1", false, ""},
		{"169.254.1.1", false, ""},
		{"203.0.113.1", false, ""},
		{"2001:db8::1", false, ""},
	}
	for _, c := range cases {
		addr, err := netip.ParseAddr(c.ip)
		if err != nil {
			t.Fatalf("bad fixture %q: %v", c.ip, err)
		}
		refuse, why := neverBannable(addr)
		if refuse != c.refuse {
			t.Errorf("neverBannable(%s) = %v, want %v", c.ip, refuse, c.refuse)
		}
		if refuse && why != c.why {
			t.Errorf("neverBannable(%s) reason = %q, want %q", c.ip, why, c.why)
		}
	}
}

// Protect and Unban must keep working — the new early return sits before them.
func TestTheGuardDoesNotBreakProtectOrUnban(t *testing.T) {
	e, _ := newExporter(t)
	e.Ban("198.51.100.7", time.Hour)
	if e.Count() != 1 {
		t.Fatalf("setup: Count() = %d", e.Count())
	}
	e.Unban("198.51.100.7")
	if e.Count() != 0 {
		t.Errorf("Unban did not withdraw the ban: Count() = %d", e.Count())
	}
	e.Protect("198.51.100.8")
	e.Ban("198.51.100.8", time.Hour)
	if e.Count() != 0 {
		t.Errorf("a protected operator address was banned: Count() = %d", e.Count())
	}
}
