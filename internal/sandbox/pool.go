package sandbox

import (
	"context"
	"fmt"
	"sync"

	"github.com/johalputt/vayupress/internal/logging"
)

// Pool manages a fixed set of SubprocessPlugin instances for a single Manifest.
// Each pool member is an independent process; Invoke round-robins across members.
type Pool struct {
	mu      sync.Mutex
	members []*SubprocessPlugin
	next    int
}

// NewPool starts n subprocess workers for the given manifest.
// Call Shutdown when done.
func NewPool(m Manifest, n int) (*Pool, error) {
	if n <= 0 {
		n = 1
	}
	p := &Pool{members: make([]*SubprocessPlugin, n)}
	for i := 0; i < n; i++ {
		sp := NewSubprocessPlugin(m)
		p.mu.Lock()
		if err := sp.start(); err != nil {
			p.mu.Unlock()
			// Shutdown whatever started successfully before returning.
			for j := 0; j < i; j++ {
				p.members[j].Shutdown()
			}
			return nil, fmt.Errorf("sandbox pool %s[%d]: %w", m.Name, i, err)
		}
		p.mu.Unlock()
		p.members[i] = sp
	}
	logging.LogInfo("sandbox", fmt.Sprintf("pool started: plugin=%s workers=%d", m.Name, n))
	return p, nil
}

// Invoke dispatches the hook to the next available pool member (round-robin).
func (p *Pool) Invoke(ctx context.Context, hook string, payload map[string]interface{}) error {
	p.mu.Lock()
	idx := p.next % len(p.members)
	p.next++
	member := p.members[idx]
	p.mu.Unlock()
	return member.Invoke(ctx, hook, payload)
}

// Stats returns a snapshot of every pool member's state.
func (p *Pool) Stats() []SubprocessStats {
	out := make([]SubprocessStats, len(p.members))
	for i, m := range p.members {
		out[i] = m.Stats()
	}
	return out
}

// Shutdown terminates all subprocess workers in the pool.
func (p *Pool) Shutdown() {
	for _, m := range p.members {
		m.Shutdown()
	}
	logging.LogInfo("sandbox", fmt.Sprintf("pool shutdown: %d workers stopped", len(p.members)))
}
