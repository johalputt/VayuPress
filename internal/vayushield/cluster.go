// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/vayushield/brain"
	"github.com/johalputt/vayupress/internal/vayushield/gossip"
)

// cluster.go — the shield's side of multi-node verdict sharing.
//
// The gossip package moves messages and knows nothing about jails or
// reputation; this file is the only place the two meet. Keeping the boundary
// there is deliberate: a transport that could reach into the shield could be
// argued into doing more than the shield itself can, and the whole safety
// argument for accepting a peer's word rests on it doing strictly less.

// JoinCluster wires this node into a fleet.
//
// peers are the base URLs of the OTHER nodes. secret is the install's shared
// secret; the gossip key is derived from it rather than used directly, so a
// node holding the gossip key cannot mint an API token or speak to the MCP
// server. An empty secret or an empty peer list leaves clustering off, and
// leaving it off must cost nothing on the request path — which is the state
// almost every install is in.
func (m *Manager) JoinCluster(nodeID, secret string, peers []string) error {
	if m == nil || len(peers) == 0 {
		return nil
	}
	key, err := gossip.DeriveKey(secret)
	if err != nil {
		return err
	}
	m.gossipKey = &key
	m.gossipSeen = gossip.NewSeen(8192)
	m.gossipApply = gossip.NewApplier(clusterSink{m: m})
	m.gossipPush = gossip.NewPusher(key, nodeID, peers)
	return nil
}

// Clustered reports whether verdict sharing is configured.
func (m *Manager) Clustered() bool { return m != nil && m.gossipPush.Peers() > 0 }

// GossipHandler returns the endpoint peers push verdicts to, or nil when
// clustering is off. Returning nil rather than a handler that always refuses is
// intentional: an install with no fleet should not expose the route at all.
func (m *Manager) GossipHandler() http.Handler {
	if m == nil || m.gossipKey == nil {
		return nil
	}
	return gossip.Handler(*m.gossipKey, m.gossipSeen, func(msg gossip.Message) int {
		n := m.gossipApply.Apply(msg, m.cfg.Now())
		m.gossipIn.Add(int64(n))
		return n
	}, m.cfg.Now)
}

// FlushCluster pushes queued verdicts to the peers. The caller drives the
// cadence; a second is short enough that a fleet converges well inside the
// freshness window and long enough that a flood of local decisions becomes one
// batch per peer rather than N requests per decision.
func (m *Manager) FlushCluster(ctx context.Context) int {
	if m == nil {
		return 0
	}
	return m.gossipPush.Flush(ctx, m.cfg.Now())
}

// ClusterStats reports what sharing actually did, so the posture report can be
// computed from evidence rather than from the fact that peers are configured.
func (m *Manager) ClusterStats() (peers int, in, refused, sent, failed int64) {
	if m == nil {
		return 0, 0, 0, 0, 0
	}
	_, ref, _ := m.gossipApply.Stats()
	s, f := m.gossipPush.Stats()
	return m.gossipPush.Peers(), m.gossipIn.Load(), ref, s, f
}

// shareVerdict queues a local decision for the fleet. A no-op on the
// overwhelmingly common single-node install.
//
// Suppressed in observe-only mode, and that is the same correctness point the
// kernel offload makes. In-memory state IS still updated while observing, on
// purpose, so the counters reflect what a real rollout would look like including
// escalation. A push to a peer is not in-memory state: it is enforcement, on
// another machine, in a mode whose entire promise is that nothing enforces — and
// carried out in the one place the observing operator's own panel would never
// show it. A PARDON is exempt, because it only ever restores access, and
// withholding it would let observe mode make a peer's false positive last longer
// than it otherwise would.
func (m *Manager) shareVerdict(kind gossip.Kind, source string, weight float64) {
	if m == nil || m.gossipPush == nil {
		return
	}
	if kind != gossip.KindPardon {
		if lc := m.live(); lc != nil && lc.observe {
			return
		}
	}
	m.gossipPush.Queue(gossip.Verdict{Kind: kind, Source: source, Weight: weight})
}

// clusterSink applies a peer's verdicts, and is the narrow surface through
// which a remote node is allowed to affect this one.
//
// Every method here does strictly less than the corresponding local path. A
// peer cannot choose a sentence length, cannot reach the kernel banlist, and
// cannot touch a source the receiving operator has declared trusted.
type clusterSink struct{ m *Manager }

// Jail applies the RECEIVER's configured sentence, never one the sender chose.
//
// It deliberately does not call the kernel offload. A kernel drop happens
// outside this process and cannot be un-dropped, so letting a remote node reach
// it would hand a compromised peer a control the local operator cannot easily
// undo — and the amnesty button could not fully reverse. An in-memory jail is
// recoverable; a kernel ban a peer asked for is not.
func (s clusterSink) Jail(source string) {
	s.m.blocklist.Block(source, s.m.jailTTL())
}

// Suspect lowers standing through the same brain the local path uses, so the
// escalation, decay and pardon behaviour a peer's verdict participates in is
// exactly the behaviour a local one does.
func (s clusterSink) Suspect(source string, weight float64) {
	// weight is already clamped by the applier; it selects how hard the signal
	// lands rather than being applied as a raw score.
	sig := brain.SignalRateBurst
	if weight >= gossip.MaxSuspectWeight {
		sig = brain.SignalBlock
	}
	s.m.brain.Observe(source, sig)
}

// Pardon restores access. It is the one verdict that runs even for a source the
// operator trusts, because it can only ever give access back.
func (s clusterSink) Pardon(source string) {
	s.m.blocklist.Unblock(source)
	s.m.brain.Observe(source, brain.SignalProof)
}

// Trusted consults the receiving operator's OWN allow list. This is what stops
// a compromised peer from locking an operator out of their entire fleet with one
// message, and it is the same precedence the local middleware applies: an
// operator's stated fact outranks anything anyone inferred, including a peer.
// The source here is an ENFORCEMENT KEY, which is a prefix for IPv6 (always)
// and for IPv4 under /24 grouping — so AllowsAny rather than Source. Comparing a
// bare address against a prefix silently answered "not trusted" for every IPv6
// verdict, which meant this control did not exist for IPv6 at all.
func (s clusterSink) Trusted(source string) bool {
	return s.m.Policy().AllowsAny(source)
}

// clusterFields is embedded in Manager. Grouped here so the cluster surface is
// one thing to read rather than five fields scattered through the engine.
type clusterFields struct {
	gossipKey   *[32]byte
	gossipSeen  *gossip.Seen
	gossipApply *gossip.Applier
	gossipPush  *gossip.Pusher
	gossipIn    atomic.Int64
}

// FlushInterval is the cadence a caller should drive FlushCluster at.
const FlushInterval = time.Second
