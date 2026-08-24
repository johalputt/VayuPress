// SPDX-License-Identifier: Apache-2.0

// Package policy is the operator's own rules: which networks are always
// trusted, which are never served, which countries are refused, and how much a
// given route actually costs to serve.
//
// Everything else in the shield reaches a verdict by inference — a score, a
// sketch, a reputation. This package is where an operator states a fact. The two
// need different handling: a heuristic that is wrong about someone should cost
// them a solvable challenge, and a rule an operator wrote down should do exactly
// what it says.
//
// # The parts, and why each exists
//
//   - Allow/deny CIDRs. Every self-hoster has an office, a monitoring probe or a
//     CI runner that must never be challenged, and usually a network they have
//     decided not to serve. Neither is expressible today.
//   - Country verdicts. geoip.Country is already computed and wired — as a
//     RECORDING field only. It has never influenced a verdict, so an operator
//     who can see where abuse comes from cannot act on it.
//   - Route cost. The L0 sovereignty lane counts a 2 ms cached page and a 400 ms
//     search as one slot each, so a flood aimed at the expensive route fills the
//     lane at a fraction of the request rate a flood aimed at the cheap one
//     needs. Cost weights make the lane account for work rather than for
//     arrivals.
//
// # Parsing happens once
//
// Rules are compiled at load and read lock-free afterwards. An operator edits
// them rarely and every request reads them, so anything that parsed a CIDR per
// request would be a self-inflicted cost far larger than the rules save.
package policy

import (
	"net/netip"
	"strings"
)

// Verdict is what a policy says about a request.
type Verdict uint8

const (
	// VerdictNone — no operator rule matched. The rest of the shield decides.
	VerdictNone Verdict = iota
	// VerdictAllow — an operator-trusted source. Skips every gate, including the
	// jail: this is the escape hatch that gets an operator back into their own
	// site after a false-positive run.
	VerdictAllow
	// VerdictDeny — an operator-refused source. Refused outright.
	VerdictDeny
	// VerdictChallenge — serve a solvable proof-of-work first.
	//
	// The middle setting country rules needed. A deny is a statement that these
	// people are not customers; a challenge is a statement that this traffic is
	// mostly automated and a human should be able to prove otherwise in a moment.
	// Those are very different claims about a place, and having only the first
	// meant an operator who wanted the second had to refuse a country outright.
	//
	// It can only ever cost a visitor one puzzle. It never escalates to a block,
	// because a rule keyed on geography is wrong about individuals by
	// construction — a traveller, a VPN and a privacy network all resolve
	// somewhere other than where the person is.
	VerdictChallenge
)

// Rules is a compiled policy set. The zero value is a valid empty policy that
// matches nothing, so a caller that never configures one pays a nil check.
type Rules struct {
	allow []netip.Prefix
	deny  []netip.Prefix

	allowCountries       map[string]bool // when non-empty, ONLY these are served
	denyCountries        map[string]bool
	challengeCountries   map[string]bool
	refuseUnknownCountry bool // with an allow list, an unresolvable country is refused

	routes []Route

	// onionMode records that this is a Tor Space, where every peer is 127.0.0.1.
	// Both address rules and country rules are compiled away there — see
	// SourceActive and GeoActive for why each is not merely useless but actively
	// wrong. Route rules survive: they key on host, path and method, which mean
	// the same thing over an onion as over clearnet.
	onionMode bool
}

// Route is a named policy for a set of requests.
type Route struct {
	Name string
	// Host, when set, must match exactly (case-insensitive). This is the in-app
	// answer for a dedicated MCP or API host, which until now depended on an
	// operator having run the right shell script.
	Host string
	// Prefix is matched on a path-segment boundary, so "/os" matches "/os" and
	// "/os/settings" and never "/oscar".
	Prefix string
	// Methods, when non-empty, restricts the rule to those methods.
	Methods []string

	// Cost is how many sovereignty-lane slots one request on this route occupies.
	// 0 means the default of 1. A search that takes 400 ms and a cached page that
	// takes 2 ms are not one slot each, and treating them as such is what lets a
	// flood aimed at the expensive route fill the lane at a fraction of the rate
	// the cheap one would need.
	Cost int
}

// Config is the operator-facing form, before compilation.
type Config struct {
	AllowCIDRs     []string
	DenyCIDRs      []string
	AllowCountries []string
	DenyCountries  []string
	// ChallengeCountries are served a solvable proof of work rather than
	// refused. See VerdictChallenge.
	ChallengeCountries []string
	// RefuseUnknownCountry, when an AllowCountries list is active, refuses
	// visitors whose country could not be resolved at all instead of serving
	// them (audit: the unresolved case failed open). Off by default: an empty
	// answer usually means "no data for this address", and refusing every
	// address without table coverage would blank whole regions. An operator
	// running a strict geo fence opts into the sharper behaviour.
	RefuseUnknownCountry bool
	Routes               []Route
	// OnionMode compiles away both the address rules and the country rules,
	// leaving only the route rules. See Source and Country for why each is wrong
	// rather than merely useless where every peer is 127.0.0.1.
	OnionMode bool
}

