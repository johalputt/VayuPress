// SPDX-License-Identifier: Apache-2.0

// Package shieldaudit produces an honest, verifiable report of a VayuPress
// install's VayuShield posture: which defences are actually enforcing, which are
// switched on but inert, and — deliberately — what this product cannot do for
// the operator at all.
//
// It is modelled on internal/anonaudit and inherits its discipline. anonaudit
// refuses to claim "100% anonymous" because a false guarantee puts a person at
// real risk. The equivalent here is volumetric absorption: no single-origin
// product can provide it, and an operator who believes otherwise will find out
// during an attack rather than before one. So this report carries a permanent
// Fail row saying so, which no configuration can turn green.
//
// The reason it exists at all is a defect this codebase actually shipped. Tier 3
// declared its rate-limit zones and left every `limit_req` commented out, so the
// panel read "Active" while the layer enforced nothing — and there was no way for
// an operator, or for CI, to notice. A posture report that is computed from
// observed enforcement rather than from the toggle the operator flipped is what
// makes that class of failure visible.
//
// # The privilege boundary
//
// The app runs unprivileged (ADR-0123). It cannot run `nft list table`, which
// needs CAP_NET_ADMIN, and has no business reading /etc/nginx. So the inputs come
// from two places and are kept distinct on purpose:
//
//   - What the app knows about ITSELF — which address it bound, which gates are
//     enabled, whether real-client-IP resolution worked. This is introspection,
//     never a probe: the app must not call itself over the network to find out,
//     because in onion mode that would be the very clearnet callback the whole
//     Tor Space design forbids.
//   - What the root reconcile agent reports — the enforcement digest. The agent
//     has the privilege to look at the kernel and at nginx; the app only reads
//     the fixed-schema file it writes.
//
// Absent evidence is never a pass. If the agent is not installed, or its digest
// is stale, the tier rows say exactly that instead of reporting the operator's
// intent back to them as if it were fact.
package shieldaudit

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Status is a single control's verdict.
type Status int

const (
	// Pass — the control is active and enforcing.
	Pass Status = iota
	// Warn — enabled but not doing what the operator likely expects, or a
	// residual risk worth understanding.
	Warn
	// Info — context or an inherent limit, not a pass/fail.
	Info
	// Fail — a control that should be enforcing is not, or a claim the product
	// cannot honour.
	Fail
)

func (s Status) String() string {
	switch s {
	case Pass:
		return "pass"
	case Warn:
		return "warn"
	case Info:
		return "info"
	case Fail:
		return "fail"
	}
	return "unknown"
}

// Check is one line of the report.
type Check struct {
	Title  string
	Status Status
	Detail string
}

// Digest is the enforcement evidence the root agent observes and writes to the
// control directory. Every field is tri-state on purpose: the zero value means
// "the agent did not report this", which must never be read as "no".
//
// This is a new agent->app data direction. The app trusts it about the kernel
// and nginx because only the agent can see those; it never lets the digest widen
// what the app itself does.
type Digest struct {
	// Present is whether a digest was found and parsed at all.
	Present bool
	// Age is how long ago the agent generated it. A stale digest describes a
	// machine that may have changed underneath it.
	Age time.Duration

	Tier2TablePresent Tri // the nftables table is loaded
	Tier2MetersV4     Tri // per-source meters exist for IPv4
	Tier2MetersV6     Tri // ...and for IPv6, which is the half that was missing
	ConntrackSized    Tri // nf_conntrack_max read back at the configured value

	Tier3Installed Tri // the conf file is in place
	Tier3Enforcing Tri // ...and actually contains active limit_req/limit_conn
	DefaultServer  Tri // a 443 default_server exists, so an unknown Host is not
	// served by the first vhost that happens to match
	MCPVhostRestricted Tri
	// MCPVhostOpenAt is the file:line of the catch-all the agent objected to,
	// empty when it objected to nothing.
	//
	// A verdict that cannot say what produced it cannot be checked by the one
	// person who can see the config. Two releases went by with an operator
	// re-pressing a button and re-reading the same sentence while the panel and
	// the report disagreed, because neither could point at anything.
	MCPVhostOpenAt string
	// RealIPHeader is the real_ip_header actually in effect at http level, as the
	// agent read it out of `nginx -T`. Empty when none is set.
	//
	// The app cannot obtain this: it is unprivileged and has no business reading
	// /etc/nginx. Without it the real-IP row can only report that resolution did
	// not happen, which leaves the operator — and anyone helping them — guessing
	// between a wrong header, a missing range list, and a reload that never
	// happened. That guessing cost three releases.
	RealIPHeader string
	// RealIPRanges is how many set_real_ip_from directives the running config
	// carries. Zero with a header set means the directive has nothing to trust,
	// which fails exactly as silently as the wrong header does.
	RealIPRanges int
}

