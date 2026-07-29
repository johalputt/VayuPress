// SPDX-License-Identifier: Apache-2.0

package gossip

import (
	"sync"
	"time"
)

// apply.go — the receiving side, which is where a compromised node's blast
// radius is actually bounded.
//
// Authentication proves a message came from a key-holder. It says nothing about
// whether that key-holder is still trustworthy, and in a fleet of N edge nodes
// the realistic compromise is exactly one of them. Everything here assumes the
// sender may be hostile and asks a different question: what is the most damage a
// valid key can do?

// Sink is what a receiver does with an accepted verdict. The shield supplies
// this; the package deliberately knows nothing about blocklists or reputation,
// so it cannot grow the authority of what it applies.
type Sink interface {
	// Jail puts a source in the receiver's own jail, for the receiver's own
	// configured duration. No duration is passed, because a sending node does not
	// get to choose how long another operator punishes someone.
	Jail(source string)
	// Suspect lowers a source's standing by a clamped delta.
	Suspect(source string, weight float64)
	// Pardon releases a source.
	Pardon(source string)
	// Trusted reports whether the RECEIVING operator's own allow list covers this
	// source. Nothing a peer says can override it — which is also what stops a
	// compromised node from locking an operator out of their own fleet.
	Trusted(source string) bool
}

// Limits on what one peer may do per window. These are the blast radius.
const (
	// PerPeerBudget is how many verdicts from one origin node are applied per
	// window. A legitimate node under attack decides far fewer than this; a
	// compromised one trying to enumerate the internet into the fleet's jails
	// runs out immediately.
	PerPeerBudget = 600
	// budgetWindow is the accounting period.
	budgetWindow = time.Minute
	// MaxSuspectWeight clamps an inbound reputation delta. Unclamped, one message
	// could collapse a source's standing to an auto-jail — which is the jail
	// verdict wearing a different name, with none of the same accounting.
	MaxSuspectWeight = 0.3
	// maxPeers bounds the accounting table. Node names come from a message, so a
	// map keyed on them grows to whatever a peer chooses unless it is capped.
	maxPeers = 64
)

// Applier applies inbound verdicts under a per-peer budget.
type Applier struct {
	sink Sink

	mu      sync.Mutex
	windows map[string]*peerWindow

	applied, refused, overBudget int64
}

type peerWindow struct {
	start time.Time
	used  int
}

// NewApplier returns an applier writing to sink.
func NewApplier(s Sink) *Applier {
	return &Applier{sink: s, windows: make(map[string]*peerWindow, 8)}
}

// Apply enacts an authenticated message and returns how many verdicts it
// applied. The message must already have passed Open and the replay check —
// this function assumes authenticity and assumes nothing else.
func (a *Applier) Apply(m Message, now time.Time) int {
	if a == nil || a.sink == nil {
		return 0
	}
	n := 0
	for _, v := range m.Verdicts {
		if !a.claim(m.Node, now) {
			a.mu.Lock()
			a.overBudget++
			a.mu.Unlock()
			break // the rest of this batch is over budget too
		}
		// The receiving operator's own rules outrank every peer. A network they
		// declared trusted cannot be jailed by anyone else's verdict, and a
		// PARDON is still honoured because it only ever restores access.
		if v.Kind != KindPardon && a.sink.Trusted(v.Source) {
			a.mu.Lock()
			a.refused++
			a.mu.Unlock()
			continue
		}
		switch v.Kind {
		case KindJail:
			a.sink.Jail(v.Source)
		case KindSuspect:
			w := v.Weight
			if w > MaxSuspectWeight {
				w = MaxSuspectWeight
			}
			if w <= 0 {
				continue
			}
			a.sink.Suspect(v.Source, w)
		case KindPardon:
			a.sink.Pardon(v.Source)
		default:
			continue
		}
		n++
		a.mu.Lock()
		a.applied++
		a.mu.Unlock()
	}
	return n
}

// claim takes one unit of a peer's budget.
func (a *Applier) claim(node string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	w := a.windows[node]
	if w == nil {
		if len(a.windows) >= maxPeers {
			// The table is full of live peers. Refuse rather than evict: node names
			// are chosen by the sender, so an eviction policy is a way for one
			// compromised node to rotate names and reset its own budget forever.
			return false
		}
		w = &peerWindow{start: now}
		a.windows[node] = w
	}
	if now.Sub(w.start) >= budgetWindow {
		w.start = now
		w.used = 0
	}
	if w.used >= PerPeerBudget {
		return false
	}
	w.used++
	return true
}

// Stats returns the applier's counters: verdicts enacted, verdicts refused
// because the receiving operator trusts the source, and verdicts dropped
// because a peer exhausted its budget.
func (a *Applier) Stats() (applied, refused, overBudget int64) {
	if a == nil {
		return 0, 0, 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.applied, a.refused, a.overBudget
}
