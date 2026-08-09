// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/settings"
)

// Retiring the per-site Theme Studio must not leave a hosted site's colours with
// no editor at all — that would be a removal dressed as a consolidation, and
// ADR-0154 D3 named Theme Studio as the editor for colour.
//
// It never was one for a hosted site: its script posts to absolute /os/api/…
// routes, so its writes landed on the primary. The keys moved to the per-site
// settings page, which writes through osScope and genuinely scopes.
func TestAHostedSiteCanStillEditItsOwnColours(t *testing.T) {
	page := scopedSettingsBody("d1", "customer.example", map[string]string{
		settings.KeyThemeAccentLight: "#2563eb",
	}, presCustom)

	for _, k := range []string{
		settings.KeyThemeAccentLight, settings.KeyThemeAccentDark, settings.KeyHeadThemeColor,
	} {
		if !strings.Contains(page, `data-scoped-key="`+k+`"`) {
			t.Errorf("the per-site settings page has no field for %s, so this site's colours "+
				"have no editor now that the per-site Theme Studio is retired", k)
		}
	}
	if !strings.Contains(page, "#2563eb") {
		t.Error("a stored accent is not shown in its field, so the editor cannot round-trip")
	}
}

// The colour keys must be SAVEABLE, not merely rendered. The save allowlists
// keys explicitly, and a field the page draws but the handler drops is the same
// silent no-op as the branding editor that wrote a store nobody read.
func TestTheColourKeysAreOnTheSaveAllowlist(t *testing.T) {
	allowed := map[string]bool{}
	for _, f := range scopedEditableKeys() {
		allowed[f.Key] = true
	}
	for _, k := range []string{
		settings.KeyThemeAccentLight, settings.KeyThemeAccentDark, settings.KeyHeadThemeColor,
	} {
		if !allowed[k] {
			t.Errorf("%s is rendered as a field but not on the save allowlist, so editing it "+
				"reports success and changes nothing", k)
		}
	}
	// The allowlist must still be an ALLOWLIST — a client-facing surface built on
	// this endpoint later must not be able to write any of the ~327 keys.
	if allowed[settings.KeyFeatureComments] || allowed["site.robots"] {
		t.Error("the save allowlist has widened beyond the fields this page owns")
	}
	// And the save must read from the combined set, or a colour edit is dropped.
	body := goFuncBody(readSourceFile(t, "admin_os_scoped_settings.go"), "handleOSScopedSettingsSave")
	if !strings.Contains(body, "scopedEditableKeys()") {
		t.Error("the save builds its allowlist from the identity set alone, so every colour " +
			"field on the page is silently discarded")
	}
}

// The Identity tile counts scopedSettingKeys. Folding colour into that set would
// make "4 of 7" mean something no label on the page explains, which is why the
// two groups are separate.
func TestColourIsNotCountedAsIdentity(t *testing.T) {
	for _, f := range scopedSettingKeys {
		if strings.HasPrefix(f.Key, "theme.") || strings.HasPrefix(f.Key, "head.") {
			t.Errorf("%s is in the identity set, so the Identity tile now counts colour as "+
				"identity and its number stops matching its label", f.Key)
		}
	}
	if len(scopedColourKeys) == 0 {
		t.Fatal("the colour group is empty")
	}
}
