// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// siteRoutes walks the real console router for every route under a hosted
// site's address, with the names of the middleware each runs behind.
func siteRoutes(t *testing.T) map[string][]string {
	t.Helper()
	router := chi.NewRouter()
	(&App{}).registerAdminOSUIRoutes(router)
	out := map[string][]string{}
	err := chi.Walk(router, func(method, route string, _ http.Handler, mws ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/os/d/{id}") {
			return nil
		}
		var names []string
		for _, mw := range mws {
			names = append(names, runtime.FuncForPC(reflect.ValueOf(mw).Pointer()).Name())
		}
		out[method+" "+route] = names
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A key bound to one hosted site reaches no other site's console route:
// every route under /os/d/{id}, as the real router registers them (so a
// route added later is held too), runs behind scopedDomainMiddleware, and
// that middleware refuses a key bound to another site before any handler
// runs, while letting the site's own key through.
func TestAKeyBoundToOneSiteReachesNoOtherSitesRoute(t *testing.T) {
	routes := siteRoutes(t)
	if len(routes) < 40 {
		t.Fatalf("%d routes under /os/d/{id}; the walk did not see the route table", len(routes))
	}
	for r, mws := range routes {
		if !strings.Contains(strings.Join(mws, " "), ".scopedDomainMiddleware") {
			t.Errorf("%s does not run behind scopedDomainMiddleware: %v", r, mws)
		}
	}

	a, _ := shieldSitesApp(t)
	if _, err := dbpkg.DB.Exec(`INSERT INTO domains(id,host,site_type,is_primary,status,created_at,updated_at) VALUES('c2','two.example','blog',0,'active',datetime('now'),datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	param := regexp.MustCompile(`\{[a-z]+\}`)
	for r := range routes {
		method, route, _ := strings.Cut(r, " ")
		path := strings.TrimSuffix(strings.Replace(route, "{id}", "c2", 1), "/*")
		path = param.ReplaceAllString(path, "x")
		for key, wantReached := range map[string]bool{"c1": false, "c2": true} {
			reached := false
			mux := chi.NewRouter()
			mux.Route("/os/d/{id}", func(dr chi.Router) {
				dr.Use(a.scopedDomainMiddleware)
				dr.HandleFunc("/*", func(http.ResponseWriter, *http.Request) { reached = true })
				dr.HandleFunc("/", func(http.ResponseWriter, *http.Request) { reached = true })
			})
			req := auth.RequestWithKeyInfo(httptest.NewRequest(method, path, nil), apikeys.KeyInfo{ID: "k-" + key, DomainID: key})
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if reached != wantReached {
				t.Errorf("%s %s with a key bound to %s: reached %v, want %v (status %d)", method, path, key, reached, wantReached, w.Code)
			}
			if !wantReached && w.Code != http.StatusForbidden {
				t.Errorf("%s %s with another site's key answered %d, want 403", method, path, w.Code)
			}
		}
	}
}
