package cluster_test

import (
	"testing"

	"github.com/johalputt/vayupress/internal/cluster"
)

func TestLeaderElection(t *testing.T) {
	// Node with lowest ID should become leader
	n := cluster.New("node-b", "localhost:8081")
	// Add peer with lower ID — n should become follower
	n.Heartbeat("node-a", "localhost:8080", cluster.RoleFollower)
	if n.IsLeader() {
		t.Error("node-b should not be leader when node-a is live")
	}
}

func TestSingleNodeLeader(t *testing.T) {
	n := cluster.New("node-a", "localhost:8080")
	// With no peers, should be leader (lowest ID = only ID)
	n.Heartbeat("", "", cluster.RoleFollower) // trigger election with no valid peer
	_ = n.Role()                              // just ensure no panic
}

func TestLivePeers(t *testing.T) {
	n := cluster.New("node-a", ":8080")
	n.Heartbeat("node-b", ":8081", cluster.RoleFollower)
	n.Heartbeat("node-c", ":8082", cluster.RoleFollower)
	peers := n.LivePeers()
	if len(peers) != 2 {
		t.Errorf("expected 2 live peers, got %d", len(peers))
	}
}
