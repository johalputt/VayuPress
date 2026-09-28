// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/metrics"
)

// A request is counted under the router's pattern for it, not the address it
// asked for: a thousand article slugs are one route, and the map stays bounded.
// A request no route matched is counted as what it may be, an uploaded site's
// file or a miss, rather than dropped.
func TestLatencyIsRecordedPerRoutePattern(t *testing.T) {
	r := chi.NewRouter()
	r.Use(structuredLoggerMiddleware)
	r.Get("/latency-test/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	for _, p := range []string{"/latency-test/1", "/latency-test/2", "/latency-test/3", "/nowhere-like-this"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}
	seen := map[string]int64{}
	for _, st := range metrics.RouteLatency.Slowest(1000) {
		seen[st.Route] = st.Requests
	}
	if seen["GET /latency-test/{id}"] < 3 {
		t.Errorf("three requests to one route were not counted under its pattern: %v", seen)
	}
	for route := range seen {
		if route == "GET /latency-test/1" || route == "GET /nowhere-like-this" {
			t.Errorf("a raw address became a route of its own: %q", route)
		}
	}
	if seen["GET (uploaded sites and not found)"] < 1 {
		t.Errorf("an unmatched request was not counted: %v", seen)
	}
}

// The connector reads the same breakdown the Monitoring page shows.
//
// The route is recorded directly, and slower than any request a test serves:
// the tool lists only the slowest fifteen, and the whole suite shares one
// window, so a route served in no time fell off the list whenever the suite
// ran together (CI's integration run) and passed when this test ran alone.
// That the middleware records a route is TestLatencyIsRecordedPerRoutePattern.
func TestTheConnectorReportsTheSlowestRoutes(t *testing.T) {
	a := siteApp(t)
	metrics.RouteLatency.Record("GET /connector-latency/{id}", 10*time.Minute)

	j, err := runTool(t, a, "get_performance", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	routes, _ := j["slowest_routes"].([]any)
	for _, x := range routes {
		if x.(map[string]any)["route"] == "GET /connector-latency/{id}" {
			return
		}
	}
	t.Errorf("get_performance does not name the route just served: %v", j["slowest_routes"])
}