// CDNSentHeaders are the header names a CDN actually sets to carry the visitor's
// address. A real_ip_header naming anything else — X-Real-IP above all, because
// it is nginx's own default and therefore the likeliest thing already written in
// a config — leaves nginx with nothing to read and resolves nobody.
var CDNSentHeaders = []string{"CF-Connecting-IP", "True-Client-IP", "X-Forwarded-For"}

// headerCanResolve reports whether h is one a CDN sends.
func headerCanResolve(h string) bool {
	for _, c := range CDNSentHeaders {
		if strings.EqualFold(h, c) {
			return true
		}
	}
	return false
}

// Tri is a three-valued flag: the agent said yes, the agent said no, or the
// agent did not say. Collapsing "unknown" into "no" would turn a missing agent
// into a wall of red; collapsing it into "yes" would turn it into a wall of
// green. Both are lies, and the second is the dangerous one.
type Tri uint8

const (
	Unknown Tri = iota
	Yes
	No
)

// ParseTri maps the digest's fixed vocabulary. Anything else is Unknown — the
// digest is written by a root process and read by an unprivileged one, so an
// unrecognised value is treated as no information rather than guessed at.
func ParseTri(s string) Tri {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "yes", "true", "1", "present", "active", "enforcing":
		return Yes
	case "no", "false", "0", "absent", "inactive":
		return No
	}
	return Unknown
}

// Inputs is the live state the report is computed from — passed in so the report
// is pure and testable, exactly as anonaudit.Inputs is.
type Inputs struct {
	// --- What the operator asked for -----------------------------------------

	// Tier2Wanted / Tier3Wanted are the operator's intent flags.
	Tier2Wanted, Tier3Wanted bool
	// Tier2State / Tier3State are what the agent last reported ("active",
	// "inactive", "error", "" when there is no agent).
	Tier2State, Tier3State string
	// AgentAlive is whether the root reconcile agent's heartbeat is fresh.
	AgentAlive bool

	// --- What the app knows about itself -------------------------------------

	// BindAddr is the address the HTTP listener actually bound. Introspection,
	// not a probe: the app must never call itself over the network to find out.
	BindAddr string
	// OnionMode marks a Tor Space install.
	OnionMode bool
	// RateLimit, LoadShed, AutoBlock, Surge are the in-binary gates.
	RateLimit, LoadShed, AutoBlock, Surge bool
	// ObserveOnly is the measure-but-do-not-enforce mode.
	ObserveOnly bool

	// SweepActive reports that the population-level corpus-sweep detector is
	// currently firing; SweepBaseline is the healthy assets-per-document ratio
	// this install has demonstrated. Both are needed to say anything honest:
	// "not sweeping" on an install whose baseline never reached the arming
	// threshold means the detector is dormant, not that the site is clean.
	SweepActive   bool
	SweepBaseline float64
	// TorInertGates names the enforcement rules that deliberately do not act in a
	// Tor Space. Empty on a clearnet install.
	TorInertGates []string
	// CaptureWired is whether TLS ClientHello capture is available, without which
	// classification runs on HTTP signals alone.
	CaptureWired bool
	// BehindCDN is the operator's "this site is proxied" switch.
	BehindCDN bool
	// ClientIPResolved reports whether real-client-IP resolution produced a
	// distinct visitor address on the request that generated this report. When a
	// site is proxied and this is false, every visitor is being counted as the
	// edge — the failure that makes per-IP limits meaningless.
	ClientIPResolved bool
	// ClientIPFromVisitorTraffic marks that the answer above came from recent
	// VISITOR traffic rather than from the request being reported on — which
	// happens when the operator reaches their own console without going through
	// the proxy. The row says so, because "verified from your readers' traffic"
	// and "verified from your own request" are different strengths of evidence
	// and the report's whole value is not overstating either.
	ClientIPFromVisitorTraffic bool
	// CountryEdgeAnswers / CountryTableAnswers / CountryDisagreements record which
	// source answered "what country is this visitor in", and how often the two
	// available sources named DIFFERENT countries for the same request.
	//
	// The disagreement was invisible, and being invisible is what made it
	// expensive: a live install refused a country, the enforcement side resolved
	// addresses through the compiled-in table and never matched it, and Analytics
	// read the edge's per-request header and reported that country as 91% of the
	// audience. Both sides were working. Nothing could show they were answering
	// different questions.
	CountryEdgeAnswers   int64
	CountryTableAnswers  int64
	CountryDisagreements int64
	// GeoRulesSet reports that the operator has stated at least one rule keyed on
	// the visitor's country. It changes what the real-IP failure MEANS: without
	// geo rules an unresolved visitor address is a rate-limiting problem, and
	// with them it is also a control the panel shows as set and never applies.
	GeoRulesSet bool
	// LinkSpeedMbps is the operator's measured ingress capacity, read from
	// /sys/class/net/*/speed. Zero when it could not be determined.
	LinkSpeedMbps int
	// InspectRules and InspectRuleset describe the compiled-in request-inspection
	// ruleset. They are reported so an operator can tell which rules their build
	// carries — the ruleset is never fetched, so "latest" means nothing and the
	// binary's own number is the only honest answer.
	InspectRules, InspectRuleset int
	// ClusterPeers is how many OTHER nodes this one shares verdicts with. Used to
	// state the aggregate ingress and, in the same breath, its ceiling.
	ClusterPeers int
	// ClusterVerdictsIn and ClusterRefused are what the applier actually did with
	// inbound verdicts, so "clustering is on" is backed by evidence rather than
	// by the toggle the operator flipped — the same discipline as the tier rows.
	ClusterVerdictsIn, ClusterRefused int64
	// IntelFeeds is the live state of every third-party network-intelligence feed
	// the operator has switched on. Empty on the installs that enabled none,
	// which is the default.
	IntelFeeds []IntelFeed

	// --- What the agent observed ---------------------------------------------

	Digest Digest
	// DigestMaxAge is how old a digest may be before it is reported as stale.
	// Zero uses the default.
	DigestMaxAge time.Duration
}

