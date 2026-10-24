// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	vasession "github.com/johalputt/vayupress/internal/vayuanalytics/session"
	vastore "github.com/johalputt/vayupress/internal/vayuanalytics/store"
	"github.com/johalputt/vayupress/internal/vayushield"
)

// An engagement and vitals row did not say which site its page was on, so no
// site could read its own Performance. The site is the one
// this install served the request for; a beacon naming another is ignored.
func TestAnEngagementRowIsItsSites(t *testing.T) {
	openMigratedDB(t)
	a := &App{
		vayuShield:   vayushield.New(vayushield.Config{Enabled: true}),
		vaSessions:   vasession.NewHasher(),
		vaEngagement: vastore.New(dbpkg.DB),
		domains:      domain.New(dbpkg.DB, dbpkg.DB),
	}
	req := httptest.NewRequest(http.MethodPost, "/__vayuanalytics/enter", strings.NewReader(`{"p":"/a-post","domain_id":"d-other"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-GB,en;q=0.9")
	req.RemoteAddr = "203.0.113.209:4000" // an address no other test spends the beacon budget of
	req = req.WithContext(context.WithValue(req.Context(), ctxKeyDomain{}, domain.Domain{ID: "d-served"}))
	a.handleVAEnter(httptest.NewRecorder(), req)

	var site string
	if err := dbpkg.DB.QueryRow(`SELECT domain_id FROM vayuanalytics_sessions WHERE page_path='/a-post'`).Scan(&site); err != nil {
		t.Fatalf("no engagement row was written: %v", err)
	}
	if site != "d-served" {
		t.Errorf("the engagement row's site = %q, want the served site d-served", site)
	}
}

// A reading is keyed by the page's path as its views are, without a trailing
// slash: "/a-post/" kept its own rows, so its readings never met its views.
func TestAReadingIsKeyedAsItsPagesViews(t *testing.T) {
	openMigratedDB(t)
	a := &App{
		vayuShield:   vayushield.New(vayushield.Config{Enabled: true}),
		vaSessions:   vasession.NewHasher(),
		vaEngagement: vastore.New(dbpkg.DB),
		domains:      domain.New(dbpkg.DB, dbpkg.DB),
	}
	req := httptest.NewRequest(http.MethodPost, "/__vayuanalytics/enter", strings.NewReader(`{"p":"/slashed-post/"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-GB,en;q=0.9")
	req.RemoteAddr = "203.0.113.211:4000" // an address no other test spends the beacon budget of
	a.handleVAEnter(httptest.NewRecorder(), req)

	var path string
	if err := dbpkg.DB.QueryRow(`SELECT page_path FROM vayuanalytics_sessions WHERE page_path LIKE '/slashed-post%'`).Scan(&path); err != nil {
		t.Fatalf("no engagement row was written: %v", err)
	}
	if path != "/slashed-post" {
		t.Errorf("the reading's path = %q, want /slashed-post as its views are recorded", path)
	}
}
