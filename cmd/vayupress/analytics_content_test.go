// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	vastore "github.com/johalputt/vayupress/internal/vayuanalytics/store"
)

// A site's content is its own pages, with their readings: eight pages of
// 40 views each, none the week before, so each is rising; each read twelve
// times, /p8 in rows from before the beacon dropped the trailing slash, half
// of them "/p8/", which are the same page's. Another site's busier page is
// not among them.
func TestASitesContentIsItsOwnPages(t *testing.T) {
	openMigratedDB(t)
	ctx := context.Background()
	for _, q := range []string{
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<320)
		 INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id)
		 SELECT 'v'||i, 's'||i, '/p'||((i-1)/40+1), 1, 's1', datetime('now'), 'r'||i FROM n`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<90)
		 INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id)
		 SELECT 'w'||i, 't'||i, '/elsewhere', 1, 's2', datetime('now'), 'q'||i FROM n`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<96)
		 INSERT INTO vayuanalytics_sessions(session_hash,page_path,entry_time,exit_time,client_type,domain_id,engaged)
		 SELECT 'h'||i, CASE WHEN i>90 THEN '/p8/' ELSE '/p'||((i-1)/12+1) END, datetime('now'), datetime('now'), 'human', 's1', i%2 FROM n`,
	} {
		if _, err := dbpkg.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{analytics: analytics.New(dbpkg.DB), vaEngagement: vastore.New(dbpkg.DB)}
	got := a.osContentSection(ctx, "s1", "/os/d/s1/analytics/explore", 7)
	for _, want := range []string{
		"8 pages had views enough to compare, and 8 of them readings enough as well",
		"<td>/p1</td><td>40 views, none in the days before</td>",
		"5 of 8. ", // five of the rising pages shown, the rest in the Explorer
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the site's content has no %q", want)
		}
	}
	if strings.Contains(got, "/elsewhere") {
		t.Error("the site's content lists another site's page")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sites/s1/analytics/content?days=7", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxSiteAnalyticsKey{}, siteAnalyticsScope{scope: "s1", d: domain.Domain{ID: "s1"}}))
	rec := httptest.NewRecorder()
	a.handleSiteAnalyticsContent(rec, req)
	var body struct {
		Categories []analytics.ContentCategory `json:"categories"`
		Engagement int                         `json:"pages_with_engagement"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body.String(), err)
	}
	rising := -1
	for _, c := range body.Categories {
		if c.ID == "rising" {
			rising = c.Total
		}
	}
	if rec.Code != http.StatusOK || body.Engagement != 8 || rising != 8 || len(body.Categories) != 7 {
		t.Errorf("the API's content = %d, %d pages with engagement, %d rising, %d categories; want 200, 8, 8, 7", rec.Code, body.Engagement, rising, len(body.Categories))
	}
}
