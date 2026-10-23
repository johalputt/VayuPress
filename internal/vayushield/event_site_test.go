// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayushield/challenge"
	"github.com/johalputt/vayupress/internal/vayushield/policy"
	_ "github.com/mattn/go-sqlite3"
)

// eventsDB is an in-memory database holding shield_events as migration 116
// creates it, read from the migration itself.
func eventsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1) // one connection: :memory: is per connection
	t.Cleanup(func() { _ = db.Close() })
	src, err := os.ReadFile("../db/migrations/116-shield-events.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(line, "CREATE ") {
			if _, err := db.Exec(line); err != nil {
				t.Fatal(err)
			}
		}
	}
	return db
}

type eventRow struct {
	site, req, layer, rule, action, outcome, reason, path string
	status                                                int
	evalUS                                                int64
}

func readEvents(t *testing.T, db *sql.DB) []eventRow {
	t.Helper()
	rows, err := db.Query(`SELECT domain_id, request_id, layer, rule_id, action, outcome, reason, path, status, eval_us FROM shield_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []eventRow
	for rows.Next() {
		var e eventRow
		if err := rows.Scan(&e.site, &e.req, &e.layer, &e.rule, &e.action, &e.outcome, &e.reason, &e.path, &e.status, &e.evalUS); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Every event names its site and request, the gate and rule that acted, what
// it answered and what deciding cost; a block is one row, not two.
func TestAnEventSaysWhoActedAndWhatItCost(t *testing.T) {
	db := eventsDB(t)
	m := New(Config{Enabled: true, DB: db, Signer: challenge.NewSigner([]byte("s")),
		SiteFn:      func(*http.Request) string { return "site-7" },
		RequestIDFn: func(*http.Request) string { return "req-42" }})
	m.ApplySettings(Settings{Enabled: true, PoWThreshold: 0.99, JSThreshold: 0.995, BlockThreshold: 1,
		Rules: "block path^=/wp-\nchallenge path=/search\nlimit 1/min path=/feedback"})
	hit := func(path string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "198.51.100.20:40000"
		req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0")
		m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(rec, req)
	}
	hit("/wp-login.php")
	hit("/search")
	hit("/feedback")
	hit("/feedback")
	m.recordChallengeSolved(httptest.NewRequest(http.MethodPost, "/__vayushield/verify", nil))

	got := readEvents(t, db)
	want := []eventRow{
		{action: "block", layer: "operator_rule", rule: "line 1", status: 403, path: "/wp-login.php", reason: "operator rule line 1: block path^=/wp-"},
		{action: "challenge", outcome: "issued", layer: "operator_rule", rule: "line 2", status: 503, path: "/search"},
		{action: "rate_limit", layer: "operator_rule", rule: "line 3", status: 429, path: "/feedback", reason: "operator rule line 3: limit 1/min path=/feedback"},
		{action: "challenge", outcome: "solved", layer: "challenge", path: "/__vayushield/verify"},
	}
	if len(got) != len(want) {
		t.Fatalf("%d events, want %d (one per act; a block is one row):\n%+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.site != "site-7" || g.req != "req-42" {
			t.Errorf("event %d recorded site %q, request %q", i, g.site, g.req)
		}
		if g.action != w.action || g.outcome != w.outcome || g.layer != w.layer || g.rule != w.rule || g.status != w.status || g.path != w.path ||
			(w.reason != "" && g.reason != w.reason) {
			t.Errorf("event %d = %+v,\n want %+v", i, g, w)
		}
		// The three the middleware decided record what deciding cost; the
		// solve, answered by the verify endpoint, has no decision to time.
		if i < 3 && g.evalUS <= 0 {
			t.Errorf("event %d recorded no evaluation cost (%d µs)", i, g.evalUS)
		}
	}
}

// The ladder's own decisions name the ladder, not a rule.
func TestTheLaddersEventNamesTheLadder(t *testing.T) {
	db := eventsDB(t)
	m := New(Config{Enabled: true, DB: db, Signer: challenge.NewSigner([]byte("s"))})
	m.ApplySettings(Settings{Enabled: true, PoWThreshold: 0.01, JSThreshold: 0.98, BlockThreshold: 0.99,
		Rules: "log path^=/"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/article", nil)
	req.Header.Set("User-Agent", "curl/8.5.0")
	m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})).ServeHTTP(rec, req)
	got := readEvents(t, db)
	if len(got) != 1 || got[0].action != "challenge" || got[0].layer != "challenge" || got[0].rule != "" {
		t.Errorf("the ladder's challenge recorded %+v; want one challenge by the challenge layer, no rule", got)
	}
}

// A refusal offered to a browser as a challenge names the gate that refused.
func TestAJailedChallengeNamesItsGate(t *testing.T) {
	db := eventsDB(t)
	m := New(Config{Enabled: true, DB: db, Signer: challenge.NewSigner([]byte("s"))})
	m.ApplySettings(Settings{Enabled: true, Policy: policy.Config{DenyCIDRs: []string{"198.51.100.0/24"}}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/article", nil)
	req.RemoteAddr = "198.51.100.9:40000"
	req.Header.Set("Accept", "text/html")
	m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})).ServeHTTP(rec, req)
	got := readEvents(t, db)
	if len(got) != 1 || got[0].layer != "policy_deny" || got[0].action != "challenge" {
		t.Errorf("recorded %+v; want one challenge by the policy_deny layer", got)
	}
}
