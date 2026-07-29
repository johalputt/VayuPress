// SPDX-License-Identifier: Apache-2.0

package shieldaudit

import (
	"strings"
	"testing"
	"time"
)

// find returns the check with the given title, or a zero Check.
func find(checks []Check, title string) (Check, bool) {
	for _, c := range checks {
		if c.Title == title {
			return c, true
		}
	}
	return Check{}, false
}

// healthy is an install with everything on and every piece of evidence agreeing.
func healthy() Inputs {
	return Inputs{
		Tier2Wanted: true, Tier3Wanted: true,
		Tier2State: "active", Tier3State: "active",
		AgentAlive:   true,
		BindAddr:     "127.0.0.1:8080",
		RateLimit:    true,
		LoadShed:     true,
		AutoBlock:    true,
		CaptureWired: true,
		Digest: Digest{
			Present:            true,
			Age:                30 * time.Second,
			Tier2TablePresent:  Yes,
			Tier2MetersV4:      Yes,
			Tier2MetersV6:      Yes,
			ConntrackSized:     Yes,
			Tier3Installed:     Yes,
			Tier3Enforcing:     Yes,
			DefaultServer:      Yes,
			MCPVhostRestricted: Yes,
		},
		LinkSpeedMbps: 1000,
	}
}

// TestVolumetricRowNeverTurnsGreen is the honesty invariant, and it is the
// reason this package exists in the shape it does.
//
// Every defence in this product runs after packets have crossed the operator's
// uplink. A flood large enough to fill that link is decided by their provider's
// network, and no setting on any panel changes it. An operator who believes
// otherwise finds out during an attack instead of before one — so this row is
// permanent, and no input may satisfy it.
func TestVolumetricRowNeverTurnsGreen(t *testing.T) {
	c, ok := find(Run(healthy()), "Volumetric absorption")
	if !ok {
		t.Fatal("the volumetric-absorption row is missing — the report now implies a guarantee the product cannot honour")
	}
	if c.Status != Fail {
		t.Errorf("volumetric row status = %v on a perfectly configured install, want Fail", c.Status)
	}
	if !strings.Contains(c.Detail, "single-origin") {
		t.Error("the row does not say WHY it can never pass, so it reads as a bug rather than a limit")
	}
}

// TestTier3ActiveButEnforcingNothingIsAFail is the regression that motivated the
// whole package. Tier 3 computed "active" from the existence of its conf file
// while every limit_req inside it was commented out, so the panel showed a
// healthy layer that did no work — for as long as nobody looked.
func TestTier3ActiveButEnforcingNothingIsAFail(t *testing.T) {
	in := healthy()
	in.Digest.Tier3Enforcing = No // installed, but the limits are inert

	checks := Run(in)
	c, ok := find(checks, "Tier 3 · edge (nginx)")
	if !ok {
		t.Fatal("no Tier 3 row")
	}
	if c.Status != Fail {
		t.Errorf("Tier 3 status = %v with an installed-but-inert config, want Fail", c.Status)
	}
	// And the contradiction must be called out separately, because the state file
	// still says "active" and that is what the rest of the panel shows.
	if _, ok := find(checks, "State contradicts evidence"); !ok {
		t.Error("no row reports that the tier's reported state disagrees with the observed evidence")
	}
}

// TestIPv4OnlyMetersAreAFail — in the inet family an `ip saddr` match carries an
// implicit IPv4-only dependency, so a v4-keyed meter never matches a v6 packet
// while a bare drop beside it matches everything. The site then looks perfectly
// healthy over IPv4 while being unprotected or broken over IPv6, and nothing in
// the panel distinguishes the two.
func TestIPv4OnlyMetersAreAFail(t *testing.T) {
	in := healthy()
	in.Digest.Tier2MetersV6 = No

	c, _ := find(Run(in), "Tier 2 · kernel firewall")
	if c.Status != Fail {
		t.Errorf("Tier 2 status = %v with IPv4-only meters, want Fail", c.Status)
	}
	if !strings.Contains(c.Detail, "IPv6") {
		t.Error("the row does not name the address family that is unprotected")
	}
}