// IntelFeed is one enabled third-party feed, as the report needs to see it.
//
// Entries is the count the publisher actually served, which is the number that
// separates "on" from "working" — a feed that is switched on and holds nothing
// looks identical to a healthy one in every other indicator.
type IntelFeed struct {
	Name    string
	Hostile bool
	Entries int
	// Refused and LastError are kept apart because they are different events. A
	// transport error is the publisher being unreachable; a refusal is the
	// publisher ANSWERING with something unlike what they served yesterday, which
	// is the one thing the delta bound exists to catch.
	Refused, LastError string
}

const defaultDigestMaxAge = 10 * time.Minute

// Run computes the posture report.
func Run(in Inputs) []Check {
	maxAge := in.DigestMaxAge
	if maxAge <= 0 {
		maxAge = defaultDigestMaxAge
	}
	stale := in.Digest.Present && in.Digest.Age > maxAge

	var checks []Check
	add := func(title string, st Status, detail string) {
		checks = append(checks, Check{Title: title, Status: st, Detail: detail})
	}

	// --- Observe-only mode ---------------------------------------------------
	//
	// First, and unconditionally Fail. It is a legitimate and useful mode, but a
	// site running it is a site with nothing enforcing, and the one way that goes
	// wrong is being left on and forgotten. A row an operator has to scroll past
	// on every visit is the cheapest possible protection against that.
	if in.ObserveOnly {
		add("Observe-only mode is ENGAGED", Fail,
			"Every gate is counting what it would have done and letting the request through. Nothing is being blocked, rate-limited, challenged or banned in the kernel. This is the right way to trial a threshold change — and the wrong state to leave a site in.")
	}

	// --- Tier 1: the in-binary engine ----------------------------------------

	switch {
	case in.RateLimit && in.LoadShed:
		add("Tier 1 · in-binary gates", Pass,
			"Rate limiting and load shedding are both on. These are the only layers that see an HTTP request, so they are the only ones that can tell a reader from a scraper.")
	case in.LoadShed && !in.RateLimit:
		// The shipped default, so the row explains the gap and how to close it
		// safely rather than reading as a misconfiguration.
		add("Tier 1 · in-binary gates", Warn,
			"Load shedding is on, so the process cannot be driven to collapse — but nothing bounds what a SINGLE source can ask for. Rate limiting is the gate for that, and it is off by default because it keys on the client address: on a proxied origin without \"Behind a CDN\" set, every visitor resolves to a few edge addresses and one shared bucket. Check the real-visitor-IP row below, then trial rate limiting in observe-only mode before enforcing it.")
	case in.RateLimit && !in.LoadShed:
		add("Tier 1 · in-binary gates", Warn,
			"Rate limiting is on but load shedding is off. Rate limiting bounds one source at a time; load shedding is what stops many sources at once from driving the process into collapse. They cover different failures.")
	default:
		add("Tier 1 · in-binary gates", Fail,
			"Rate limiting and load shedding are both off. Nothing bounds how much work a single visitor can ask this process to do.")
	}

	switch {
	case in.SweepActive:
		add("Corpus-sweep detection", Warn,
			"A sweep is in progress: this install's assets-per-document ratio has collapsed against the "+
				"healthy ratio it normally runs at, which is what a crawl taking a page or two from each of "+
				"many addresses looks like from above. While it holds, a client fetching documents and none "+
				"of their sub-resources is scored after three requests instead of eight, so a crawler that "+
				"stays under every per-client threshold can be challenged at all. Nothing is blocked on this "+
				"signal: it changes how soon a client is judged, never the verdict, and the result is a "+
				"solvable puzzle.")
	case in.SweepBaseline >= 1.0:
		add("Corpus-sweep detection", Pass,
			fmt.Sprintf("Armed and quiet. This origin is seeing %.1f sub-resources per document, so a "+
				"collapse in that ratio would be visible as the population-level signature of a distributed "+
				"crawl.", in.SweepBaseline))
	default:
		add("Corpus-sweep detection", Info,
			fmt.Sprintf("Dormant, by design. This origin sees only %.2f sub-resources per document — "+
				"typically because the edge answers for stylesheets, scripts and images without consulting "+
				"the app — so there is no healthy ratio to detect a collapse against. The detector stays off "+
				"rather than treating every reader here as a crawler. It arms itself if the origin starts "+
				"serving its own assets; no setting turns it on.", in.SweepBaseline))
	}

	if in.AutoBlock {
		add("Reputation jail feeds the kernel", Pass,
			"Auto-block is on, so the shield's own verdicts reach the kernel offload and a confirmed bad actor is dropped before a connection exists.")
	} else {
		add("Reputation jail feeds the kernel", Warn,
			"Auto-block is off. The kernel offload table is created and stays empty: nothing is ever added to it, whatever Tier 2 reports. Turning Tier 2 on does not change this.")
	}

	if in.CaptureWired {
		add("TLS fingerprint capture", Pass, "ClientHello capture is wired, so classification uses TLS signals and not just HTTP headers — which an attacker controls entirely.")
	} else {
		add("TLS fingerprint capture", Info, "No ClientHello capture. Classification runs on HTTP signals alone, which are cheaper for an attacker to imitate.")
	}

	// --- The real-IP failure, which silently disables every per-IP limit -----

	switch {
	case in.BehindCDN && !in.ClientIPResolved:
		detail := "This site is marked as proxied, but the request that generated this report did not resolve to a visitor address distinct from the peer. Every per-IP limit is therefore measuring the edge, not the reader: the whole audience shares one bucket, and the first busy minute shows everyone a challenge."
		// The country rules deserve their own sentence, because their failure is
		// silent in a way the rate limiter's is not. A shared bucket announces
		// itself — readers get challenged and the operator hears about it. A
		// country rule that cannot see the visitor simply never matches: the panel
		// lists it, the shield reports itself as protecting, and the traffic the
		// operator refused keeps arriving with nothing anywhere saying why.
		if in.GeoRulesSet {
			detail += " Your country rules are not being applied to anyone arriving through the proxy either — the lookup is resolving the edge's location, never the reader's, so a \"never serve\" country is still being served. Traffic from a country you have refused will keep appearing in Analytics, which reads the country from the proxy's own header and is therefore right while the rule is not."
		}
		// And WHY, from what the agent read out of the running config. A row that
		// can only say "it did not resolve" sends the operator round the loop of
		// pressing the remediation again; these three sentences name the line to
		// change. Each is stated only when the agent actually observed it, because
		// a confident wrong cause is worse than none.
		switch {
		case !in.Digest.Present:
			// Nothing observed; say nothing rather than guess.
		case in.Digest.RealIPHeader == "":
			detail += " The running nginx config sets no real_ip_header at all, so nginx never looks for a forwarded address. Press \"Resolve the real visitor address\" in Hardening below."
		case !headerCanResolve(in.Digest.RealIPHeader):
			detail += " The cause is in the running config: real_ip_header is \"" + in.Digest.RealIPHeader +
				"\", which no CDN sends — nginx therefore finds nothing to read and every visitor keeps the edge's address. Change it to CF-Connecting-IP, or delete that line and press \"Resolve the real visitor address\" so the helper writes it."
		case in.Digest.RealIPRanges == 0:
			detail += " The cause is in the running config: real_ip_header is \"" + in.Digest.RealIPHeader +
				"\" but no set_real_ip_from range is configured, so nginx trusts nobody to declare an address and ignores the header entirely. Press \"Allowlist your proxy's edge ranges\", then \"Resolve the real visitor address\"."
		default:
			detail += " The running config looks right — real_ip_header is \"" + in.Digest.RealIPHeader +
				"\" over " + itoa(in.Digest.RealIPRanges) + " trusted range(s) — so the addresses reaching this origin are not coming from the ranges listed. That happens when the proxy connects over an address family the list does not cover."
		}
		add("Real visitor IP", Fail, detail)
	case in.BehindCDN && in.ClientIPFromVisitorTraffic:
		add("Real visitor IP", Pass,
			"Recent visitor traffic arrives through the proxy and resolves to distinct reader addresses, so per-IP limits apply per reader. Your OWN connection skips the proxy — usually a hosts entry so the console stays reachable when the edge is unwell — so this row is verified from your readers' requests rather than from this one.")
	case in.BehindCDN:
		add("Real visitor IP", Pass, "The proxy's forwarding header resolved to a distinct visitor address, so per-IP limits apply per reader.")
	case in.ClientIPResolved:
		add("Real visitor IP", Warn,
			"A forwarding header was honoured, but this site is not marked as proxied. Check that the peer really is a trusted proxy — otherwise a visitor can choose the address they are limited under.")
	default:
		add("Real visitor IP", Pass, "Traffic arrives directly, so the connection's peer address is the visitor.")
	}

	// --- Where the country verdict comes from --------------------------------

	switch {
	case in.CountryEdgeAnswers+in.CountryTableAnswers == 0:
		// Nothing has been classified yet; say nothing rather than invent a state.
	case in.CountryDisagreements > 0:
		add("Country source", Warn,
			"Your CDN and the country table compiled into this build disagree about where some "+
				"visitors are: "+itoa64(in.CountryDisagreements)+" request(s) so far. The CDN's answer "+
				"is used, because it is computed per request from live data and it is the number you "+
				"are looking at in Analytics when you write a country rule — so the rule and the "+
				"report now act on the same fact. A rising count means the embedded table is behind "+
				"the edge for some address space; nothing is broken by it, and an update refreshes "+
				"the table.")
	case in.CountryEdgeAnswers > 0:
		add("Country source", Pass,
			"Countries come from your CDN's per-request header, verified to have arrived from a "+
				"trusted peer. Analytics and every country rule read that same value, so what you "+
				"see and what is enforced cannot drift apart.")
	default:
		add("Country source", Info,
			"No CDN is stating a country for these requests, so countries are resolved from the "+
				"visitor's address using the table compiled into this build. That is the right source "+
				"for a direct-served origin. It is a release-time snapshot: address space that "+
				"changed hands since this build will be attributed to its previous owner.")
	}

	// --- Bind address --------------------------------------------------------

	switch {
	case in.OnionMode && !isLoopback(in.BindAddr):
		add("Listener binding", Fail,
			"A Tor Space bound "+in.BindAddr+" rather than loopback. The onion service reaches the app over 127.0.0.1; anything wider is a clearnet listener on a host whose whole purpose is not having one.")
	case in.OnionMode:
		add("Listener binding", Pass, "Bound to loopback. Only the Tor daemon can reach the app.")
		// A Tor Space does not run the same shield, and an operator who assumes it
		// does will misread every other row on this page. The gates keyed on a
		// source address are measuring the Tor daemon there, and the ones needing a
		// browser to compute a proof cannot be satisfied at all over plain-http
		// .onion — so they are off by design rather than by misconfiguration.
		if len(in.TorInertGates) > 0 {
			add("Defences inactive in this Tor Space", Info,
				"These do not enforce here, deliberately: "+strings.Join(in.TorInertGates, ", ")+
					". Every peer is 127.0.0.1, so a per-source limit would shed the whole audience as "+
					"one heavy hitter; and the plain-http onion leaves window.crypto.subtle undefined, "+
					"so no visitor could ever solve a challenge. The gates that do not depend on either "+
					"still enforce.")
		}
	case isLoopback(in.BindAddr):
		add("Listener binding", Pass, "Bound to loopback, so the app is reachable only through the local reverse proxy and every Tier 3 limit is unavoidable.")
	default:
		add("Listener binding", Warn,
			"Bound "+in.BindAddr+", which is reachable from off-host. Traffic that reaches the app directly skips nginx, and with it every Tier 3 limit, TLS termination, and the forwarding headers this app is configured to trust.")
	}

	// --- Tier 2 and Tier 3, judged on evidence rather than intent ------------

	// --- Request inspection, and what it is not ------------------------------
	//
	// This row exists to be read, not to be passed. The layer it describes is the
	// one most likely to be mistaken for a web application firewall, and an
	// operator who believes they have a WAF is an operator who might relax a
	// control that is actually holding. Info rather than Pass is the point: there
	// is nothing here to congratulate.
	if in.InspectRules > 0 {
		add("Request inspection", Info,
			"The compiled-in ruleset ("+itoa(in.InspectRules)+" rules, set v"+itoa(in.InspectRuleset)+
				") recognises scanners from the shape of their requests and raises their score. "+
				"It is NOT a web application firewall and it filters nothing: injection defence in "+
				"this product is parameterised queries, output sanitising, a strict CSP and path "+
				"hardening, and none of that becomes less necessary because this exists. It reads "+
				"only the path and the query — never a body — so writing about attacks is still "+
				"publishing. It can raise a client into a solvable challenge and can never, on its "+
				"own, block one.")
	}

	checks = append(checks, intelChecks(in)...)

	checks = append(checks, tierChecks(in, stale)...)

	// --- The two rows that never turn green ----------------------------------

	if in.LinkSpeedMbps > 0 {
		add("Measured ingress capacity", Info, fmt.Sprintf(
			"This host's link reports %d Mbps. That is the ceiling every layer above shares: once inbound traffic exceeds it, packets are dropped by the upstream network and nothing running here is consulted. Sizing any DDoS expectation starts from this number, not from a threshold in this panel.",
			in.LinkSpeedMbps))
	} else {
		add("Measured ingress capacity", Info,
			"This host's link speed could not be read. It is still the ceiling every layer above shares: once inbound traffic exceeds the uplink, packets are dropped upstream and nothing running here is consulted.")
	}

	// Clustering, stated with its ceiling in the same row. N nodes multiply
	// ingress LINEARLY — from one uplink to N — which is a real improvement in
	// availability and is routinely sold as something much larger. The number is
	// computed from the operator's own measured link so they can see their actual
	// cliff rather than a figure this product asserts.
	if in.ClusterPeers > 0 {
		nodes := in.ClusterPeers + 1
		detail := fmt.Sprintf("Verdicts are shared with %d other node(s), so a jail, a reputation loss or a pardon decided anywhere applies everywhere — a swarm no longer gets one free ride per node, and an attacker's escalation survives a deploy. %d nodes multiply your ingress LINEARLY: %d uplinks instead of one. That is not anycast, not scrubbing, and not a defence against anyone who brings more bandwidth than the sum of your links — and renting that capacity scales faster and cheaper than adding nodes does.",
			in.ClusterPeers, nodes, nodes)
		if in.LinkSpeedMbps > 0 {
			detail += fmt.Sprintf(" On this host's %d Mbps link that is roughly %d Mbps aggregate, IF every node has the same uplink and traffic is spread evenly across them; DNS-based routing moves traffic in minutes, not milliseconds, so a node failing is a minutes-long event.",
				in.LinkSpeedMbps, in.LinkSpeedMbps*nodes)
		}
		add("Multi-node ingress", Info, detail)

		if in.ClusterVerdictsIn == 0 {
			add("Peer verdicts", Warn,
				"Clustering is configured but no verdict from any peer has been applied. Either the peers are not reaching this node, or they are not sharing the same install secret — the gossip key is derived from it, so a mismatch fails authentication silently and by design. Until one arrives, each node is defending alone.")
		} else {
			d := fmt.Sprintf("%d verdicts from peers applied.", in.ClusterVerdictsIn)
			if in.ClusterRefused > 0 {
				d += fmt.Sprintf(" %d were refused because your own allow list covers the source — that override is what stops a compromised node from locking you out of your own fleet.", in.ClusterRefused)
			}
			add("Peer verdicts", Pass, d)
		}
	}

	add("Volumetric absorption", Fail,
		"Not provided by this or any single-origin product, and no setting on this page changes that. Every defence here runs after packets have already crossed your uplink, so a flood large enough to fill the link is decided by your provider's network, not by software on this machine. Absorbing that requires capacity in more places than one — an anycast network, or several origins in front of this one. Adding nodes raises the ceiling in proportion to the nodes; it does not remove it.")

	return checks
}

