// SPDX-License-Identifier: Apache-2.0

package botdb

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const blockedSchema = `CREATE TABLE vayushield_blocked(id INTEGER PRIMARY KEY AUTOINCREMENT,fingerprint_hash TEXT NOT NULL DEFAULT '',ja3_hash TEXT NOT NULL DEFAULT '',ip_hash TEXT NOT NULL DEFAULT '',user_agent TEXT NOT NULL DEFAULT '',request_path TEXT NOT NULL DEFAULT '',block_reason TEXT NOT NULL DEFAULT '',bot_score REAL NOT NULL DEFAULT 0,country_code TEXT NOT NULL DEFAULT '',operator_reviewed INTEGER NOT NULL DEFAULT 0,false_positive INTEGER NOT NULL DEFAULT 0,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`

// TestPurgeStaleChunkedRemovesAll: a large backlog of stale one-off candidates
// (well over one chunk) is fully removed across chunks, and fresh/recurring rows
// survive — so a swarm's one-off fingerprints never accumulate.
func TestPurgeStaleChunkedRemovesAll(t *testing.T) {
	s := testStore(t)
	old := time.Now().UTC().AddDate(0, 0, -30)
	// 1200 stale one-off candidates (> 2 chunks) + one recurring + one fresh.
	for i := 0; i < 1200; i++ {
		_, err := s.db.Exec(`INSERT INTO vayushield_signatures(fingerprint_hash,classification,auto_learned,operator_verified,confidence,first_seen,last_seen,request_count) VALUES(?,?,1,0,0.6,?,?,1)`,
			"stale"+strconv.Itoa(i), "unknown", old, old)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	_, _ = s.db.Exec(`INSERT INTO vayushield_signatures(fingerprint_hash,auto_learned,operator_verified,confidence,first_seen,last_seen,request_count) VALUES('recurring',1,0,0.6,?,?,9)`, old, old)
	_, _ = s.db.Exec(`INSERT INTO vayushield_signatures(fingerprint_hash,auto_learned,operator_verified,confidence,first_seen,last_seen,request_count) VALUES('fresh',1,0,0.6,?,?,1)`, time.Now().UTC(), time.Now().UTC())

	n, err := s.PurgeStale(context.Background(), 7)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1200 {
		t.Fatalf("expected 1200 stale removed, got %d", n)
	}
	var remaining int
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM vayushield_signatures`).Scan(&remaining)
	if remaining != 2 {
		t.Fatalf("recurring + fresh must survive, got %d rows", remaining)
	}
}

func TestPurgeBlockedRetention(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(blockedSchema); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	old := time.Now().UTC().AddDate(0, 0, -30)
	for i := 0; i < 700; i++ {
		_, _ = db.Exec(`INSERT INTO vayushield_blocked(ip_hash,created_at) VALUES(?,?)`, "h"+strconv.Itoa(i), old)
	}
	_, _ = db.Exec(`INSERT INTO vayushield_blocked(ip_hash,created_at) VALUES('recent',?)`, time.Now().UTC())

	n, err := s.PurgeBlocked(context.Background(), 7)
	if err != nil {
		t.Fatalf("purge blocked: %v", err)
	}
	if n != 700 {
		t.Fatalf("expected 700 old rows removed, got %d", n)
	}
	var remaining int
	_ = db.QueryRow(`SELECT COUNT(1) FROM vayushield_blocked`).Scan(&remaining)
	if remaining != 1 {
		t.Fatalf("recent block must survive, got %d", remaining)
	}
}
