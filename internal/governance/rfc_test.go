package governance_test

import (
	"testing"

	"github.com/johalputt/vayupress/internal/governance"
)

func TestRFCCreateAndVote(t *testing.T) {
	s := governance.NewStore()
	rfc := &governance.RFC{ID: "rfc-001", Title: "Add IPFS backend", Author: "alice", Body: "proposal"}
	if err := s.Create(rfc); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// 2 in favour, 1 against: should accept at quorum=3 (2/3 >= threshold)
	_ = s.CastVote("rfc-001", "alice", true, "", 3)
	_ = s.CastVote("rfc-001", "bob", true, "", 3)
	if err := s.CastVote("rfc-001", "carol", false, "", 3); err != nil {
		t.Fatalf("CastVote: %v", err)
	}

	r, _ := s.Get("rfc-001")
	if r.Status != governance.RFCAccepted {
		t.Errorf("expected accepted, got %s", r.Status)
	}
}

func TestDuplicateVote(t *testing.T) {
	s := governance.NewStore()
	_ = s.Create(&governance.RFC{ID: "rfc-002", Title: "T", Author: "a", Body: "b"})
	_ = s.CastVote("rfc-002", "alice", true, "", 0)
	err := s.CastVote("rfc-002", "alice", false, "", 0)
	if err != governance.ErrDuplicateVote {
		t.Errorf("expected ErrDuplicateVote, got %v", err)
	}
}
