// SPDX-License-Identifier: Apache-2.0

package policy

import (
	"net/http"
	"strings"
	"testing"
)

// TestDenyWinsOverAllow is the ordering decision, and it is the one that matters
// most: an operator commonly allows a /24 they trust and denies a /32 inside it.
// Getting the precedence backwards means an attacker who lands inside a trusted
// range bypasses every gate in the shield, which is far worse than the opposite
// error — a trusted host being refused is visible within seconds.
func TestDenyWinsOverAllow(t *testing.T) {
	r, bad := Compile(Config{
		AllowCIDRs: []string{"198.51.100.0/24"},
		DenyCIDRs:  []string{"198.51.100.7/32"},
	})
	if len(bad) != 0 {
		t.Fatalf("unexpected parse failures: %v", bad)
	}
	if got := r.Source("198.51.100.7"); got != VerdictDeny {
		t.Errorf("an address both allowed by /24 and denied by /32 got %v, want deny", got)
	}
	if got := r.Source("198.51.100.8"); got != VerdictAllow {
		t.Errorf("the rest of the allowed range got %v, want allow", got)
	}
	if got := r.Source("203.0.113.1"); got != VerdictNone {
		t.Errorf("an unrelated address got %v, want none", got)
	}
}

// TestBareAddressesWork — operators write "1.2.3.4" as often as "1.2.3.4/32",
// and rejecting the shorter form would be a parse failure that reads as a bug in
// the product rather than in the input.
func TestBareAddressesWork(t *testing.T) {
	r, bad := Compile(Config{AllowCIDRs: []string{"203.0.113.5", "2001:db8::1"}})
	if len(bad) != 0 {
		t.Fatalf("bare addresses were rejected: %v", bad)
	}
	if r.Source("203.0.113.5") != VerdictAllow {
		t.Error("a bare IPv4 address did not match")
	}
	if r.Source("2001:db8::1") != VerdictAllow {
		t.Error("a bare IPv6 address did not match")
	}
	if r.Source("203.0.113.6") != VerdictNone {
		t.Error("a bare address matched its neighbour — it was not treated as a /32")
	}
}

