// Package cluster provides multi-node sovereign clustering for VayuPress.
// Uses a simple leader-election protocol: nodes exchange heartbeats;
// the node with the lowest ID among live peers becomes leader.
// For production use, replace with Raft (etcd/raft or hashicorp/raft).
package cluster

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
)

// Role is a cluster node's current role.
type Role string

const (
	RoleFollower  Role = "follower"
	RoleLeader    Role = "leader"
	RoleCandidate Role = "candidate"
)

// NodeState holds the observed state of a peer node.
type NodeState struct {
	ID          string    `json:"id"`
	Addr        string    `json:"addr"`
	Role        Role      `json:"role"`
	LastSeen    time.Time `json:"last_seen"`
}

// Node is a sovereign cluster participant.
type Node struct {
	mu          sync.RWMutex
	id          string
	addr        string
	role        Role
	peers       map[string]*NodeState
	heartbeatTTL time.Duration
}

// New creates a cluster Node with the given ID and address.
func New(id, addr string) *Node {
	return &Node{
		id:           id,
		addr:         addr,
		role:         RoleFollower,
		peers:        make(map[string]*NodeState),
		heartbeatTTL: 5 * time.Second,
	}
}

// Heartbeat records a heartbeat from a peer.
func (n *Node) Heartbeat(peerID, peerAddr string, peerRole Role) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.peers[peerID] = &NodeState{
		ID:       peerID,
		Addr:     peerAddr,
		Role:     peerRole,
		LastSeen: time.Now(),
	}
	n.electLeader()
}

// electLeader applies the deterministic election rule: lowest ID among live nodes wins.
// Must be called with n.mu held.
func (n *Node) electLeader() {
	live := []string{n.id}
	for id, ps := range n.peers {
		if time.Since(ps.LastSeen) < n.heartbeatTTL {
			live = append(live, id)
		}
	}
	sort.Strings(live)
	if live[0] == n.id {
		n.role = RoleLeader
	} else {
		n.role = RoleFollower
	}
}

// Role returns the node's current role.
func (n *Node) Role() Role {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.role
}

// IsLeader returns true if this node is currently the leader.
func (n *Node) IsLeader() bool {
	return n.Role() == RoleLeader
}

// ID returns this node's identifier.
func (n *Node) ID() string { return n.id }

// LivePeers returns the IDs of peers seen within heartbeatTTL.
func (n *Node) LivePeers() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	var out []string
	for id, ps := range n.peers {
		if time.Since(ps.LastSeen) < n.heartbeatTTL {
			out = append(out, id)
		}
	}
	return out
}

// RunHeartbeat starts emitting heartbeats to peers via sendFn until ctx is cancelled.
func (n *Node) RunHeartbeat(ctx context.Context, interval time.Duration, sendFn func(peerID string, state NodeState)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.mu.RLock()
			state := NodeState{ID: n.id, Addr: n.addr, Role: n.role, LastSeen: time.Now()}
			peers := make([]string, 0, len(n.peers))
			for id := range n.peers {
				peers = append(peers, id)
			}
			n.mu.RUnlock()
			for _, pid := range peers {
				sendFn(pid, state)
			}
			logging.LogJSON(logging.LogFields{
				Level:     "debug",
				Component: "cluster",
				Msg:       fmt.Sprintf("node %s heartbeat (role=%s)", n.id, state.Role),
			})
		}
	}
}
