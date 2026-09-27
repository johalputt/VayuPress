// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/ui"
)

// TestAPIBaseURL covers the three resolutions: dedicated VAYUOS_API_HOST wins,
// else the apex domain, else a relative path for a local/dev run.
func TestAPIBaseURL(t *testing.T) {
	oldHost, oldDomain := config.Cfg.APIHost, config.Cfg.Domain
	t.Cleanup(func() { config.Cfg.APIHost, config.Cfg.Domain = oldHost, oldDomain })

	config.Cfg.APIHost, config.Cfg.Domain = "api.example.com", "example.com"
	if got := apiBaseURL(); got != "https://api.example.com/api/v1" {
		t.Errorf("with APIHost set: got %q", got)
	}

	config.Cfg.APIHost, config.Cfg.Domain = "", "example.com"
	if got := apiBaseURL(); got != "https://example.com/api/v1" {
		t.Errorf("apex fallback: got %q", got)
	}

	config.Cfg.APIHost, config.Cfg.Domain = "", "localhost"
	if got := apiBaseURL(); got != "/api/v1" {
		t.Errorf("localhost/dev: got %q", got)
	}
}

// TestOSAPIBaseRows checks the rows show the resolved base URL and the right
// guidance for each of the two states (dedicated host set vs not).
func TestOSAPIBaseRows(t *testing.T) {
	oldHost, oldDomain := config.Cfg.APIHost, config.Cfg.Domain
	t.Cleanup(func() { config.Cfg.APIHost, config.Cfg.Domain = oldHost, oldDomain })

	// No dedicated host → apex base + "point a dedicated api.<domain>" guidance.
	config.Cfg.APIHost, config.Cfg.Domain = "", "example.com"
	out := string(ui.Rows(osAPIBaseRows()...))
	if !strings.Contains(out, `id="ak-apibase">https://example.com/api/v1</code>`) {
		t.Errorf("rows missing apex base URL:\n%s", out)
	}
	if !strings.Contains(out, "VAYUOS_API_HOST") || !strings.Contains(out, string(ui.State("neutral", "Main domain"))) {
		t.Errorf("rows should name the main domain and the VAYUOS_API_HOST setting:\n%s", out)
	}
	// CSP-safe-prose rule: never the literal "CDN" in admin connector-style copy.
	if strings.Contains(out, "CDN") {
		t.Errorf("rows must not contain the literal 'CDN':\n%s", out)
	}

	// Dedicated host set → confirms the host and that the console is not
	// exposed there.
	config.Cfg.APIHost = "api.example.com"
	out = string(ui.Rows(osAPIBaseRows()...))
	if !strings.Contains(out, `id="ak-apibase">https://api.example.com/api/v1</code>`) || !strings.Contains(out, string(ui.State("ok", "Dedicated host"))) {
		t.Errorf("rows should confirm the dedicated host:\n%s", out)
	}
	if !strings.Contains(out, "Served on api.example.com") || !strings.Contains(out, "not the console") {
		t.Errorf("rows should say what the dedicated host serves:\n%s", out)
	}
}
