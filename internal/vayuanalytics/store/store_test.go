package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/johalputt/vayupress/internal/vayuanalytics/classifier"
)

const schema = `CREATE TABLE vayuanalytics_sessions(id INTEGER PRIMARY KEY AUTOINCREMENT,session_hash TEXT NOT NULL,page_path TEXT NOT NULL,source_category TEXT NOT NULL DEFAULT 'direct',source_detail TEXT NOT NULL DEFAULT '',referrer_domain TEXT NOT NULL DEFAULT '',referrer_path TEXT NOT NULL DEFAULT '',entry_time DATETIME NOT NULL,exit_time DATETIME,time_on_page_seconds INTEGER NOT NULL DEFAULT 0,scroll_depth_percent INTEGER NOT NULL DEFAULT 0,engaged INTEGER NOT NULL DEFAULT 0,bounce INTEGER NOT NULL DEFAULT 0,interaction_count INTEGER NOT NULL DEFAULT 0,country_code TEXT NOT NULL DEFAULT '',client_type TEXT NOT NULL DEFAULT 'human',bot_score REAL NOT NULL DEFAULT 0,is_new_session INTEGER NOT NULL DEFAULT 1,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`

func testStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return New(db)
}

func enter(sess, path, cat, detail, ct string, now time.Time) EnterInput {
	return EnterInput{
		SessionHash: sess, PagePath: path,
		Class:      classifier.Result{Category: classifier.Category(cat), Detail: detail},
		ClientType: ct, Now: now,
	}
}

func TestEnterNewVsReturning(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	now := time.Now().UTC()
	if err := s.RecordEnter(ctx, enter("sess1", "/a", "organic", "Google", "human", now)); err != nil {
		t.Fatalf("enter1: %v", err)
	}
	// Same session, second page same day → not a new session.
	if err := s.RecordEnter(ctx, enter("sess1", "/b", "direct", "internal", "human", now.Add(time.Minute))); err != nil {
		t.Fatalf("enter2: %v", err)
	}
	o, err := s.Overview(ctx, 30)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if o.Views != 2 {
		t.Fatalf("views want 2 got %d", o.Views)
	}
	if o.UniqueSessions != 1 {
		t.Fatalf("unique want 1 got %d", o.UniqueSessions)
	}
	if o.NewSessions != 1 {
		t.Fatalf("new want 1 got %d", o.NewSessions)
	}
}

func TestBeaconEngagementAndBounce(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	now := time.Now().UTC()
	_ = s.RecordEnter(ctx, enter("s", "/post", "organic", "Google", "human", now))
	// Engaged: 45s + 60% scroll.
	if err := s.RecordBeacon(ctx, BeaconInput{SessionHash: "s", PagePath: "/post", TimeOnPage: 45, ScrollDepth: 60, Interactions: 3, Now: now.Add(time.Minute)}); err != nil {
		t.Fatalf("beacon: %v", err)
	}
	o, _ := s.Overview(ctx, 30)
	if o.EngagementRate == 0 {
		t.Fatalf("expected engaged read, overview=%+v", o)
	}
	if o.AvgTimeSeconds < 45 {
		t.Fatalf("avg time not recorded: %v", o.AvgTimeSeconds)
	}

	// A separate bouncing session.
	_ = s.RecordEnter(ctx, enter("s2", "/x", "social", "X/Twitter", "human", now))
	_ = s.RecordBeacon(ctx, BeaconInput{SessionHash: "s2", PagePath: "/x", TimeOnPage: 3, ScrollDepth: 5, Now: now})
	o2, _ := s.Overview(ctx, 30)
	if o2.BounceRate == 0 {
		t.Fatalf("expected a bounce, got %+v", o2)
	}
}

