// SPDX-License-Identifier: Apache-2.0

package gossip

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// transport.go — moving a sealed message between nodes.
//
// Direct push to configured peers, never a relay. See the package doc for why
// there is no hop count: a relaying mesh turns one message into N and a loop in
// the peer graph is a self-inflicted flood on machines that are already under
// attack.

// HeaderIssued carries the message timestamp, which is bound into the seal as
// additional data. It is a header rather than part of the payload so the
// receiver can bound freshness before decrypting — but it is authenticated, so
// editing it fails the tag rather than extending the message.
const HeaderIssued = "X-Vayushield-Issued"

// Path is the endpoint a node exposes to its peers.
const Path = "/__vayushield/gossip"

// Handler returns the HTTP handler that receives peer verdicts.
//
// It is deliberately terse in what it tells an unauthenticated caller. Every
// rejection is the same 403 with no body: distinguishing "bad key" from "stale"
// from "replayed" would hand a prober an oracle for tuning their attempts, and
// the operator learns the real reason from the counters and the log instead.
func Handler(key [32]byte, seen *Seen, apply func(Message) int, now func() time.Time) http.Handler {
	if now == nil {
		now = time.Now
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// Read at most one message's worth. http.MaxBytesReader caps the read
		// itself, so a peer streaming gigabytes is stopped at the socket rather
		// than after it has been buffered.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxMessageBytes+1))
		if err != nil || len(body) > MaxMessageBytes {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		issued, err := strconv.ParseInt(r.Header.Get(HeaderIssued), 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		m, err := Open(key, body, issued, now())
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		// The replay check happens AFTER authentication, so an unauthenticated
		// caller can never write to the cache — otherwise filling it would be a
		// denial of service any stranger could mount.
		if !seen.Fresh(m.Nonce, now()) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if apply != nil {
			apply(m)
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// Pusher sends verdicts to peers.
type Pusher struct {
	key   [32]byte
	node  string
	peers []string
	cl    *http.Client

	mu      sync.Mutex
	pending []Verdict

	sent, failed int64
}

// NewPusher returns a pusher for the given peer base URLs.
//
// The client carries a short timeout on purpose. A peer that has fallen over is
// the LIKELY state during an attack, and a node that blocks waiting for it is a
// node the attack has taken down by proxy.
func NewPusher(key [32]byte, node string, peers []string) *Pusher {
	return &Pusher{
		key:   key,
		node:  node,
		peers: peers,
		cl:    &http.Client{Timeout: 3 * time.Second},
	}
}

// Queue records a verdict for the next flush.
//
// Batched rather than sent per verdict, because the moment verdicts are being
// produced fastest is the moment the node can least afford N outbound requests
// per local decision. The queue is bounded: under a flood it fills, and dropping
// the excess is correct — the peers will reach the same conclusions themselves
// from the same traffic, so a dropped verdict costs a little speed and nothing
// else.
func (p *Pusher) Queue(v Verdict) {
	if p == nil || len(p.peers) == 0 || !v.Kind.Valid() || v.Source == "" {
		return
	}
	p.mu.Lock()
	if len(p.pending) < MaxVerdicts {
		p.pending = append(p.pending, v)
	}
	p.mu.Unlock()
}

// Flush seals the queued verdicts and pushes them to every peer. It returns the
// number of verdicts sent.
func (p *Pusher) Flush(ctx context.Context, now time.Time) int {
	if p == nil || len(p.peers) == 0 {
		return 0
	}
	p.mu.Lock()
	batch := p.pending
	p.pending = nil
	p.mu.Unlock()
	if len(batch) == 0 {
		return 0
	}

	m := Message{Node: p.node, Issued: now.Unix(), Verdicts: batch}
	sealed, err := Seal(p.key, m)
	if err != nil {
		return 0
	}
	issued := strconv.FormatInt(m.Issued, 10)

	// Peers are pushed to concurrently. Serially, one unreachable peer delays
	// every other by the full timeout, and with a handful of peers that is longer
	// than the freshness window — the batch would arrive already stale.
	var wg sync.WaitGroup
	for _, peer := range p.peers {
		wg.Add(1)
		go func(peer string) {
			defer wg.Done()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				strings.TrimSuffix(peer, "/")+Path, bytes.NewReader(sealed))
			if err != nil {
				p.mu.Lock()
				p.failed++
				p.mu.Unlock()
				return
			}
			req.Header.Set(HeaderIssued, issued)
			req.Header.Set("Content-Type", "application/octet-stream")
			resp, err := p.cl.Do(req)
			p.mu.Lock()
			if err != nil || resp.StatusCode >= 300 {
				p.failed++
			} else {
				p.sent++
			}
			p.mu.Unlock()
			if resp != nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
				_ = resp.Body.Close()
			}
		}(peer)
	}
	wg.Wait()
	return len(batch)
}

// Stats reports successful and failed pushes.
func (p *Pusher) Stats() (sent, failed int64) {
	if p == nil {
		return 0, 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sent, p.failed
}

// Peers returns how many peers are configured.
func (p *Pusher) Peers() int {
	if p == nil {
		return 0
	}
	return len(p.peers)
}
