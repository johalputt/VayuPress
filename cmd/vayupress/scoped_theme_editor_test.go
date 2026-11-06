// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/theme"
)

// A hosted site's Halcyon options saved on its settings page are what its
// pages are drawn with; options it does not take are refused.
func TestAHostedSitesThemeIsItsOwn(t *testing.T) {
	a, _, blog := hostedBlogApp(t)
	if s, _ := a.brandForRequest(requestFor("/", blog)); s.Halcyon != nil {
		t.Fatal("a site that has chosen nothing has a theme of its own")
	}
	opts, _ := json.Marshal(`{"face":"sans","home":"river"}`)
	if rec := scopedSave(a, blog, `{"settings":{"`+settings.KeyThemeHalcyonOptions+`":`+string(opts)+`}}`); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	s, _ := a.brandForRequest(requestFor("/", blog))
	if s.Halcyon == nil || s.Halcyon.Face != "sans" || s.Halcyon.Home != "river" {
		t.Errorf("the site is drawn with %+v", s.Halcyon)
	}
	bad, _ := json.Marshal(`{"face":"comic"}`)
	if rec := scopedSave(a, blog, `{"settings":{"`+settings.KeyThemeHalcyonOptions+`":`+string(bad)+`}}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a face the theme does not have: %d", rec.Code)
	}
}

// The Theme section starts from your own site's options until the site
// chooses, then shows its own; it is offered only when the install is on
// Halcyon, whose options these are.
func TestTheThemeSectionShowsWhatTheSiteIsDrawnWith(t *testing.T) {
	inst := theme.Halcyon()
	inst.Options["face"] = "sans"
	sel := func(page, key, value string) bool {
		i := strings.Index(page, `data-halcyon-opt="`+key+`"`)
		if i < 0 {
			return false
		}
		end := strings.Index(page[i:], "</select>")
		return strings.Contains(page[i:i+end], `value="`+value+`" selected`)
	}
	following := scopedSettingsBody("d1", "h", map[string]string{}, presDefault, scopedExtras{Install: inst})
	if !sel(following, "face", "sans") || !strings.Contains(following, `data-halcyon-input data-scoped-key="`+settings.KeyThemeHalcyonOptions+`" value=""`) {
		t.Error("a site that has chosen nothing is not shown your own site's options")
	}
	chosen := scopedSettingsBody("d1", "h", map[string]string{settings.KeyThemeHalcyonOptions: `{"face":"serif"}`}, presDefault, scopedExtras{Install: inst})
	if !sel(chosen, "face", "serif") {
		t.Error("the site's own choice is not shown")
	}
	if strings.Contains(scopedSettingsBody("d1", "h", nil, presDefault, scopedExtras{Install: theme.Default()}), "data-halcyon-opt") {
		t.Error("Halcyon's options are offered on an install that is not on Halcyon")
	}
	if strings.Contains(following, `data-halcyon-opt="accent"`) {
		t.Error("the accent is offered; a site's colours are in Colour")
	}
}
