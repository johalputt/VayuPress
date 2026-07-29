// SPDX-License-Identifier: Apache-2.0

package intel

import (
	"context"
	"fmt"
	"math/rand"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/safefetch"
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
		base = append(base, netip.MustParsePrefix("203."+strconv.Itoa(i*2%256)+"."+strconv.Itoa(i/128)+".0/24"))
	}
	current, _ := Build(KindHostile, "feed", base)

	// A vendor adding a region: a few percent. Accepted.
	grown, _ := Build(KindHostile, "feed", append(append([]netip.Prefix{}, base...),
		netip.MustParsePrefix("205.0.0.0/16"), netip.MustParsePrefix("205.1.0.0/16")))
	if ok, why := AcceptRefresh(current, grown); !ok {
		t.Errorf("a small legitimate growth was refused: %s", why)
	}

	// The file replaced wholesale. Refused.
	if ok, _ := AcceptRefresh(current, mustSet(t, KindHostile, "feed", "203.0.113.0/24")); ok {
		t.Error("a feed that shrank to a single entry was accepted — that is precisely what a " +
			"hijacked or truncated endpoint looks like, and accepting it replaces a working " +
			"blocklist with whatever the attacker chose")
	}
	// Grown tenfold. Also refused.
	big := make([]netip.Prefix, 0, 2000)
	for i := 0; i < 2000; i++ {
		big = append(big, netip.MustParsePrefix("204."+strconv.Itoa(i%256)+"."+strconv.Itoa(i/256)+".0/24"))
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
		"203.0.113.0/24", "198.51.100.0/24", "192.0.2.0/24", "100.64.0.0/10")
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

// ── feeds ────────────────────────────────────────────────────────────────────

// TestEveryShippedFeedIsCoherent — a feed definition with a wrong kind or a
// missing parser is a feed that either does nothing or does the wrong thing, and
// both are silent.
func TestEveryShippedFeedIsCoherent(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range DefaultFeeds() {
		if f.ID == "" || seen[f.ID] {
			t.Errorf("feed %q has a missing or duplicate ID — the ID keys its cache file and its "+
				"opt-in setting, so a collision silently shares both", f.ID)
		}
		seen[f.ID] = true
		if !f.Kind.Valid() {
			t.Errorf("feed %q states no kind", f.ID)
		}
		if f.Parse == nil {
			t.Errorf("feed %q has no parser", f.ID)
		}
		if !strings.HasPrefix(f.URL, "https://") {
			t.Errorf("feed %q is not fetched over https (%q). Plain http means anyone on the "+
				"path chooses what this install treats as hostile", f.ID, f.URL)
		}
		if strings.TrimSpace(f.Note) == "" {
			t.Errorf("feed %q says nothing about its provenance. \"Trust this list\" is not a "+
				"thing to ask without naming whose list it is", f.ID)
		}
	}
}

// TestOnlyConservativeListsMayDeny — the datacenter tier is evidence and the
// hostile tier is grounds to refuse, so what lands in the second one is a much
// sharper decision. A community abuse aggregation in KindHostile would start
// denying real readers on a third party's false positive.
func TestOnlyConservativeListsMayDeny(t *testing.T) {
	for _, f := range DefaultFeeds() {
		if f.Kind != KindHostile {
			continue
		}
		if !strings.Contains(strings.ToLower(f.Note), "conservative") {
			t.Errorf("feed %q may DENY but its note does not state why it is conservative enough "+
				"to. Only lists that are conservative by construction belong in this tier", f.ID)
		}
	}
}

