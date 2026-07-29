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

// IngressPerMinute is how many pushes ONE source address may make per minute.
//
// A real peer flushes once a second, so 60 is its steady state; the allowance is
// double that so a burst after a network hiccup is not refused. The point is the
// ceiling, not the number: this route is exempt from the shield's own gates —
// like every machine-protocol endpoint, because a peer cannot solve a browser
// challenge — and an exemption without a limit of its own is not "it carries its
// own rate limit", it is an unmetered route.
const IngressPerMinute = 120

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
	ing := newIngress()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// Rate-limited by source address BEFORE any work — before the body is
		// read and long before an AEAD open. Everything below is cheap per
		// request and unbounded in aggregate, which is the shape of a compute
		// sink rather than an endpoint. Same uniform 403, so this is not an
		// oracle either.
		if !ing.allow(sourceOf(r), now()) {
			w.WriteHeader(http.StatusForbidden)
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

// Pending reports how many verdicts are queued but not yet pushed. Exported for
// tests that need to assert a node queued NOTHING — an observe-only install must
// not be enforcing on its peers.
func (p *Pusher) Pending() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending)
}

// Peers returns how many peers are configured.
func (p *Pusher) Peers() int {
	if p == nil {
		return 0
	}
	return len(p.peers)
}

// ── Ingress limiting ──────────────────────────────────────────────────────────

// ingress is a fixed-memory per-source request counter.
//
// Fixed memory rather than a map that grows: the key is the caller's address, so
// a growing table is a table an attacker sizes — which would make the defence
// against a flood into a second way to mount one.
type ingress struct {
	mu     sync.Mutex
	counts map[string]int
	window int64
}

func newIngress() *ingress { return &ingress{counts: make(map[string]int, 8)} }

// maxIngressSources bounds the table. Beyond it every new source is refused for
// the rest of the window — which under a distributed flood also refuses some
// real peers, and that is the right trade: a peer retries a second later, and
// the alternative is unbounded memory chosen by the attacker.
const maxIngressSources = 1024

func (i *ingress) allow(src string, now time.Time) bool {
	if src == "" {
		return false
	}
	win := now.Unix() / 60
	i.mu.Lock()
	defer i.mu.Unlock()
	if win != i.window {
		i.window = win
		clear(i.counts)
	}
	n, known := i.counts[src]
	if !known && len(i.counts) >= maxIngressSources {
		return false
	}
	if n >= IngressPerMinute {
		return false
	}
	i.counts[src] = n + 1
	return true
}

// sourceOf is the caller's address as the socket sees it.
//
// Deliberately NOT any forwarded header. Peer addresses are operator-configured
// and reached directly, so there is no proxy whose header would be meaningful —
// and trusting one here would let any caller mint an unlimited number of
// identities and walk straight through the limit above.
func sourceOf(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i > 0 && !strings.Contains(host[i:], "]") {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}
