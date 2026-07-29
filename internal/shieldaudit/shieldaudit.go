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
	// LinkSpeedMbps is the operator's measured ingress capacity, read from
	// /sys/class/net/*/speed. Zero when it could not be determined.
	LinkSpeedMbps int

	// --- What the agent observed ---------------------------------------------

	Digest Digest
	// DigestMaxAge is how old a digest may be before it is reported as stale.
	// Zero uses the default.
	DigestMaxAge time.Duration
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
	case in.RateLimit || in.LoadShed:
		add("Tier 1 · in-binary gates", Warn,
			"Only one of rate limiting and load shedding is on. Rate limiting bounds one source; load shedding protects the process from all of them at once. They cover different failures.")
	default:
		add("Tier 1 · in-binary gates", Fail,
			"Rate limiting and load shedding are both off. Nothing bounds how much work a single visitor can ask this process to do.")
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
		add("Real visitor IP", Fail,
			"This site is marked as proxied, but the request that generated this report did not resolve to a visitor address distinct from the peer. Every per-IP limit is therefore measuring the edge, not the reader: the whole audience shares one bucket, and the first busy minute shows everyone a challenge.")
	case in.BehindCDN:
		add("Real visitor IP", Pass, "The proxy's forwarding header resolved to a distinct visitor address, so per-IP limits apply per reader.")
	case in.ClientIPResolved:
		add("Real visitor IP", Warn,
			"A forwarding header was honoured, but this site is not marked as proxied. Check that the peer really is a trusted proxy — otherwise a visitor can choose the address they are limited under.")
	default:
		add("Real visitor IP", Pass, "Traffic arrives directly, so the connection's peer address is the visitor.")
	}

	// --- Bind address --------------------------------------------------------

	switch {
	case in.OnionMode && !isLoopback(in.BindAddr):
		add("Listener binding", Fail,
			"A Tor Space bound "+in.BindAddr+" rather than loopback. The onion service reaches the app over 127.0.0.1; anything wider is a clearnet listener on a host whose whole purpose is not having one.")
	case in.OnionMode:
		add("Listener binding", Pass, "Bound to loopback. Only the Tor daemon can reach the app.")
	case isLoopback(in.BindAddr):
		add("Listener binding", Pass, "Bound to loopback, so the app is reachable only through the local reverse proxy and every Tier 3 limit is unavoidable.")
	default:
		add("Listener binding", Warn,
			"Bound "+in.BindAddr+", which is reachable from off-host. Traffic that reaches the app directly skips nginx, and with it every Tier 3 limit, TLS termination, and the forwarding headers this app is configured to trust.")
	}

	// --- Tier 2 and Tier 3, judged on evidence rather than intent ------------

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

	add("Volumetric absorption", Fail,
		"Not provided by this or any single-origin product, and no setting on this page changes that. Every defence here runs after packets have already crossed your uplink, so a flood large enough to fill the link is decided by your provider's network, not by software on this machine. Absorbing that requires capacity in more places than one — an anycast network, or several origins in front of this one.")

	return checks
}

// tierChecks reports Tier 2 and Tier 3 from what the agent OBSERVED, and
// contradicts the state file when the two disagree.
//
// This is the row that would have caught the defect that motivated the package:
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
			add("MCP host surface", Warn, "The dedicated MCP host is serving more than the MCP and health endpoints.")
		}
	}

	return out
}

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
