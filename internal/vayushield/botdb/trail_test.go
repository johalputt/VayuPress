// SPDX-License-Identifier: Apache-2.0

package botdb

import (
	"context"
	"strconv"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const challengesSchema = `CREATE TABLE vayushield_challenges(id INTEGER PRIMARY KEY AUTOINCREMENT,session_hash TEXT NOT NULL DEFAULT '',challenge_type TEXT NOT NULL,bot_score REAL NOT NULL DEFAULT 0,fingerprint_hash TEXT NOT NULL DEFAULT '',outcome TEXT NOT NULL DEFAULT '',time_to_solve_ms INTEGER NOT NULL DEFAULT 0,ip_hash TEXT NOT NULL DEFAULT '',country_code TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`

// trailStore builds a store with both audit-trail tables present.
func trailStore(t *testing.T) *Store {
	t.Helper()
	s := testStore(t)
	for _, ddl := range []string{blockedSchema, challengesSchema} {
		if _, err := s.db.Exec(ddl); err != nil {
			// blockedSchema may already exist in testStore; only challenges is new.
			t.Logf("schema: %v", err)
		}
	}
	return s
}

func insertBlock(t *testing.T, s *Store, at time.Time, reason, path, country string) {
	t.Helper()
	if _, err := s.db.Exec(
		`INSERT INTO vayushield_blocked(ip_hash,request_path,block_reason,country_code,created_at) VALUES(?,?,?,?,?)`,
		"hash", path, reason, country, at.UTC().Format("2006-01-02 15:04:05")); err != nil {
		t.Fatalf("insert block: %v", err)
	}
}

func insertChallenge(t *testing.T, s *Store, at time.Time, outcome string) {
	t.Helper()
	if _, err := s.db.Exec(
		`INSERT INTO vayushield_challenges(challenge_type,outcome,created_at) VALUES('pow',?,?)`,
		outcome, at.UTC().Format("2006-01-02 15:04:05")); err != nil {
		t.Fatalf("insert challenge: %v", err)
	}
}

// TestTrailAggregatesTheWindow — both tables have been written on every event
// since they were introduced and read by nothing but a DELETE purge. These are
// the aggregates that make that data worth keeping.
func TestTrailAggregatesTheWindow(t *testing.T) {
	s := trailStore(t)
	now := time.Now().UTC()

	for i := 0; i < 5; i++ {
		insertBlock(t, s, now.Add(-time.Hour), "bot_score>=block_threshold", "/wp-login.php", "CN")
	}
	for i := 0; i < 3; i++ {
		insertBlock(t, s, now.Add(-2*time.Hour), "known_bad_signature", "/xmlrpc.php", "RU")
	}
	// Outside the window: must not be counted.
	insertBlock(t, s, now.Add(-48*time.Hour), "ancient", "/old", "US")

	// Seeded with the outcomes the manager ACTUALLY writes. The first version of
	// this fixture used "solved" and "abandoned" against a one-row-per-challenge
	// model where the outcome is later mutated — a schema that was designed and
	// never built. recordChallenge only ever INSERTs, and only ever with
	// "issued", "blocked" or "delayed". So this test passed for months while the
	// live panel reported "0% of challenges passed" every hour of every day: it
	// was asserting over data the production path cannot produce.
	for i := 0; i < 15; i++ {
		insertChallenge(t, s, now.Add(-time.Hour), "issued")
	}
	for i := 0; i < 10; i++ {
		insertChallenge(t, s, now.Add(-time.Hour), "solved")
	}
	// Blocks and tarpit delays share this table and are NOT invitations, so they
	// must not reach the denominator of a pass rate.
	insertChallenge(t, s, now.Add(-time.Hour), "blocked")
	insertChallenge(t, s, now.Add(-time.Hour), "delayed")

	tr, err := s.ReadTrail(context.Background(), 24, 8, 30)
	if err != nil {
		t.Fatalf("ReadTrail: %v", err)
	}

	if tr.TotalBlocks != 8 {
		t.Errorf("TotalBlocks = %d, want 8 (the 48h-old row is outside the window)", tr.TotalBlocks)
	}
	if tr.TotalChallenges != 15 || tr.TotalSolved != 10 {
		t.Errorf("challenges = %d solved = %d, want 15 / 10", tr.TotalChallenges, tr.TotalSolved)
	}
	if len(tr.Reasons) == 0 || tr.Reasons[0].Key != "bot_score>=block_threshold" || tr.Reasons[0].Count != 5 {
		t.Errorf("top reason = %+v, want bot_score>=block_threshold x5", tr.Reasons)
	}
	if len(tr.Paths) == 0 || tr.Paths[0].Key != "/wp-login.php" {
		t.Errorf("top path = %+v, want /wp-login.php", tr.Paths)
	}
	if tr.RetentionDays != 30 {
		t.Errorf("RetentionDays = %d, want it carried with the report", tr.RetentionDays)
	}
}

// TestTrailWindowIsActuallyBounded is the assertion that catches the most likely
// way this goes wrong. created_at is a SQLite DATETIME stored as
// "YYYY-MM-DD HH:MM:SS" in UTC; comparing it against a Go time formatted any
// other way compares two strings that merely look like dates, and the comparison
// silently succeeds while selecting the wrong rows.
func TestTrailWindowIsActuallyBounded(t *testing.T) {
	s := trailStore(t)
	now := time.Now().UTC()

	insertBlock(t, s, now.Add(-30*time.Minute), "recent", "/a", "")
	for i := 0; i < 20; i++ {
		insertBlock(t, s, now.Add(-time.Duration(10+i)*time.Hour), "old", "/b", "")
	}

	tr, err := s.ReadTrail(context.Background(), 1, 8, 30)
	if err != nil {
		t.Fatalf("ReadTrail: %v", err)
	}
	if tr.TotalBlocks != 1 {
		t.Errorf("a one-hour window returned %d blocks, want 1 — the created_at comparison "+
			"is not bounding anything", tr.TotalBlocks)
	}
	if len(tr.Reasons) != 1 || tr.Reasons[0].Key != "recent" {
		t.Errorf("reasons = %+v, want only the in-window row", tr.Reasons)
	}
}

// TestHourlySeriesIsChronological — this is the time dimension the panel has
// never had: every counter on it is cumulative-since-boot, so an operator can
// see that thousands of requests were blocked without being able to tell whether
// that was last night or over six weeks.
func TestHourlySeriesIsChronological(t *testing.T) {
	s := trailStore(t)
	now := time.Now().UTC().Truncate(time.Hour)

	insertBlock(t, s, now.Add(-3*time.Hour+time.Minute), "r", "/p", "")
	insertBlock(t, s, now.Add(-time.Hour+time.Minute), "r", "/p", "")
	insertBlock(t, s, now.Add(-time.Hour+2*time.Minute), "r", "/p", "")
	insertChallenge(t, s, now.Add(-time.Hour+time.Minute), "issued")
	insertChallenge(t, s, now.Add(-time.Hour+2*time.Minute), "solved")

	tr, err := s.ReadTrail(context.Background(), 6, 8, 30)
	if err != nil {
		t.Fatalf("ReadTrail: %v", err)
	}
	if len(tr.Hours) < 2 {
		t.Fatalf("hourly series has %d buckets, want at least 2: %+v", len(tr.Hours), tr.Hours)
	}
	for i := 1; i < len(tr.Hours); i++ {
		if tr.Hours[i].Hour <= tr.Hours[i-1].Hour {
			t.Errorf("hourly series is not ascending at %d: %q then %q",
				i, tr.Hours[i-1].Hour, tr.Hours[i].Hour)
		}
	}
	last := tr.Hours[len(tr.Hours)-1]
	if last.Blocks != 2 || last.Challenges != 1 || last.Solved != 1 {
		t.Errorf("most recent bucket = %+v, want 2 blocks / 1 challenge / 1 solved", last)
	}
}

// TestPassRateRefusesToReportNoise — two challenges and one solve is not a "50%
// pass rate" in any useful sense, and rendering it as one invites an operator to
// retune thresholds on nothing.
func TestPassRateRefusesToReportNoise(t *testing.T) {
	if _, ok := (HourBucket{Challenges: 2, Solved: 1}).PassRate(); ok {
		t.Error("a 2-sample hour was reported as a meaningful pass rate")
	}
	rate, ok := (HourBucket{Challenges: 100, Solved: 90}).PassRate()
	if !ok || rate < 0.89 || rate > 0.91 {
		t.Errorf("PassRate() = %v, %v for 90/100, want ~0.9 and meaningful", rate, ok)
	}
	if _, ok := (HourBucket{}).PassRate(); ok {
		t.Error("an empty hour reported a meaningful pass rate")
	}
}

// TestEmptyValuesGetTheirOwnBucket — country_code is empty whenever no geo
// lookup is wired, which is the default. Left alone, "" sorts to the top of the
// list as though it were a country, and the panel's most prominent row becomes a
// blank one.
func TestEmptyValuesGetTheirOwnBucket(t *testing.T) {
	s := trailStore(t)
	now := time.Now().UTC()
	for i := 0; i < 4; i++ {
		insertBlock(t, s, now.Add(-time.Minute), "r", "/p", "")
	}
	insertBlock(t, s, now.Add(-time.Minute), "r", "/p", "DE")

	tr, err := s.ReadTrail(context.Background(), 24, 8, 30)
	if err != nil {
		t.Fatalf("ReadTrail: %v", err)
	}
	if len(tr.Countries) == 0 {
		t.Fatal("no country aggregate")
	}
	if tr.Countries[0].Key == "" {
		t.Error("the empty country is rendered as a blank row at the top of the list")
	}
	if tr.Countries[0].Key != "(no geo data)" || tr.Countries[0].Count != 4 {
		t.Errorf("top country = %+v, want the labelled no-geo bucket with 4", tr.Countries[0])
	}
}

// TestTrailSurvivesAnAbsentStore — the panel calls this on every refresh, and a
// store can be nil while the shield is disabled or the adaptive DB is off.
func TestTrailSurvivesAnAbsentStore(t *testing.T) {
	var s *Store
	tr, err := s.ReadTrail(context.Background(), 24, 8, 30)
	if err != nil {
		t.Errorf("nil store returned an error: %v", err)
	}
	if tr.TotalBlocks != 0 {
		t.Errorf("nil store reported %d blocks", tr.TotalBlocks)
	}
}

// TestTopNIsClamped — the limit reaches SQL. An unbounded value would let a
// caller ask one query to materialise every distinct path an attacker has ever
// probed, on the same SQLite the site is served from.
func TestTopNIsClamped(t *testing.T) {
	s := trailStore(t)
	now := time.Now().UTC()
	// MORE distinct paths than the clamp allows, or the assertion below is
	// vacuous — an earlier version inserted 30 and passed with the clamp removed,
	// because LIMIT 1000 over 30 rows returns 30 either way.
	for i := 0; i < 80; i++ {
		insertBlock(t, s, now.Add(-time.Minute), "r", "/p"+strconv.Itoa(i), "")
	}
	tr, err := s.ReadTrail(context.Background(), 24, 1000, 30)
	if err != nil {
		t.Fatalf("ReadTrail: %v", err)
	}
	if len(tr.Paths) > 50 {
		t.Errorf("topN=1000 returned %d of 80 distinct paths — the clamp is not applied, so a "+
			"caller can make one query materialise every path an attacker has ever probed", len(tr.Paths))
	}
	// And a sane request is honoured exactly.
	if tr, err = s.ReadTrail(context.Background(), 24, 5, 30); err != nil || len(tr.Paths) != 5 {
		t.Errorf("topN=5 returned %d rows (err=%v), want exactly 5", len(tr.Paths), err)
	}
}