func TestBeaconAccumulatesMax(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	now := time.Now().UTC()
	_ = s.RecordEnter(ctx, enter("s", "/p", "direct", "typed", "human", now))
	_ = s.RecordBeacon(ctx, BeaconInput{SessionHash: "s", PagePath: "/p", TimeOnPage: 50, ScrollDepth: 80, Now: now})
	// A later, smaller beacon must not lower the recorded maxima.
	_ = s.RecordBeacon(ctx, BeaconInput{SessionHash: "s", PagePath: "/p", TimeOnPage: 10, ScrollDepth: 20, Now: now})
	o, _ := s.Overview(ctx, 30)
	if o.AvgScrollPct < 80 {
		t.Fatalf("scroll should stay at max 80, got %v", o.AvgScrollPct)
	}
}

func TestBotTrafficSeparated(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	now := time.Now().UTC()
	_ = s.RecordEnter(ctx, enter("human1", "/a", "organic", "Google", "human", now))
	_ = s.RecordEnter(ctx, enter("bot1", "/a", "bot", "", "BadBot", now))
	o, _ := s.Overview(ctx, 30)
	if o.Views != 1 {
		t.Fatalf("human views should exclude bot, got %d", o.Views)
	}
	if o.BotViews != 1 {
		t.Fatalf("bot views want 1 got %d", o.BotViews)
	}
}

func TestAITrafficReport(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	now := time.Now().UTC()
	// 2 AI (Perplexity) visits, engaged; 1 organic, not engaged.
	_ = s.RecordEnter(ctx, enter("a1", "/p", "ai_assisted", "Perplexity", "human", now))
	_ = s.RecordBeacon(ctx, BeaconInput{SessionHash: "a1", PagePath: "/p", TimeOnPage: 90, ScrollDepth: 70, Now: now})
	_ = s.RecordEnter(ctx, enter("a2", "/p", "ai_assisted", "Perplexity", "human", now))
	_ = s.RecordBeacon(ctx, BeaconInput{SessionHash: "a2", PagePath: "/p", TimeOnPage: 60, ScrollDepth: 55, Now: now})
	_ = s.RecordEnter(ctx, enter("o1", "/p", "organic", "Google", "human", now))
	rep, err := s.AITraffic(ctx, 30)
	if err != nil {
		t.Fatalf("aitraffic: %v", err)
	}
	if rep.AISummary.Views != 2 {
		t.Fatalf("AI views want 2 got %d", rep.AISummary.Views)
	}
	if len(rep.BySystem) != 1 || rep.BySystem[0].Detail != "Perplexity" {
		t.Fatalf("by-system wrong: %+v", rep.BySystem)
	}
	if rep.AISharePercent <= 0 {
		t.Fatalf("AI share should be > 0, got %v", rep.AISharePercent)
	}
	if rep.AISummary.EngagementRate <= rep.OrganicSummary.EngagementRate {
		t.Logf("AI engagement %.2f vs organic %.2f", rep.AISummary.EngagementRate, rep.OrganicSummary.EngagementRate)
	}
}

func TestPurgeRetention(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	old := time.Now().UTC().AddDate(0, 0, -400)
	_ = s.RecordEnter(ctx, enter("old", "/a", "direct", "typed", "human", old))
	_ = s.RecordEnter(ctx, enter("new", "/a", "direct", "typed", "human", time.Now().UTC()))
	n, err := s.Purge(ctx, 365)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 purged got %d", n)
	}
	o, _ := s.Overview(ctx, 365)
	if o.Views != 1 {
		t.Fatalf("only recent should remain, got %d", o.Views)
	}
}

func TestRealtime(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	now := time.Now().UTC()
	in := enter("live", "/now", "organic", "Google", "human", now)
	in.Country = "US"
	_ = s.RecordEnter(ctx, in)
	rt, err := s.Realtime(ctx, 5)
	if err != nil {
		t.Fatalf("realtime: %v", err)
	}
	if rt.ActiveVisitors != 1 {
		t.Fatalf("active want 1 got %d", rt.ActiveVisitors)
	}
	if rt.ByCountry["US"] != 1 {
		t.Fatalf("country US want 1 got %v", rt.ByCountry)
	}
}
