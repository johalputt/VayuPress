// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"
)

// TestPortalScriptShipsOnlyWhenMembershipIsOn — the widget's own init() fetches
// /api/v1/members/me and returns immediately unless membership is enabled. So on
// an install with membership off, shipping it made every visitor download ~10 KiB
// and pay an extra request to learn something the SERVER ALREADY KNEW when it
// rendered the page.
func TestPortalScriptShipsOnlyWhenMembershipIsOn(t *testing.T) {
	t.Cleanup(func() { SetMembershipEnabled(false) })

	SetMembershipEnabled(false)
	if got := PortalJSLink(); got != "" {
		t.Errorf("membership off, but the portal script still ships: %q", got)
	}

	SetMembershipEnabled(true)
	got := string(PortalJSLink())
	if !strings.Contains(got, "/static/js/portal.js") {
		t.Errorf("membership on, but the portal script is missing: %q", got)
	}
	if !strings.Contains(got, "v="+PortalJSVersion()) {
		t.Error("the script tag lost its cache-busting version")
	}
}

// TestActiveSettingsDrivesThePortalGate — the gate must be derived at the single
// chokepoint every settings path already goes through, not set by hand at each
// call site. Otherwise a future save path forgets it and the renderer ships a
// script that contradicts the live setting.
func TestActiveSettingsDrivesThePortalGate(t *testing.T) {
	t.Cleanup(func() { SetMembershipEnabled(false) })

	SetActiveSettings(SiteSettings{ShowMembership: true})
	if PortalJSLink() == "" {
		t.Error("enabling membership through SetActiveSettings did not open the portal gate")
	}

	SetActiveSettings(SiteSettings{ShowMembership: false})
	if PortalJSLink() != "" {
		t.Error("disabling membership through SetActiveSettings did not close the portal gate")
	}
}
