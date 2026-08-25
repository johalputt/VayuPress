// SPDX-License-Identifier: Apache-2.0

package botdb

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

const schema = `CREATE TABLE vayushield_signatures(id INTEGER PRIMARY KEY AUTOINCREMENT,fingerprint_hash TEXT NOT NULL UNIQUE,ja3_hash TEXT NOT NULL DEFAULT '',ja4_hash TEXT NOT NULL DEFAULT '',http2_settings_hash TEXT NOT NULL DEFAULT '',header_order_hash TEXT NOT NULL DEFAULT '',user_agent_pattern TEXT NOT NULL DEFAULT '',ip_range_hint TEXT NOT NULL DEFAULT '',post_quantum_present INTEGER NOT NULL DEFAULT 0,classification TEXT NOT NULL DEFAULT 'unknown',bot_name TEXT NOT NULL DEFAULT '',confidence REAL NOT NULL DEFAULT 0.5,first_seen DATETIME NOT NULL,last_seen DATETIME NOT NULL,request_count INTEGER NOT NULL DEFAULT 1,false_positive_count INTEGER NOT NULL DEFAULT 0,auto_learned INTEGER NOT NULL DEFAULT 0,operator_verified INTEGER NOT NULL DEFAULT 0,notes TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`

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

func TestStaticMatchUA(t *testing.T) {
	d := NewStaticDB()
	cases := map[string]Classification{
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)": ClassGoodBot,
		"Mozilla/5.0 AppleWebKit ChatGPT-User/1.0":                                 ClassAIAgent,
		// Generic HTTP libraries are challenge-tier since the 2025 audit: a
		// library UA is not a conviction (monitors, webhooks, feed fetchers).
		"python-requests/2.31.0":                  ClassUnknown,
		"sqlmap/1.7":                              ClassBadBot,
		"Mozilla/5.0 (compatible; ClaudeBot/1.0)": ClassAIAgent,
	}
	for ua, want := range cases {
		sig, ok := d.MatchUA(ua)
		if !ok {
			t.Fatalf("expected match for %q", ua)
		}
		if sig.Classification != want {
			t.Fatalf("%q -> %s, want %s (%s)", ua, sig.Classification, want, sig.Name)
		}
	}
	if _, ok := d.MatchUA("Mozilla/5.0 (Windows NT 10.0) Chrome/130 Safari/537.36"); ok {
		t.Fatal("real browser UA should not match a static bot signature")
	}
}

func TestStaticReferrerAI(t *testing.T) {
	d := NewStaticDB()
	if name, ok := d.MatchReferrerAI("chatgpt.com"); !ok || name != "ChatGPT" {
		t.Fatalf("chatgpt.com -> %q,%v", name, ok)
	}
	if name, ok := d.MatchReferrerAI("www.perplexity.ai"); !ok || name != "Perplexity" {
		t.Fatalf("perplexity subdomain -> %q,%v", name, ok)
	}
	if _, ok := d.MatchReferrerAI("google.com"); ok {
		t.Fatal("plain google.com is search, not AI-assisted")
	}
	// Spoof resistance (2025 audit): a lookalike host that merely CONTAINS a
	// real AI domain must never claim AI-assisted attribution.
	if _, ok := d.MatchReferrerAI("chatgpt.com.evil.tld"); ok {
		t.Fatal("lookalike host spoofing the AI-referrer ladder")
	}
	if _, ok := d.MatchReferrerAI("not-chatgpt.com"); ok {
		t.Fatal("hyphen-lookalike host spoofing the AI-referrer ladder")
	}
}

func TestObserveUpsertAndPromote(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	obs := Observation{FingerprintHash: "fp1", Classification: ClassUnknown, Confidence: 0.5, AutoLearned: true, UserAgentPattern: "http-lib"}
	for i := 0; i < 6; i++ {
		if err := s.Observe(ctx, obs); err != nil {
			t.Fatalf("observe %d: %v", i, err)
		}
	}
	sig, ok := s.Lookup(ctx, "fp1")
	if !ok {
		t.Fatal("expected fp1 present")
	}
	if sig.RequestCount != 6 {
		t.Fatalf("request_count want 6 got %d", sig.RequestCount)
	}
	if sig.Confidence <= 0.5 {
		t.Fatalf("confidence should have been promoted above 0.5, got %.2f", sig.Confidence)
	}
}