// Compile turns operator text into a rule set, and reports the entries it could
// not parse.
//
// Bad entries are SKIPPED and named rather than failing the whole set. An
// operator with a typo in one line of ten should not lose the other nine — but
// they must be told, because a silently dropped allow entry means the source it
// was protecting starts being challenged with no visible cause.
func Compile(c Config) (Rules, []string) {
	var bad []string
	r := Rules{onionMode: c.OnionMode}

	parse := func(list []string, what string) []netip.Prefix {
		out := make([]netip.Prefix, 0, len(list))
		for _, s := range list {
			s = strings.TrimSpace(s)
			if s == "" || strings.HasPrefix(s, "#") {
				continue
			}
			// A bare address is a host route; operators write both.
			if !strings.Contains(s, "/") {
				if a, err := netip.ParseAddr(s); err == nil {
					out = append(out, netip.PrefixFrom(a, a.BitLen()))
					continue
				}
				bad = append(bad, what+": "+s)
				continue
			}
			p, err := netip.ParsePrefix(s)
			if err != nil {
				bad = append(bad, what+": "+s)
				continue
			}
			out = append(out, p.Masked())
		}
		return out
	}
	// Parsed even in a Tor Space, and then discarded. The parse is what tells an
	// operator their line is malformed; validating only the rules that happen to
	// be live would mean a typo sits unreported until the day they switch worlds.
	r.allow = parse(c.AllowCIDRs, "allow")
	r.deny = parse(c.DenyCIDRs, "deny")
	if c.OnionMode {
		r.allow, r.deny = nil, nil
	}

	set := func(list []string) map[string]bool {
		if len(list) == 0 {
			return nil
		}
		m := make(map[string]bool, len(list))
		for _, s := range list {
			if s = strings.ToUpper(strings.TrimSpace(s)); s != "" {
				m[s] = true
			}
		}
		return m
	}
	if !c.OnionMode {
		r.allowCountries = set(c.AllowCountries)
		r.denyCountries = set(c.DenyCountries)
		r.challengeCountries = set(c.ChallengeCountries)
		r.refuseUnknownCountry = c.RefuseUnknownCountry
	}

	for _, rt := range c.Routes {
		rt.Host = strings.ToLower(strings.TrimSpace(rt.Host))
		rt.Prefix = strings.TrimSpace(rt.Prefix)
		for i := range rt.Methods {
			rt.Methods[i] = strings.ToUpper(strings.TrimSpace(rt.Methods[i]))
		}
		if rt.Cost < 0 {
			bad = append(bad, "route "+rt.Name+": negative cost")
			continue
		}
		r.routes = append(r.routes, rt)
	}
	return r, bad
}

// Source returns the operator's verdict on a client address.
//
// Inert in a Tor Space, and the ALLOW side is why. There every peer is
// 127.0.0.1, so a deny list can only ever mean "refuse everyone" or "refuse
// nobody" — useless, but survivable. An allow list is not survivable: an entry
// as ordinary as 127.0.0.1, written for a health check or a monitoring probe on
// a clearnet install, would match EVERY visitor to the onion and hand each one
// the operator-trusted bypass past every gate in the shield. A control that
// silently turns the whole shield off in the other world is worse than one that
// does nothing there, so this does nothing there.
//
// DENY is checked first. If an operator has both allowed and denied a source —
// usually because a /24 they trust contains a /32 they do not — the refusal is
// the more specific intent, and the failure mode of getting it backwards (an
// attacker inside a trusted range bypassing every gate) is far worse than the
// failure mode of getting it right (a trusted host being refused, which is
// visible immediately).
func (r Rules) Source(ip string) Verdict {
	if len(r.allow) == 0 && len(r.deny) == 0 {
		return VerdictNone
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return VerdictNone
	}
	addr = addr.Unmap().WithZone("")
	for _, p := range r.deny {
		if p.Contains(addr) {
			return VerdictDeny
		}
	}
	for _, p := range r.allow {
		if p.Contains(addr) {
			return VerdictAllow
		}
	}
	return VerdictNone
}

