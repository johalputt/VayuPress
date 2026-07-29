// SPDX-License-Identifier: Apache-2.0

package intel

import (
	"math/rand"
	"net/netip"
	"strconv"
	"testing"
)

func mustPrefixes(t *testing.T, ss ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(ss))
	for _, s := range ss {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		out = append(out, p)
	}
	return out
}

// TestThereIsNoWayForAFeedToGrantAccess is the property the whole package is
// built around, and it is asserted structurally rather than by trying every call
// path.
//
// The realistic compromise of a feed is not that it goes offline — it is that
// somebody edits what it serves. A feed that could add entries to an always-allow
// set would hand an attacker a silent bypass of every gate in the shield, with no
// local misconfiguration for the operator to find. A feed that can only add
// suspicion or denial causes over-blocking: bad, visible, recoverable. Those two
// failures are not comparable, and that asymmetry is why KindAllow does not
// exist.
func TestThereIsNoWayForAFeedToGrantAccess(t *testing.T) {
	// Every value the type can hold, including ones nobody named.
	for i := 0; i < 256; i++ {
		k := Kind(i)
		if !k.Valid() {
			continue
		}
		if k != KindDatacenter && k != KindHostile {
			t.Errorf("Kind(%d) reports itself valid but is neither datacenter nor hostile — a "+
				"third meaning exists and nothing here says what it grants", i)
		}
	}
	// And the zero value must not be constructible into a set: a set built
	// without stating what it means is a set nobody decided the meaning of.
	if _, err := Build(kindUnset, "x", mustPrefixes(t, "203.0.113.0/24")); err == nil {
		t.Error("a set was built with no stated kind")
	}
}

// TestNestedAndAdjacentRangesAreMerged — cloud vendors publish overlapping and
// nested ranges routinely. A list that answered containment by scanning for a
// first match would be correct only when the publisher happened to order things
// helpfully, which is not a property anyone guarantees.
func TestNestedAndAdjacentRangesAreMerged(t *testing.T) {
	s, err := Build(KindDatacenter, "test", mustPrefixes(t,
		"10.0.0.0/8",     // contains the next two
		"10.1.0.0/16",    // nested
		"10.1.2.0/24",    // nested deeper
		"11.0.0.0/8",     // adjacent to 10/8 — merges into one range
		"203.0.113.0/24", // separate
	))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if s.Len() != 2 {
		t.Errorf("merged to %d ranges, want 2 (10.0.0.0-11.255.255.255 and 203.0.113.0/24)", s.Len())
	}
	for _, in := range []string{"10.0.0.1", "10.1.2.3", "10.255.255.255", "11.0.0.1", "203.0.113.7"} {
		if !s.ContainsString(in) {
			t.Errorf("%q should be in the set", in)
		}
	}
	for _, out := range []string{"9.255.255.255", "12.0.0.1", "203.0.114.1", "2001:db8::1"} {
		if s.ContainsString(out) {
			t.Errorf("%q should NOT be in the set", out)
		}
	}
}

// TestBoundariesAreInclusive — an off-by-one at a range edge is the classic way
// a blocklist misses the first or last address of a network, and it is invisible
// until exactly that address is the one that matters.
func TestBoundariesAreInclusive(t *testing.T) {
	s, _ := Build(KindHostile, "test", mustPrefixes(t, "198.51.100.0/24", "2001:db8::/32"))
	for _, in := range []string{
		"198.51.100.0", "198.51.100.255", // both v4 edges
		"2001:db8::", "2001:db8:ffff:ffff:ffff:ffff:ffff:ffff", // both v6 edges
	} {
		if !s.ContainsString(in) {
			t.Errorf("%q is an edge of a listed range and was not matched", in)
		}
	}
	for _, out := range []string{"198.51.99.255", "198.51.101.0", "2001:db7:ffff::1", "2001:db9::1"} {
		if s.ContainsString(out) {
			t.Errorf("%q is just outside a listed range and matched", out)
		}
	}
}

// TestIPv4AndIPv6DoNotCollide — both are mapped into one 128-bit space so a
// single structure serves both. Getting that wrong makes an IPv4 address match an
// IPv6 range, which is the sort of bug that only shows up as an inexplicable
// block.
func TestIPv4AndIPv6DoNotCollide(t *testing.T) {
	v4 := mustPrefixes(t, "203.0.113.0/24")
	s, _ := Build(KindHostile, "test", v4)
	// ::ffff:203.0.113.7 is the SAME host in v4-mapped form and must match.
	if !s.ContainsString("::ffff:203.0.113.7") {
		t.Error("the IPv4-mapped form of a listed address did not match, so whether a rule " +
			"applies would depend on the listener's socket family")
	}
	// A real v6 address that shares the low bits must not.
	if s.ContainsString("2001:db8::cb00:7107") {
		t.Error("an unrelated IPv6 address matched an IPv4 range")
	}
}

