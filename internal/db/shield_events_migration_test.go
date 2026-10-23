// SPDX-License-Identifier: Apache-2.0

package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// applyUpTo runs, as runMigrations does, every migration before version.
func applyUpTo(t testing.TB, db *sql.DB, version string) {
	t.Helper()
	for _, m := range migrations {
		if m.Version >= version {
			return
		}
		applySQL(t, db, m.Up)
	}
}

func applySQL(t testing.TB, db *sql.DB, src string) {
	t.Helper()
	for _, stmt := range strings.Split(src, "\n") {
		if stmt = strings.TrimSpace(stmt); stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil &&
			!strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func migration116(t testing.TB) migration {
	t.Helper()
	for _, m := range migrations {
		if strings.HasPrefix(m.Version, "116-") {
			return m
		}
	}
	t.Fatal("migration 116 is not embedded")
	return migration{}
}

func freshDB(t testing.TB) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "m.db")+"?_journal_mode=WAL")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// The trail's rows move into shield_events in the new shape: a block once,
// a challenge with its outcome, a tarpit delay as its own action, each with
// its site and request. The duplicate row a block wrote to the challenge
// table is not kept, and the old tables are gone.
func TestMigration116MovesTheTrail(t *testing.T) {
	db := freshDB(t)
	applyUpTo(t, db, "116-")
	for _, q := range []string{
		`INSERT INTO vayushield_blocked(request_path,block_reason,ip_hash,user_agent,country_code,domain_id,request_id,created_at) VALUES('/wp-login.php','scanner','h1','curl','CN','s1','r1','2026-10-20 10:00:00')`,
		`INSERT INTO vayushield_challenges(challenge_type,outcome,ip_hash,domain_id,request_id,created_at) VALUES('pow','issued','h2','s1','r2','2026-10-20 10:01:00')`,
		`INSERT INTO vayushield_challenges(challenge_type,outcome,ip_hash,domain_id,created_at) VALUES('pow','solved','h2','s1','2026-10-20 10:02:00')`,
		`INSERT INTO vayushield_challenges(challenge_type,outcome,ip_hash,domain_id,created_at) VALUES('block','blocked','h1','s1','2026-10-20 10:00:00')`,
		`INSERT INTO vayushield_challenges(challenge_type,outcome,ip_hash,created_at) VALUES('tarpit','delayed','h3','2026-10-20 10:03:00')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	applySQL(t, db, migration116(t).Up)

	rows, err := db.Query(`SELECT action, outcome, reason, path, ua_family, country_code, domain_id, request_id, created_at FROM shield_events ORDER BY created_at, action`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var a, o, re, p, ua, c, d, rq, at string
		if err := rows.Scan(&a, &o, &re, &p, &ua, &c, &d, &rq, &at); err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.Join([]string{a, o, re, p, ua, c, d, rq, at[:16]}, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	want := []string{
		"block||scanner|/wp-login.php|curl|CN|s1|r1|2026-10-20T10:00",
		"challenge|issued|||||s1|r2|2026-10-20T10:01",
		"challenge|solved|||||s1||2026-10-20T10:02",
		"tarpit|delayed|||||*||2026-10-20T10:03",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("moved rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, old := range []string{"vayushield_blocked", "vayushield_challenges"} {
		var n int
		_ = db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE name=?`, old).Scan(&n)
		if n != 0 {
			t.Errorf("%s survived the migration", old)
		}
	}

	// And back: the old tables return, empty, and shield_events goes.
	applySQL(t, db, migration116(t).Down)
	for name, want := range map[string]int{"vayushield_blocked": 1, "vayushield_challenges": 1, "shield_events": 0} {
		var n int
		_ = db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
		if n != want {
			t.Errorf("after the down migration %s exists %d times, want %d", name, n, want)
		}
	}
}

// What the move costs at a busy install's size. Opt in, it writes millions
// of rows: VAYU_MIGRATION_SCALE=4300000 go test -run Migration116Scale.
func TestMigration116Scale(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("VAYU_MIGRATION_SCALE"))
	if n <= 0 {
		t.Skip("set VAYU_MIGRATION_SCALE to the number of challenge rows")
	}
	db := freshDB(t)
	applyUpTo(t, db, "116-")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ch, err := tx.Prepare(`INSERT INTO vayushield_challenges(challenge_type,outcome,ip_hash,fingerprint_hash,country_code,domain_id,created_at) VALUES('pow',?,?,?,'IN',?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ch.Close() }()
	bl, err := tx.Prepare(`INSERT INTO vayushield_blocked(request_path,block_reason,ip_hash,user_agent,country_code,domain_id,created_at) VALUES('/wp-login.php','bot_score>=block_threshold',?,'curl','CN',?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()
	base := time.Now().UTC().AddDate(0, 0, -30)
	outcomes := []string{"issued", "issued", "issued", "solved", "blocked", "delayed"}
	for i := 0; i < n; i++ {
		at := base.Add(time.Duration(i) * 30 * 24 * time.Hour / time.Duration(n)).Format("2006-01-02 15:04:05")
		site := "s" + strconv.Itoa(i%14)
		if _, err := ch.Exec(outcomes[i%len(outcomes)], "h"+strconv.Itoa(i%50000), "f"+strconv.Itoa(i%9000), site, at); err != nil {
			t.Fatal(err)
		}
		if i%15 == 0 {
			if _, err := bl.Exec("h"+strconv.Itoa(i%50000), site, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	tx2, _ := db.Begin()
	for _, stmt := range strings.Split(migration116(t).Up, "\n") {
		if stmt = strings.TrimSpace(stmt); stmt != "" {
			if _, err := tx2.Exec(stmt); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}
	var moved int
	_ = db.QueryRow(`SELECT COUNT(1) FROM shield_events`).Scan(&moved)
	t.Logf("migration 116 moved %d events (from %d challenge rows and %d blocks) in %v", moved, n, n/15+1, time.Since(start).Round(time.Millisecond))
}
