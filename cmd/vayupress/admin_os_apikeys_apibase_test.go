package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
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

// TestOSAPIBaseCard checks the card shows the resolved base URL and the right
// guidance for each of the two states (dedicated host set vs not).
func TestOSAPIBaseCard(t *testing.T) {
	oldHost, oldDomain := config.Cfg.APIHost, config.Cfg.Domain
	t.Cleanup(func() { config.Cfg.APIHost, config.Cfg.Domain = oldHost, oldDomain })

	// No dedicated host → apex base + "point a dedicated api.<domain>" guidance.
	config.Cfg.APIHost, config.Cfg.Domain = "", "example.com"
	card := osAPIBaseCard()
	if !strings.Contains(card, "https://example.com/api/v1") {
		t.Errorf("card missing apex base URL:\n%s", card)
	}
	if !strings.Contains(card, "VAYUOS_API_HOST") {
		t.Errorf("card should mention the VAYUOS_API_HOST setting:\n%s", card)
	}
	// CSP-safe-prose rule: never the literal "CDN" in admin connector-style copy.
	if strings.Contains(card, "CDN") {
		t.Errorf("card must not contain the literal 'CDN':\n%s", card)
	}

	// Dedicated host set → confirms the host and that /os is not exposed there.
	config.Cfg.APIHost = "api.example.com"
	card = osAPIBaseCard()
	if !strings.Contains(card, "https://api.example.com/api/v1") || !strings.Contains(card, "api.example.com</code>") {
		t.Errorf("card should confirm the dedicated host:\n%s", card)
	}
}
