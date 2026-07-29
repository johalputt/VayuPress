// SPDX-License-Identifier: Apache-2.0

package gossip

import (
	"strconv"
	"testing"
	"time"
)

// These tests assume the sender is authentic and hostile, which is the
// realistic compromise in a fleet of N edge nodes: not a stranger forging
// messages, but one node the operator owns that is no longer theirs.

type recorder struct {
	jailed, suspected, pardoned []string
	weights                     []float64
	trusted                     map[string]bool
}

func (r *recorder) Jail(s string) { r.jailed = append(r.jailed, s) }
func (r *recorder) Suspect(s string, w float64) {
	r.suspected = append(r.suspected, s)
	r.weights = append(r.weights, w)
}
func (r *recorder) Pardon(s string)       { r.pardoned = append(r.pardoned, s) }
func (r *recorder) Trusted(s string) bool { return r.trusted[s] }

// TestAPeerCannotJailTheOperatorsOwnNetworks is the control that keeps a
// compromised node from locking an operator out of their own fleet — and it is
// the same rule as everywhere else in the shield: the operator's stated facts
// outrank anything anyone infers, including another node.
func TestAPeerCannotJailTheOperatorsOwnNetworks(t *testing.T) {
	rec := &recorder{trusted: map[string]bool{"203.0.113.5": true}}
	a := NewApplier(rec)
	now := time.Now()

	a.Apply(Message{Node: "compromised", Issued: now.Unix(), Verdicts: []Verdict{
		{Kind: KindJail, Source: "203.0.113.5"}, // the operator's own office
		{Kind: KindSuspect, Source: "203.0.113.5", Weight: 0.3},
		{Kind: KindJail, Source: "198.51.100.9"}, // an unrelated source
	}}, now)

	for _, s := range rec.jailed {
		if s == "203.0.113.5" {
			t.Error("a peer jailed a network the receiving operator declared trusted — one " +
				"compromised node can now lock the operator out of their whole fleet")
		}
	}
	for _, s := range rec.suspected {
		if s == "203.0.113.5" {
			t.Error("a peer lowered the standing of a trusted network, which auto-jails it once " +
				"the standing collapses — the same lockout by a slower route")
		}
	}
	if len(rec.jailed) != 1 || rec.jailed[0] != "198.51.100.9" {
		t.Errorf("the unrelated verdict was not applied: %v", rec.jailed)
	}
}

// TestAPardonForATrustedSourceIsStillHonoured — the allow-list override exists
// to protect access, so it must not block the one verdict that only ever
// restores it.
func TestAPardonForATrustedSourceIsStillHonoured(t *testing.T) {
	rec := &recorder{trusted: map[string]bool{"203.0.113.5": true}}
	a := NewApplier(rec)
	now := time.Now()
	a.Apply(Message{Node: "edge-2", Issued: now.Unix(),
		Verdicts: []Verdict{{Kind: KindPardon, Source: "203.0.113.5"}}}, now)
	if len(rec.pardoned) != 1 {
		t.Error("a pardon for a trusted source was refused; the override is blocking the one " +
			"verdict that can only ever restore access")
	}
}

// TestOneCompromisedNodeCannotJailTheInternet — a valid key lets a node issue
// verdicts, so the bound is on volume rather than on authenticity. Without it,
// one node walks an address list into every jail in the fleet.
func TestOneCompromisedNodeCannotJailTheInternet(t *testing.T) {
	rec := &recorder{}
	a := NewApplier(rec)
	now := time.Now()

	// Ten full batches from one node inside one window.
	for batch := 0; batch < 10; batch++ {
		m := Message{Node: "compromised", Issued: now.Unix()}
		for i := 0; i < MaxVerdicts; i++ {
			m.Verdicts = append(m.Verdicts,
				Verdict{Kind: KindJail, Source: "198.51.100." + strconv.Itoa(i%256)})
		}
		a.Apply(m, now)
	}
	if len(rec.jailed) > PerPeerBudget {
		t.Errorf("one node applied %d verdicts in a window against a budget of %d — a single "+
			"compromised edge can enumerate addresses into the whole fleet's jails",
			len(rec.jailed), PerPeerBudget)
	}
	if _, _, over := a.Stats(); over == 0 {
		t.Error("nothing was recorded as over budget, so an operator has no way to notice a " +
			"peer behaving like this")
	}

	// An honest peer is unaffected by the noisy one's budget.
	before := len(rec.jailed)
	a.Apply(Message{Node: "edge-2", Issued: now.Unix(),
		Verdicts: []Verdict{{Kind: KindJail, Source: "192.0.2.1"}}}, now)
	if len(rec.jailed) != before+1 {
		t.Error("a well-behaved peer was starved by another peer's budget — the accounting is " +
			"shared rather than per-node")
	}
}

