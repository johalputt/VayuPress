// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/domain"
)

// segmentCall sends one segment request, for site (nil for the install's).
func segmentCall(a *App, h http.HandlerFunc, method string, site *domain.Domain, sid, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/os/api/analytics/segments", strings.NewReader(body))
	rc := chi.NewRouteContext()
	if sid != "" {
		rc.URLParams.Add("sid", sid)
	}
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rc)
	if site != nil {
		ctx = context.WithValue(ctx, ctxScopedDomainKey, *site)
	}
	w := httptest.NewRecorder()
	h(w, r.WithContext(ctx))
	return w
}

// A segment saved on a site's page is that site's: it narrows that site's
// Explorer, another site's URL cannot delete it, and a body naming another
// site saves nothing.
func TestASegmentIsSavedForItsSiteAndApplied(t *testing.T) {
	a := exploreApp(t)
	one, two := domain.Domain{ID: "s1", Host: "one.test"}, domain.Domain{ID: "s2", Host: "two.test"}
	body := `{"domain_id":"s1","name":"From search","filters":[{"dim":"visit_channel","op":"is","value":"organic"}]}`
	if w := segmentCall(a, a.handleOSSegmentCreate, http.MethodPost, &one, "", strings.Replace(body, `"s1"`, `"s2"`, 1)); w.Code != http.StatusBadRequest {
		t.Errorf("a body naming another site answered %d", w.Code)
	}
	bad := `{"domain_id":"s1","name":"x","filters":[{"dim":"visit_views","op":"atleast","value":"many"}]}`
	if w := segmentCall(a, a.handleOSSegmentCreate, http.MethodPost, &one, "", bad); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "whole number") {
		t.Errorf("a segment that cannot be applied answered %d: %s", w.Code, w.Body.String())
	}
	w := segmentCall(a, a.handleOSSegmentCreate, http.MethodPost, &one, "", body)
	var created struct{ ID string }
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("no id: %d %s", w.Code, w.Body.String())
	}
	// Site one's visit v1 arrived from a search: its three views of /a.
	page := explorePage(t, a, "/os/d/s1/analytics/explore?metric=views&by=path&days=7&seg="+created.ID, &one).Body.String()
	for _, want := range []string{`in the segment &#34;From search&#34;`, "<td>/a</td><td>3</td>", `<option value="` + created.ID + `" selected>From search</option>`} {
		if !strings.Contains(page, want) {
			t.Errorf("the Explorer with the segment has no %q", want)
		}
	}
	if strings.Contains(page, "<td>/b</td>") {
		t.Errorf("the segment did not narrow the count")
	}
	// Another site's Explorer does not know it.
	if other := explorePage(t, a, "/os/d/s2/analytics/explore?metric=views&days=7&seg="+created.ID, &two).Body.String(); !strings.Contains(other, "no such segment here") {
		t.Errorf("site two's Explorer applies site one's segment")
	}
	if w := segmentCall(a, a.handleOSSegmentDelete, http.MethodDelete, &two, created.ID, ""); w.Code != http.StatusNotFound {
		t.Errorf("site two deleting site one's segment answered %d", w.Code)
	}
	if w := segmentCall(a, a.handleOSSegmentDelete, http.MethodDelete, &one, created.ID, ""); w.Code != http.StatusOK {
		t.Errorf("site one deleting its segment answered %d", w.Code)
	}
}

// The Analytics page lists each segment with its visitors and their share,
// linked into the Explorer with it applied.
func TestTheAnalyticsPageListsSegments(t *testing.T) {
	a := exploreApp(t)
	ctx := context.Background()
	if empty := a.osSegmentsSection(ctx, hostedView(domain.Domain{ID: "s1"}), 7); !strings.Contains(empty, "No segments yet") {
		t.Errorf("no segments is not said")
	}
	id, err := a.analytics.CreateSegment(ctx, "s1", "Mobile", []analytics.ExploreFilter{{Dim: "device", Op: analytics.FilterIs, Value: "mobile"}})
	if err != nil {
		t.Fatal(err)
	}
	got := a.osSegmentsSection(ctx, hostedView(domain.Domain{ID: "s1"}), 7)
	for _, want := range []string{`href="/os/d/s1/analytics/explore?seg=` + id + `&amp;metric=visitors&amp;by=day&amp;days=7">Mobile</a>`,
		`device is &#34;mobile&#34;`, "<td>1</td><td>50%</td>"} {
		if !strings.Contains(got, want) {
			t.Errorf("the segments section has no %q:\n%s", want, got)
		}
	}
}

// The Explorer offers the segments to start from, and saving the filters
// only when there are filters to save.
func TestTheExplorerOffersSegments(t *testing.T) {
	a := exploreApp(t)
	plain := explorePage(t, a, "/os/analytics/explore?metric=views&days=7", nil).Body.String()
	if !strings.Contains(plain, "Start from: ") || !strings.Contains(plain, ">Organic search</a>") || strings.Contains(plain, `data-sheet="segment-new"`) {
		t.Errorf("without filters the Explorer does not offer presets alone")
	}
	filtered := explorePage(t, a, "/os/analytics/explore?metric=views&days=7&fd=device&fo=is&fv=mobile", nil).Body.String()
	if !strings.Contains(filtered, `data-sheet="segment-new"`) || !strings.Contains(filtered, "It stands for device is &#34;mobile&#34;.") {
		t.Errorf("with filters the Explorer does not offer to save them")
	}
}
