// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
)

// The request log said which path was asked for and not of which site, on an
// install serving fourteen. The site is the Host the client sent, so only a
// hostname's shape is written. One seed per rule.
func TestTheRequestLogNamesTheSite(t *testing.T) {
	for host, want := range map[string]string{
		"Johal.in:443":           "johal.in", // lowercased, port dropped
		"blog.example.com":       "blog.example.com",
		"evil host":              "", // a space
		"a\nb":                   "", // a line break
		"x<script>":              "", // markup
		strings.Repeat("a", 254): "", // longer than a hostname
		"":                       "",
	} {
		if got := logSite(host); got != want {
			t.Errorf("logSite(%q) = %q, want %q", host, got, want)
		}
	}
}

// And the request log line carries it.
func TestTheRequestLogLineCarriesTheSite(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	h := structuredLoggerMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/a-post", nil)
	req.Host = "Blog.Example.com:8443"
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !strings.Contains(buf.String(), `"site":"blog.example.com"`) {
		t.Errorf("the request log line does not name the site:\n%s", buf.String())
	}
}

func auditRows(t *testing.T, action string) []string {
	t.Helper()
	rows, err := dbpkg.DB.Query(`SELECT target||' '||detail FROM audit_log WHERE action=?`, action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// A goal, a funnel and a setting could be changed with no record of who or
// when (row 5, 0.5). Each change is audited; a setting by its key alone,
// because some settings are credentials.
func TestAnalyticsAndSettingsChangesAreAudited(t *testing.T) {
	openMigratedDB(t)
	a := &App{analytics: analytics.New(dbpkg.DB), siteSettings: settings.New(dbpkg.DB)}
	do := func(h http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}

	rec := do(a.handleAnalyticsCreateGoal, http.MethodPost, "/os/api/analytics/goals", `{"name":"signup","kind":"event","target":"signup"}`)
	var made struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &made)
	if got := auditRows(t, "analytics.goal.create"); len(got) != 1 || !strings.HasPrefix(got[0], made.ID+" event signup") {
		t.Errorf("a goal made = %v, want one record naming it", got)
	}
	if got := auditRows(t, "analytics.funnel.create"); len(got) != 0 {
		t.Fatalf("records before any funnel: %v", got)
	}
	do(a.handleAnalyticsCreateFunnel, http.MethodPost, "/api/v1/analytics/funnels", `{"name":"buy","steps":[{"name":"a","url_path":"/a"},{"name":"b","url_path":"/b"}],"time_window":3600}`)
	if got := auditRows(t, "analytics.funnel.create"); len(got) != 1 || !strings.Contains(got[0], "buy") {
		t.Errorf("a funnel made = %v, want one record naming it", got)
	}

	const secret = "tsk-live-0123456789"
	do(a.handleOSSettingsAPI, http.MethodPost, "/os/api/settings", `{"key":"`+settings.KeyTorSpaceAPIKey+`","value":"`+secret+`"}`)
	got := auditRows(t, "settings.set")
	if len(got) != 1 || !strings.HasPrefix(got[0], settings.KeyTorSpaceAPIKey) {
		t.Errorf("a setting changed = %v, want one record naming its key", got)
	}
	for _, r := range got {
		if strings.Contains(r, secret) {
			t.Errorf("the audit record carries the setting's value: %q", r)
		}
	}
	do(a.handleOSSettingsAPI, http.MethodPost, "/os/api/settings", `{"key":"`+settings.KeyTorSpaceAPIKey+`","value":"`+secret+`"}`)
	if got := auditRows(t, "settings.set"); len(got) != 1 {
		t.Errorf("saving the same value again = %d records, want still 1", len(got))
	}
}
