// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/settings"
)

// Each host's /ads.txt authorises that site's sellers alone: the install's
// publisher ID never appears on a hosted site's, and a site with none has no
// ads.txt at all.
func TestEachSitesAdsTxtIsItsOwn(t *testing.T) {
	a, shop, blog := hostedBlogApp(t)
	ctx := context.Background()
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{
		settings.KeyAdsenseClient: "ca-pub-1111111111111111", settings.KeyAdsTxtLines: "example-exchange.com, 42, RESELLER\n\n"}); err != nil {
		t.Fatal(err)
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForDomain(shop.ID), map[string]string{settings.KeyAdsenseClient: "ca-pub-2222222222222222"}); err != nil {
		t.Fatal(err)
	}
	get := func(r *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		a.handleAdsTxt(rec, r)
		return rec
	}
	if body := get(httptest.NewRequest(http.MethodGet, "/ads.txt", nil)).Body.String(); body != "google.com, pub-1111111111111111, DIRECT, f08c47fec0942fa0\nexample-exchange.com, 42, RESELLER\n" {
		t.Errorf("the install's ads.txt:\n%q", body)
	}
	if body := get(requestFor("/ads.txt", shop)).Body.String(); body != "google.com, pub-2222222222222222, DIRECT, f08c47fec0942fa0\n" {
		t.Errorf("a site's own ads.txt:\n%q", body)
	}
	if rec := get(requestFor("/ads.txt", blog)); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "pub-") {
		t.Errorf("a site with no ads settings: %d %q", rec.Code, rec.Body.String())
	}
	if body := adsTxtBody("ca-pub-12x4", ""); body != "" {
		t.Errorf("a malformed publisher ID gave %q", body)
	}
}