// TestAbsentEvidenceIsNeverAPass — the most dangerous failure mode for a posture
// report is reporting the operator's intent back to them as if it were fact. A
// missing agent, or a digest with nothing in it, must not produce green rows.
func TestAbsentEvidenceIsNeverAPass(t *testing.T) {
	in := healthy()
	in.Digest = Digest{} // no digest at all
	checks := Run(in)

	for _, title := range []string{"Tier 2 · kernel firewall", "Tier 3 · edge (nginx)"} {
		c, ok := find(checks, title)
		if !ok {
			t.Fatalf("no %q row", title)
		}
		if c.Status == Pass {
			t.Errorf("%q reported Pass with no evidence at all — that is the operator's toggle "+
				"being read back to them as a fact", title)
		}
	}
	if _, ok := find(checks, "Enforcement evidence"); !ok {
		t.Error("nothing tells the operator that the rows below are unverified")
	}

	// The other half of the same principle, and a surviving mutant found it: an
	// UNVERIFIED control must not be reported as a broken one either. A newer
	// agent and an older app (or the reverse) is a routine version skew, and a
	// skew that paints the panel red is a panel the operator stops reading. The
	// honest verdict for "installed, but the agent said nothing about what it
	// enforces" is Warn, not Fail.
	skew := healthy()
	skew.Digest = Digest{Present: true, Age: time.Minute, Tier3Installed: Yes, Tier2TablePresent: Yes}
	for _, title := range []string{"Tier 2 · kernel firewall", "Tier 3 · edge (nginx)"} {
		c, ok := find(Run(skew), title)
		if !ok {
			t.Fatalf("no %q row", title)
		}
		if c.Status != Warn {
			t.Errorf("%q = %v when the agent reported it installed but said nothing about "+
				"enforcement, want Warn — an unverified control is not a broken one", title, c.Status)
		}
	}

	// A dead agent with tiers switched on is worse still: nothing is maintaining
	// them and the states describe whatever was last left on the machine.
	in.AgentAlive = false
	c, ok := find(Run(in), "Kernel and edge layers")
	if !ok || c.Status != Fail {
		t.Errorf("tiers on with a dead agent = %v, want a Fail row", c.Status)
	}
}

// TestStaleDigestIsFlagged — a digest describes the machine at the moment it was
// written. An old one describes a machine that may have changed since, and
// presenting it as current is the same class of error as presenting intent as
// evidence.
func TestStaleDigestIsFlagged(t *testing.T) {
	in := healthy()
	in.Digest.Age = 2 * time.Hour

	c, ok := find(Run(in), "Enforcement evidence")
	if !ok {
		t.Fatal("a two-hour-old digest was presented as current")
	}
	if c.Status != Warn || !strings.Contains(c.Detail, "old") {
		t.Errorf("stale digest row = %v / %q, want a Warn saying how old it is", c.Status, c.Detail)
	}
}

// TestProxiedSiteWithUnresolvedClientIP — this is the single most likely reason
// a proxied site's traffic misbehaves, and it produces no error anywhere. Every
// per-IP limit ends up measuring the edge instead of the reader, so the whole
// audience shares one bucket.
func TestProxiedSiteWithUnresolvedClientIP(t *testing.T) {
	in := healthy()
	in.BehindCDN = true
	in.ClientIPResolved = false

	c, _ := find(Run(in), "Real visitor IP")
	if c.Status != Fail {
		t.Errorf("status = %v for a proxied site whose forwarding header did not resolve, want Fail", c.Status)
	}

	// Resolved is the healthy case.
	in.ClientIPResolved = true
	if c, _ := find(Run(in), "Real visitor IP"); c.Status != Pass {
		t.Errorf("status = %v for a proxied site with a resolved visitor IP, want Pass", c.Status)
	}

	// A header honoured on a site NOT marked as proxied is the spoofing shape,
	// and must not read as healthy.
	in.BehindCDN = false
	if c, _ := find(Run(in), "Real visitor IP"); c.Status != Warn {
		t.Errorf("status = %v for a forwarding header honoured on an unproxied site, want Warn", c.Status)
	}
}