// tierChecks reports Tier 2 and Tier 3 from what the agent OBSERVED, and
// contradicts the state file when the two disagree.
//
// This is the row that would have caught the defect that motivated the package:
// intelChecks reports the third-party network feeds, judged on their contents
// rather than on the toggle.
//
// The same standard as the tier rows and for the same reason: a feed that
// stopped updating months ago, or that was switched on and never fetched, reads
// as healthy everywhere except its entry count. Nothing is reported at all when
// no feed is enabled — the report exists to say what is enforcing, and a row per
// unused feature is how a report stops being read.
func intelChecks(in Inputs) []Check {
	if len(in.IntelFeeds) == 0 {
		return nil
	}
	var out []Check
	add := func(title string, s Status, detail string) {
		out = append(out, Check{Title: title, Status: s, Detail: detail})
	}

	hostile := 0
	for _, f := range in.IntelFeeds {
		if f.Hostile {
			hostile++
		}
	}
	ceiling := "Third-party lists are consulted on unverified requests. " +
		"None of them can grant access — that is the type rather than a check, so a hijacked " +
		"endpoint's worst case is over-blocking, which is visible and recoverable, instead of a " +
		"silent bypass of every gate here. Most carry no signature to verify, so integrity rests " +
		"on refusing any refresh that changes a list by more than a third and keeping the last-good " +
		"copy: that catches a wholesale swap and would NOT catch a careful attacker adding ten " +
		"entries at a time."
	if hostile > 0 {
		ceiling += " " + itoa(hostile) + " of these can refuse a visitor outright."
	} else {
		ceiling += " None of these can refuse a visitor; they weigh a score toward a solvable check."
	}
	add("Published network lists", Info, ceiling)

	for _, f := range in.IntelFeeds {
		switch {
		case f.Refused != "":
			add("Feed: "+f.Name, Warn,
				"The last refresh was REFUSED and the previous copy is still in use — "+f.Refused+
					". This is the integrity bound doing its job, and it means the endpoint answered "+
					"with something unlike what it served before. Worth looking at rather than waiting out.")
		case f.Entries == 0:
			add("Feed: "+f.Name, Warn,
				"Enabled but holding nothing. It is switched on and doing no work — either it has "+
					"not fetched yet, or every attempt has failed. Until it loads, this layer is a "+
					"setting rather than a defence.")
		case f.LastError != "":
			add("Feed: "+f.Name, Warn, itoa(f.Entries)+" range(s) in use from the last good copy, but the "+
				"most recent refresh failed — "+f.LastError+". The data is stale, not absent.")
		default:
			add("Feed: "+f.Name, Pass, itoa(f.Entries)+" published range(s) loaded and consulted.")
		}
	}
	return out
}

