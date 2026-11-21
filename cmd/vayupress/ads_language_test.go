// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/microcosm-cc/bluemonday"

	"github.com/johalputt/vayupress/internal/ads"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// A hosted site's Advertising is drawn in its reader's language, as the
// install's is, and what its endpoints refuse is said in the language of the
// page that asked: its calls go to the site's own address, not /os/api/. A
// slot's name is the operator's own and is marked not to be translated.
func TestAdvertisingIsDrawnAndAnsweredInThePagesLanguage(t *testing.T) {
	a, _, blog := hostedBlogApp(t)
	a.ads, a.policy = ads.New(dbpkg.DB), bluemonday.UGCPolicy()
	if _, err := a.ads.Create(context.Background(), blog.ID, ads.SlotInput{Name: "Site banner", Placement: ads.PlacementSidebar, Kind: ads.KindImage, ImageURL: "/s.png", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	r := siteAdsRequest(http.MethodGet, "", blog)
	r.Header.Set("Accept-Language", "de")
	rec := httptest.NewRecorder()
	consoleLanguage(http.HandlerFunc(a.handleOSAds)).ServeHTTP(rec, r)
	page := withoutScriptWords(rec.Body.String())
	for _, want := range []string{"Werbeplätze", "Werbeplatz hinzufügen", "Seitenleiste", "Die Anzeigen dieser Website", "Meine Anzeigen auf diese Website kopieren",
		`<td class="row-title" translate="no">Site banner</td>`, `aria-label="Schließen"`, "admin-os-ads.js"} {
		if !strings.Contains(page, want) {
			t.Errorf("the German site's Ads page lacks %q", want)
		}
	}
	for _, english := range []string{"Add an ad slot", "Ad slots", "Sidebar", "This site's ads", "Turn off"} {
		if strings.Contains(page, english) {
			t.Errorf("the German site's Ads page still says %q", english)
		}
	}

	save := httptest.NewRequest(http.MethodPost, "/os/d/"+blog.ID+"/api/ads/settings", strings.NewReader(`{"settings":{"x":"y"}}`))
	save = save.WithContext(siteAdsRequest(http.MethodPost, "", blog).Context())
	save.Header.Set(pageLangHeader, "de")
	rec = httptest.NewRecorder()
	consoleLanguage(http.HandlerFunc(a.handleOSSiteAdSettingsSave)).ServeHTTP(rec, save)
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error.Message != "Diesen Wert nimmt x nicht an; nichts wurde gespeichert." {
		t.Errorf("the refusal to a German page reads %q", rec.Body.String())
	}
}