// TestAutoBlockOffIsNotSilent — the kernel offload is fed only from
// auto-block-guarded paths, and auto-block defaults off. So on a default install
// the offload table is created and stays empty forever, while Tier 2 reports
// active. Saying nothing there is what let the panel claim a working layer.
func TestAutoBlockOffIsNotSilent(t *testing.T) {
	in := healthy()
	in.AutoBlock = false

	c, ok := find(Run(in), "Reputation jail feeds the kernel")
	if !ok || c.Status == Pass {
		t.Errorf("auto-block off produced %v — the offload ships inert and nothing says so", c.Status)
	}
	if !strings.Contains(c.Detail, "Tier 2") {
		t.Error("the row does not correct the belief that turning Tier 2 on populates the offload")
	}
}

// TestOnionModeRequiresLoopback — a Tor Space that bound a public address has a
// clearnet listener on a host whose entire purpose is not having one.
func TestOnionModeRequiresLoopback(t *testing.T) {
	in := healthy()
	in.OnionMode = true
	in.BindAddr = "0.0.0.0:8080"

	c, _ := find(Run(in), "Listener binding")
	if c.Status != Fail {
		t.Errorf("status = %v for a Tor Space bound 0.0.0.0, want Fail", c.Status)
	}

	in.BindAddr = "127.0.0.1:8080"
	if c, _ := find(Run(in), "Listener binding"); c.Status != Pass {
		t.Errorf("status = %v for a loopback-bound Tor Space, want Pass", c.Status)
	}
}

// TestHealthyInstallHasExactlyTheBaselineFail — the permanent volumetric row
// means "zero fails" is unreachable. A caller summarising posture has to compare
// against the baseline, or a perfect install reads as broken and the operator
// learns to ignore the number.
func TestHealthyInstallHasExactlyTheBaselineFail(t *testing.T) {
	checks := Run(healthy())
	_, _, _, fail := Summary(checks)
	if fail != BaselineFails {
		var titles []string
		for _, c := range checks {
			if c.Status == Fail {
				titles = append(titles, c.Title)
			}
		}
		t.Errorf("a fully-configured install has %d Fail rows, want %d: %v", fail, BaselineFails, titles)
	}
}

// TestParseTriTreatsUnknownAsUnknown — the digest is written by a root process
// and read by an unprivileged one. An unrecognised value must become "no
// information", never a guess in either direction: guessing "no" turns an agent
// version skew into a wall of red, and guessing "yes" turns it into a wall of
// green, which is the dangerous one.
func TestParseTriTreatsUnknownAsUnknown(t *testing.T) {
	for _, s := range []string{"yes", "YES", "true", "1", "present", " active "} {
		if got := ParseTri(s); got != Yes {
			t.Errorf("ParseTri(%q) = %v, want Yes", s, got)
		}
	}
	for _, s := range []string{"no", "false", "0", "absent"} {
		if got := ParseTri(s); got != No {
			t.Errorf("ParseTri(%q) = %v, want No", s, got)
		}
	}
	for _, s := range []string{"", "maybe", "probably", "enforcing-partially", "2"} {
		if got := ParseTri(s); got != Unknown {
			t.Errorf("ParseTri(%q) = %v, want Unknown", s, got)
		}
	}
}

// TestTier1OffIsAFail — with neither rate limiting nor load shedding, nothing
// bounds how much work one visitor can ask the process to do. That is the state
// a default install is in, and the report must not soften it.
func TestTier1OffIsAFail(t *testing.T) {
	in := healthy()
	in.RateLimit, in.LoadShed = false, false
	if c, _ := find(Run(in), "Tier 1 · in-binary gates"); c.Status != Fail {
		t.Errorf("status = %v with both in-binary gates off, want Fail", c.Status)
	}
	in.RateLimit = true
	if c, _ := find(Run(in), "Tier 1 · in-binary gates"); c.Status != Warn {
		t.Errorf("status = %v with only rate limiting on, want Warn", c.Status)
	}
}

