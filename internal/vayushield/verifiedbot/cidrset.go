// SPDX-License-Identifier: Apache-2.0

package verifiedbot

import (
	"net/netip"
	"sort"
	"sync/atomic"
)

// cidrset is an immutable, longest-prefix-match set of network prefixes for one
// vendor (both IPv4 and IPv6). It is built once from a feed and swapped in
// atomically on refresh, so the hot-path Contains is lock-free.
//
// Membership is the only question asked of it, so it does not need a full radix
// trie: prefixes are bucketed by their masked-length and, within each family,
// checked with the stdlib netip.Prefix.Contains (well-tested, exact for v4+v6).
// The verdict cache in verifier.go fronts every lookup, so a Contains scan runs
// at most once per source IP per cache-TTL — never on the steady-state hot path
// — which keeps the structure simple and obviously correct without costing
// latency under load.
type cidrset struct {
	v4 []netip.Prefix
	v6 []netip.Prefix
}

func newCIDRSet(prefixes []netip.Prefix) *cidrset {
	s := &cidrset{}
	for _, p := range prefixes {
		if !p.IsValid() {
			continue
		}
		p = p.Masked()
		if p.Addr().Is4() {
			s.v4 = append(s.v4, p)
		} else {
			s.v6 = append(s.v6, p)
		}
	}
	// Sort most-specific first so the first Contains hit is the longest match.
	sort.Slice(s.v4, func(i, j int) bool { return s.v4[i].Bits() > s.v4[j].Bits() })
	sort.Slice(s.v6, func(i, j int) bool { return s.v6[i].Bits() > s.v6[j].Bits() })
	return s
}

// contains reports whether ip falls inside any prefix in the set.
func (s *cidrset) contains(ip netip.Addr) bool {
	if s == nil || !ip.IsValid() {
		return false
	}
	// Normalise a 4-in-6 (::ffff:a.b.c.d) address to plain IPv4 so it matches
	// the v4 prefix list.
	if ip.Is4In6() {
		ip = ip.Unmap()
	}
	list := s.v6
	if ip.Is4() {
		list = s.v4
	}
	for _, p := range list {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *cidrset) len() int {
	if s == nil {
		return 0
	}
	return len(s.v4) + len(s.v6)
}

// atomicCIDRSet holds a *cidrset that a background refresh swaps atomically.
type atomicCIDRSet struct {
	p atomic.Pointer[cidrset]
}

func (a *atomicCIDRSet) store(s *cidrset) { a.p.Store(s) }

func (a *atomicCIDRSet) load() *cidrset { return a.p.Load() }

// loaded reports whether a non-empty set has been installed (i.e. we have data
// to make a confirm/deny decision against).
func (a *atomicCIDRSet) loaded() bool {
	s := a.p.Load()
	return s != nil && s.len() > 0
}
