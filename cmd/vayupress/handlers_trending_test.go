package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// resetTrendingState clears the package-level trending memo between tests (the
// cache is process-global by design).
func resetTrendingState() {
	trendingMu.Lock()
	trendingCache = nil
	trendingExpiry = time.Time{}
	trendingComputing = false
	trendingGen = 0
	trendingMu.Unlock()
}

// TestTrendingSingleFlightServesWhileComputing proves the fix for the recurring
// post-restart /api/trending herd: when a recompute is already in flight, a
// concurrent request is served IMMEDIATELY (from the cheap warming payload on a
// cold start) and must NOT start its own compute or clear the single-flight
// guard. Only one goroutine ever runs the heavy trending queries.
func TestTrendingSingleFlightServesWhileComputing(t *testing.T) {
	resetTrendingState()
	t.Cleanup(resetTrendingState)

	// Simulate a compute already owned by another goroutine.
	trendingMu.Lock()
	trendingComputing = true
	trendingMu.Unlock()

	a := &App{} // nil DB/analytics → warming payload is empty-but-enabled
	rec := httptest.NewRecorder()
	a.handleTrendingJSON(rec, httptest.NewRequest(http.MethodGet, "/api/trending", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var p trendingPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if !p.Enabled {
		t.Error("warming payload should be enabled so the widget still renders")
	}

	trendingMu.Lock()
	computing, cache := trendingComputing, trendingCache
	trendingMu.Unlock()
	if !computing {
		t.Error("a non-owning request cleared the single-flight guard")
	}
	if cache != nil {
		t.Error("a non-owning request must not populate the cache")
	}
}

// TestTrendingServesFreshCache proves a fresh memo is served directly without a
// recompute.
func TestTrendingServesFreshCache(t *testing.T) {
	resetTrendingState()
	t.Cleanup(resetTrendingState)

	fresh := trendingPayload{
		Enabled: true,
		Pinned:  []trendingItem{{Slug: "pinned-x", Title: "X"}},
		Windows: map[string][]trendingItem{"7": {{Slug: "fresh-hit", Title: "Fresh"}}, "30": {}},
	}
	trendingMu.Lock()
	trendingCache = &fresh
	trendingExpiry = time.Now().Add(time.Hour)
	trendingMu.Unlock()

	a := &App{}
	rec := httptest.NewRecorder()
	a.handleTrendingJSON(rec, httptest.NewRequest(http.MethodGet, "/api/trending", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "fresh-hit") {
		t.Errorf("expected the fresh cached payload to be served; got %s", rec.Body.String())
	}
}

// TestTrendingConcurrentColdRequestsRaceClean fires a burst of concurrent cold
// requests (the post-restart scenario). All must return 200 with an enabled
// payload, with no panic and no data race — the race detector validates the
// mutex discipline around the shared memo and single-flight guard.
func TestTrendingConcurrentColdRequestsRaceClean(t *testing.T) {
	resetTrendingState()
	t.Cleanup(resetTrendingState)

	a := &App{} // nil DB/analytics → compute + warming return empty-but-enabled
	const n = 64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			a.handleTrendingJSON(rec, httptest.NewRequest(http.MethodGet, "/api/trending", nil))
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", rec.Code)
			}
		}()
	}
	wg.Wait()
}

// TestTrendingEmptyPayloadIsNeverCached is the regression guard for the widget
// showing on some page loads and not others. The script hides the section when a
// payload has nothing to show, so such a payload must never be storable — if a
// reader cached one during a cold start they kept getting the hidden widget from
// their own browser cache for minutes, across every post they opened.
func TestTrendingEmptyPayloadIsNeverCached(t *testing.T) {
	a := &App{}
	cases := []struct {
		name      string
		payload   trendingPayload
		wantStore bool // true => must be publicly cacheable
	}{
		{
			name: "has content",
			payload: trendingPayload{Enabled: true,
				Pinned:  []trendingItem{{Slug: "a", Title: "A"}},
				Windows: map[string][]trendingItem{"7": {{Slug: "b"}}, "30": {}}},
			wantStore: true,
		},
		{
			name:      "empty",
			payload:   trendingPayload{Enabled: true, Windows: map[string][]trendingItem{"7": {}, "30": {}}},
			wantStore: false,
		},
		{
			name: "degraded warming payload with content",
			payload: trendingPayload{Enabled: true, degraded: true,
				Pinned:  []trendingItem{{Slug: "a"}},
				Windows: map[string][]trendingItem{"7": {{Slug: "b"}}}},
			wantStore: false,
		},
		{
			name:      "feature disabled is a stable answer",
			payload:   trendingPayload{Enabled: false, Windows: map[string][]trendingItem{}},
			wantStore: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			a.writeTrending(rr, httptest.NewRequest("GET", "/api/trending", nil), tc.payload)
			cc := rr.Header().Get("Cache-Control")
			if tc.wantStore && !strings.Contains(cc, "max-age=300") {
				t.Errorf("Cache-Control = %q, want the public short cache", cc)
			}
			if !tc.wantStore && !strings.Contains(cc, "no-store") {
				t.Errorf("Cache-Control = %q, want no-store so it is never pinned", cc)
			}
		})
	}
}

// TestTrendingWarmingIgnoresClientDisconnect: the warming payload must not be
// built on the request's context — a reader navigating away mid-fetch would
// cancel the queries and produce an empty widget.
func TestTrendingWarmingIgnoresClientDisconnect(t *testing.T) {
	a := &App{}
	req := httptest.NewRequest("GET", "/api/trending", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel() // client is already gone
	req = req.WithContext(ctx)
	// Must still return a well-formed payload rather than panicking or blocking.
	got := a.trendingWarming(req)
	if !got.Enabled {
		t.Error("warming payload should still be enabled")
	}
	if !got.degraded {
		t.Error("warming payload must be marked degraded so it is never cached")
	}
}
