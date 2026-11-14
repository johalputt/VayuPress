// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

// Home draws the list the server already has, fresh or stale, so Most read is
// in the page at its first paint; with nothing to show it leaves the widget
// to the script, as before.
func TestHomeIsGivenTheCachedTrendingList(t *testing.T) {
	trendingMu.Lock()
	saved := trendingSites
	trendingSites = map[string]*trendingEntry{}
	trendingMu.Unlock()
	t.Cleanup(func() {
		trendingMu.Lock()
		trendingSites = saved
		trendingMu.Unlock()
	})
	a := &App{}
	req := httptest.NewRequest("GET", "/", nil)
	set := func(p *trendingPayload, expiry time.Time, computing bool) {
		trendingMu.Lock()
		trendingSites[""] = &trendingEntry{payload: p, expiry: expiry, computing: computing}
		trendingMu.Unlock()
	}

	list := &trendingPayload{Enabled: true,
		Pinned:  []trendingItem{{Slug: "kept", Title: "Kept"}},
		Windows: map[string][]trendingItem{"7": {{Slug: "week", Title: "Week", Image: "/m/w.png"}}, "30": {{Slug: "month", Title: "Month"}}}}
	set(list, time.Now().Add(time.Hour), false)
	got := a.trendingForHome(req)
	if got == nil || len(got.Pinned) != 1 || got.Week[0].Slug != "week" || got.Week[0].Image != "/m/w.png" || got.Month[0].Title != "Month" {
		t.Fatalf("a fresh list was not handed to Home as it is: %+v", got)
	}

	// Stale, with another request already refreshing it: drawn as it is, and
	// no second refresh started.
	set(list, time.Now().Add(-time.Hour), true)
	if a.trendingForHome(req) == nil {
		t.Error("a stale list was not drawn while it is refreshed")
	}

	set(&trendingPayload{Enabled: true, Windows: map[string][]trendingItem{}}, time.Now().Add(time.Hour), false)
	if a.trendingForHome(req) != nil {
		t.Error("a list with nothing in it was drawn, an empty section where the widget would stay hidden")
	}

	// None yet, and a refresh already under way: Home is not held, and the
	// widget fills the page in.
	set(nil, time.Time{}, true)
	start := time.Now()
	if a.trendingForHome(req) != nil {
		t.Error("Home drew a list the server does not have")
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Error("Home waited for the list to be computed")
	}
}
