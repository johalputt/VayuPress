// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// Every block and challenge names the site it was on and the request that
// drew it, so an install serving many sites can say which one is under
// attack, and an event leads back to its log line.
func TestAnEventNamesItsSiteAndItsRequest(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, ddl := range []string{
		`CREATE TABLE vayushield_blocked(id INTEGER PRIMARY KEY AUTOINCREMENT,fingerprint_hash TEXT NOT NULL DEFAULT '',ja3_hash TEXT NOT NULL DEFAULT '',ip_hash TEXT NOT NULL DEFAULT '',user_agent TEXT NOT NULL DEFAULT '',request_path TEXT NOT NULL DEFAULT '',block_reason TEXT NOT NULL DEFAULT '',bot_score REAL NOT NULL DEFAULT 0,country_code TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,domain_id TEXT NOT NULL DEFAULT '*',request_id TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE vayushield_challenges(id INTEGER PRIMARY KEY AUTOINCREMENT,session_hash TEXT NOT NULL DEFAULT '',challenge_type TEXT NOT NULL,bot_score REAL NOT NULL DEFAULT 0,fingerprint_hash TEXT NOT NULL DEFAULT '',outcome TEXT NOT NULL DEFAULT '',time_to_solve_ms INTEGER NOT NULL DEFAULT 0,ip_hash TEXT NOT NULL DEFAULT '',country_code TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,domain_id TEXT NOT NULL DEFAULT '*',request_id TEXT NOT NULL DEFAULT '')`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	m := New(Config{Enabled: true, DB: db,
		SiteFn:      func(*http.Request) string { return "site-7" },
		RequestIDFn: func(*http.Request) string { return "req-42" }})
	r := httptest.NewRequest("GET", "/wp-login.php", nil)
	m.recordBlock(r, Verdict{})
	m.recordChallenge(r, Verdict{}, "pow", "issued", 0)
	m.recordChallengeSolved(r)
	for _, table := range []string{"vayushield_blocked", "vayushield_challenges"} {
		rows, err := db.Query(`SELECT domain_id, request_id FROM ` + table)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for rows.Next() {
			var site, req string
			if err := rows.Scan(&site, &req); err != nil {
				t.Fatal(err)
			}
			n++
			if site != "site-7" || req != "req-42" {
				t.Errorf("%s recorded site %q, request %q; want site-7, req-42", table, site, req)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
		if n == 0 {
			t.Errorf("%s recorded nothing", table)
		}
	}
}
