// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// A site's Searches says what followed its searches: the pages opened and
// the queries tried again, each with its share.
func TestSearchesSayWhatFollowed(t *testing.T) {
	openMigratedDB(t)
	for _, q := range []string{
		`INSERT INTO analytics_sessions(id,visitor_id,domain_id,created_at,events,views) VALUES('v','r','s1',datetime('now','-10 minutes'),6,3)`,
		`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,event_name,domain_id,created_at,visitor_id) VALUES
			('a','v','/search',3,'rust','s1',datetime('now','-9 minutes'),'r'),('b','v','/search',1,'','s1',datetime('now','-9 minutes','+1 second'),'r'),
			('c','v','/search',3,'rust lang','s1',datetime('now','-8 minutes'),'r'),('d','v','/post-b',1,'','s1',datetime('now','-7 minutes'),'r'),
			('e','v','/search',3,'zig','s1',datetime('now','-6 minutes'),'r')`,
	} {
		if _, err := dbpkg.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{analytics: analytics.New(dbpkg.DB)}
	got := a.osSearchesSection(context.Background(), "s1", 7)
	for _, want := range []string{"a page, 1 (33%)", "another search, 1 (33%)", "nothing, 1 (33%)",
		"Opened after searching", "/post-b", "rust → rust lang"} {
		if !strings.Contains(got, want) {
			t.Errorf("site one's Searches has no %q", want)
		}
	}
}

// A page's share is of every search followed by a page, not of the ten
// listed: twelve opened, /p0 twice, read 2 of 12.
func TestPagesAfterSearchingAreSharesOfAll(t *testing.T) {
	openMigratedDB(t)
	for i := 0; i <= 11; i++ {
		v, page := "v"+strconv.Itoa(i), "/p"+strconv.Itoa(i)
		if i == 11 {
			page = "/p0"
		}
		if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,event_name,domain_id,created_at,visitor_id) VALUES
			(?,?,'/search',3,'q','s1',datetime('now','-9 minutes'),'r'),(?,?,?,1,'','s1',datetime('now','-8 minutes'),'r')`, v+"s", v, v+"p", v, page); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{analytics: analytics.New(dbpkg.DB)}
	if got := a.osSearchesSection(context.Background(), "s1", 7); !strings.Contains(got, `/p0</span><span class="vp-bar__val">2<span class="vp-bar__pct">16%</span>`) {
		t.Errorf("/p0 is not 2 of the 12 searches followed by a page")
	}
}
