// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/users"
)

// A job's correlation ID is whatever the request that caused it sent in
// X-Correlation-ID, and the Replay page printed it, the op and the dead reason
// into its tables as markup. An anonymous visitor could put HTML in front of
// the administrator who opens the page. Every field is text. One seed per
// field, in each table.
func TestTheReplayPagePrintsJobFieldsAsText(t *testing.T) {
	a := resetSessionApp(t)
	for _, status := range []string{"dead_letter", "quarantined"} {
		if _, err := dbpkg.DB.Exec(`INSERT INTO write_jobs(article_json,op,status,dead_reason,correlation_id,created_at)
			VALUES('{}','<i>op','` + status + `','<u>why','<b>corr',datetime('now'))`); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/os/replay", nil)
	r = withUser(r, &users.User{Role: users.RoleAdmin})
	w := httptest.NewRecorder()
	a.handleReplayPage(w, r)
	body := w.Body.String()
	for _, raw := range []string{"<i>op", "<u>why", "<b>corr"} {
		if strings.Contains(body, raw) {
			t.Errorf("the Replay page printed %q as markup", raw)
		}
	}
	if !strings.Contains(body, "&lt;b&gt;corr") {
		t.Error("the correlation ID is missing from the page altogether")
	}
}
