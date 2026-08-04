// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// ADR-0153 Phase 4 — a hosted domain's theme is its own.
//
// Before this, a hosted domain was rendered from render.GetActiveSettings() —
// the OPERATOR's live settings — with six brand fields painted over the top. The
// theme, the custom CSS, the head meta and 315 other keys were never the
// domain's. That is what "the tools are all linked" meant concretely.

// Theme Studio must be ONE handler mounted twice, not a per-domain copy.
// A parallel implementation is a second place for every future theme change to
// be forgotten, and the forgotten one is always the client-facing one.
func TestThemeStudioIsOneHandlerMountedTwice(t *testing.T) {
	routes := readSourceFile(t, "admin_os_ui.go")
	if !strings.Contains(routes, `dr.Get("/theme", a.handleOSTheme)`) {
		t.Fatal("Theme Studio is not mounted under the per-domain route family")
	}
	if !strings.Contains(routes, `pr.Get("/os/theme", a.handleOSTheme)`) &&
		!strings.Contains(routes, `"/os/theme", a.handleOSTheme`) {
		t.Error("the primary's own Theme Studio route no longer points at the same handler")
	}
	// And that handler must read its scope from the request rather than a constant.
	body := goFuncBody(readSourceFile(t, "admin_os_theme.go"), "handleOSTheme")
	if strings.Contains(body, "settings.ForPrimary()") {
		t.Error("Theme Studio reads the PRIMARY explicitly, so opening it under a hosted " +
			"domain's URL shows and edits the operator's own theme")
	}
	if !strings.Contains(body, "osScope(r)") {
		t.Error("Theme Studio does not read the request's scope")
	}
}

// The cross-tenant write this phase nearly introduced.
//
// render.SetActiveSettings is a PROCESS-WIDE singleton holding the primary
// site's live configuration. A hosted domain's save pushing into it would
// repaint the operator's own site with a client's theme until the next restart —
// no database change, no error, and nothing to point at afterwards.
func TestAScopedThemeSaveNeverWritesTheGlobalRenderSettings(t *testing.T) {
	src := readSourceFile(t, "admin_os_theme.go")
	for _, fn := range []string{"handleOSThemeCode", "handleOSThemeImport"} {
		body := goFuncBody(src, fn)
		if body == "" {
			t.Fatalf("%s not found", fn)
		}
		at := strings.Index(body, "render.SetActiveSettings")
		if at < 0 {
			continue // this handler does not touch the global at all
		}
		// The guard must be the condition the global write hangs off.
		guarded := strings.Contains(body[:at], "osScope(r).IsPrimary()")
		if !guarded {
			t.Errorf("%s writes render.SetActiveSettings without checking the scope is the "+
				"primary. Saving a hosted domain's theme would overwrite the OPERATOR's live "+
				"render settings — their own site repainted with a client's colours, in memory, "+
				"until the process restarts", fn)
		}
	}
}

// The public render path must build a hosted domain's page from that domain's
// settings, not from the primary's with an overlay.
func TestThePublicPathRendersAHostedDomainFromItsOwnScope(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "middleware_domain.go"), "brandForRequest")
	if !strings.Contains(body, "settings.ForDomain(d.ID)") {
		t.Fatal("the public render path does not resolve the served domain's own settings " +
			"scope, so a hosted domain is still the primary site with six fields painted over")
	}
	scope := strings.Index(body, "settings.ForDomain(d.ID)")
	overlay := strings.Index(body, "applyBrand(")
	if overlay >= 0 && overlay < scope {
		t.Error("the brand overlay runs BEFORE the scope lookup, so the primary's settings " +
			"still decide what a hosted domain looks like")
	}
}

// The settings→render mapping must exist once. It was copy-pasted in four
// handlers, so a key added to one and not the others saved correctly and
// rendered empty — a defect that looks like the save failing.
func TestTheSettingsToRenderMappingIsNotDuplicated(t *testing.T) {
	vals := map[string]string{
		settings.KeySiteName:         "Client Ltd",
		settings.KeyThemePrimaryDark: "#abcdef",
		settings.KeyThemeCustomCSS:   "body{}",
	}
	got := siteSettingsFromValues(vals)
	if got.Name != "Client Ltd" || got.PrimaryDark != "#abcdef" || got.CustomCSS != "body{}" {
		t.Errorf("siteSettingsFromValues dropped fields: %+v", got)
	}
	// An empty map must produce empty values, never a panic and never the
	// primary's — the caller decides what unset means, not this function.
	if empty := siteSettingsFromValues(map[string]string{}); empty.Name != "" {
		t.Errorf("an empty scope produced a non-empty site name %q", empty.Name)
	}
	var _ render.SiteSettings = got
}

// Theme Studio is now live on the per-domain console, so the card must link.
func TestThemeStudioIsLinkedFromTheScopedConsole(t *testing.T) {
	var found bool
	for _, tool := range scopedTools {
		if tool.Title == "Theme Studio" {
			found = true
			if !tool.Live {
				t.Error("Theme Studio is scoped but still listed as not-yet-available")
			}
		}
	}
	if !found {
		t.Fatal("Theme Studio is missing from the per-domain console")
	}
	page := scopedConsolePage(testDomain("abc123", "client.example"), 0, 0, 0, false, nil, nil, nil, nil)
	if !strings.Contains(page, `href="/os/d/abc123/theme"`) {
		t.Error("the console does not link this domain's Theme Studio")
	}
}
