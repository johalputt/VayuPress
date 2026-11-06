// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/settings"
)

// A hosted site's tab icon is its own mark, or the product's when it has none;
// never the operator's, which stood in for every site without one.
func TestAHostedSiteNeverWearsTheOperatorsMark(t *testing.T) {
	a, shop, blog := hostedBlogApp(t)
	ctx := context.Background()
	operator, own := []byte("operator-mark"), []byte("shop-mark")
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{
		settings.KeyBrandFavicon: b64(operator), settings.KeyBrandFaviconType: "image/png"}); err != nil {
		t.Fatal(err)
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForDomain(shop.ID), map[string]string{
		settings.KeyBrandFavicon: b64(own), settings.KeyBrandFaviconType: "image/png"}); err != nil {
		t.Fatal(err)
	}
	icon := func(r *httptest.ResponseRecorder) []byte { return r.Body.Bytes() }
	serve := a.serveFavicon(faviconDarkPNG, 0)

	rec := httptest.NewRecorder()
	serve(rec, requestFor("/favicon.ico", blog))
	if !bytes.Equal(icon(rec), faviconDarkPNG) {
		t.Errorf("a site with no mark is served %q, want the product's", icon(rec))
	}
	rec = httptest.NewRecorder()
	serve(rec, requestFor("/favicon.ico", shop))
	if !bytes.Equal(icon(rec), own) {
		t.Errorf("a site with its own mark is served %q", icon(rec))
	}
	rec = httptest.NewRecorder()
	serve(rec, httptest.NewRequest("GET", "/favicon.ico", nil))
	if !bytes.Equal(icon(rec), operator) {
		t.Errorf("the install's own site is served %q", icon(rec))
	}
}

// A hosted site's settings page uploads its mark to its own scope, with the
// install's editor, and draws no preview when it has none.
func TestAHostedSitesMarkIsEditedOnItsSettingsPage(t *testing.T) {
	none := scopedSettingsBody("d1", "notes.example.com", map[string]string{}, presDefault, scopedExtras{})
	if !strings.Contains(none, `data-mark-post="/os/d/d1/api/branding/favicon"`) ||
		!strings.Contains(none, `<img id="brand-favicon-img" class="settings-mark__img" hidden data-mark-src="/os/d/d1/branding/mark"`) {
		t.Errorf("the site's mark editor without a mark is wrong:\n%s", none[strings.Index(none, "Site mark"):][:900])
	}
	if own := scopedSettingsBody("d1", "notes.example.com", map[string]string{}, presDefault, scopedExtras{OwnMark: true}); !strings.Contains(own, `src="/os/d/d1/branding/mark?t=`) {
		t.Error("the site's own mark is not shown")
	}
	if !strings.Contains(string(siteMarkHTML("/favicon.ico", "/favicon.ico", "/os/api/branding/favicon", false)), `src="/favicon.ico?t=`) {
		t.Error("the install's mark editor shows no preview of the default")
	}
	if !strings.Contains(scopedSettingsScript("n"), siteMarkScript) || !strings.Contains(settingsPageScript, siteMarkScript) {
		t.Error("the site's page and the install's Settings do not run the same mark script")
	}
}
