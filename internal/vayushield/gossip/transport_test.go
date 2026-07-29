// SPDX-License-Identifier: Apache-2.0

package gossip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestTwoNodesShareAVerdict is the end-to-end shape: a decision on one node
// becomes a decision on another, which is the entire point of the package.
func TestTwoNodesShareAVerdict(t *testing.T) {
	key := testKey(t)
	rec := &recorder{}
	applier := NewApplier(rec)

	srv := httptest.NewServer(Handler(key, NewSeen(1024),
		func(m Message) int { return applier.Apply(m, time.Now()) }, time.Now))
	defer srv.Close()

	p := NewPusher(key, "edge-1", []string{srv.URL})
	p.Queue(Verdict{Kind: KindJail, Source: "198.51.100.9"})
	p.Queue(Verdict{Kind: KindPardon, Source: "203.0.113.4"})
	if n := p.Flush(context.Background(), time.Now()); n != 2 {
		t.Fatalf("flushed %d verdicts, want 2", n)
	}
	if len(rec.jailed) != 1 || rec.jailed[0] != "198.51.100.9" {
		t.Errorf("the jail verdict did not arrive: %v", rec.jailed)
	}
	if len(rec.pardoned) != 1 {
		t.Errorf("the pardon did not arrive: %v", rec.pardoned)
	}
	if sent, failed := p.Stats(); sent != 1 || failed != 0 {
		t.Errorf("push stats sent=%d failed=%d", sent, failed)
	}
}

// TestANodeWithADifferentSecretIsRefused — the gossip key is derived from the
// install secret, so this is what a fleet with a mismatched secret looks like:
// silent refusal. That is right for security and awful for diagnosis, which is
// why the posture report carries a row about it.
func TestANodeWithADifferentSecretIsRefused(t *testing.T) {
	good := testKey(t)
	bad, _ := DeriveKey("a-different-install-secret")
	rec := &recorder{}
	applier := NewApplier(rec)

	srv := httptest.NewServer(Handler(good, NewSeen(1024),
		func(m Message) int { return applier.Apply(m, time.Now()) }, time.Now))
	defer srv.Close()

	p := NewPusher(bad, "stranger", []string{srv.URL})
	p.Queue(Verdict{Kind: KindJail, Source: "198.51.100.9"})
	p.Flush(context.Background(), time.Now())

	if len(rec.jailed) != 0 {
		t.Error("a node holding a different secret got a verdict applied")
	}
	if _, failed := p.Stats(); failed != 1 {
		t.Error("the sender was not told its push was refused, so a misconfigured fleet looks " +
			"identical to a working one from the sending side too")
	}
}

// TestTheHandlerTellsAProberNothing — distinguishing "bad key" from "stale" from
// "replayed" hands an attacker an oracle for tuning their attempts. Every
// rejection is the same, and the operator learns the real reason from the
// counters instead.
func TestTheHandlerTellsAProberNothing(t *testing.T) {
	key := testKey(t)
	other, _ := DeriveKey("someone-else")
	now := time.Now()
	h := Handler(key, NewSeen(1024), func(Message) int { return 0 }, func() time.Time { return now })

	m := Message{Node: "edge-1", Issued: now.Unix(),
		Verdicts: []Verdict{{Kind: KindJail, Source: "198.51.100.9"}}}
	forged, _ := Seal(other, m)
	stale := Message{Node: "edge-1", Issued: now.Add(-2 * MaxAge).Unix(), Verdicts: m.Verdicts}
	staleBytes, _ := Seal(key, stale)
	good, _ := Seal(key, m)

	type probe struct {
		name   string
		body   []byte
		issued int64
	}
	probes := []probe{
		{"forged", forged, m.Issued},
		{"stale", staleBytes, stale.Issued},
		{"garbage", []byte("not a message at all, but long enough to pass the length floor"), m.Issued},
		{"replayed", good, m.Issued}, // sent twice below
	}

	// Prime the replay cache with the good message so the last probe is a replay.
	first := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(string(good)))
	req.Header.Set(HeaderIssued, strconv.FormatInt(m.Issued, 10))
	h.ServeHTTP(first, req)
	if first.Code != http.StatusNoContent {
		t.Fatalf("setup: an authentic message got %d", first.Code)
	}

	var codes []int
	for _, pr := range probes {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(string(pr.body)))
		r.Header.Set(HeaderIssued, strconv.FormatInt(pr.issued, 10))
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s probe got %d, want a uniform 403", pr.name, w.Code)
		}
		if w.Body.Len() != 0 {
			t.Errorf("%s probe got a body %q — the reason for refusal is an oracle", pr.name, w.Body)
		}
		codes = append(codes, w.Code)
	}
	for i := range codes {
		if codes[i] != codes[0] {
			t.Error("rejection codes differ between failure modes, so a prober can tell which " +
				"part of the check they failed and tune the next attempt")
		}
	}
}

