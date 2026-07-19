package render

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// TestBuildCSPOnionMode: a Tor/anonymous Space forbids external images (no
// https: in img-src) so a reader's Tor Browser can't leak its IP; a clearnet
// Space keeps https: exactly as before (ADR-0141).
func TestBuildCSPOnionMode(t *testing.T) {
	prev := config.Cfg.OnionMode
	defer func() { config.Cfg.OnionMode = prev }()

	config.Cfg.OnionMode = false
	clearnet := BuildCSP("nonce123", nil)
	if !strings.Contains(clearnet, "img-src 'self' data: https:") {
		t.Errorf("clearnet CSP must keep external https images:\n%s", clearnet)
	}

	config.Cfg.OnionMode = true
	onion := BuildCSP("nonce123", nil)
	if strings.Contains(onion, "https:") {
		t.Errorf("Tor-mode CSP must not permit any external https resource:\n%s", onion)
	}
	if !strings.Contains(onion, "img-src 'self' data:;") {
		t.Errorf("Tor-mode CSP img-src should be self+data only:\n%s", onion)
	}
	// The nonce and other directives are untouched.
	if !strings.Contains(onion, "'nonce-nonce123'") || !strings.Contains(onion, "connect-src 'self'") {
		t.Errorf("Tor-mode CSP must preserve the rest of the policy:\n%s", onion)
	}
}