// Tier 3 reported "active" from the mere existence of its conf file, while every
// limit_req inside it was commented out.
func tierChecks(in Inputs, stale bool) []Check {
	var out []Check
	add := func(title string, st Status, detail string) {
		out = append(out, Check{Title: title, Status: st, Detail: detail})
	}

	if !in.AgentAlive {
		if in.Tier2Wanted || in.Tier3Wanted {
			add("Kernel and edge layers", Fail,
				"Tier 2 and/or Tier 3 are switched on, but the root reconcile agent is not running. Nothing is applying or maintaining them, and the states shown elsewhere describe whatever was last left on the machine.")
		} else {
			add("Kernel and edge layers", Info,
				"The root reconcile agent is not installed, so Tier 2 (kernel) and Tier 3 (edge) are unavailable. The in-binary gates above still apply.")
		}
		return out
	}
	if !in.Digest.Present {
		add("Enforcement evidence", Warn,
			"The agent is running but has not written an enforcement digest, so the tier states below are its own report of what it did, not an observation of what is in force. Upgrade the agent to get verified rows here.")
	} else if stale {
		add("Enforcement evidence", Warn, fmt.Sprintf(
			"The agent's enforcement digest is %s old. It describes a machine that may have changed since.", in.Digest.Age.Round(time.Minute)))
	}

	// Tier 2.
	d := in.Digest
	switch {
	case !in.Tier2Wanted:
		add("Tier 2 · kernel firewall", Info, "Switched off. Volumetric floods reach the Go process rather than being dropped in the kernel.")
	case d.Tier2TablePresent == No:
		add("Tier 2 · kernel firewall", Fail, "Switched on, but the agent cannot see its nftables table. Nothing is being dropped in the kernel.")
	case d.Tier2MetersV4 == Yes && d.Tier2MetersV6 == Yes:
		add("Tier 2 · kernel firewall", Pass, "Per-source connection and rate meters are loaded for both address families.")
	case d.Tier2MetersV4 == Yes && d.Tier2MetersV6 == No:
		add("Tier 2 · kernel firewall", Fail,
			"Per-source meters exist for IPv4 only. In the inet family an `ip saddr` match never matches an IPv6 packet, so IPv6 traffic is either unlimited or dropped wholesale depending on what sits beside it — and a site can look perfectly healthy over IPv4 while being broken or unprotected over IPv6.")
	case d.Tier2MetersV4 == Unknown:
		add("Tier 2 · kernel firewall", Warn, "Switched on and the agent reports it active, but the digest carries no meter observation, so this is intent rather than evidence.")
	default:
		add("Tier 2 · kernel firewall", Warn, "Switched on, but the agent's observation of the per-source meters is incomplete.")
	}

	if in.Tier2Wanted {
		switch d.ConntrackSized {
		case Yes:
			add("Connection tracking", Pass, "nf_conntrack_max read back at the configured value. Every Tier 2 rule is stateful, so this table is the firewall's own dependency.")
		case No:
			add("Connection tracking", Fail,
				"The conntrack sizing did not take. Every Tier 2 rule is stateful, so exhausting this table disarms the firewall — and it surfaces as unattributable packet loss for every visitor, with nothing in this panel to explain it.")
		default:
			add("Connection tracking", Warn, "The agent did not report whether the conntrack sizing took effect.")
		}
	}

	// Tier 3 — the contradiction check.
	switch {
	case !in.Tier3Wanted:
		add("Tier 3 · edge (nginx)", Info, "Switched off. Requests reach the app without an edge-level rate or connection limit in front of them.")
	case d.Tier3Enforcing == Yes:
		add("Tier 3 · edge (nginx)", Pass, "The installed edge config contains active request and connection limits.")
	case d.Tier3Enforcing == No && d.Tier3Installed == Yes:
		add("Tier 3 · edge (nginx)", Fail,
			"The edge config is installed but enforces nothing: its limit zones are declared and no limit_req or limit_conn applies them. This is the exact shape of a layer that reports Active while doing no work, which is why this row is computed from the file's contents and not from its existence.")
	case d.Tier3Installed == No:
		add("Tier 3 · edge (nginx)", Fail, "Switched on, but the agent cannot find the edge config on this machine.")
	default:
		add("Tier 3 · edge (nginx)", Warn, "Switched on and reported active, but the digest carries no observation of what the config enforces.")
	}

	// Contradiction gate: a tier claiming active while its evidence disagrees is
	// worse than a tier that is simply off, because the operator has stopped
	// looking at it.
	if in.Tier3State == "active" && d.Tier3Enforcing == No {
		add("State contradicts evidence", Fail,
			"Tier 3 reports active while the agent observes that its config enforces nothing. Trust the evidence: this is a layer the panel would otherwise show as healthy indefinitely.")
	}
	if in.Tier2State == "active" && d.Tier2TablePresent == No {
		add("State contradicts evidence", Fail,
			"Tier 2 reports active while the agent cannot see its nftables table. Trust the evidence.")
	}

	if in.Tier3Wanted {
		switch d.DefaultServer {
		case Yes:
			add("Unknown-Host requests", Pass, "A default server handles requests for hostnames this install does not serve, so a direct-to-IP scan does not land on the first vhost that happens to match.")
		case No:
			add("Unknown-Host requests", Warn, "No default server for 443. A request with an unknown Host header is served by whichever vhost is listed first, which is rarely the one intended.")
		}
		switch d.MCPVhostRestricted {
		case Yes:
			add("MCP host surface", Pass, "The dedicated MCP host exposes only the endpoints it needs.")
		case No:
			// Name the evidence. "It is serving more than it should" with nothing
			// to check is not a finding an operator can act on or refute — and
			// when the detector is the thing that is wrong, it is the sentence
			// that keeps them pressing a button that was never going to help.
			msg := "The dedicated MCP host is serving more than the MCP and health endpoints."
			if in.Digest.MCPVhostOpenAt != "" {
				msg += " The catch-all objected to is at " + in.Digest.MCPVhostOpenAt +
					" — open that line and you are looking at exactly what the agent read."
			}
			add("MCP host surface", Warn, msg)
		}
	}

	return out
}

