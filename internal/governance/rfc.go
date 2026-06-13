// Package governance implements the VayuPress RFC and constitutional voting system.
// Any maintainer can open an RFC; acceptance requires 2/3 majority of active voters.
package governance

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// RFCStatus represents the lifecycle stage of an RFC.
type RFCStatus string

const (
	RFCOpen     RFCStatus = "open"
	RFCAccepted RFCStatus = "accepted"
	RFCRejected RFCStatus = "rejected"
	RFCWithdrawn RFCStatus = "withdrawn"
)

// RFC represents a Request for Comments — a governance proposal.
type RFC struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	Author      string    `json:"author"`
	Status      RFCStatus `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	Votes       []Vote    `json:"votes"`
}

// Vote is a single maintainer vote on an RFC.
type Vote struct {
	Voter     string    `json:"voter"`
	InFavor   bool      `json:"in_favor"`
	Timestamp time.Time `json:"timestamp"`
	Reason    string    `json:"reason,omitempty"`
}

// AcceptanceThreshold is the minimum fraction of votes that must be in favour.
const AcceptanceThreshold = 2.0 / 3.0

// ErrDuplicateVote is returned if a voter votes twice on the same RFC.
var ErrDuplicateVote = errors.New("governance: voter has already voted on this RFC")

// Tally counts votes for and against the RFC.
func (r *RFC) Tally() (inFavor, against int) {
	for _, v := range r.Votes {
		if v.InFavor {
			inFavor++
		} else {
			against++
		}
	}
	return
}

// Result returns the current acceptance result if quorum is met.
// quorum is the minimum number of votes required before resolving.
func (r *RFC) Result(quorum int) (accepted bool, decided bool) {
	total := len(r.Votes)
	if total < quorum {
		return false, false
	}
	inf, _ := r.Tally()
	return float64(inf)/float64(total) >= AcceptanceThreshold, true
}

// Store is an in-memory RFC store (replace with SQLite in production).
type Store struct {
	mu   sync.RWMutex
	rfcs map[string]*RFC
}

// NewStore creates an empty governance store.
func NewStore() *Store {
	return &Store{rfcs: make(map[string]*RFC)}
}

// Create adds a new RFC. Returns an error if ID already exists.
func (s *Store) Create(r *RFC) error {
	if r.ID == "" || r.Title == "" || r.Author == "" {
		return errors.New("governance: RFC ID, Title, and Author are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.rfcs[r.ID]; exists {
		return fmt.Errorf("governance: RFC %s already exists", r.ID)
	}
	r.Status = RFCOpen
	r.CreatedAt = time.Now().UTC()
	s.rfcs[r.ID] = r
	return nil
}

// Get retrieves an RFC by ID.
func (s *Store) Get(id string) (*RFC, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rfcs[id]
	return r, ok
}

// List returns all RFCs.
func (s *Store) List() []*RFC {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*RFC, 0, len(s.rfcs))
	for _, r := range s.rfcs {
		out = append(out, r)
	}
	return out
}

// CastVote records a vote on an open RFC.
// quorum: minimum votes before resolution; 0 = never auto-resolve.
func (s *Store) CastVote(rfcID, voter string, inFavor bool, reason string, quorum int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rfcs[rfcID]
	if !ok {
		return fmt.Errorf("governance: RFC %s not found", rfcID)
	}
	if r.Status != RFCOpen {
		return fmt.Errorf("governance: RFC %s is not open (status: %s)", rfcID, r.Status)
	}
	for _, v := range r.Votes {
		if v.Voter == voter {
			return ErrDuplicateVote
		}
	}
	r.Votes = append(r.Votes, Vote{
		Voter:     voter,
		InFavor:   inFavor,
		Timestamp: time.Now().UTC(),
		Reason:    reason,
	})
	// Auto-resolve if quorum is met.
	if quorum > 0 {
		if accepted, decided := r.Result(quorum); decided {
			now := time.Now().UTC()
			r.ResolvedAt = &now
			if accepted {
				r.Status = RFCAccepted
			} else {
				r.Status = RFCRejected
			}
		}
	}
	return nil
}