// TestAParserRefusesADocumentThatIsNotItsOwn.
//
// The failure this guards against is not a malformed line, it is an endpoint that
// starts serving something else — a login page, an error document, another
// vendor's list. A tolerant parser scanning for anything CIDR-shaped would keep
// "working" through that, which is the opposite of what is wanted from the one
// feed permitted to deny.
func TestAParserRefusesADocumentThatIsNotItsOwn(t *testing.T) {
	notOurs := [][]byte{
		[]byte("<html><body>Access denied</body></html>"),
		[]byte("{\"error\":\"forbidden\"}"),
		[]byte(""),
		[]byte("\n\n\n"),
		[]byte("; only comments\n; and nothing else\n"),
	}
	for _, b := range notOurs {
		if _, err := parseDROP(b); err == nil {
			t.Errorf("the DROP parser accepted %.40q — an endpoint serving something else would "+
				"silently replace the blocklist", b)
		}
	}
	// Its real shape still parses, comments and trailing SBL ids and all.
	good := []byte("; Spamhaus DROP List\n192.0.2.0/24 ; SBL123\n198.51.100.0/22 ; SBL456\n\n")
	got, err := parseDROP(good)
	if err != nil {
		t.Fatalf("the real format was rejected: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("parsed %d prefixes from the real format, want 2", len(got))
	}
}

// TestTheCloudParsersReadTheirOwnShapes — and refuse each other's, because a
// feed URL that silently started returning a different vendor's document would
// otherwise be applied without complaint.
func TestTheCloudParsersReadTheirOwnShapes(t *testing.T) {
	aws := []byte(`{"prefixes":[{"ip_prefix":"3.5.140.0/22"}],"ipv6_prefixes":[{"ipv6_prefix":"2600:1f00::/24"}]}`)
	gcp := []byte(`{"prefixes":[{"ipv4Prefix":"34.80.0.0/15"},{"ipv6Prefix":"2600:1900::/28"}]}`)

	if got, err := parseAWS(aws); err != nil || len(got) != 2 {
		t.Errorf("the AWS parser read its own format as %v (%v), want 2 prefixes", got, err)
	}
	if got, err := parseGCP(gcp); err != nil || len(got) != 2 {
		t.Errorf("the GCP parser read its own format as %v (%v), want 2 prefixes", got, err)
	}
	// Cross-fed, each yields nothing usable and must say so rather than return
	// an empty set that would be applied as "this vendor now owns no addresses".
	if _, err := parseAWS(gcp); err == nil {
		t.Error("the AWS parser accepted a GCP document and produced no prefixes without erroring")
	}
	if _, err := parseGCP(aws); err == nil {
		t.Error("the GCP parser accepted an AWS document and produced no prefixes without erroring")
	}
}

// TestAFeedIsOffUntilAnOperatorTurnsItOn — these are third-party lists with
// third-party terms, some restricting commercial use. Shipping one enabled would
// make that decision on the operator's behalf, and would also mean every install
// fetching from an endpoint nobody chose.
func TestAFeedIsOffUntilAnOperatorTurnsItOn(t *testing.T) {
	f := NewFetcher(t.TempDir())
	for _, def := range DefaultFeeds() {
		f.Add(def, false)
	}
	for _, st := range f.Statuses() {
		if st.Enabled {
			t.Errorf("feed %q registered as enabled", st.ID)
		}
		if st.Entries != 0 {
			t.Errorf("feed %q holds %d entries while disabled", st.ID, st.Entries)
		}
	}
	// And a disabled feed matches nothing, rather than merely being hidden.
	if k, _ := f.Match("3.5.140.1"); k.Valid() {
		t.Errorf("a disabled feed produced the verdict %v", k)
	}
}

// TestHostileBeatsDatacenter — a source in both answers two different questions,
// and the operator-relevant answer is the one that can refuse.
func TestHostileBeatsDatacenter(t *testing.T) {
	f := NewFetcher(t.TempDir())
	both := "192.0.2.0/24"
	f.Add(Feed{ID: "dc", Name: "dc", Kind: KindDatacenter, URL: "https://x", Note: "n",
		Parse: func([]byte) ([]netip.Prefix, error) { return nil, nil }}, true)
	f.Add(Feed{ID: "bad", Name: "bad", Kind: KindHostile, URL: "https://x", Note: "n",
		Parse: func([]byte) ([]netip.Prefix, error) { return nil, nil }}, true)

	f.mu.Lock()
	f.feeds["dc"].live.Store(mustSet(t, KindDatacenter, "dc", both))
	f.feeds["bad"].live.Store(mustSet(t, KindHostile, "bad", both))
	f.mu.Unlock()

	if k, src := f.Match("192.0.2.7"); k != KindHostile {
		t.Errorf("an address in both a datacenter and a hostile list resolved to %v (%s), want "+
			"hostile — the two answer different questions and only one of them can refuse", k, src)
	}
}

// TestATorSpaceMakesNoOutboundRequest — a feed refresh is a clearnet callback,
// and ADR-0141 forbids those there. The reason must also be RECORDED per feed,
// because a layer that silently makes no requests is indistinguishable from one
// that is broken.
func TestATorSpaceMakesNoOutboundRequest(t *testing.T) {
	safefetch.SetBlockClearnetEgress(true)
	t.Cleanup(func() { safefetch.SetBlockClearnetEgress(false) })

	f := NewFetcher(t.TempDir())
	f.Add(Feed{ID: "x", Name: "x", Kind: KindHostile, URL: "https://example.invalid/list",
		Note: "conservative", Parse: parseDROP}, true)
	f.Refresh(context.Background())

	st := f.Statuses()
	if len(st) != 1 {
		t.Fatalf("want one feed, got %d", len(st))
	}
	if st[0].LastError == "" {
		t.Error("a Tor Space refresh recorded no reason. A layer that silently does nothing " +
			"looks exactly like one that is working")
	}
	if !strings.Contains(strings.ToLower(st[0].LastError), "clearnet") {
		t.Errorf("the recorded reason does not name the cause: %q", st[0].LastError)
	}
}

// TestAHostileListCannotRefuseTheWholeInternet is the gap the delta bound could
// never see.
//
// AcceptRefresh compares entry COUNTS, so swapping one line of a thousand-line
// list for 0.0.0.0/0 changes the count by nothing, passes the 35% bound, and
// refuses every visitor to the site. Counting entries answers "was this list
// replaced"; it says nothing about what one entry now covers.
func TestAHostileListCannotRefuseTheWholeInternet(t *testing.T) {
	for _, cidr := range []string{"0.0.0.0/0", "0.0.0.0/4", "128.0.0.0/2", "2000::/3"} {
		if _, err := Build(KindHostile, "poisoned", []netip.Prefix{netip.MustParsePrefix(cidr)}); err == nil {
			t.Errorf("a hostile list containing %s must be refused outright", cidr)
		}
	}
	// The datacenter tier is exempt on purpose: cloud vendors publish very large
	// blocks legitimately, and the worst a wrong one does there is add to a score.
	if _, err := Build(KindDatacenter, "cloud", []netip.Prefix{netip.MustParsePrefix("0.0.0.0/4")}); err != nil {
		t.Errorf("a broad datacenter range is legitimate and must build: %v", err)
	}
}

// TestAHostileListCannotRefuseThisMachine — loopback and private space can never
// be a public visitor's source on a correct install, but they are exactly what a
// request carries when something in front of the app is misconfigured, and they
// are what EVERY request carries in a Tor Space. An entry here would not refuse
// an attacker; it would refuse the whole audience.
func TestAHostileListCannotRefuseThisMachine(t *testing.T) {
	for _, cidr := range []string{"127.0.0.1/32", "127.0.0.0/8", "10.0.0.0/8", "192.168.1.0/24",
		"169.254.0.0/16", "::1/128"} {
		if _, err := Build(KindHostile, "poisoned", []netip.Prefix{netip.MustParsePrefix(cidr)}); err == nil {
			t.Errorf("a hostile list containing %s must be refused — it can only match this "+
				"install's own traffic", cidr)
		}
	}
}

// TestOneBadEntryFailsTheWholeHostileList — salvaging the rest would apply an
// attacker's edit minus the part that made it obvious. A list wanting to refuse
// a quarter of the internet does not have one bad line; it is not the list it is
// supposed to be.
func TestOneBadEntryFailsTheWholeHostileList(t *testing.T) {
	good := make([]netip.Prefix, 0, 200)
	for i := 0; i < 200; i++ {
		good = append(good, netip.MustParsePrefix(fmt.Sprintf("203.0.%d.0/24", i%256)))
	}
	if _, err := Build(KindHostile, "ok", good); err != nil {
		t.Fatalf("the clean list must build: %v", err)
	}
	if _, err := Build(KindHostile, "poisoned", append(good, netip.MustParsePrefix("0.0.0.0/0"))); err == nil {
		t.Fatal("one poisoned entry among 200 good ones must fail the whole build")
	}
}

// TestTheSanityFloorSurvivesRealisticEntries — a bound that also rejects what
// these lists genuinely publish would make the layer unusable, which is its own
// kind of failure.
func TestTheSanityFloorSurvivesRealisticEntries(t *testing.T) {
	realistic := []netip.Prefix{
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("198.51.100.0/22"),
		netip.MustParsePrefix("192.0.2.0/23"),
		netip.MustParsePrefix("100.64.0.0/10"), // DROP does publish blocks this size
		netip.MustParsePrefix("2001:db8::/32"),
	}
	if _, err := Build(KindHostile, "drop-shaped", realistic); err != nil {
		t.Fatalf("entries of the shape a real hostile list publishes must build: %v", err)
	}
}

// BenchmarkMatchNoFeeds is the case nearly every install is in: the feature
// exists and nobody enabled it. It must cost approximately nothing, because it
// runs on every unverified request whether or not anyone opted in.
func BenchmarkMatchNoFeeds(b *testing.B) {
	f := NewFetcher(b.TempDir())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = f.Match("203.0.113.7")
	}
}

// BenchmarkMatchThreeFeeds is the shipped datacenter configuration — AWS, GCP
// and DigitalOcean, at roughly their real sizes. This is the number that decides
// whether enabling the feature is felt by a reader.
func BenchmarkMatchThreeFeeds(b *testing.B) {
	f := NewFetcher(b.TempDir())
	for n, size := range map[string]int{"aws": 10000, "gcp": 1000, "do": 1200} {
		prefixes := make([]netip.Prefix, 0, size)
		for i := 0; i < size; i++ {
			prefixes = append(prefixes, netip.MustParsePrefix(
				fmt.Sprintf("%d.%d.%d.0/24", 12+i/65536, (i/256)%256, i%256)))
		}
		set, err := Build(KindDatacenter, n, prefixes)
		if err != nil {
			b.Fatal(err)
		}
		def := Feed{ID: n, Name: n, Kind: KindDatacenter,
			Parse: func([]byte) ([]netip.Prefix, error) { return prefixes, nil }}
		f.Add(def, true)
		f.mu.Lock()
		f.feeds[n].live.Store(set)
		f.mu.Unlock()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = f.Match("203.0.113.7") // a miss: the worst case, every feed searched
	}
}
