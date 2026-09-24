// SPDX-License-Identifier: Apache-2.0

//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// TestAGoalChangeReachesTheCachedReport — the Analytics page is served from a
// background cache, and a goal added there stayed out of the page for up to the
// cache's 90-second lifetime. The report is read here with an hour-long TTL, so
// only the goal handler's own refresh can bring the new goal into it.
func TestAGoalChangeReachesTheCachedReport(t *testing.T) {
	_, _ = newTestHarness(t)
	a := &App{analytics: analytics.New(dbpkg.DB)}
	const key = "analytics:30"
	compute := func(ctx context.Context) string { return a.renderAnalyticsBody(ctx, 30, "last 30 days") }
	report := func() string { h, _ := adminDash.get(key, time.Hour, compute); return h }

	adminDash.warm(key, compute)
	deadline := time.Now().Add(10 * time.Second)
	for report() == "" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if report() == "" {
		t.Fatal("seed: the report never computed")
	}

	req := httptest.NewRequest(http.MethodPost, "/os/api/analytics/goals",
		strings.NewReader(`{"name":"Cache probe goal","kind":"path","target":"/probe"}`))
	rec := httptest.NewRecorder()
	a.handleAnalyticsCreateGoal(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create goal = %d (%s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("create goal returned no id: %s", rec.Body.String())
	}
	id := created.ID

	deadline = time.Now().Add(10 * time.Second)
	for !strings.Contains(report(), "Cache probe goal") {
		if time.Now().After(deadline) {
			t.Fatal("the cached Analytics report still does not list a goal added ten seconds ago")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// And out again when it is deleted.
	del := httptest.NewRequest(http.MethodDelete, "/os/api/analytics/goals/"+id, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	del = del.WithContext(context.WithValue(del.Context(), chi.RouteCtxKey, rctx))
	rec = httptest.NewRecorder()
	a.handleAnalyticsDeleteGoal(rec, del)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete goal = %d (%s)", rec.Code, rec.Body.String())
	}
	deadline = time.Now().Add(10 * time.Second)
	for strings.Contains(report(), "Cache probe goal") {
		if time.Now().After(deadline) {
			t.Fatal("the cached Analytics report still lists a goal deleted ten seconds ago")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
