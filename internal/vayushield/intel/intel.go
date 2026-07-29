// SPDX-License-Identifier: Apache-2.0

// Package intel holds third-party network intelligence: which addresses belong
// to datacenters, and which belong to networks somebody credible has published
// as hostile.
//
// # The rule that shapes everything here
//
// A feed can never produce an ALLOW.
//
// That is not a policy check somewhere in the call path — it is the type. Kind
// has exactly two values and neither of them grants anything. The reason is the
// threat model: the realistic compromise of a feed is not that it goes offline,
// it is that somebody edits what it serves. A hijacked feed that could add
// entries to an always-allow set would hand an attacker a bypass of every gate
// in the shield, silently, with no local misconfiguration to find. A hijacked
// feed that can only add suspicion or denial causes over-blocking, which is bad
// and visible and recoverable.
//
// Those two failures are not comparable, and the asymmetry is the whole design.
// The existing ADR declined managed threat intel partly because "a hijacked
// vendor endpoint could inject CIDRs into an always-allow fast path". That
// objection is correct, and this is the answer to it rather than a disagreement
// with it.
//
// # The other three defences, because "we verify the signature" is not available
//
// Most of these feeds are plain text over HTTPS with no signature to check. So
// the integrity story cannot be "we verify it", and pretending otherwise would be
// worse than admitting it:
//
//   - A refresh that changes the set by more than MaxDelta is REFUSED and the
//     last-good set is kept. An attacker who must inject slowly is an attacker
//     whose injection spans days of visible churn.
//   - Every fetch records a checksum, so "this feed changed a lot today" is
//     something an operator can see rather than infer.
//   - Every set is bounded in entries and every response in bytes, so a feed
//     cannot become a memory exhaustion.
//
// # Cost
//
// Lookups run on unverified requests, so the hot path is a binary search over
// merged, sorted ranges — never a linear scan of prefixes. Merging at compile
// time also removes the nesting problem: cloud vendors publish overlapping
// ranges routinely, and a naive "first match wins" list would answer correctly
// only by luck.
package intel

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"net/netip"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Kind is what a set is allowed to say about an address.
//
// There is deliberately no KindAllow. See the package doc: a feed that could
// grant access would turn a hijacked endpoint into a silent bypass of the entire
// shield, and no amount of care around the call sites is worth as much as the
// value simply not existing.
type Kind uint8

const (
	// kindUnset is the zero value and not a valid answer — a set built without
	// stating what it means is a set nobody decided the meaning of.
	kindUnset Kind = iota
	// KindDatacenter — the address belongs to hosting or cloud infrastructure.
	// Evidence, never a verdict: a VPN user and an office egress are both here,
	// so this raises a score and must never deny on its own.
	KindDatacenter
	// KindHostile — a network a credible publisher lists as hijacked, criminal
	// or otherwise not worth serving. Strong enough to deny, and only lists that
	// are conservative by construction belong in it.
	KindHostile
)

func (k Kind) String() string {
	switch k {
	case KindDatacenter:
		return "datacenter"
	case KindHostile:
		return "hostile"
	}
	return "unset"
}

// Valid reports whether a kind was actually chosen.
func (k Kind) Valid() bool { return k == KindDatacenter || k == KindHostile }

// Limits. Every one is enforced when a set is BUILT, so a caller cannot hand a
// bad set to the request path and rely on a check further down.
const (
	// MaxEntries per set. Every cloud vendor's published ranges together are a
	// few tens of thousands; this is well above that and far below anything that
	// makes a lookup table expensive.
	MaxEntries = 200_000
	// MaxDelta is how much of a set may change in one refresh before the refresh
	// is refused. A vendor adding a region moves a few percent; a feed replaced
	// wholesale does not.
	MaxDelta = 0.35
	// MinEntriesForDelta is the size below which the delta check is skipped. A
	// set of nine entries going to thirteen is a 44% change and means nothing,
	// and refusing it would make small feeds permanently unupdatable.
	MinEntriesForDelta = 50
)

// DatacenterDelta is everything a datacenter match is worth to the score, and
// the number is small on purpose.
//
// On the shipped 0.25 base it lands an otherwise-featureless client at 0.40 —
// the challenge threshold exactly, a puzzle and nothing more. A VPN user with a
// coherent browser User-Agent nets out BELOW it, because the "consistent browser
// User-Agent" credit applies at 0.40; a scraper announcing python-requests from
// the same address is already past a block on its User-Agent alone.
//
// That is the intended shape. This signal is about the network, and the question
// the shield is actually asking is about the visitor. It should be able to tip a
// decision, never make one.
const DatacenterDelta = 0.15

