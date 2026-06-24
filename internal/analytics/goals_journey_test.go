package analytics

import (
	"context"
	"testing"
)

// addGoalsTable adds the analytics_goals table to the in-memory test store
// (newExtStore only creates the session/pageview/event tables).
func addGoalsTable(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec(`CREATE TABLE analytics_goals(id TEXT PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL DEFAULT 'path', target TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`); err != nil {
		t.Fatalf("goals ddl: %v", err)
	}
}

func TestGoalsConversion(t *testing.T) {
	t.Parallel()
	s := newExtStore(t)
	addGoalsTable(t, s)
	ctx := context.Background()

	// Visitor A: views "/" then "/pricing".
	_ = s.Collect(ctx, CollectRequest{URL: "/", Hostname: "h", EventType: 1}, "1.1.1.1", "Chrome")
	_ = s.Collect(ctx, CollectRequest{URL: "/pricing", Hostname: "h", EventType: 1}, "1.1.1.1", "Chrome")
	// Visitor B: views "/pricing" and fires a "signup" custom event.
	_ = s.Collect(ctx, CollectRequest{URL: "/pricing", Hostname: "h", EventType: 1}, "2.2.2.2", "Firefox")
	_ = s.Collect(ctx, CollectRequest{URL: "/welcome", Hostname: "h", EventType: 2, EventName: "signup"}, "2.2.2.2", "Firefox")

	pathGoal, err := s.CreateGoal(ctx, "Saw pricing", GoalKindPath, "/pricing")
	if err != nil {
		t.Fatalf("create path goal: %v", err)
	}
	if _, err := s.CreateGoal(ctx, "Signed up", GoalKindEvent, "signup"); err != nil {
		t.Fatalf("create event goal: %v", err)
	}

	results, err := s.GoalResults(ctx, 30)
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	byName := map[string]GoalResult{}
	for _, r := range results {
		byName[r.Name] = r
	}

	pg := byName["Saw pricing"]
	if pg.Completions != 2 || pg.UniqueVisitors != 2 {
		t.Fatalf("path goal: completions=%d unique=%d want 2/2", pg.Completions, pg.UniqueVisitors)
	}
	if pg.ConversionRate < 99.9 { // 2 of 2 visitors
		t.Fatalf("path goal conversion=%.1f want ~100", pg.ConversionRate)
	}
	eg := byName["Signed up"]
	if eg.Completions != 1 || eg.UniqueVisitors != 1 {
		t.Fatalf("event goal: completions=%d unique=%d want 1/1", eg.Completions, eg.UniqueVisitors)
	}
	if eg.ConversionRate < 49.9 || eg.ConversionRate > 50.1 { // 1 of 2 visitors
		t.Fatalf("event goal conversion=%.1f want ~50", eg.ConversionRate)
	}

	// Validation + delete.
	if _, err := s.CreateGoal(ctx, "", GoalKindPath, "/x"); err == nil {
		t.Fatalf("empty name should error")
	}
	if _, err := s.CreateGoal(ctx, "bad", "weird", "/x"); err == nil {
		t.Fatalf("invalid kind should error")
	}
	if err := s.DeleteGoal(ctx, pathGoal); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.DeleteGoal(ctx, "nope"); err == nil {
		t.Fatalf("deleting missing goal should error")
	}
	left, _ := s.ListGoals(ctx)
	if len(left) != 1 {
		t.Fatalf("expected 1 goal left, got %d", len(left))
	}
}

func TestGoalPathPrefixWildcard(t *testing.T) {
	t.Parallel()
	s := newExtStore(t)
	addGoalsTable(t, s)
	ctx := context.Background()
	_ = s.Collect(ctx, CollectRequest{URL: "/blog/a", Hostname: "h", EventType: 1}, "1.1.1.1", "Chrome")
	_ = s.Collect(ctx, CollectRequest{URL: "/blog/b", Hostname: "h", EventType: 1}, "2.2.2.2", "Firefox")
	_ = s.Collect(ctx, CollectRequest{URL: "/about", Hostname: "h", EventType: 1}, "3.3.3.3", "Safari")

	if _, err := s.CreateGoal(ctx, "Read blog", GoalKindPath, "/blog*"); err != nil {
		t.Fatalf("create: %v", err)
	}
	results, err := s.GoalResults(ctx, 30)
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	if len(results) != 1 || results[0].Completions != 2 {
		t.Fatalf("wildcard goal completions wrong: %+v", results)
	}
}

func TestPathFlowsJourney(t *testing.T) {
	t.Parallel()
	s := newExtStore(t)
	ctx := context.Background()

	// One visitor walks /, /pricing, /checkout in a single session.
	_ = s.Collect(ctx, CollectRequest{URL: "/", Hostname: "h", EventType: 1}, "1.1.1.1", "Chrome")
	_ = s.Collect(ctx, CollectRequest{URL: "/pricing", Hostname: "h", EventType: 1}, "1.1.1.1", "Chrome")
	_ = s.Collect(ctx, CollectRequest{URL: "/checkout", Hostname: "h", EventType: 1}, "1.1.1.1", "Chrome")

	flows, err := s.PathFlows(ctx, 14, 100)
	if err != nil {
		t.Fatalf("pathflows: %v", err)
	}
	get := func(from, to string) int {
		for _, f := range flows {
			if f.From == from && f.To == to {
				return f.Count
			}
		}
		return 0
	}
	if get(entryMarker, "/") != 1 {
		t.Fatalf("missing entry->/ flow: %+v", flows)
	}
	if get("/", "/pricing") != 1 {
		t.Fatalf("missing /->/pricing flow: %+v", flows)
	}
	if get("/pricing", "/checkout") != 1 {
		t.Fatalf("missing /pricing->/checkout flow: %+v", flows)
	}
	if get("/checkout", exitMarker) != 1 {
		t.Fatalf("missing /checkout->exit flow: %+v", flows)
	}
}
