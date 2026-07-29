// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/johalputt/vayupress/internal/vayushield/botdb"
	"github.com/johalputt/vayupress/internal/vayushield/challenge"
)

const sigSchema = `CREATE TABLE vayushield_signatures(id INTEGER PRIMARY KEY AUTOINCREMENT,fingerprint_hash TEXT NOT NULL UNIQUE,ja3_hash TEXT NOT NULL DEFAULT '',ja4_hash TEXT NOT NULL DEFAULT '',http2_settings_hash TEXT NOT NULL DEFAULT '',header_order_hash TEXT NOT NULL DEFAULT '',user_agent_pattern TEXT NOT NULL DEFAULT '',ip_range_hint TEXT NOT NULL DEFAULT '',post_quantum_present INTEGER NOT NULL DEFAULT 0,classification TEXT NOT NULL DEFAULT 'unknown',bot_name TEXT NOT NULL DEFAULT '',confidence REAL NOT NULL DEFAULT 0.5,first_seen DATETIME NOT NULL,last_seen DATETIME NOT NULL,request_count INTEGER NOT NULL DEFAULT 1,false_positive_count INTEGER NOT NULL DEFAULT 0,auto_learned INTEGER NOT NULL DEFAULT 0,operator_verified INTEGER NOT NULL DEFAULT 0,notes TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`

func learningManager(t *testing.T) (*Manager, *botdb.Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(sigSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	bots := botdb.New(db)
	m := New(Config{
		Enabled:  true,
		Signer:   challenge.NewSigner([]byte("s")),
		Bots:     bots,
		DB:       db,
		Now:      time.Now,
		ClientIP: func(r *http.Request) string { return "203.0.113.7:1" },
	})
	m.ApplySettings(Settings{Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8})
	return m, bots, db
}

func countSignatures(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(1) FROM vayushield_signatures`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// TestMaybeLearnFiresOnNearMiss is the regression guard for the dead-code fix:
// an UNKNOWN client that scores high enough to be challenged (but not blocked)
// is recorded as an auto-learned review candidate. An empty UA scores ~0.70 →
// Unknown + JS challenge = the canonical near-miss. Before the fix its trigger
// (BotScore>0.75 && Unknown) was unsatisfiable and nothing was ever learned.
func TestMaybeLearnFiresOnNearMiss(t *testing.T) {
	m, _, db := learningManager(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/post", nil)
	req.Header.Set("User-Agent", "") // empty UA -> ~0.70 score -> Unknown + challenged
	m.Middleware(okHandler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("empty-UA client should be challenged, got %d", rr.Code)
	}
	if got := countSignatures(t, db); got == 0 {
		t.Fatal("a challenged near-miss unknown must be recorded as an auto-learned candidate")
	}
}

// TestMaybeLearnIgnoresAllowedTraffic: a plain real browser is Allowed and must
// NEVER be recorded — the learning DB must not fill with legitimate visitors.
func TestMaybeLearnIgnoresAllowedTraffic(t *testing.T) {
	m, _, db := learningManager(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/post", nil)
	req.Header.Set("User-Agent", realBrowserUA)
	m.Middleware(okHandler()).ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("real browser should be allowed, got %d", rr.Code)
	}
	if got := countSignatures(t, db); got != 0 {
		t.Fatalf("allowed traffic must not be learned, got %d rows", got)
	}
}

// TestSolvedChallengeArmsFPGuard proves step 3b: solving a challenge reports a
// false positive for that fingerprint, so a learned candidate a real user
// solves stops climbing toward an auto-block.
func TestSolvedChallengeArmsFPGuard(t *testing.T) {
	m, bots, _ := learningManager(t)
	// Seed a candidate for the fingerprint an empty-UA request produces.
	sig, _ := m.signals(httptest.NewRequest("GET", "/x", nil))
	fph := sig.Fingerprint().FingerprintHash
	if fph == "" {
		t.Skip("no fingerprint derivable in this environment")
	}
	_ = bots.Observe(context.Background(), botdb.Observation{FingerprintHash: fph, Classification: botdb.ClassBadBot, Confidence: 0.7, AutoLearned: true})

	pow, _ := m.cfg.Signer.IssuePoW(challenge.DefaultDifficulty, time.Minute)
	nonce, _ := challenge.Solve(pow.Salt, pow.Difficulty, 5_000_000)
	if _, ok := m.VerifyPoW(httptest.NewRequest("POST", "/__vayushield/pow", nil), pow, nonce); !ok {
		t.Fatal("verify should succeed")
	}
	got, ok := bots.Lookup(context.Background(), fph)
	if !ok {
		t.Fatal("candidate vanished")
	}
	if got.FalsePositives == 0 {
		t.Fatal("a solved challenge must record a false positive for its fingerprint")
	}
}

// TestPromotionRequiresARecentCluster — "recurring" needs a time window, and
// until this the promotion query had none: five sightings spread over ninety
// days promoted exactly as five in a minute.
//
// That difference matters more than it looks. A fingerprint seen occasionally
// over months is what a browser BUILD looks like — and promoting it to bad_bot
// at 0.85 confidence hard-blocks everyone using that build. A cluster of
// sightings inside a week that is still active today is the recurring bot the
// cycle exists to catch.
func TestPromotionRequiresARecentCluster(t *testing.T) {
	m, _, db := learningManager(t)
	defer func() { _ = db.Close() }()

	ins := func(fp string, first, last time.Time, count int) {
		if _, err := db.Exec(`INSERT INTO vayushield_signatures
(fingerprint_hash,classification,auto_learned,operator_verified,confidence,first_seen,last_seen,request_count,false_positive_count)
VALUES(?,'unknown',1,0,0.5,?,?,?,0)`, fp, first, last, count); err != nil {
			t.Fatalf("insert %s: %v", fp, err)
		}
	}
	now := time.Now().UTC()

	// The genuine recurring cluster: many sightings, inside a week, still active.
	ins("recent-cluster", now.AddDate(0, 0, -3), now.Add(-time.Hour), 20)
	// A fingerprint seen occasionally over months — a browser build's profile.
	ins("long-tail", now.AddDate(0, 0, -90), now.Add(-time.Hour), 6)
	// A tight cluster, but from last quarter: whatever it was, it is not here now.
	ins("stale-burst", now.AddDate(0, 0, -60), now.AddDate(0, 0, -58), 40)

	if _, err := m.RunLearningCycle(context.Background(), 0); err != nil {
		t.Fatalf("learning cycle: %v", err)
	}

	class := func(fp string) string {
		var c string
		if err := db.QueryRow(`SELECT classification FROM vayushield_signatures WHERE fingerprint_hash=?`, fp).Scan(&c); err != nil {
			t.Fatalf("read %s: %v", fp, err)
		}
		return c
	}
	if got := class("recent-cluster"); got != "bad_bot" {
		t.Errorf("a recent, clustered, recurring signature was not promoted (got %q)", got)
	}
	if got := class("long-tail"); got != "unknown" {
		t.Errorf("a signature seen six times across ninety days was promoted to %q — that is "+
			"the shape of a browser build, and promoting it hard-blocks everyone using it", got)
	}
	if got := class("stale-burst"); got != "unknown" {
		t.Errorf("a burst from two months ago was promoted to %q — it is not active now", got)
	}
}