// TestTheBudgetRefillsSoAnHonestPeerIsNotSilencedForever.
func TestTheBudgetRefillsSoAnHonestPeerIsNotSilencedForever(t *testing.T) {
	rec := &recorder{}
	a := NewApplier(rec)
	now := time.Now()
	for i := 0; i < PerPeerBudget+50; i++ {
		a.Apply(Message{Node: "edge-1", Issued: now.Unix(),
			Verdicts: []Verdict{{Kind: KindJail, Source: "198.51.100.1"}}}, now)
	}
	spent := len(rec.jailed)
	if spent != PerPeerBudget {
		t.Fatalf("applied %d in the first window, want the %d budget", spent, PerPeerBudget)
	}
	later := now.Add(budgetWindow + time.Second)
	a.Apply(Message{Node: "edge-1", Issued: later.Unix(),
		Verdicts: []Verdict{{Kind: KindJail, Source: "198.51.100.2"}}}, later)
	if len(rec.jailed) != spent+1 {
		t.Error("the budget never refills, so a node that was briefly noisy is silenced for the " +
			"life of the process")
	}
}

// TestARotatingNodeNameCannotResetItsOwnBudget — the node name comes from the
// message, so an eviction policy on the accounting table is a way for one
// compromised node to mint a fresh budget by renaming itself.
func TestARotatingNodeNameCannotResetItsOwnBudget(t *testing.T) {
	rec := &recorder{}
	a := NewApplier(rec)
	now := time.Now()
	for i := 0; i < maxPeers*20; i++ {
		a.Apply(Message{Node: "node-" + strconv.Itoa(i), Issued: now.Unix(),
			Verdicts: []Verdict{{Kind: KindJail, Source: "198.51.100.1"}}}, now)
	}
	if len(rec.jailed) > maxPeers {
		t.Errorf("%d verdicts applied by %d rotating names against a %d-peer table — renaming "+
			"resets the budget, so the per-peer limit is not a limit at all",
			len(rec.jailed), maxPeers*20, maxPeers)
	}
}

// TestAnInboundReputationDeltaIsClamped — unclamped, one message collapses a
// source's standing straight into an auto-jail. That is the jail verdict wearing
// a different name, with none of the same accounting against the peer.
func TestAnInboundReputationDeltaIsClamped(t *testing.T) {
	rec := &recorder{}
	a := NewApplier(rec)
	now := time.Now()
	a.Apply(Message{Node: "edge-1", Issued: now.Unix(), Verdicts: []Verdict{
		{Kind: KindSuspect, Source: "198.51.100.9", Weight: 1000},
		{Kind: KindSuspect, Source: "198.51.100.8", Weight: -5}, // a negative "penalty" is a boost
	}}, now)
	for _, w := range rec.weights {
		if w > MaxSuspectWeight {
			t.Errorf("applied a reputation delta of %v against a %v clamp", w, MaxSuspectWeight)
		}
		if w <= 0 {
			t.Errorf("applied a non-positive delta of %v — a peer must not be able to RAISE a "+
				"source's standing through the penalty channel, which would let a compromised "+
				"node whitelist its own swarm", w)
		}
	}
}

// TestNoSinkIsSafe — the applier must be inert rather than panicking on an
// install that has not configured clustering.
func TestNoSinkIsSafe(t *testing.T) {
	var a *Applier
	if n := a.Apply(Message{Node: "x", Verdicts: []Verdict{{Kind: KindJail, Source: "1.2.3.4"}}}, time.Now()); n != 0 {
		t.Errorf("a nil applier applied %d verdicts", n)
	}
	if n := NewApplier(nil).Apply(Message{Node: "x"}, time.Now()); n != 0 {
		t.Errorf("an applier with no sink applied %d verdicts", n)
	}
}

// TestEveryAccessorIsSafeOnASingleNodeInstall. Nearly every install has no
// fleet, so every one of these is nil there — and the panel and the posture
// report call all of them on every page load. A missing nil guard is not a
// cost on that path, it is a crash on it, which is how this was found.
func TestEveryAccessorIsSafeOnASingleNodeInstall(t *testing.T) {
	var (
		a *Applier
		s *Seen
		p *Pusher
	)
	if applied, refused, over := a.Stats(); applied != 0 || refused != 0 || over != 0 {
		t.Error("a nil applier reported non-zero counters")
	}
	if s.Len() != 0 {
		t.Error("a nil replay cache reported entries")
	}
	if s.Fresh("x", time.Now()) {
		t.Error("a nil replay cache accepted a nonce, so an unconfigured node would apply " +
			"anything that reached it")
	}
	if sent, failed := p.Stats(); sent != 0 || failed != 0 {
		t.Error("a nil pusher reported traffic")
	}
	if p.Peers() != 0 {
		t.Error("a nil pusher reported peers")
	}
}