// The floor on how broad a HOSTILE entry may be.
//
// This closes a gap the delta bound cannot see. AcceptRefresh compares entry
// COUNTS, so swapping one entry of a thousand-line list for `0.0.0.0/0` changes
// the count by nothing at all, sails through the 35% bound, and refuses every
// visitor to the site from a file the operator does not control. Counting
// entries answers "was this list replaced"; it says nothing about what a single
// entry now covers.
//
// A /8 is already 1/256th of IPv4 — far broader than anything a conservative
// hijacked-netblock list publishes, and broad enough that an operator would want
// to look at it rather than have it applied silently. The datacenter tier is
// deliberately exempt: cloud vendors legitimately publish very large blocks, and
// the worst a wrong one does there is add 0.15 to a score.
const (
	minHostileBitsV4 = 8
	minHostileBitsV6 = 32
)

// rng is one merged address range, held as a comparable integer pair so the hot
// path is a comparison rather than a prefix operation.
type rng struct {
	lo, hi [2]uint64
}

func less(a, b [2]uint64) bool {
	if a[0] != b[0] {
		return a[0] < b[0]
	}
	return a[1] < b[1]
}

func lessOrEqual(a, b [2]uint64) bool { return !less(b, a) }

// key maps an address to a sortable 128-bit value. IPv4 is mapped into the v6
// space so one structure serves both without a branch per lookup.
func key(a netip.Addr) [2]uint64 {
	b := a.Unmap().As16()
	return [2]uint64{binary.BigEndian.Uint64(b[:8]), binary.BigEndian.Uint64(b[8:])}
}

// Set is an immutable, sorted collection of merged ranges.
//
// The zero value is a valid empty set that contains nothing, so a caller with no
// feed configured pays one length check.
type Set struct {
	kind   Kind
	ranges []rng
	// entries is how many prefixes the feed actually published, kept alongside
	// the merged range count because the two can differ enormously and the delta
	// check needs the former.
	//
	// Found by a test whose fixture used 200 adjacent /16s: they merge into ONE
	// range, so a delta computed from Len() saw a set of size 1, fell below the
	// small-feed floor, and waved through both a wholesale truncation and a
	// tenfold expansion. A feed's size is what it published, not what the lookup
	// structure compressed it to.
	entries int
	source  string
	fetched time.Time
	sum     string
}

// Kind, Source, Len, FetchedAt and Checksum describe the set for the panel and
// the posture report. An operator deciding whether to trust a feed needs to see
// how big it is and when it last changed, not just that it is "on".
func (s Set) Kind() Kind     { return s.kind }
func (s Set) Source() string { return s.source }
func (s Set) Len() int       { return len(s.ranges) }

// Entries is how many prefixes the feed published, before merging. This is what
// "the feed changed by 40%" is measured against — see the Set.entries comment.
func (s Set) Entries() int         { return s.entries }
func (s Set) FetchedAt() time.Time { return s.fetched }
func (s Set) Checksum() string     { return s.sum }

// Build compiles prefixes into a set, merging overlaps and rejecting anything
// that would make the result unsafe or unusable.
//
// Merging is not an optimisation. Cloud vendors publish nested and adjacent
// ranges as a matter of course, and a list that answered "is this inside?" by
// scanning for a first match would be correct only when the publisher happened
// to order things helpfully.
func Build(kind Kind, source string, prefixes []netip.Prefix) (Set, error) {
	if !kind.Valid() {
		return Set{}, errUnsetKind
	}
	if len(prefixes) > MaxEntries {
		return Set{}, errTooLarge
	}
	out := make([]rng, 0, len(prefixes))
	entries := 0
	h := sha256.New()
	for _, p := range prefixes {
		if !p.IsValid() {
			continue
		}
		if kind == KindHostile {
			if err := hostileEntryIsSane(p); err != nil {
				// Fails the WHOLE build rather than skipping the entry. A list that
				// wants to refuse loopback, or a quarter of the internet, is not a
				// list that has one bad line in it — it is a list that is not what it
				// is supposed to be, and salvaging the rest would apply an
				// attacker's edit minus the part that made it obvious.
				return Set{}, err
			}
		}
		entries++
		p = p.Masked()
		lo := key(p.Addr())
		hi := key(lastAddr(p))
		out = append(out, rng{lo: lo, hi: hi})
		h.Write([]byte(p.String()))
		h.Write([]byte{'\n'})
	}
	sort.Slice(out, func(i, j int) bool { return less(out[i].lo, out[j].lo) })

	// Merge overlapping and touching ranges so the search below can stop at the
	// first candidate instead of walking backwards through nested entries.
	merged := out[:0]
	for _, r := range out {
		if n := len(merged); n > 0 && lessOrEqual(r.lo, next(merged[n-1].hi)) {
			if less(merged[n-1].hi, r.hi) {
				merged[n-1].hi = r.hi
			}
			continue
		}
		merged = append(merged, r)
	}
	return Set{
		kind:    kind,
		ranges:  merged,
		entries: entries,
		source:  source,
		fetched: time.Now(),
		sum:     hex.EncodeToString(h.Sum(nil))[:16],
	}, nil
}

