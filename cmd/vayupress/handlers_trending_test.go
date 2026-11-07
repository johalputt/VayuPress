// SPDX-License-Identifier: Apache-2.0

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

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
)

// resetTrendingState clears the package-level trending memos between tests
// (they are process-global by design).
func resetTrendingState() {
	trendingMu.Lock()
	trendingSites = map[string]*trendingEntry{}
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
	trendingEntryFor("").computing = true
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
	computing, cache := trendingEntryFor("").computing, trendingEntryFor("").payload
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
	e := trendingEntryFor("")
	e.payload, e.expiry = &fresh, time.Now().Add(time.Hour)
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

// TestTrendingWarmingIsNeverCached: the warming payload is built on its own
// context (it is given the site, not the request, so a reader navigating away
// mid-fetch cannot cancel its queries), and it is marked degraded so it is
// never stored.
func TestTrendingWarmingIsNeverCached(t *testing.T) {
	a := &App{}
	got := a.trendingWarming("")
	if !got.Enabled {
		t.Error("warming payload should still be enabled")
	}
	if !got.degraded {
		t.Error("warming payload must be marked degraded so it is never cached")
	}
}

// TestEachSiteTrendsAndPinsOnlyItsOwnPosts — a hosted blog listed the main
// site's most-read and pinned posts, at addresses it does not serve: one memo
// for the install, and queries with no site. Each list is seeded on its own:
// pinned, the newest-posts fallback, and the ranking by views. The main site
// is asked first, so a memo shared between sites would answer the blog.
func TestEachSiteTrendsAndPinsOnlyItsOwnPosts(t *testing.T) {
	ask := func(t *testing.T, a *App, d *domain.Domain) trendingPayload {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/trending", nil)
		if d != nil {
			r = requestFor("/api/trending", *d)
		}
		rec := httptest.NewRecorder()
		a.handleTrendingJSON(rec, r)
		var p trendingPayload
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		return p
	}
	slugs := func(items []trendingItem) string {
		var out []string
		for _, it := range items {
			out = append(out, it.Slug)
		}
		return strings.Join(out, " ")
	}
	post := func(t *testing.T, slug, site string, featured int) {
		t.Helper()
		if _, err := dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,created_at,updated_at,status,is_page,featured,domain_id) VALUES(?,?,?,'',?,?,'published',0,?,?)`,
			slug, slug, slug, time.Now().UTC(), time.Now().UTC(), featured, site); err != nil {
			t.Fatal(err)
		}
	}
	for _, list := range []string{"pinned", "newest", "most read"} {
		t.Run(list, func(t *testing.T) {
			resetTrendingState()
			t.Cleanup(resetTrendingState)
			a, _, blog := hostedBlogApp(t)
			switch list {
			case "pinned":
				post(t, "main-pinned", "", 1)
				post(t, "blog-pinned", blog.ID, 1)
			case "newest":
				post(t, "main-new", "", 0)
				post(t, "blog-new", blog.ID, 0)
			case "most read":
				a.analytics = analytics.New(dbpkg.DB)
				for slug, site := range map[string]string{"main-read": "", "blog-read": blog.ID} {
					post(t, slug, site, 0)
					if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,created_at,domain_id) VALUES(?,'',?,1,?,?)`,
						slug, "/"+slug, time.Now().UTC(), site); err != nil {
						t.Fatal(err)
					}
				}
			}
			main, hosted := ask(t, a, nil), ask(t, a, &blog)
			got := map[string][2]string{
				"pinned":    {slugs(main.Pinned), slugs(hosted.Pinned)},
				"newest":    {slugs(main.Windows["7"]), slugs(hosted.Windows["7"])},
				"most read": {slugs(main.Windows["7"]), slugs(hosted.Windows["7"])},
			}[list]
			want := map[string][2]string{
				"pinned":    {"main-pinned", "blog-pinned"},
				"newest":    {"main-new", "blog-new"},
				"most read": {"main-read", "blog-read"},
			}[list]
			if got != want {
				t.Errorf("%s: the main site lists %q and the blog %q, want %q and %q", list, got[0], got[1], want[0], want[1])
			}
		})
	}
}

// TestEachSiteHasItsOwnTrendingSwitch — the switch was read from the
// install's settings for every site. Turned off for the blog, only the blog's
// widget goes; turned off for the main site, the blog's stays.
func TestEachSiteHasItsOwnTrendingSwitch(t *testing.T) {
	resetTrendingState()
	t.Cleanup(resetTrendingState)
	a, _, blog := hostedBlogApp(t)
	ctx := context.Background()
	enabled := func(d *domain.Domain) bool {
		r := httptest.NewRequest(http.MethodGet, "/api/trending", nil)
		if d != nil {
			r = requestFor("/api/trending", *d)
		}
		rec := httptest.NewRecorder()
		a.handleTrendingJSON(rec, r)
		var p trendingPayload
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		return p.Enabled
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForDomain(blog.ID), map[string]string{settings.KeyFeatureTrending: "off"}); err != nil {
		t.Fatal(err)
	}
	if !enabled(nil) || enabled(&blog) {
		t.Errorf("off for the blog: main on=%v, blog on=%v; want main on, blog off", enabled(nil), enabled(&blog))
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForDomain(blog.ID), map[string]string{settings.KeyFeatureTrending: "on"}); err != nil {
		t.Fatal(err)
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{settings.KeyFeatureTrending: "off"}); err != nil {
		t.Fatal(err)
	}
	if enabled(nil) || !enabled(&blog) {
		t.Errorf("off for the main site: main on=%v, blog on=%v; want main off, blog on", enabled(nil), enabled(&blog))
	}
}
