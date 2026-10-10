// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"strings"
	"testing"
)

func TestResolveHalcyonFallsBackToEachDefault(t *testing.T) {
	got := ResolveHalcyon(map[string]string{"home": "carousel", "textsize": "99", "dock": "maybe"})
	if got.Home != "front" || got.TextSize != 20 || !got.Dock {
		t.Errorf("an unknown value must fall back to the option's default: %+v", got)
	}
}

func TestResolveHalcyonTakesEveryChoice(t *testing.T) {
	got := ResolveHalcyon(map[string]string{"home": "river", "archives": "home", "article": "margin", "face": "sans",
		"textsize": "23", "smallcaps": "off", "appearance": "dark", "mostread": "off", "comments": "off"})
	if got.Home != "river" || got.Archives != "home" || got.Article != "margin" || got.Face != "sans" ||
		got.TextSize != 23 || got.SmallCaps || got.Appearance != "dark" || got.MostRead || got.Comments {
		t.Errorf("a valid choice was not taken: %+v", got)
	}
}

// The defaults are the operator's decisions of 2026-10-10.
func TestHalcyonDefaultsAreTheOperatorsChoices(t *testing.T) {
	got := ResolveHalcyon(nil)
	if got.Home != "front" || got.Archives != "index" || got.Article != "column" || got.Face != "serif" ||
		got.TextSize != 20 || !got.SmallCaps || got.Appearance != "auto" || ResolveHalcyonAccent(nil) != "site" {
		t.Errorf("defaults = %+v", got)
	}
}

func halcyonWithAccent(choice, light, dark string) Tokens {
	h := Halcyon()
	h.Options["accent"] = choice
	h.AccentLight, h.AccentDark = light, dark
	return h
}

func TestHalcyonFixedAccentsCompile(t *testing.T) {
	for _, c := range []string{"site", "stillair"} {
		if _, err := CompileCSS(halcyonWithAccent(c, "", "")); err != nil {
			t.Errorf("the %s accent must reach 4.5:1 on every surface: %v", c, err)
		}
	}
}

func TestHalcyonRefusesAnUnreadableLightAccent(t *testing.T) {
	_, err := CompileCSS(halcyonWithAccent("custom", "#7cc4b8", "#7cc4b8"))
	if err == nil || !strings.Contains(err.Error(), "Paper (light)") || !strings.Contains(err.Error(), "4.5:1") {
		t.Errorf("a pale accent on paper must be refused, naming the scheme: %v", err)
	}
}

func TestHalcyonRefusesAnUnreadableDarkAccent(t *testing.T) {
	_, err := CompileCSS(halcyonWithAccent("custom", "#0f6b63", "#0f6b63"))
	if err == nil || !strings.Contains(err.Error(), "Dusk (dark)") {
		t.Errorf("a deep accent on dusk must be refused, naming the scheme: %v", err)
	}
}

// An alpha channel would be measured against whatever lies beneath it.
func TestHalcyonRefusesATranslucentAccent(t *testing.T) {
	if _, err := CompileCSS(halcyonWithAccent("custom", "#0f6b6380", "#7cc4b8")); err == nil {
		t.Error("an accent with an alpha channel must be refused")
	}
}

func TestHalcyonKeepsAReadableCustomAccent(t *testing.T) {
	css, err := CompileCSS(halcyonWithAccent("custom", "#7a3e9d", "#d4b0f0"))
	if err != nil {
		t.Fatalf("a readable custom accent was refused: %v", err)
	}
	if !strings.Contains(css, "--accent:#7a3e9d") || !strings.Contains(css, "--accent:#d4b0f0") {
		t.Error("the custom accent did not reach the stylesheet")
	}
}

// Only Halcyon is held to its accent rule: another theme's accent is the
// operator's to choose, as before.
func TestOtherThemesKeepTheirAccentFreedom(t *testing.T) {
	d := Default()
	d.AccentLight = "#eeeeee"
	css, err := CompileCSS(d)
	if err != nil {
		t.Fatalf("a theme on the shared templates was refused an accent: %v", err)
	}
	if !strings.Contains(css, "--accent:#eeeeee") {
		t.Error("a theme on the shared templates had its accent replaced")
	}
}

func TestHalcyonShowsOnlyItsOwnOptions(t *testing.T) {
	opts := OptionsFor("Halcyon")
	if len(opts) != len(HalcyonOptions()) || opts[0].Key != "home" {
		t.Errorf("Halcyon's Studio should list only its own options, got %d", len(opts))
	}
	for _, o := range OptionsFor("Default") {
		if o.Key == "home" {
			t.Error("another theme lists Halcyon's options")
		}
	}
}

func TestHalcyonOptionsAreInTheValidatedVocabulary(t *testing.T) {
	keys := map[string]bool{}
	for _, o := range AllOptionDefs() {
		keys[o.Key] = true
	}
	for _, o := range HalcyonOptions() {
		if !keys[o.Key] {
			t.Errorf("%s is missing from the option vocabulary themes are validated against", o.Key)
		}
	}
}

func TestHalcyonLeadsTheStore(t *testing.T) {
	if s := Store(); len(s) == 0 || s[0].Tokens.Name != "Halcyon" || s[0].Tokens.Layout != LayoutHalcyon {
		t.Error("Halcyon, the theme a new install starts on, should lead the store")
	}
}