// TestConntrackFailureIsSurfaced — every Tier 2 rule is stateful, so the
// connection table is the firewall's own dependency. Exhausting it disarms the
// firewall and surfaces as unattributable packet loss with nothing in the panel
// to explain it, which is precisely what this row is for.
func TestConntrackFailureIsSurfaced(t *testing.T) {
	in := healthy()
	in.Digest.ConntrackSized = No
	if c, _ := find(Run(in), "Connection tracking"); c.Status != Fail {
		t.Errorf("status = %v when the conntrack sizing did not take, want Fail", c.Status)
	}
}

// TestTorSpaceSaysWhichDefencesAreOff — a Tor Space does not run the same
// shield, and an operator who assumes it does will misread every other row on
// the page. The gates keyed on a source address are measuring the Tor daemon
// there, and the ones needing a browser to compute a proof cannot be satisfied
// at all over a plain-http onion. That they are off is a design decision, and a
// design decision nobody states is indistinguishable from a bug.
func TestTorSpaceSaysWhichDefencesAreOff(t *testing.T) {
	in := healthy()
	in.OnionMode = true
	in.BindAddr = "127.0.0.1:8080"
	in.TorInertGates = []string{"fair shed", "rate limit", "sovereign surge", "challenge ladder"}

	c, ok := find(Run(in), "Defences inactive in this Tor Space")
	if !ok {
		t.Fatal("a Tor Space's report says nothing about which gates do not enforce there")
	}
	if c.Status != Info {
		t.Errorf("status = %v, want Info — these are off by design, not broken", c.Status)
	}
	for _, want := range []string{"rate limit", "challenge ladder", "127.0.0.1"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("the row does not mention %q", want)
		}
	}

	// A clearnet install must not carry the row at all.
	in.OnionMode = false
	in.TorInertGates = nil
	if _, ok := find(Run(in), "Defences inactive in this Tor Space"); ok {
		t.Error("a clearnet install was told about Tor-mode exemptions that do not apply to it")
	}
}

// TestInspectionRowRefusesToClaimItIsAWAF. The request-inspection layer is the
// one thing in this shield most likely to be mistaken for a web application
// firewall, and that mistake is expensive in a specific way: an operator who
// believes they have a WAF may relax a control that is actually holding.
//
// So the row is Info, never Pass — there is nothing here to congratulate — and
// it has to say what it does not do. This test pins the copy because the copy IS
// the control.
func TestInspectionRowRefusesToClaimItIsAWAF(t *testing.T) {
	out := Run(Inputs{InspectRules: 41, InspectRuleset: 1})
	var row *Check
	for i := range out {
		if strings.Contains(out[i].Title, "Request inspection") {
			row = &out[i]
		}
	}
	if row == nil {
		t.Fatal("no request-inspection row; an operator has no way to see which rules their " +
			"build carries, and the ruleset is never fetched so there is no other source")
	}
	if row.Status != Info {
		t.Errorf("status = %v, want Info — a Pass invites an operator to read pattern matching "+
			"as protection", row.Status)
	}
	d := strings.ToLower(row.Detail)
	for _, must := range []string{
		"not a web application firewall", // the disclaimer, stated plainly
		"parameterised queries",          // what actually defends against injection
		"never a body",                   // why writing about attacks still publishes
		"solvable challenge",             // the bound: it cannot block alone
	} {
		if !strings.Contains(d, must) {
			t.Errorf("the row does not say %q. This copy is the control: without it the feature "+
				"reads as a WAF, and a WAF a defender trusts is worse than no WAF", must)
		}
	}
	if !strings.Contains(row.Detail, "41 rules") || !strings.Contains(row.Detail, "v1") {
		t.Errorf("the row does not report the rule count and ruleset version: %q", row.Detail)
	}

	// And an install whose build carries no ruleset must not show the row at all,
	// rather than showing one that describes rules it does not have.
	for _, c := range Run(Inputs{}) {
		if strings.Contains(c.Title, "Request inspection") {
			t.Error("a build with no ruleset still reported a request-inspection row")
		}
	}
}

