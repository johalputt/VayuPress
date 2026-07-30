// SPDX-License-Identifier: Apache-2.0

package botdb

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// seedChallengeTrail builds the minimum schema the trail reads and fills it with
// one hour's worth of the outcomes the manager actually writes.
func seedChallengeTrail(t *testing.T, outcomes []string) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+t.TempDir()+"/trail.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, ddl := range []string{
		`CREATE TABLE vayushield_blocked(created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  block_reason TEXT, request_path TEXT, country_code TEXT, ip_hash TEXT)`,
		`CREATE TABLE vayushield_challenges(created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		  session_hash TEXT, challenge_type TEXT, bot_score REAL, fingerprint_hash TEXT,
		  outcome TEXT, time_to_solve_ms INTEGER, ip_hash TEXT, country_code TEXT)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}
	for _, o := range outcomes {
		if _, err := db.Exec(`INSERT INTO vayushield_challenges
		  (session_hash,challenge_type,bot_score,fingerprint_hash,outcome,time_to_solve_ms,ip_hash,country_code)
		  VALUES('','pow',0,'',?,0,'h','')`, o); err != nil {
			t.Fatalf("insert %s: %v", o, err)
		}
	}
	return New(db)
}

// TestChallengeTrailCountsInvitationsAndSolves is the regression guard for a
// panel that told an operator, across a full 24 hours and every hour in it,
// "7341 challenged · 0% solved" — not one real browser passing.
//
// Both halves of that fraction were wrong. Nothing anywhere wrote
// outcome='solved' (a solve was acknowledged only to the in-memory calibrator),
// so the numerator could not be anything but zero. And the denominator counted
// every row in the table, which also holds one per block and one per tarpit
// delay — events that were never an invitation and could not be accepted.
func TestChallengeTrailCountsInvitationsAndSolves(t *testing.T) {
	// What a real hour looks like: some invitations, some of them solved, plus
	// the block and tarpit rows that share this table.
	s := seedChallengeTrail(t, []string{
		"issued", "issued", "issued", "issued",
		"solved", "solved",
		"blocked", "blocked", "blocked",
		"delayed",
	})

	tr, err := s.ReadTrail(context.Background(), 24, 10, 365)
	if err != nil {
		t.Fatalf("ReadTrail: %v", err)
	}

	if tr.TotalChallenges != 4 {
		t.Errorf("TotalChallenges = %d, want 4 (only the invitations actually extended; "+
			"blocks and tarpit delays share this table and were inflating the denominator)",
			tr.TotalChallenges)
	}
	if tr.TotalSolved != 2 {
		t.Errorf("TotalSolved = %d, want 2 — if this is 0 the 'solved' outcome is not being "+
			"written and the pass rate is structurally incapable of being anything else",
			tr.TotalSolved)
	}
}

// TestPassRateCannotReadZeroWhenSolvesExist states the operator-facing claim
// directly: a pass rate of 0% must mean nobody passed, not that nobody counted.
func TestPassRateCannotReadZeroWhenSolvesExist(t *testing.T) {
	outcomes := make([]string, 0, 40)
	for i := 0; i < 20; i++ {
		outcomes = append(outcomes, "issued")
	}
	for i := 0; i < 5; i++ {
		outcomes = append(outcomes, "solved")
	}
	s := seedChallengeTrail(t, outcomes)

	tr, err := s.ReadTrail(context.Background(), 24, 10, 365)
	if err != nil {
		t.Fatalf("ReadTrail: %v", err)
	}
	if len(tr.Hours) == 0 {
		t.Fatal("no hourly buckets; the per-hour pass rate is what an operator scans")
	}
	var sawChallenges, sawSolved bool
	for _, h := range tr.Hours {
		if h.Challenges > 0 {
			sawChallenges = true
		}
		if h.Solved > 0 {
			sawSolved = true
		}
	}
	if !sawChallenges {
		t.Error("hourly buckets recorded no challenges at all")
	}
	if !sawSolved {
		t.Error("hourly buckets recorded no solves, so every hour renders 0% — the exact " +
			"reading that sent an operator looking for a broken challenge")
	}
}

// TestTrailOutcomeVocabularyMatchesWhatIsWritten is the guard that would have
// caught the 0%-pass-rate report before an operator did.
//
// The trail's queries and the manager's inserts are in different packages, so
// nothing forced them to agree on the outcome strings. They did not: the query
// looked for 'solved' and 'abandoned' while the manager only ever wrote
// 'issued', 'blocked' and 'delayed'. Both sides were internally consistent and
// both were covered by passing tests, because the test fixtures were written
// from the same imagined vocabulary as the query.
//
// This asserts the vocabulary against the producer's source, so a rename or a
// new outcome on either side has to be reconciled rather than silently
// producing a column of zeros.
func TestTrailOutcomeVocabularyMatchesWhatIsWritten(t *testing.T) {
	src, err := os.ReadFile("../vayushield.go")
	if err != nil {
		t.Skipf("producer source not readable here: %v", err)
	}
	s := string(src)
	// An outcome may be written as a Go argument ("issued") or inlined in the SQL
	// ('solved'). Both count as the manager emitting it — checking only the Go
	// spelling made this very test report a false miss on its first run.
	writes := func(outcome string) bool {
		return strings.Contains(s, `"`+outcome+`"`) || strings.Contains(s, `'`+outcome+`'`)
	}
	// Every outcome this package's SQL reasons about must be one the manager
	// actually emits.
	for _, outcome := range []string{"issued", "solved"} {
		if !writes(outcome) {
			t.Errorf("the trail counts outcome %q, but the manager never writes it — "+
				"that column can only ever read zero", outcome)
		}
	}
	// And the reverse direction: an outcome the manager writes which the trail
	// silently ignores is a gap worth seeing, even when it is deliberate.
	for _, outcome := range []string{"blocked", "delayed"} {
		if !writes(outcome) {
			t.Errorf("expected the manager to still write %q; if it stopped, the trail's "+
				"exclusion of it from the challenge count needs revisiting", outcome)
		}
	}

	// Defining the writer is not writing. The checks above are satisfied by
	// recordChallengeSolved's own body, so deleting its only CALL leaves them
	// green while the pass rate silently returns to a column of zeros — the same
	// shape of hole as a guard that matched a helper's definition instead of its
	// use. Assert the call inside the function that verifies a proof.
	body := funcBody(s, "func (m *Manager) VerifyPoW(")
	if body == "" {
		t.Fatal("VerifyPoW not found in the manager source; this check is no longer anchored")
	}
	if !strings.Contains(body, "recordChallengeSolved(") {
		t.Error("VerifyPoW does not record the solve, so a successful proof never reaches the " +
			"trail and the panel reports 0% of challenges passed no matter how many are")
	}
}

// funcBody returns the source of the function starting with decl, up to the
// next top-level "\nfunc " (or end of file).
func funcBody(src, decl string) string {
	i := strings.Index(src, decl)
	if i < 0 {
		return ""
	}
	rest := src[i+len(decl):]
	if j := strings.Index(rest, "\nfunc "); j >= 0 {
		return rest[:j]
	}
	return rest
}