// Contains reports whether the address falls in the set. O(log n).
func (s Set) Contains(a netip.Addr) bool {
	if len(s.ranges) == 0 || !a.IsValid() {
		return false
	}
	k := key(a.Unmap().WithZone(""))
	// The first range whose start is greater than k; the candidate is the one
	// before it. Because ranges are merged and disjoint, no earlier range can
	// contain k.
	i := sort.Search(len(s.ranges), func(i int) bool { return less(k, s.ranges[i].lo) })
	if i == 0 {
		return false
	}
	r := s.ranges[i-1]
	return lessOrEqual(r.lo, k) && lessOrEqual(k, r.hi)
}

// ContainsString is Contains for a caller holding text. An unparseable address
// is not in any set — never treated as a match, because a parse failure must not
// become a verdict.
func (s Set) ContainsString(ip string) bool {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false
	}
	return s.Contains(a)
}

// AcceptRefresh reports whether a newly fetched set may replace the current one.
//
// This is the integrity control that exists BECAUSE most of these feeds carry no
// signature to verify. It cannot detect a careful attacker adding ten entries;
// it makes a wholesale swap — the thing a hijacked endpoint actually does —
// refuse itself and keep the last-good set. An attacker forced to move slowly is
// an attacker whose changes span days of churn an operator can see.
func AcceptRefresh(current, next Set) (bool, string) {
	if len(next.ranges) == 0 || next.Entries() == 0 {
		return false, "the feed returned no usable entries"
	}
	// Measured on PUBLISHED entries, never on merged ranges: adjacent prefixes
	// collapse, so a feed of 200 contiguous /16s compresses to a single range and
	// a delta computed from that would compare 1 against 1 and notice nothing.
	if current.Entries() < MinEntriesForDelta {
		return true, ""
	}
	delta := float64(next.Entries()-current.Entries()) / float64(current.Entries())
	if delta < 0 {
		delta = -delta
	}
	if delta > MaxDelta {
		return false, "the feed changed by " + pct(delta) + " in one refresh, over the " +
			pct(MaxDelta) + " bound — keeping the last-good set"
	}
	return true, ""
}

// Live is a swappable set for the request path.
type Live struct{ v atomic.Pointer[Set] }

// Store installs a set. Callers gate this on AcceptRefresh.
func (l *Live) Store(s Set) { l.v.Store(&s) }

// Get returns the live set. Never nil.
func (l *Live) Get() Set {
	if l == nil {
		return Set{}
	}
	if p := l.v.Load(); p != nil {
		return *p
	}
	return Set{}
}

// hostileEntryIsSane rejects an entry no conservative hijacked-netblock list
// would ever legitimately carry.
//
// Two separate refusals, and the second matters more than the first.
//
// Too BROAD: see minHostileBitsV4. The delta bound measures counts, so one
// swapped line covering a quarter of the internet is invisible to it.
//
// Special-use SPACE: loopback, private ranges, link-local. These can never be a
// public visitor's source address on a correctly configured install — but they
// are exactly what a request carries when something in front of the app is
// misconfigured, or when the install is a Tor Space where every peer is
// 127.0.0.1. An entry here would not refuse an attacker; it would refuse the
// whole audience, and on a Tor Space it would do so silently.
func hostileEntryIsSane(p netip.Prefix) error {
	min := minHostileBitsV4
	if p.Addr().Is6() && !p.Addr().Is4In6() {
		min = minHostileBitsV6
	}
	if p.Bits() < min {
		return errHostileTooBroad
	}
	a := p.Masked().Addr()
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() ||
		a.IsLinkLocalMulticast() || a.IsUnspecified() {
		return errHostileReserved
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func lastAddr(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr()
	bits := a.BitLen() - p.Bits()
	b := a.As16()
	// Set every host bit. The address is already in 16-byte form, so IPv4 works
	// without a separate path.
	for i := 0; i < bits; i++ {
		idx := 15 - i/8
		b[idx] |= 1 << (i % 8)
	}
	out := netip.AddrFrom16(b)
	if a.Is4() {
		return out.Unmap()
	}
	return out
}

func next(k [2]uint64) [2]uint64 {
	if k[1] != ^uint64(0) {
		return [2]uint64{k[0], k[1] + 1}
	}
	return [2]uint64{k[0] + 1, 0}
}

func pct(f float64) string {
	n := int(f*100 + 0.5)
	return itoa(n) + "%"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

type intelError string

func (e intelError) Error() string { return string(e) }

const (
	errUnsetKind       intelError = "intel: a set must state whether it is datacenter or hostile"
	errTooLarge        intelError = "intel: feed exceeds the entry limit"
	errHostileTooBroad intelError = "intel: a hostile list contains a prefix far broader than any " +
		"conservative list publishes — refusing the whole feed"
	errHostileReserved intelError = "intel: a hostile list contains loopback, private or link-local " +
		"space, which can only match this install's own traffic — refusing the whole feed"
)
