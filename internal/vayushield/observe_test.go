// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/vayushield/challenge"
)

// Observe-only mode is not the cheap primitive it looks like. "Decide already
// returns an Action" is true of exactly one of the eight enforcement points: the
// blocklist, the reputation jail, load shedding, fair shedding, the rate limiter
// and Sovereign Surge are all inline early returns that never call Decide. So
// the mode has to be threaded through every one of them, and a test that only
// exercises the classification ladder would pass while six gates still enforced.

// observeHarness builds a shield whose client IP is fixed and whose limits trip
// immediately, plus a recorder for anything pushed to the kernel.
type observeHarness struct {
	m        *Manager
	offloads []string
}

func newObserveHarness(t *testing.T, observe bool) *observeHarness {
	t.Helper()
	h := &observeHarness{}
	h.m = New(Config{
		Enabled:   true,
		Signer:    challenge.NewSigner([]byte("s")),
		ClientIP:  func(*http.Request) string { return "203.0.113.7:1234" },
		OffloadFn: func(ip string, _ time.Duration) { h.offloads = append(h.offloads, ip) },
	})
	h.m.ApplySettings(Settings{
		Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8,
		RateLimit: true, RatePerMinute: 1, Burst: 1,
		LoadShed: true, MaxInFlight: 1,
		AutoBlock: true, AutoBlockJailMinutes: 10,
		ObserveOnly: observe,
	})
	return h
}

// hit sends one request and returns the status code the visitor saw.
func (h *observeHarness) hit(path string) int {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) observe-probe")
	h.m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	return rec.Code
}

// TestObserveModeEnforcesNothing — the defining property. Every request must be
// served, however hard the same traffic is refused with the mode off.
func TestObserveModeEnforcesNothing(t *testing.T) {
	enforcing := newObserveHarness(t, false)
	refused := 0
	for i := 0; i < 40; i++ {
		if enforcing.hit("/article") != http.StatusOK {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("setup failed: with enforcement ON this traffic was never refused, so the " +
			"observe comparison below proves nothing")
	}

	observing := newObserveHarness(t, true)
	for i := 0; i < 40; i++ {
		if code := observing.hit("/article"); code != http.StatusOK {
			t.Fatalf("request %d got %d in observe-only mode — the mode is enforcing", i, code)
		}
	}
	if total := sum(observing.m.WouldHave()); total == 0 {
		t.Error("observe mode served everything and counted nothing — it is indistinguishable " +
			"from switching the shield off, which is the one thing it must not be")
	}
}

func sum(a [gateCount]int64) int64 {
	var n int64
	for _, v := range a {
		n += v
	}
	return n
}

// TestObserveModeNeverTouchesTheKernel is the sharpest correctness point of the
// whole mode. Every other verdict is an in-memory decision this middleware can
// decline to act on, and a request that was let through is still served. A
// kernel ban happens OUTSIDE this process and cannot be un-dropped — so an
// "observe only" mode that still installed nftables bans would be enforcing,
// silently, in the one layer the operator cannot see from the panel.
func TestObserveModeNeverTouchesTheKernel(t *testing.T) {
	enforcing := newObserveHarness(t, false)
	for i := 0; i < 200; i++ {
		enforcing.hit("/article")
	}
	if len(enforcing.offloads) == 0 {
		t.Skip("enforcement did not escalate to the kernel offload in this run — nothing to compare")
	}

	observing := newObserveHarness(t, true)
	for i := 0; i < 200; i++ {
		observing.hit("/article")
	}
	if len(observing.offloads) != 0 {
		t.Errorf("observe mode pushed %d ban(s) to the kernel: %v — a dropped packet cannot be "+
			"un-dropped, so this is enforcement in the layer the panel cannot see",
			len(observing.offloads), observing.offloads)
	}
}

// TestObserveModeStillEscalatesInMemory — the counters have to reflect what a
// real rollout would look like, including a source that escalates from
// rate-limited to jailed. Suppressing the in-memory consequences too would make
// every request look like a first offence and undercount the very outcome the
// operator turned the mode on to see.
func TestObserveModeStillEscalatesInMemory(t *testing.T) {
	h := newObserveHarness(t, true)
	for i := 0; i < 60; i++ {
		h.hit("/article")
	}
	w := h.m.WouldHave()
	if w[GateRateLimit] == 0 {
		t.Fatalf("the rate limiter never fired: %v", w)
	}
	if w[GateBlocklist] == 0 && w[GateReputation] == 0 {
		t.Errorf("no escalation was observed after 60 refused requests: %v — the in-memory "+
			"jail is not being updated, so the counts understate a real rollout", w)
	}
}

// concurrentHits saturates the in-flight cap for real. The load-shed gate can
// ONLY fire while requests overlap, so a sequential loop never reaches it — an
// earlier version of this test looped 50 times in series, never tripped the
// gate, and let two mutations of it survive untouched.
func (h *observeHarness) concurrentHits(n int) []int {
	release := make(chan struct{})
	codes := make([]int, n)
	var wg sync.WaitGroup
	handler := h.m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release // hold the slot until every request is in flight
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/article", nil)
			req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) observe-probe")
			handler.ServeHTTP(rec, req)
			codes[i] = rec.Code
		}(i)
	}
	// Give the goroutines a moment to pile up against the cap before letting go.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	return codes
}