// TestAWholesaleSwapIsRefused is the integrity control that exists BECAUSE most
// of these feeds are plain text with no signature to verify. It cannot stop a
// careful attacker adding ten entries. It makes the thing a hijacked endpoint
// actually does — replace the file — refuse itself and keep the last-good set.
func TestAWholesaleSwapIsRefused(t *testing.T) {
	// Deliberately NON-adjacent, so the fixture exercises the delta rather than
	// the merger. An earlier version used contiguous /16s, which collapse to one
	// range — and that fixture is what revealed the delta was being computed from
	// merged ranges instead of published entries.
	base := make([]netip.Prefix, 0, 200)
	for i := 0; i < 200; i++ {
		base = append(base, netip.MustParsePrefix("10."+strconv.Itoa(i*2%256)+"."+strconv.Itoa(i/128)+".0/24"))
	}
	current, _ := Build(KindHostile, "feed", base)

	// A vendor adding a region: a few percent. Accepted.
	grown, _ := Build(KindHostile, "feed", append(append([]netip.Prefix{}, base...),
		netip.MustParsePrefix("11.0.0.0/16"), netip.MustParsePrefix("11.1.0.0/16")))
	if ok, why := AcceptRefresh(current, grown); !ok {
		t.Errorf("a small legitimate growth was refused: %s", why)
	}

	// The file replaced wholesale. Refused.
	if ok, _ := AcceptRefresh(current, mustSet(t, KindHostile, "feed", "0.0.0.0/1")); ok {
		t.Error("a feed that shrank to a single entry was accepted — that is precisely what a " +
			"hijacked or truncated endpoint looks like, and accepting it replaces a working " +
			"blocklist with whatever the attacker chose")
	}
	// Grown tenfold. Also refused.
	big := make([]netip.Prefix, 0, 2000)
	for i := 0; i < 2000; i++ {
		big = append(big, netip.MustParsePrefix("172."+strconv.Itoa(i%256)+"."+strconv.Itoa(i/256)+".0/24"))
	}
	blown, _ := Build(KindHostile, "feed", big)
	if ok, _ := AcceptRefresh(current, blown); ok {
		t.Error("a feed that grew tenfold in one refresh was accepted")
	}

	// An empty response is never a valid update — a 200 with no body is the
	// commonest broken-endpoint shape and would silently disable the feed.
	if ok, _ := AcceptRefresh(current, Set{}); ok {
		t.Error("an empty feed was accepted, which silently disables the protection")
	}
}

// TestASmallFeedStaysUpdatable — the delta bound must not make a nine-entry list
// permanently frozen. Below the floor a percentage is noise, and applying it
// would break the feeds most likely to be hand-curated.
func TestASmallFeedStaysUpdatable(t *testing.T) {
	current := mustSet(t, KindHostile, "small", "203.0.113.0/24", "198.51.100.0/24")
	next := mustSet(t, KindHostile, "small",
		"203.0.113.0/24", "198.51.100.0/24", "192.0.2.0/24", "10.0.0.0/8")
	if ok, why := AcceptRefresh(current, next); !ok {
		t.Errorf("a small curated list could not be updated: %s", why)
	}
}

// TestTheZeroSetIsSafe — the overwhelming majority of installs configure no
// feeds, and they must pay a length check rather than a crash.
func TestTheZeroSetIsSafe(t *testing.T) {
	var s Set
	if s.ContainsString("203.0.113.1") || s.Len() != 0 || s.Kind().Valid() {
		t.Error("the zero set is not inert")
	}
	var l *Live
	if l.Get().ContainsString("203.0.113.1") {
		t.Error("a nil Live matched an address")
	}
	var live Live
	if live.Get().Len() != 0 {
		t.Error("an unset Live returned a non-empty set")
	}
}

// TestLookupAgreesWithLinearScan is the correctness backstop for the binary
// search. The fast path is the only one that runs in production, so it is worth
// checking against the obvious implementation on random input rather than on the
// cases I happened to imagine.
func TestLookupAgreesWithLinearScan(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	var prefixes []netip.Prefix
	for i := 0; i < 300; i++ {
		a := netip.AddrFrom4([4]byte{byte(rnd.Intn(256)), byte(rnd.Intn(256)), byte(rnd.Intn(256)), 0})
		bits := 16 + rnd.Intn(9)
		p, err := a.Prefix(bits)
		if err != nil {
			continue
		}
		prefixes = append(prefixes, p)
	}
	s, err := Build(KindDatacenter, "rand", prefixes)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for i := 0; i < 20_000; i++ {
		a := netip.AddrFrom4([4]byte{byte(rnd.Intn(256)), byte(rnd.Intn(256)), byte(rnd.Intn(256)), byte(rnd.Intn(256))})
		want := false
		for _, p := range prefixes {
			if p.Contains(a) {
				want = true
				break
			}
		}
		if got := s.Contains(a); got != want {
			t.Fatalf("%v: merged-range lookup says %v, linear scan over the source prefixes says "+
				"%v — the structure the request path uses disagrees with the data it was built "+
				"from", a, got, want)
		}
	}
}

// TestTheEntryLimitIsEnforcedAtBuild — a cap checked at the call site is a cap
// somebody forgets at the second call site.
func TestTheEntryLimitIsEnforcedAtBuild(t *testing.T) {
	huge := make([]netip.Prefix, MaxEntries+1)
	for i := range huge {
		huge[i] = netip.MustParsePrefix("10.0.0.0/32")
	}
	if _, err := Build(KindHostile, "huge", huge); err == nil {
		t.Errorf("a set of %d entries was built, over the %d cap", len(huge), MaxEntries)
	}
}

func mustSet(t *testing.T, k Kind, src string, ss ...string) Set {
	t.Helper()
	s, err := Build(k, src, mustPrefixes(t, ss...))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return s
}

func BenchmarkContains(b *testing.B) {
	var prefixes []netip.Prefix
	for i := 0; i < 20_000; i++ {
		prefixes = append(prefixes, netip.MustParsePrefix(
			strconv.Itoa(i%223+1)+"."+strconv.Itoa(i/223%256)+".0.0/16"))
	}
	s, _ := Build(KindDatacenter, "bench", prefixes)
	a := netip.MustParseAddr("103.44.7.9")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Contains(a)
	}
}