// TestAStrangerCannotFillTheReplayCache — the cache is fixed-memory, so filling
// it is a denial of service. The replay check therefore runs only AFTER
// authentication, and this pins that ordering.
func TestAStrangerCannotFillTheReplayCache(t *testing.T) {
	key := testKey(t)
	other, _ := DeriveKey("stranger")
	now := time.Now()
	seen := NewSeen(64)
	h := Handler(key, seen, func(Message) int { return 0 }, func() time.Time { return now })

	// Spread across many source addresses so the per-source ingress limit is not
	// what stops them — this test is about the replay cache, not that limit.
	for i := 0; i < 5_000; i++ {
		m := Message{Node: "stranger", Issued: now.Unix(),
			Verdicts: []Verdict{{Kind: KindJail, Source: "198.51.100." + strconv.Itoa(i%256)}}}
		b, _ := Seal(other, m) // a fresh random nonce every time
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(string(b)))
		r.RemoteAddr = "203.0.113." + strconv.Itoa(i%200) + ":9000"
		r.Header.Set(HeaderIssued, strconv.FormatInt(m.Issued, 10))
		h.ServeHTTP(w, r)
	}
	if n := seen.Len(); n != 0 {
		t.Errorf("an unauthenticated caller wrote %d entries into the replay cache — filling it "+
			"is then a denial of service any stranger can mount, and a full cache refuses the "+
			"real peers", n)
	}
	// A legitimate peer still gets through afterwards.
	m := Message{Node: "edge-1", Issued: now.Unix(),
		Verdicts: []Verdict{{Kind: KindJail, Source: "192.0.2.1"}}}
	b, _ := Seal(key, m)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(string(b)))
	r.RemoteAddr = "198.51.100.20:9000" // a real peer, not one of the flooders
	r.Header.Set(HeaderIssued, strconv.FormatInt(m.Issued, 10))
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("a legitimate peer got %d after the flood", w.Code)
	}
}

// TestTheEndpointCarriesItsOwnRateLimit.
//
// This route is exempt from the shield's gates — like every machine-protocol
// endpoint, because a peer cannot solve a browser challenge — and it does real
// work for an UNAUTHENTICATED caller: a 64 KiB read and an AEAD open, before it
// can possibly know the caller is a stranger. An exemption without a limit of
// its own is not "it carries its own rate limit", it is an unmetered compute
// sink, and it was one.
func TestTheEndpointCarriesItsOwnRateLimit(t *testing.T) {
	key := testKey(t)
	other, _ := DeriveKey("stranger")
	now := time.Now()
	h := Handler(key, NewSeen(1024), func(Message) int { return 0 }, func() time.Time { return now })

	accepted := 0
	for i := 0; i < IngressPerMinute*20; i++ {
		m := Message{Node: "stranger", Issued: now.Unix(),
			Verdicts: []Verdict{{Kind: KindJail, Source: "198.51.100.1"}}}
		b, _ := Seal(other, m)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(string(b)))
		r.RemoteAddr = "203.0.113.99:5555"
		r.Header.Set(HeaderIssued, strconv.FormatInt(m.Issued, 10))
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			accepted++
		}
	}
	if accepted != 0 {
		t.Errorf("%d forged pushes were processed", accepted)
	}

	// A real peer flushes once a second, so its steady state is 60/minute and it
	// must be nowhere near the ceiling.
	for i := 0; i < 60; i++ {
		m := Message{Node: "edge-2", Issued: now.Unix(),
			Verdicts: []Verdict{{Kind: KindJail, Source: "192.0.2.1"}}}
		b, _ := Seal(key, m)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(string(b)))
		r.RemoteAddr = "203.0.113.50:5555"
		r.Header.Set(HeaderIssued, strconv.FormatInt(m.Issued, 10))
		h.ServeHTTP(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("a peer pushing at its normal once-a-second cadence was refused on push %d "+
				"(%d) — the limit is tighter than the protocol it is protecting", i, w.Code)
		}
	}

	// The source is the socket address, never a forwarded header. Peers are
	// operator-configured and reached directly, so there is no proxy whose header
	// would mean anything — and trusting one would let a caller mint unlimited
	// identities and walk straight through the limit above.
	blocked := 0
	for i := 0; i < IngressPerMinute*3; i++ {
		m := Message{Node: "stranger", Issued: now.Unix(),
			Verdicts: []Verdict{{Kind: KindJail, Source: "198.51.100.1"}}}
		b, _ := Seal(other, m)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(string(b)))
		r.RemoteAddr = "203.0.113.77:5555"
		r.Header.Set("X-Forwarded-For", "10.0.0."+strconv.Itoa(i%250))
		r.Header.Set("CF-Connecting-IP", "10.1.0."+strconv.Itoa(i%250))
		r.Header.Set(HeaderIssued, strconv.FormatInt(m.Issued, 10))
		h.ServeHTTP(w, r)
		if w.Code == http.StatusForbidden {
			blocked++
		}
	}
	if blocked < IngressPerMinute {
		t.Errorf("only %d of %d rotating-header pushes were refused — a forwarded header is "+
			"being used as the identity, so one caller mints unlimited identities and the "+
			"limit does not exist", blocked, IngressPerMinute*3)
	}
}