// TestLoadShedIsObserved — the shed gate is one of the six that never reach
// Decide, and it is the only one whose observe path has to do something other
// than "skip the return": a request that FAILED to acquire a slot proceeds
// anyway, so there is no slot to release afterwards.
func TestLoadShedIsObserved(t *testing.T) {
	// With enforcement on, saturating a cap of 1 must produce 503s, or the
	// comparison below proves nothing.
	enforcing := newObserveHarness(t, false)
	enforcing.m.ApplySettings(Settings{
		Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8,
		LoadShed: true, MaxInFlight: 1,
	})
	shed := 0
	for _, c := range enforcing.concurrentHits(12) {
		if c == http.StatusServiceUnavailable {
			shed++
		}
	}
	if shed == 0 {
		t.Fatal("setup failed: a cap of 1 with 12 concurrent requests shed nothing, so the " +
			"load-shed gate is not being exercised at all")
	}

	observing := newObserveHarness(t, true)
	observing.m.ApplySettings(Settings{
		Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8,
		LoadShed: true, MaxInFlight: 1, ObserveOnly: true,
	})
	for i, c := range observing.concurrentHits(12) {
		if c != http.StatusOK {
			t.Errorf("concurrent request %d got %d in observe mode — the load-shed gate is enforcing", i, c)
		}
	}
	if n := observing.m.WouldHave()[GateLoadShed]; n == 0 {
		t.Error("the load-shed gate shed nothing and counted nothing in observe mode")
	}

	// A request that never acquired a slot must not release one, and a request
	// that DID acquire one must release it.
	//
	// Asserting only the first half is not enough, and a surviving mutant proved
	// it: Release() has a floor at zero, so a handful of spurious releases from
	// shed requests silently absorb a leaked slot and the gauge reads 0 either
	// way. The discriminating case is a cap generous enough that every request is
	// admitted and none is shed — then nothing can mask a missing release.
	for _, h := range []*observeHarness{observing, enforcing} {
		h.m.ApplySettings(Settings{
			Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8,
			LoadShed: true, MaxInFlight: 64, ObserveOnly: h.m.Observing(),
		})
		for i, c := range h.concurrentHits(12) {
			if c != http.StatusOK {
				t.Fatalf("request %d got %d under a cap of 64 with 12 concurrent — nothing "+
					"should have been shed", i, c)
			}
		}
		if n := h.m.Status().InFlight; n != 0 {
			t.Errorf("in-flight gauge = %d after 12 admitted requests all completed (observe=%v), "+
				"want 0 — slots are not being released, so the cap fills permanently",
				n, h.m.Observing())
		}
	}
}

// TestGateNamesCoverEveryGate — the names are metric labels. A missing or
// misaligned one silently relabels an operator's dashboard, which is worse than
// having no label at all.
func TestGateNamesCoverEveryGate(t *testing.T) {
	seen := map[string]bool{}
	for i, n := range GateNames {
		if strings.TrimSpace(n) == "" {
			t.Errorf("gate %d has no name", i)
		}
		if seen[n] {
			t.Errorf("duplicate gate name %q — two gates would share one metric series", n)
		}
		seen[n] = true
	}
	if len(GateNames) != int(gateCount) {
		t.Errorf("GateNames has %d entries for %d gates", len(GateNames), gateCount)
	}
}

// TestObserveOffIsTheDefault — a shield that ships observing is a shield that
// ships off. The zero value of Settings must enforce.
func TestObserveOffIsTheDefault(t *testing.T) {
	m := New(Config{Enabled: true})
	m.ApplySettings(Settings{Enabled: true})
	if m.Observing() {
		t.Error("a Settings value with ObserveOnly unset engaged observe mode")
	}
	if m.Status().ObserveOnly {
		t.Error("Status reports observe mode with it unset")
	}
}

// TestOneSolvedProofAdmitsOneClient — the end-to-end shape of the replay, at the
// level an attacker would actually use it.
//
// VerifyPoW is stateless, so before the redeemer existed a proof was bound to
// nobody until it was redeemed. An attacker solves once, hands the pair to N
// hosts, and each redeems it for a session bound to its OWN address: the cost of
// entry for a whole swarm collapses from N proofs to one.
func TestOneSolvedProofAdmitsOneClient(t *testing.T) {
	signer := challenge.NewSigner([]byte("secret"))
	m := New(Config{Enabled: true, Signer: signer,
		ClientIP: func(r *http.Request) string { return r.RemoteAddr }})
	m.ApplySettings(Settings{Enabled: true})

	pow, err := signer.IssuePoW(challenge.DefaultDifficulty, 5*time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	nonce := solveFor(t, pow)

	// First host redeems it.
	r1 := httptest.NewRequest(http.MethodPost, "/verify", nil)
	r1.RemoteAddr = "203.0.113.10:1111"
	if _, ok := m.VerifyPoW(r1, pow, nonce); !ok {
		t.Fatal("the first redemption of a valid solved proof was refused")
	}

	// The swarm replays the SAME pair from other addresses.
	admitted := 0
	for i := 0; i < 25; i++ {
		r := httptest.NewRequest(http.MethodPost, "/verify", nil)
		r.RemoteAddr = "198.51.100." + strconv.Itoa(i+1) + ":2222"
		if _, ok := m.VerifyPoW(r, pow, nonce); ok {
			admitted++
		}
	}
	if admitted != 0 {
		t.Errorf("%d of 25 other hosts were admitted on a proof they did not solve — one "+
			"proof of work is buying an entire swarm its way in", admitted)
	}
}

// solveFor brute-forces a nonce for the issued proof. The difficulty is the
// default (cheap by design — the cost is meant to be trivial for one client and
// meaningful only in aggregate), so this is fast.
func solveFor(t *testing.T, p challenge.PoW) string {
	t.Helper()
	for i := 0; i < 1<<24; i++ {
		n := strconv.Itoa(i)
		if challenge.SolutionValid(p.Salt, n, p.Difficulty) {
			return n
		}
	}
	t.Fatal("could not solve the proof")
	return ""
}
