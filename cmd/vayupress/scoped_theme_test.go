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

// RETIRED — TestThemeStudioIsOneHandlerMountedTwice, which REQUIRED the
// per-domain mount to exist.
//
// It asserted the right thing about the handler and the wrong thing about the
// page. handleOSTheme does read osScope(r), so the test passed; but the page it
// renders loads a script that posts to ABSOLUTE /os/api/theme/* and
// /os/api/settings paths, which never carry the scoped context. So every write
// from /os/d/{id}/theme landed on the primary while this test reported the mount
// correctly scoped. Beneath that, theme_tokens is CHECK(id=1) and applying a
// theme sets a process global — there was no per-site theme for the handler's
// scope-awareness to reach.
//
// A test that checks the handler and not the page it serves is the shape that
// kept this alive. The mount is retired; its replacement is
// TestThePerSiteThemeStudioIsRetiredRatherThanServed, which drives the address.

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

// RETIRED — TestThemeStudioIsLinkedFromTheScopedConsole, which REQUIRED the
// per-site console to link Theme Studio and to describe it as live.
//
// It enforced the tile that sent an operator to a page which restyled their own
// install. Theme Studio is now named in sharedTools as install-wide, and
// TestTheConsoleNamesWhatIsStillInstallWide asserts it is still NAMED — an
// operator has to know what a hosted site does not get its own copy of, and
// silence there is how the original defect read as normal.