// TestTheIngressTableIsFixedMemory — the key is the caller's address, so a table
// that grows with traffic is a table an attacker sizes: the defence against a
// flood would become a second way to mount one.
func TestTheIngressTableIsFixedMemory(t *testing.T) {
	ing := newIngress()
	now := time.Now()
	for i := 0; i < maxIngressSources*20; i++ {
		ing.allow("10."+strconv.Itoa(i/65536%256)+"."+strconv.Itoa(i/256%256)+"."+strconv.Itoa(i%256), now)
	}
	ing.mu.Lock()
	n := len(ing.counts)
	ing.mu.Unlock()
	if n > maxIngressSources {
		t.Errorf("the ingress table holds %d sources against a %d cap", n, maxIngressSources)
	}
	// The window rolls, so a burst does not wedge the endpoint forever.
	if !ing.allow("198.51.100.1", now.Add(2*time.Minute)) {
		t.Error("the table never clears, so the endpoint is refused for the life of the process")
	}
}

// TestAnOversizedBodyIsStoppedAtTheSocket — reading a peer's whole body before
// checking its size is how a "bounded" message becomes unbounded memory.
func TestAnOversizedBodyIsStoppedAtTheSocket(t *testing.T) {
	key := testKey(t)
	now := time.Now()
	h := Handler(key, NewSeen(64), func(Message) int { return 0 }, func() time.Time { return now })

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(strings.Repeat("x", 8<<20)))
	r.Header.Set(HeaderIssued, strconv.FormatInt(now.Unix(), 10))
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("an 8 MiB body got %d", w.Code)
	}
}

// TestAnUnreachablePeerDoesNotStallTheOthers — a peer that has fallen over is
// the LIKELY state during an attack, so a node that blocks waiting for it has
// been taken down by proxy. Peers are pushed concurrently and the client carries
// a short timeout.
func TestAnUnreachablePeerDoesNotStallTheOthers(t *testing.T) {
	key := testKey(t)
	rec := &recorder{}
	applier := NewApplier(rec)
	var mu sync.Mutex

	live := httptest.NewServer(Handler(key, NewSeen(64), func(m Message) int {
		mu.Lock()
		defer mu.Unlock()
		return applier.Apply(m, time.Now())
	}, time.Now))
	defer live.Close()

	// A peer that accepts the connection and never answers. Released at the end
	// of the test rather than slept, so Close does not wait on it.
	release := make(chan struct{})
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer stalled.Close()
	defer close(release)

	p := NewPusher(key, "edge-1", []string{stalled.URL, live.URL})
	p.Queue(Verdict{Kind: KindJail, Source: "198.51.100.9"})

	done := make(chan struct{})
	go func() {
		p.Flush(context.Background(), time.Now())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a flush blocked on an unresponsive peer for longer than the client timeout — " +
			"one dead node stalls every push, which is the attack taking this node down by proxy")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(rec.jailed) != 1 {
		t.Error("the reachable peer did not receive the verdict while another was unresponsive")
	}
}

// TestTheOutboundQueueIsBounded — the moment verdicts are produced fastest is
// the moment the node can least afford unbounded memory. Dropping the excess is
// correct: peers reach the same conclusions from the same traffic, so a dropped
// verdict costs a little speed and nothing else.
func TestTheOutboundQueueIsBounded(t *testing.T) {
	p := NewPusher(testKey(t), "edge-1", []string{"http://127.0.0.1:1"})
	for i := 0; i < MaxVerdicts*50; i++ {
		p.Queue(Verdict{Kind: KindJail, Source: "198.51.100." + strconv.Itoa(i%256)})
	}
	p.mu.Lock()
	n := len(p.pending)
	p.mu.Unlock()
	if n > MaxVerdicts {
		t.Errorf("the outbound queue holds %d verdicts against a %d cap — under the flood that "+
			"produces them, this grows without limit", n, MaxVerdicts)
	}
}

// TestASingleNodeInstallDoesNothing — the overwhelming majority of installs have
// no peers and must pay nothing for this.
func TestASingleNodeInstallDoesNothing(t *testing.T) {
	p := NewPusher(testKey(t), "solo", nil)
	p.Queue(Verdict{Kind: KindJail, Source: "198.51.100.9"})
	if n := p.Flush(context.Background(), time.Now()); n != 0 {
		t.Errorf("a peerless install flushed %d verdicts", n)
	}
	if p.Peers() != 0 {
		t.Error("a peerless install reported peers")
	}
	var nilP *Pusher
	nilP.Queue(Verdict{Kind: KindJail, Source: "1.2.3.4"})
	if n := nilP.Flush(context.Background(), time.Now()); n != 0 {
		t.Errorf("a nil pusher flushed %d", n)
	}
}