func itoa(n int) string { return strconv.Itoa(n) }

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// isLoopback reports whether a bind address is loopback-only. The address is
// read from configuration, not parsed from a packet, so a simple prefix test is
// the honest amount of work: anything unrecognised is treated as NOT loopback,
// which is the direction that warns rather than reassures.
func isLoopback(addr string) bool {
	a := strings.TrimSpace(addr)
	if a == "" {
		return false
	}
	host := a
	if i := strings.LastIndex(a, ":"); i >= 0 {
		host = a[:i]
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return strings.HasPrefix(host, "127.")
}

// Summary counts the report by status. fail is deliberately reported separately
// from warn: the permanent volumetric row means a healthy install still has
// exactly one Fail, and a caller that wants "is anything wrong" must compare
// against that baseline rather than against zero.
func Summary(checks []Check) (pass, warn, info, fail int) {
	for _, c := range checks {
		switch c.Status {
		case Pass:
			pass++
		case Warn:
			warn++
		case Info:
			info++
		case Fail:
			fail++
		}
	}
	return pass, warn, info, fail
}

// BaselineFails is the number of Fail rows a fully-correct install still shows:
// the volumetric-absorption row, which no configuration can turn green. Callers
// summarising posture must compare against this, not against zero, or they will
// report a perfect install as broken and train the operator to ignore the count.
const BaselineFails = 1
