// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strconv"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	vastore "github.com/johalputt/vayupress/internal/vayuanalytics/store"
)

// What readers did, for one site: where its visits ended, the links they
// followed out, the files they fetched and how far they read, each its own
// site's alone.
func TestWhatReadersDidIsTheSitesOwn(t *testing.T) {
	a := exploreApp(t)
	for _, q := range []string{
		`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,event_name,domain_id,created_at,visitor_id) VALUES
			('o1','v2','/b',4,'example.com/x','s1',datetime('now'),'r2'),('d1','v2','/b',5,'/guide.pdf','s1',datetime('now'),'r2'),
			('o2','v3','/a',4,'elsewhere.test/y','s2',datetime('now'),'r3')`,
		`INSERT INTO vayuanalytics_sessions(session_hash,page_path,entry_time,exit_time,scroll_depth_percent,client_type,domain_id) VALUES
			('h1','/a',datetime('now'),datetime('now'),100,'human','s1'),('h2','/a',datetime('now'),datetime('now'),40,'human','s1'),
			('h3','/a',datetime('now'),NULL,0,'human','s1'),('h4','/a',datetime('now'),datetime('now'),100,'human','s2')`,
	} {
		if _, err := dbpkg.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	a.vaEngagement = vastore.New(dbpkg.DB)
	got := a.osBehaviourSection(context.Background(), "s1", "/os/d/s1/analytics/explore", 7, noDoor)
	for _, want := range []string{"Where visits ended", "example.com/x", "/guide.pdf",
		"<td>/a</td><td>2</td><td>50%</td><td>50%</td>"} {
		if !strings.Contains(got, want) {
			t.Errorf("site one's section has no %q", want)
		}
	}
	if strings.Contains(got, "elsewhere.test") {
		t.Errorf("site one's section shows site two's link")
	}
	// A share is of every link out, not of the ten listed: with eleven
	// places linked to, one of them twice, the first is 2 of 12.
	for i := 0; i < 10; i++ {
		if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,event_name,domain_id,created_at,visitor_id) VALUES(?,'v2','/b',4,?,'s1',datetime('now'),'r2')`,
			"ox"+strconv.Itoa(i), "z"+strconv.Itoa(i)+".test/"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,event_name,domain_id,created_at,visitor_id) VALUES('o3','v2','/b',4,'example.com/x','s1',datetime('now'),'r2')`); err != nil {
		t.Fatal(err)
	}
	if more := a.osBehaviourSection(context.Background(), "s1", "/os/d/s1/analytics/explore", 7, noDoor); !strings.Contains(more, `example.com/x</span><span class="vp-bar__val">2<span class="vp-bar__pct">16%</span>`) ||
		!strings.Contains(more, "All of them in the Explorer") {
		t.Errorf("the first link out is not 2 of all 12, with the rest in the Explorer")
	}
	// Each visit ended once: on /a, and on /b after its event.
	if strings.Count(got, `<span class="vp-bar__pct">50%</span>`) != 2 {
		t.Errorf("the exits are not one visit each of two")
	}
}