// TestTheMultiNodeRowStatesItsCeilingInTheSameBreath. Adding nodes is the only
// mechanism in this product that touches volumetric capacity at all, which makes
// it the single easiest thing to over-claim. N nodes multiply ingress LINEARLY —
// a real improvement in availability, and routinely sold as something much
// larger. An operator who reads "multi-node protection" and hears "anycast" will
// find out during an attack instead of before one.
func TestTheMultiNodeRowStatesItsCeilingInTheSameBreath(t *testing.T) {
	out := Run(Inputs{ClusterPeers: 3, LinkSpeedMbps: 1000, ClusterVerdictsIn: 12})
	var row *Check
	for i := range out {
		if strings.Contains(out[i].Title, "Multi-node ingress") {
			row = &out[i]
		}
	}
	if row == nil {
		t.Fatal("clustering is configured but nothing states what it does or does not buy")
	}
	d := strings.ToLower(row.Detail)
	for _, must := range []string{
		"linearly",    // the shape of the gain
		"not anycast", // the thing it is most often mistaken for
		"not scrubbing",
		"more bandwidth than the sum of your links", // the attack it does not stop
	} {
		if !strings.Contains(d, must) {
			t.Errorf("the multi-node row does not say %q — without it this reads as a volumetric "+
				"defence, which it is not", must)
		}
	}
	// The aggregate is computed from the operator's OWN measured link, so they
	// see their real cliff rather than a number this product asserts.
	if !strings.Contains(row.Detail, "4000 Mbps") {
		t.Errorf("the row does not compute the aggregate from this host's measured link: %q",
			row.Detail)
	}
	// And it must not present that aggregate as a fact when it rests on
	// assumptions the operator has to check.
	if !strings.Contains(d, "if every node has the same uplink") {
		t.Error("the aggregate is stated without its assumption. Nodes with different uplinks, " +
			"or traffic that lands unevenly, make the figure wrong in the direction that flatters")
	}

	// The volumetric row must stay Fail, and must say that nodes raise the
	// ceiling rather than remove it.
	for _, c := range out {
		if strings.Contains(c.Title, "Volumetric absorption") {
			if c.Status != Fail {
				t.Errorf("the volumetric row went %v once clustering was configured — no number "+
					"of nodes makes single-origin software absorb a flood", c.Status)
			}
			if !strings.Contains(c.Detail, "does not remove it") {
				t.Error("the volumetric row does not say that adding nodes raises the ceiling " +
					"without removing it")
			}
		}
	}
}

// TestClusteringConfiguredButSilentIsAWarning — a shared-secret mismatch fails
// gossip authentication silently and by design, which is correct for security
// and terrible for diagnosis. The report has to say so, or an operator believes
// their fleet is sharing verdicts when every node is defending alone.
func TestClusteringConfiguredButSilentIsAWarning(t *testing.T) {
	var got *Check
	for _, c := range Run(Inputs{ClusterPeers: 2}) {
		if strings.Contains(c.Title, "Peer verdicts") {
			got = &c
		}
	}
	if got == nil {
		t.Fatal("no peer-verdict row")
	}
	if got.Status != Warn {
		t.Errorf("status = %v, want Warn — configured-but-never-delivered is exactly the state "+
			"that looks like success from the panel", got.Status)
	}
	if !strings.Contains(got.Detail, "install secret") {
		t.Error("the row does not name the most likely cause. The gossip key is derived from the " +
			"install secret, so a mismatch is silent by design and unguessable without this hint")
	}

	// And an install with no peers must not show the row at all rather than
	// warning about a fleet that does not exist.
	for _, c := range Run(Inputs{}) {
		if strings.Contains(c.Title, "Peer verdicts") || strings.Contains(c.Title, "Multi-node") {
			t.Errorf("a single-node install reported %q", c.Title)
		}
	}
}
