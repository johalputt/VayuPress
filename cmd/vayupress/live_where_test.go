// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// The live panel names each city with its country and the country's flag,
// and each country with its flag, from the console's own flags folder.
func TestLiveCitiesAndCountriesCarryTheirFlags(t *testing.T) {
	openMigratedDB(t)
	for _, q := range []string{
		`INSERT INTO analytics_sessions(id,visitor_id,country,city,device,browser,created_at) VALUES
		 ('s1','v1','IN','Pune','Mobile','Chrome',datetime('now')),
		 ('s2','v2','FR','Paris','Desktop','Firefox',datetime('now'))`,
		`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id) VALUES
		 ('p1','s1','/a',1,'',datetime('now'),'v1'),
		 ('p2','s2','/a',1,'',datetime('now'),'v2')`,
	} {
		if _, err := dbpkg.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{analytics: analytics.New(dbpkg.DB)}
	rec := httptest.NewRecorder()
	a.writeRealtime(rec, httptest.NewRequest(http.MethodGet, "/os/api/analytics/realtime", nil), analytics.ScopeAllDomains)
	var got realtimeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	cities := map[string]string{}
	for _, c := range got.ActiveCities {
		cities[c.Name] = c.Flag
	}
	if cities["Pune, India"] != "/os/static/flags/in.svg" || cities["Paris, France"] != "/os/static/flags/fr.svg" {
		t.Errorf("live cities %+v, want Pune, India and Paris, France with their countries' flags", got.ActiveCities)
	}
	countries := map[string]string{}
	for _, c := range got.ActiveCountries {
		countries[c.Name] = c.Flag
	}
	if countries["India"] != "/os/static/flags/in.svg" {
		t.Errorf("live countries %+v, want India with its flag", got.ActiveCountries)
	}
	if len(got.ActiveDevices) != 2 || len(got.ActiveBrowsers) != 2 {
		t.Errorf("live devices %+v and browsers %+v, want two of each", got.ActiveDevices, got.ActiveBrowsers)
	}
}

// Location's country list draws each country's flag before its name, and a
// label that is not a country draws none.
func TestLocationCountriesCarryTheirFlags(t *testing.T) {
	out := osGeoSection([]analytics.AudienceStat{{Label: "IN", Count: 3}, {Label: "<b>", Count: 1}}, nil, nil, "")
	if !strings.Contains(out, `<img class="vp-flag-img" src="/os/static/flags/in.svg" alt="" width="20" height="15" loading="lazy" decoding="async"> India`) {
		t.Errorf("India has no flag before its name:\n%s", out)
	}
	if strings.Count(out, "vp-flag-img") != 1 {
		t.Errorf("a label that is not a country drew a flag:\n%s", out)
	}
}
