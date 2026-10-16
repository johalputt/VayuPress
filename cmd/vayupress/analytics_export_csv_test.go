// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// A referrer is whatever a visitor's browser, or a script, sends: the export
// writes it as text, never a formula the operator's spreadsheet would run.
func TestAnalyticsCSVExportWritesNoFormula(t *testing.T) {
	openMigratedDB(t)
	a := &App{analytics: analytics.New(dbpkg.DB)}
	// Written as the beacon would store it, whatever the beacon normalises:
	// the export's guard must not depend on what came before it.
	if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,referrer,event_type) VALUES('p1','s1','/a-post',?,1)`,
		`=HYPERLINK("https://evil.example","Click")`); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.handleAnalyticsExport(rec, httptest.NewRequest("GET", "/api/v1/analytics/export?report=referrers&format=csv&days=30", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "HYPERLINK") {
		t.Fatalf("the referrer is not in the export (%d):\n%s", rec.Code, body)
	}
	if !strings.Contains(body, `'=HYPERLINK(`) {
		t.Errorf("the referrer was exported as a formula:\n%s", body)
	}
}
