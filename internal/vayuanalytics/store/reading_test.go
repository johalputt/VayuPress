// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// A page's depth counts the readings that reported one, a person's alone, on
// its own site: one the reader left before a beacon could be sent is no
// reading, and a bot's is none either.
func TestReadingDepthCountsMeasuredReadings(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	now := time.Now().UTC()
	read := func(sess, site, ct string, depth int, beacon bool) {
		t.Helper()
		in := enter(sess, "/a", "direct", "", ct, now)
		in.DomainID = site
		if err := s.RecordEnter(ctx, in); err != nil {
			t.Fatal(err)
		}
		if beacon {
			if err := s.RecordBeacon(ctx, BeaconInput{SessionHash: sess, PagePath: "/a", TimeOnPage: 30, ScrollDepth: depth, Now: now}); err != nil {
				t.Fatal(err)
			}
		}
	}
	read("r1", "s1", "human", 100, true)
	read("r2", "s1", "human", 50, true) // half reached, exactly
	read("r7", "s1", "human", 90, true) // nine tenths reached, exactly
	read("r3", "s1", "human", 10, true)
	read("r4", "s1", "human", 0, false)   // left before its beacon
	read("r5", "s1", "BadBot", 100, true) // not a reader
	read("r6", "s2", "human", 100, true)  // another site
	got, err := s.ReadingDepth(ctx, "s1", 7, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (PageDepth{Path: "/a", Readings: 4, ReachedHalf: 3, ReachedEnd: 2}) {
		t.Errorf("site one's /a = %+v, want 4 readings, 3 at half or past, 2 at nine tenths or past", got)
	}
	all, _ := s.ReadingDepth(ctx, AllDomains, 7, 10)
	if len(all) != 1 || all[0].Readings != 5 {
		t.Errorf("every site's /a = %+v, want 5 readings", all)
	}
}

// The depth is read from a range of the time index, never a whole index.
func TestReadingDepthReadsARange(t *testing.T) {
	prev := config.Cfg.DBPath
	config.Cfg.DBPath = filepath.Join(t.TempDir(), "va.db")
	if err := dbpkg.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dbpkg.ClosePools(); _ = dbpkg.DB.Close(); config.Cfg.DBPath = prev })
	s := New(dbpkg.DB)
	for _, site := range []string{"s1", AllDomains} {
		clause, args := "", []any{fromTime(7)}
		if site != AllDomains {
			clause, args = " AND domain_id=?", append(args, site)
		}
		rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+readingDepthSQL(clause), append(args, 10)...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
		for _, l := range plan {
			if strings.HasPrefix(l, "SCAN vayuanalytics_sessions") {
				t.Errorf("%q walks the table: %s", site, strings.Join(plan, " | "))
			}
		}
	}
}