func TestReviewVerifyDismiss(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	_ = s.Observe(ctx, Observation{FingerprintHash: "cand1", Classification: ClassUnknown, Confidence: 0.8, AutoLearned: true})
	_ = s.Observe(ctx, Observation{FingerprintHash: "cand2", Classification: ClassUnknown, Confidence: 0.6, AutoLearned: true})
	q, err := s.ReviewQueue(ctx, 10)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if len(q) != 2 {
		t.Fatalf("want 2 candidates got %d", len(q))
	}
	// Highest confidence first.
	if q[0].FingerprintHash != "cand1" {
		t.Fatalf("expected cand1 first, got %s", q[0].FingerprintHash)
	}
	if err := s.Verify(ctx, q[0].ID, ClassBadBot, "manual"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := s.Dismiss(ctx, q[1].ID); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	q2, _ := s.ReviewQueue(ctx, 10)
	if len(q2) != 0 {
		t.Fatalf("review queue should be empty after verify+dismiss, got %d", len(q2))
	}
	sig, _ := s.Lookup(ctx, "cand1")
	if !sig.OperatorVerified || sig.Classification != ClassBadBot {
		t.Fatalf("cand1 should be verified bad_bot: %+v", sig)
	}
}

func TestFalsePositiveDecay(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	_ = s.Observe(ctx, Observation{FingerprintHash: "fp", Classification: ClassBadBot, Confidence: 0.9, AutoLearned: true})
	for i := 0; i < 2; i++ {
		if err := s.ReportFalsePositive(ctx, "fp"); err != nil {
			t.Fatalf("fp report: %v", err)
		}
	}
	sig, _ := s.Lookup(ctx, "fp")
	if sig.Confidence > 0.55 {
		t.Fatalf("confidence should decay after false positives, got %.2f", sig.Confidence)
	}
	if sig.FalsePositives != 2 {
		t.Fatalf("false positive count want 2 got %d", sig.FalsePositives)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := testStore(t)
	_ = src.Observe(ctx, Observation{FingerprintHash: "shareme", Classification: ClassBadBot, Confidence: 0.85, AutoLearned: true, JA4: "t13d1516h2", IPRangeHint: "secret-net"})
	data, err := src.Export(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	// IP hint must be stripped from the export.
	if containsStr(string(data), "secret-net") {
		t.Fatal("export must not leak ip_range_hint")
	}
	dst := testStore(t)
	n, err := dst.Import(ctx, data)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 imported got %d", n)
	}
	sig, ok := dst.Lookup(ctx, "shareme")
	if !ok || sig.Classification != ClassBadBot {
		t.Fatalf("imported signature missing/wrong: %+v", sig)
	}
	if sig.IPRangeHint != "" {
		t.Fatal("imported signature must not carry ip hint")
	}
}

func TestStats(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	_ = s.Observe(ctx, Observation{FingerprintHash: "a", Classification: ClassBadBot, AutoLearned: true, Confidence: 0.9})
	_ = s.Observe(ctx, Observation{FingerprintHash: "b", Classification: ClassGoodBot, Confidence: 0.9})
	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.Total != 2 {
		t.Fatalf("total want 2 got %d", st.Total)
	}
	if st.ByClass["bad_bot"] != 1 || st.ByClass["good_bot"] != 1 {
		t.Fatalf("byclass wrong: %+v", st.ByClass)
	}
}

func containsStr(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func rawTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestUseReaderRoutesReadsSeparately(t *testing.T) {
	// Wiring a reader makes reader() return it while writes stay on db.
	db := rawTestDB(t)
	rdb := rawTestDB(t)
	s := New(db)
	if s.reader() != db {
		t.Fatal("reader() must default to the writer handle")
	}
	s.UseReader(rdb)
	if s.reader() != rdb {
		t.Fatal("UseReader must route reads through the dedicated pool")
	}
	if s.db != db {
		t.Fatal("UseReader must not change the writer handle")
	}
	// nil is a no-op (keeps current reader).
	s.UseReader(nil)
	if s.reader() != rdb {
		t.Fatal("UseReader(nil) must keep the current reader")
	}
}
