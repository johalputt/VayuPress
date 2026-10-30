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

// The depth and the engagement are read from a range of the time index,
// never a whole index.
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
		for _, q := range []struct {
			sql  string
			args []any
		}{{readingDepthSQL(clause), append(engagementArgs(args), 10)}, {pageEngagementSQL(clause), engagementArgs(args)}} {
			rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+q.sql, q.args...)
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
}

// Readers' p75 is the nearest rank, the ceil(0.75n)-th smallest, of a
// person's readings alone: of 100, 200, 300 and 400 ms, 300, whatever a bot
// measured.
func TestVitalsP75IsTheNearestRankOfReaders(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	now := time.Now().UTC()
	for i, lcp := range []int{400, 100, 300, 200} {
		in := enter("v"+string(rune('a'+i)), "/a", "direct", "", "human", now)
		in.DomainID, in.LCPMs = "s1", lcp
		if err := s.RecordEnter(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	bot := enter("bot", "/a", "direct", "", "Headless", now)
	bot.DomainID, bot.LCPMs = "s1", 9000
	if err := s.RecordEnter(ctx, bot); err != nil {
		t.Fatal(err)
	}
	v, err := s.WebVitalsP75Scoped(ctx, "s1", 7)
	if err != nil {
		t.Fatal(err)
	}
	if v.P75LCPMs != 300 || v.Samples != 4 {
		t.Errorf("p75 LCP = %d over %d samples, want 300 over 4", v.P75LCPMs, v.Samples)
	}
}

// A layout shift of 0 is a reading when it was measured: three measured
// pages, one never shifting, give a p75 of their third, and a page whose
// browser could not measure one is no reading. A vital no one reported has
// no readings, never a p75 of 0.
func TestALayoutShiftOfZeroIsAReading(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	now := time.Now().UTC()
	for i, c := range []struct {
		cls   int
		known bool
	}{{0, true}, {5, true}, {20, true}, {0, false}} {
		in := enter("c"+string(rune('a'+i)), "/a", "direct", "", "human", now)
		in.DomainID, in.LCPMs, in.CLSX100, in.CLSKnown = "s1", 500, c.cls, c.known
		if err := s.RecordEnter(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	// The exit beacon of a measured page that never shifted keeps it so,
	// and a later one from a copy of the script that cannot say does not
	// unmark it.
	if err := s.RecordBeacon(ctx, BeaconInput{SessionHash: "ca", PagePath: "/a", CLSX100: 0, CLSKnown: true, Now: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordBeacon(ctx, BeaconInput{SessionHash: "cb", PagePath: "/a", CLSX100: 0, Now: now}); err != nil {
		t.Fatal(err)
	}
	v, err := s.WebVitalsP75Scoped(ctx, "s1", 7)
	if err != nil {
		t.Fatal(err)
	}
	if v.SamplesCLS != 3 || v.P75CLSX100 != 20 {
		t.Errorf("CLS = %d over %d readings, want 20 over 3", v.P75CLSX100, v.SamplesCLS)
	}
	if v.SamplesINP != 0 || v.SamplesLCP != 4 || v.Samples != 4 {
		t.Errorf("readings: LCP %d, INP %d, most %d; want 4, 0, 4", v.SamplesLCP, v.SamplesINP, v.Samples)
	}
}

// A page's engagement counts the readings measured as the reader left, a
// person's alone, on its own site, and of them those that stayed 30 seconds
// and scrolled a quarter of the page, both exactly.
func TestPageEngagementCountsEngagedReadings(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	now := time.Now().UTC()
	read := func(sess, site, ct string, secs, depth int, beacon bool) {
		t.Helper()
		in := enter(sess, "/a", "direct", "", ct, now)
		in.DomainID = site
		if err := s.RecordEnter(ctx, in); err != nil {
			t.Fatal(err)
		}
		if beacon {
			if err := s.RecordBeacon(ctx, BeaconInput{SessionHash: sess, PagePath: "/a", TimeOnPage: secs, ScrollDepth: depth, Now: now}); err != nil {
				t.Fatal(err)
			}
		}
	}
	read("e1", "s1", "human", 30, 25, true) // engaged, on both thresholds
	read("e2", "s1", "human", 29, 100, true)
	read("e3", "s1", "human", 100, 24, true)
	read("e4", "s1", "human", 0, 0, false)     // left before its beacon
	read("e5", "s1", "BadBot", 100, 100, true) // not a reader
	read("e6", "s2", "human", 100, 100, true)  // another site
	got, err := s.PageEngagement(ctx, "s1", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (PageEngaged{Path: "/a", Readings: 3, Engaged: 1}) {
		t.Errorf("site one's /a = %+v, want 3 readings, 1 engaged", got)
	}
	if all, _ := s.PageEngagement(ctx, AllDomains, 7); len(all) != 1 || all[0].Readings != 4 || all[0].Engaged != 2 {
		t.Errorf("every site's /a = %+v, want 4 readings, 2 engaged", all)
	}
}

// engagementArgs is args with the period's end after its start, as
// PageEngagement passes them.
func engagementArgs(args []any) []any {
	return append([]any{args[0], time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)}, args[1:]...)
}
