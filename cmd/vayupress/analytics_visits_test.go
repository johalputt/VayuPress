// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/users"
)

// The visit log is each visit as it went: where from and how it arrived,
// then its pages in order, each a way to that page's report; narrowed, it is
// the narrowed readers' visits alone.
func TestTheVisitLogIsEachVisitAsItWent(t *testing.T) {
	a := focusApp(t)
	per := analyticsSpan{days: 7}
	log := string(a.osVisitLog(a.scoped(context.Background(), per), primaryView, per, time.Now()))
	for _, want := range []string{
		`<div class="sa-trails__meta">just now · India · Desktop · Chrome · from www.google.com</div>`,
		`<a href="/os/analytics?days=7&amp;page=%2Fgoogle-only&amp;view=page">/google-only</a> → <a href="/os/analytics?days=7&amp;page=%2Fgoogle-next&amp;view=page">/google-next</a>`,
		`United States · Desktop · Chrome · from www.bing.com`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("the log has no %q:\n%s", want, log)
		}
	}
	us := analyticsSpan{days: 7, focus: []analytics.ExploreFilter{{Dim: "country", Op: analytics.FilterIs, Value: "US"}}}
	narrowed := string(a.osVisitLog(a.scoped(context.Background(), us), primaryView, us, time.Now()))
	if strings.Contains(narrowed, "google") || !strings.Contains(narrowed, "/bing-only") {
		t.Errorf("the log narrowed to the United States:\n%s", narrowed)
	}
}

// The log is on Live, a narrowed overview and a page's report, and only an
// administrator is shown it: an editor opening the same address, after the
// administrator warmed its cache, sees none of it.
func TestOnlyAnAdministratorSeesTheVisitLog(t *testing.T) {
	a := focusApp(t)
	t.Cleanup(func() { forgetCache(t, adminDash) })
	open := func(role, addr string) string {
		t.Helper()
		var body string
		for deadline := time.Now().Add(5 * time.Second); ; {
			r := httptest.NewRequest(http.MethodGet, addr, nil)
			r = r.WithContext(context.WithValue(r.Context(), ctxUserKey, &users.User{Role: role}))
			w := httptest.NewRecorder()
			a.handleOSAnalytics(w, r)
			body = w.Body.String()
			if !strings.Contains(body, "counted in the background") || time.Now().After(deadline) {
				return body
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	for addr, has := range map[string]bool{
		"/os/analytics?view=live&days=7":                     true,
		"/os/analytics?view=overview&days=7&f=country:is:US": true,
		"/os/analytics?view=page&page=/google-only&days=7":   true,
		"/os/analytics?view=overview&days=7":                 false,
		"/os/analytics?view=audience&days=7&f=country:is:US": false,
	} {
		if got := strings.Contains(open(users.RoleAdmin, addr), `data-part="visits"`); got != has {
			t.Errorf("an administrator's %s: visit log %v, want %v", addr, got, has)
		}
		if strings.Contains(open(users.RoleEditor, addr), `data-part="visits"`) {
			t.Errorf("an editor is shown the visit log on %s", addr)
		}
	}
	// On a page's report the log is the page's readers'.
	page := open(users.RoleAdmin, "/os/analytics?view=page&page=/bing-only&days=7")
	_, log, _ := strings.Cut(page, `data-part="visits"`)
	if !strings.Contains(log, "/bing-only") || strings.Contains(log, "google") {
		t.Errorf("the log on /bing-only's report:\n%s", log)
	}
}
