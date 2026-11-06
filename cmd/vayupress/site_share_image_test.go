// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/settings"
)

var onePixelPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")

// A link to a hosted site is shared with that site's picture, or none: the
// install's was served at /theme-assets/og for every host.
func TestEachSiteSharesItsOwnPicture(t *testing.T) {
	a, shop, blog := hostedBlogApp(t)
	ctx := context.Background()
	for sc, body := range map[settings.Scope]string{settings.ForPrimary(): "install-picture", settings.ForDomain(shop.ID): "shop-picture"} {
		if err := a.siteSettings.SetMany(ctx, sc, map[string]string{
			settings.KeyThemeOGImage: base64.StdEncoding.EncodeToString([]byte(body)), settings.KeyThemeOGImageType: "image/png"}); err != nil {
			t.Fatal(err)
		}
	}
	get := func(r *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		a.serveOGImage(rec, r)
		return rec
	}
	if rec := get(requestFor("/theme-assets/og", blog)); rec.Code != http.StatusNotFound {
		t.Errorf("a site with no picture is served %d %q", rec.Code, rec.Body.String())
	}
	if rec := get(requestFor("/theme-assets/og", shop)); rec.Body.String() != "shop-picture" {
		t.Errorf("a site with its own picture is served %q", rec.Body.String())
	}
	if rec := get(httptest.NewRequest(http.MethodGet, "/theme-assets/og", nil)); rec.Body.String() != "install-picture" {
		t.Errorf("the install's own site is served %q", rec.Body.String())
	}
}

// A share image uploaded from a site's settings page is the site's, not the
// install's: the upload wrote the install's for every page it was mounted on.
func TestASitesShareImageUploadIsItsOwn(t *testing.T) {
	a, shop, _ := hostedBlogApp(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("image", "share.png")
	_, _ = fw.Write(onePixelPNG)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/os/d/"+shop.ID+"/api/branding/og", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(context.WithValue(req.Context(), ctxScopeKey, settings.ForDomain(shop.ID)))
	rec := httptest.NewRecorder()
	a.handleOGUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	ctx := context.Background()
	if a.siteSettings.Get(ctx, settings.ForDomain(shop.ID), settings.KeyThemeOGImage) == "" {
		t.Error("the site has no share image after its upload")
	}
	if a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyThemeOGImage) != "" {
		t.Error("the site's upload became the install's share image")
	}
}

// The site's page offers the upload at its own address and draws no preview
// until there is a picture.
func TestTheShareImageSectionIsTheSites(t *testing.T) {
	none := scopedSettingsBody("d1", "h", nil, presDefault, scopedExtras{})
	if !strings.Contains(none, `id="scoped-og-img" class="settings-mark__img" alt="Current share image" width="64" height="34" hidden>`) ||
		!strings.Contains(scopedSettingsScript("n"), "'/api/branding/og'") {
		t.Error("the share image section without a picture is wrong")
	}
	if own := scopedSettingsBody("d1", "h", nil, presDefault, scopedExtras{OwnShare: true}); !strings.Contains(own, `src="/os/d/d1/branding/og?t=`) {
		t.Error("the site's own share image is not shown")
	}
}
