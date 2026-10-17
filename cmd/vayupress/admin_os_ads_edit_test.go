// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/ads"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

func putAd(a *App, id, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Put("/os/api/ads/{id}", a.handleOSAdUpdate)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/os/api/ads/"+id, strings.NewReader(body)))
	return rec
}

// An ad once placed could only be turned off or deleted (3cj). Saving an edit
// changes what the slot shows; a member's paid ad keeps its review status, so
// an edit cannot publish what the operator has not approved; and an edit the
// store refuses leaves the slot as it was.
func TestAPlacedAdCanBeEdited(t *testing.T) {
	openMigratedDB(t)
	a := &App{ads: ads.New(dbpkg.DB)}
	ctx := context.Background()
	slot, err := a.ads.Create(ctx, ads.SlotInput{Name: "Banner", Placement: ads.PlacementBelowPost, Kind: ads.KindImage,
		ImageURL: "/media/old.png", LinkURL: "https://old.example", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	rec := putAd(a, slot.ID, `{"name":"Banner, autumn","placement":"`+ads.PlacementAbovePost+`","kind":"image","image_url":"/media/new.png","link_url":"https://new.example","alt_text":"Sponsored","enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit = %d %s", rec.Code, rec.Body.String())
	}
	got, _ := a.ads.GetByID(ctx, slot.ID)
	if got.Name != "Banner, autumn" || got.Placement != ads.PlacementAbovePost || got.ImageURL != "/media/new.png" || got.LinkURL != "https://new.example" || got.AltText != "Sponsored" {
		t.Errorf("the edit did not land: %+v", got)
	}

	member, err := a.ads.CreateMemberAd(ctx, ads.MemberAdInput{OwnerEmail: "reader@example.com", Placement: ads.PlacementSidebar, ImageURL: "/media/m.png"})
	if err != nil {
		t.Fatal(err)
	}
	if rec := putAd(a, member.ID, `{"name":"Member ad","placement":"sidebar","kind":"image","image_url":"/media/m2.png","enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("member edit = %d %s", rec.Code, rec.Body.String())
	}
	if m, _ := a.ads.GetByID(ctx, member.ID); m.Status != ads.StatusPendingPayment || m.ImageURL != "/media/m2.png" {
		t.Errorf("a member ad after an edit: status %q image %q, want %q and the new image", m.Status, m.ImageURL, ads.StatusPendingPayment)
	}

	if rec := putAd(a, slot.ID, `{"name":"Banner","placement":"nowhere","kind":"image","enabled":true}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an edit to an unknown placement = %d, want 400", rec.Code)
	}
	if got, _ := a.ads.GetByID(ctx, slot.ID); got.Placement != ads.PlacementAbovePost {
		t.Errorf("a refused edit changed the slot: placement %q", got.Placement)
	}
}

// The Advertising page gives each slot an Edit button and a sheet filled with
// what the slot holds now, escaped.
func TestTheAdvertisingPageOffersAFilledEditSheet(t *testing.T) {
	sl := ads.Slot{ID: "ad_1", Name: `Q4 "launch"`, Placement: ads.PlacementFooter, Kind: ads.KindHTML, HTML: `<b>Hi</b>`, LinkURL: "https://x.example"}
	if row := adsSlotsTable([]ads.Slot{sl}); !strings.Contains(row, `data-sheet="ad-e-ad_1">Edit</button>`) {
		t.Errorf("the slot's row has no Edit button: %s", row)
	}
	form := adSlotForm("ad-e-ad_1", sl)
	for _, want := range []string{
		`id="ad-e-ad_1-name" class="input" type="text" placeholder="Below-post banner" value="Q4 &#34;launch&#34;"`,
		`<option value="` + ads.PlacementFooter + `" selected>`,
		`<option value="html" selected>`,
		`value="https://x.example"`,
		`>&lt;b&gt;Hi&lt;/b&gt;</textarea>`,
	} {
		if !strings.Contains(form, want) {
			t.Errorf("the edit form does not carry %s", want)
		}
	}
}