// Country returns the operator's verdict on a country code.
//
// Inert in a Tor Space, and that is not an optimisation. There, every peer is
// 127.0.0.1, so a lookup returns the server's own location for every visitor —
// a country rule would either refuse everyone or serve everyone, while appearing
// to be doing geography.
//
// An allow list, when present, is exclusive: anything not on it is refused. That
// is a much sharper instrument than a deny list and it is stated here because
// the difference is easy to miss when both fields sit next to each other in a
// form.
func (r Rules) Country(code string) Verdict {
	if r.onionMode || code == "" {
		// An allow list that cannot see a country used to read as "no verdict,
		// serve" — a visitor who simply had no resolvable geography sailed past
		// the fence the operator built. With RefuseUnknownCountry the operator
		// chooses the sharp edge instead; without it the historical lenient
		// reading stands, because empty usually means no data, not evasion.
		if code == "" && !r.onionMode && r.refuseUnknownCountry && len(r.allowCountries) > 0 {
			return VerdictDeny
		}
		return VerdictNone
	}
	code = strings.ToUpper(code)
	// Refusals first, then the softer verdict. An operator who both denies a
	// country and lists it for a challenge meant the refusal — it is the more
	// specific intent, and the same precedence the address rules use.
	if r.denyCountries[code] {
		return VerdictDeny
	}
	if len(r.allowCountries) > 0 && !r.allowCountries[code] {
		return VerdictDeny
	}
	if r.challengeCountries[code] {
		return VerdictChallenge
	}
	return VerdictNone
}

// AllowsAny reports whether the allow list covers ANY address in key, where key
// may be a bare address or a CIDR prefix.
//
// It exists because the shield's enforcement key is a PREFIX for IPv6 (always)
// and for IPv4 under /24 grouping, so Source — which parses an address — could
// never match one and every prefix-keyed source read as untrusted. That was fine
// where the answer only decided whether to skip gates, and not fine at all where
// it decides whether a remote node may jail the operator's own network.
//
// Overlap, not containment, and deliberately so. If an operator trusts one
// address and something asks to jail the /64 around it, jailing that /64 takes
// the trusted host with it. The failure modes are not symmetric: refusing the
// jail costs a fleet-wide sentence that each node still reaches locally, while
// applying it locks an operator out of their own infrastructure.
func (r Rules) AllowsAny(key string) bool {
	if len(r.allow) == 0 || key == "" {
		return false
	}
	if !strings.Contains(key, "/") {
		return r.Source(key) == VerdictAllow
	}
	p, err := netip.ParsePrefix(key)
	if err != nil {
		return false
	}
	p = p.Masked()
	for _, a := range r.allow {
		if a.Overlaps(p) {
			return true
		}
	}
	return false
}

// GeoActive reports whether country rules can mean anything in this mode, so the
// panel can show them as disabled rather than as configured-but-ignored.
func (r Rules) GeoActive() bool { return !r.onionMode }

// SourceActive reports the same for the allow/deny address lists. The panel has
// to be able to say "these are off here" — an operator who can see their own
// rules listed and has no way to tell they are inert is an operator who believes
// a network is refused when it is being served.
func (r Rules) SourceActive() bool { return !r.onionMode }

// CostOf returns how many sovereignty-lane slots a request occupies. The first
// matching route wins, so an operator orders from most to least specific.
func (r Rules) CostOf(host, path, method string) int {
	for _, rt := range r.routes {
		if rt.matches(host, path, method) {
			if rt.Cost <= 0 {
				return 1
			}
			return rt.Cost
		}
	}
	return 1
}

// RouteName returns the name of the first matching route, for telemetry and the
// panel. Empty when nothing matched.
func (r Rules) RouteName(host, path, method string) string {
	for _, rt := range r.routes {
		if rt.matches(host, path, method) {
			return rt.Name
		}
	}
	return ""
}

func (rt Route) matches(host, path, method string) bool {
	if rt.Host != "" && !strings.EqualFold(stripPort(host), rt.Host) {
		return false
	}
	if len(rt.Methods) > 0 {
		ok := false
		for _, m := range rt.Methods {
			if strings.EqualFold(m, method) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if rt.Prefix == "" {
		return true
	}
	return pathHasPrefix(path, rt.Prefix)
}

// pathHasPrefix matches on a segment boundary. "/os" must match "/os" and
// "/os/settings" and must NOT match "/oscar" — a prefix rule that matched the
// latter would apply an admin policy to an article whose slug happened to start
// with the right letters.
func pathHasPrefix(path, prefix string) bool {
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" || prefix == "/" {
		return true
	}
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	return len(path) == len(prefix) || path[len(prefix)] == '/'
}

func stripPort(host string) string {
	if i := strings.LastIndexByte(host, ':'); i > 0 && !strings.Contains(host[i:], "]") {
		return host[:i]
	}
	return host
}

// Empty reports whether the rule set says nothing at all, so a caller can skip
// the checks entirely on the overwhelmingly common install that has configured
// none of this.
func (r Rules) Empty() bool {
	return len(r.allow) == 0 && len(r.deny) == 0 &&
		len(r.allowCountries) == 0 && len(r.denyCountries) == 0 &&
		len(r.challengeCountries) == 0 && len(r.routes) == 0
}