// TestBadEntriesAreSkippedAndNamed — one typo in ten lines must not lose the
// other nine, and must not be silent either: a silently dropped ALLOW entry
// means the source it was protecting starts getting challenged with no visible
// cause, which is close to undiagnosable from the operator's side.
func TestBadEntriesAreSkippedAndNamed(t *testing.T) {
	r, bad := Compile(Config{
		AllowCIDRs: []string{"198.51.100.0/24", "not-an-address", "10.0.0.0/8", "999.1.1.1/24"},
	})
	if len(bad) != 2 {
		t.Errorf("reported %d bad entries, want 2: %v", len(bad), bad)
	}
	for _, want := range []string{"not-an-address", "999.1.1.1/24"} {
		found := false
		for _, b := range bad {
			if strings.Contains(b, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%q was dropped without being named", want)
		}
	}
	if r.Source("198.51.100.1") != VerdictAllow || r.Source("10.1.2.3") != VerdictAllow {
		t.Error("valid entries were lost because a sibling was malformed")
	}
}

// TestCountryRulesAreInertInTorMode — every peer in a Tor Space is 127.0.0.1, so
// a country lookup returns the SERVER's location for every visitor. A rule built
// on that would either refuse everyone or serve everyone while appearing to do
// geography, which is worse than having no rule: it looks like a working control.
func TestCountryRulesAreInertInTorMode(t *testing.T) {
	clearnet, _ := Compile(Config{DenyCountries: []string{"XX"}})
	if got := clearnet.Country("XX"); got != VerdictDeny {
		t.Errorf("clearnet: denied country got %v, want deny", got)
	}
	if !clearnet.GeoActive() {
		t.Error("clearnet reported geo inactive")
	}

	onion, _ := Compile(Config{DenyCountries: []string{"XX"}, OnionMode: true})
	if got := onion.Country("XX"); got != VerdictNone {
		t.Errorf("Tor Space: a country rule returned %v — every peer there is loopback, so this "+
			"is the server's own location applied to every visitor", got)
	}
	if onion.GeoActive() {
		t.Error("a Tor Space reported geo as active, so the panel would show it as configured " +
			"rather than as disabled")
	}
}

// TestSourceRulesAreInertInTorMode — the deny side of this is merely useless
// there (every peer is 127.0.0.1, so a list refuses everyone or nobody). The
// ALLOW side is dangerous: 127.0.0.1 is an utterly ordinary allow entry on a
// clearnet install — a health check, a monitoring probe, an uptime agent — and
// carrying it into a Tor Space would match every visitor and hand each one the
// operator-trusted bypass past every gate in the shield.
func TestSourceRulesAreInertInTorMode(t *testing.T) {
	onion, bad := Compile(Config{
		AllowCIDRs: []string{"127.0.0.1", "not-an-address"},
		DenyCIDRs:  []string{"198.51.100.0/24"},
		OnionMode:  true,
	})
	if got := onion.Source("127.0.0.1"); got != VerdictNone {
		t.Errorf("a loopback allow entry got %v in a Tor Space — that is every visitor there, "+
			"each one bypassing the entire shield", got)
	}
	if got := onion.Source("198.51.100.9"); got != VerdictDeny && got != VerdictNone {
		t.Errorf("unexpected verdict %v", got)
	} else if got == VerdictDeny {
		t.Error("an address rule reached a verdict in a Tor Space")
	}
	if onion.SourceActive() {
		t.Error("a Tor Space reported address rules as active, so the panel would show them as " +
			"enforcing rather than as disabled")
	}
	// Still validated, so a typo is reported the day it is typed rather than the
	// day the operator switches worlds.
	if len(bad) != 1 {
		t.Errorf("malformed entries went unreported in a Tor Space: %v", bad)
	}
	// Route rules key on host/path/method, which mean the same thing in both
	// worlds, so they must survive.
	onion2, _ := Compile(Config{Routes: []Route{{Name: "search", Prefix: "/search", Cost: 8}}, OnionMode: true})
	if got := onion2.CostOf("h", "/search", http.MethodGet); got != 8 {
		t.Errorf("route cost = %d in a Tor Space, want 8 — a search costs the same to serve "+
			"over an onion as over clearnet", got)
	}
}

// TestAllowCountriesIsExclusive — an allow list is a much sharper instrument
// than a deny list, and the two sit next to each other in the panel. Anything not
// on an allow list is refused, and that must be unambiguous in the code as well
// as in the copy.
func TestAllowCountriesIsExclusive(t *testing.T) {
	r, _ := Compile(Config{AllowCountries: []string{"GB", "de"}})
	if got := r.Country("GB"); got != VerdictNone {
		t.Errorf("an allowed country got %v, want none (proceed to the rest of the shield)", got)
	}
	if got := r.Country("DE"); got != VerdictNone {
		t.Errorf("case-insensitivity failed: %v", got)
	}
	if got := r.Country("FR"); got != VerdictDeny {
		t.Errorf("a country absent from a non-empty allow list got %v, want deny", got)
	}
	// With no allow list, absence means nothing.
	r2, _ := Compile(Config{DenyCountries: []string{"XX"}})
	if got := r2.Country("FR"); got != VerdictNone {
		t.Errorf("with only a deny list, an unlisted country got %v, want none", got)
	}
}

// TestRouteCostAccountsForWorkNotArrivals is the defect the route policy exists
// for. The L0 lane counted a 2 ms cached page and a 400 ms search as one slot
// each, so a flood aimed at the expensive route filled the lane at a fraction of
// the request rate the cheap one would need.
func TestRouteCostAccountsForWorkNotArrivals(t *testing.T) {
	r, bad := Compile(Config{Routes: []Route{
		{Name: "search", Prefix: "/search", Cost: 8},
		{Name: "feed", Prefix: "/feed", Cost: 3},
	}})
	if len(bad) != 0 {
		t.Fatalf("route parse failures: %v", bad)
	}
	if got := r.CostOf("johal.in", "/search", http.MethodGet); got != 8 {
		t.Errorf("search cost = %d, want 8", got)
	}
	if got := r.CostOf("johal.in", "/article/slug", http.MethodGet); got != 1 {
		t.Errorf("an unmatched route cost %d, want the default 1", got)
	}
	if got := r.RouteName("johal.in", "/feed/atom", http.MethodGet); got != "feed" {
		t.Errorf("route name = %q, want feed", got)
	}
}

// TestPrefixMatchesOnSegmentBoundaries — "/os" must match "/os/settings" and must
// NOT match "/oscar". Without this, an admin-scoped policy applies to any article
// whose slug happens to start with the right letters.
func TestPrefixMatchesOnSegmentBoundaries(t *testing.T) {
	r, _ := Compile(Config{Routes: []Route{{Name: "admin", Prefix: "/os", Cost: 4}}})

	for _, p := range []string{"/os", "/os/", "/os/settings", "/os/api/x"} {
		if got := r.RouteName("h", p, http.MethodGet); got != "admin" {
			t.Errorf("%q did not match the /os policy", p)
		}
	}
	for _, p := range []string{"/oscar", "/oscars/2026", "/o", "/article/os"} {
		if got := r.RouteName("h", p, http.MethodGet); got != "" {
			t.Errorf("%q matched the /os policy as %q — an admin policy is being applied to "+
				"ordinary content", p, got)
		}
	}
}

// TestHostAndMethodScoping — this is the in-app answer for a dedicated MCP or API
// host, which until now depended on the operator having run the right shell
// script on the right machine.
func TestHostAndMethodScoping(t *testing.T) {
	r, _ := Compile(Config{Routes: []Route{
		{Name: "mcp", Host: "mcp.johal.in", Cost: 2},
		{Name: "writes", Prefix: "/api", Methods: []string{"POST", "DELETE"}, Cost: 6},
	}})

	if got := r.RouteName("mcp.johal.in", "/anything", http.MethodGet); got != "mcp" {
		t.Errorf("the MCP host did not match: %q", got)
	}
	if got := r.RouteName("mcp.johal.in:443", "/anything", http.MethodGet); got != "mcp" {
		t.Error("a host carrying a port did not match — Host headers routinely include one")
	}
	if got := r.RouteName("johal.in", "/anything", http.MethodGet); got != "" {
		t.Errorf("a host-scoped rule leaked to another host: %q", got)
	}
	if got := r.RouteName("johal.in", "/api/posts", http.MethodPost); got != "writes" {
		t.Errorf("a method-scoped rule did not match POST: %q", got)
	}
	if got := r.RouteName("johal.in", "/api/posts", http.MethodGet); got != "" {
		t.Errorf("a POST/DELETE rule matched a GET: %q", got)
	}
}

// TestEmptyPolicyIsCheap — the overwhelming majority of installs configure none
// of this, and they must not pay for it on every request.
func TestEmptyPolicyIsCheap(t *testing.T) {
	var r Rules
	if !r.Empty() {
		t.Error("the zero value reported itself non-empty")
	}
	if r.Source("1.2.3.4") != VerdictNone || r.Country("GB") != VerdictNone {
		t.Error("the zero value produced a verdict")
	}
	if r.CostOf("h", "/p", http.MethodGet) != 1 {
		t.Error("the zero value did not cost the default 1 slot")
	}

	compiled, _ := Compile(Config{AllowCIDRs: []string{"10.0.0.0/8"}})
	if compiled.Empty() {
		t.Error("a configured policy reported itself empty")
	}
}

// TestIPv4MappedAddressesAreNormalised — a client can arrive as ::ffff:1.2.3.4
// depending on the listener and the proxy. If that did not match a 1.2.3.0/24
// deny rule, an operator's refusal would be bypassable by notation.
func TestIPv4MappedAddressesAreNormalised(t *testing.T) {
	r, _ := Compile(Config{DenyCIDRs: []string{"198.51.100.0/24"}})
	if got := r.Source("::ffff:198.51.100.9"); got != VerdictDeny {
		t.Errorf("an IPv4-mapped address got %v — an operator's deny rule is bypassable by "+
			"writing the address differently", got)
	}
}

// TestChallengeIsTheMiddleSettingCountryRulesNeeded.
//
// Before this there were two answers about a country: refuse it, or ignore it.
// An operator whose traffic from somewhere is mostly automated but not entirely
// had to pick between locking out the real readers and doing nothing. That is
// not a threshold question, it is a missing verdict.
func TestChallengeIsTheMiddleSettingCountryRulesNeeded(t *testing.T) {
	r, bad := Compile(Config{
		DenyCountries:      []string{"VN"},
		ChallengeCountries: []string{"CN", "ru", "TR"},
	})
	if len(bad) != 0 {
		t.Fatalf("parse failures: %v", bad)
	}
	if got := r.Country("CN"); got != VerdictChallenge {
		t.Errorf("a challenged country got %v, want challenge", got)
	}
	if got := r.Country("RU"); got != VerdictChallenge {
		t.Errorf("case-insensitivity failed: %v", got)
	}
	if got := r.Country("VN"); got != VerdictDeny {
		t.Errorf("a denied country got %v, want deny", got)
	}
	if got := r.Country("GB"); got != VerdictNone {
		t.Errorf("an unlisted country got %v, want none", got)
	}
}

// TestARefusalBeatsAChallengeForTheSameCountry — an operator who lists a country
// in both fields meant the refusal. It is the more specific intent, and it is the
// same precedence the address rules already use, so the two halves of this page
// cannot disagree about what "both" means.
func TestARefusalBeatsAChallengeForTheSameCountry(t *testing.T) {
	r, _ := Compile(Config{DenyCountries: []string{"CN"}, ChallengeCountries: []string{"CN"}})
	if got := r.Country("CN"); got != VerdictDeny {
		t.Errorf("a country both denied and challenged got %v, want deny", got)
	}
	// And an exclusive allow list still refuses everything off it, rather than
	// letting a challenge entry quietly re-admit a country the operator excluded.
	r2, _ := Compile(Config{AllowCountries: []string{"GB"}, ChallengeCountries: []string{"CN"}})
	if got := r2.Country("CN"); got != VerdictDeny {
		t.Errorf("a country off an exclusive allow list got %v — a challenge entry re-admitted "+
			"a country the operator had excluded", got)
	}
}

// TestChallengeCountriesAreInertInTorMode — two independent reasons, either
// enough on its own: a lookup there returns the SERVER's location for every
// visitor, and the plain-http onion exposes no window.crypto.subtle, so nobody
// could solve the check even if the geography meant something.
func TestChallengeCountriesAreInertInTorMode(t *testing.T) {
	onion, _ := Compile(Config{ChallengeCountries: []string{"CN"}, OnionMode: true})
	if got := onion.Country("CN"); got != VerdictNone {
		t.Errorf("a Tor Space returned %v — every visitor would be handed a check they have no "+
			"crypto API to solve, on the strength of the server's own location", got)
	}
	if !onion.Empty() {
		t.Error("a Tor Space compiled challenge countries into a non-empty rule set, so the " +
			"per-request path pays for a rule that can never fire")
	}
}
