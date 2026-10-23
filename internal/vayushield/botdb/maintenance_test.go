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

func TestPurgeEventsRetention(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	createEvents(t, db)
	s := New(db)
	old := time.Now().UTC().AddDate(0, 0, -30)
	for i := 0; i < 700; i++ {
		_, _ = db.Exec(`INSERT INTO shield_events(action,ip_hash,created_at) VALUES('block',?,?)`, "h"+strconv.Itoa(i), old)
	}
	_, _ = db.Exec(`INSERT INTO shield_events(action,ip_hash,created_at) VALUES('challenge','recent',?)`, time.Now().UTC())

	n, err := s.PurgeEvents(context.Background(), 7)
	if err != nil {
		t.Fatalf("purge events: %v", err)
	}
	if n != 700 {
		t.Fatalf("expected 700 old rows removed, got %d", n)
	}
	var remaining int
	_ = db.QueryRow(`SELECT COUNT(1) FROM shield_events`).Scan(&remaining)
	if remaining != 1 {
		t.Fatalf("the recent event must survive, got %d", remaining)
	}
}
