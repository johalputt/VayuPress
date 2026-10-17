// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	vastore "github.com/johalputt/vayupress/internal/vayuanalytics/store"
)

// identifying names a column that ties a row to a visitor or a session.
var identifying = map[string]bool{
	"visitor_id": true, "session_id": true, "session_hash": true, "visitor_hash": true, "uniques_hll": true,
}

// timeColumns are the columns the purges date a row by, in preference order.
var timeColumns = []string{"day", "created_at", "entry_time"}

// The privacy disclosure publishes one retention window for analytics, and two
// tables holding visitor or session identifiers were never purged under it.
// The tables are found from the live schema, not from the purges' own lists:
// every analytics table with an identifying column gets a row older than the
// window, and after one purge none may be left.
func TestRetentionCoversEveryTableThatIdentifiesAVisitor(t *testing.T) {
	openMigratedDB(t)
	tables := identifyingAnalyticsTables(t, dbpkg.DB)
	if len(tables) < 5 {
		t.Fatalf("found %d analytics tables with an identifying column, want the six known today: %v", len(tables), tables)
	}
	for _, tbl := range tables {
		seedOldRow(t, dbpkg.DB, tbl)
	}
	a := &App{analytics: analytics.New(dbpkg.DB), vaEngagement: vastore.New(dbpkg.DB)}
	a.purgeExpiredAnalytics(context.Background())
	for _, tbl := range tables {
		var n int
		if err := dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM ` + tbl).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s keeps %d row(s) older than the retention window", tbl, n)
		}
	}
}

func identifyingAnalyticsTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND (name LIKE 'analytics_%' OR name LIKE 'vayuanalytics_%')`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		names = append(names, n)
	}
	rows.Close()
	var out []string
	for _, n := range names {
		for _, c := range columnsOf(t, db, n) {
			if identifying[c.name] {
				out = append(out, n)
				break
			}
		}
	}
	return out
}

type colInfo struct {
	name, typ string
	notNull   bool
	hasDflt   bool
	pk        bool
}

func columnsOf(t *testing.T, db *sql.DB, table string) []colInfo {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var cols []colInfo
	for rows.Next() {
		var cid, nn, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &nn, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, colInfo{name: name, typ: strings.ToUpper(typ), notNull: nn == 1, hasDflt: dflt.Valid, pk: pk > 0})
	}
	return cols
}

// seedOldRow inserts one row dated two years back into table, filling every
// required column with a value of its type.
func seedOldRow(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	cols := columnsOf(t, db, table)
	dated := ""
	for _, want := range timeColumns {
		for _, c := range cols {
			if c.name == want {
				dated = want
				break
			}
		}
		if dated != "" {
			break
		}
	}
	if dated == "" {
		t.Fatalf("%s has an identifying column but no time column a purge could date it by", table)
	}
	var names, marks []string
	var args []any
	for _, c := range cols {
		dateCol := strings.Contains(c.typ, "DATE") || strings.Contains(c.typ, "TIME")
		if c.name != dated && !dateCol && (!c.notNull || c.hasDflt) && !(c.pk && c.typ == "TEXT") {
			continue
		}
		if c.pk && c.typ == "INTEGER" {
			continue
		}
		names, marks = append(names, c.name), append(marks, "?")
		switch {
		case c.name == "day":
			args = append(args, "2020-01-01")
		case c.name == dated, strings.Contains(c.typ, "DATE"), strings.Contains(c.typ, "TIME"):
			args = append(args, "2020-01-01 00:00:00")
		case strings.Contains(c.typ, "INT"), strings.Contains(c.typ, "REAL"):
			args = append(args, 1)
		case strings.Contains(c.typ, "BLOB"):
			args = append(args, []byte{0})
		default:
			args = append(args, fmt.Sprintf("old-%s", c.name))
		}
	}
	q := `INSERT INTO ` + table + `(` + strings.Join(names, ",") + `) VALUES(` + strings.Join(marks, ",") + `)`
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("seed %s: %v (%s)", table, err, q)
	}
}
