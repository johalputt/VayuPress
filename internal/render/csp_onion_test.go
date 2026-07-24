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

// TestApplyOnionCSPStripsAllExternalOrigins is the anti-regression guard for the
// central CSP chokepoint (audit M6/L8): in a Tor Space, NO directive — img,
// script, frame, connect — may carry an external origin, whether from a video
// facade or the AdSense policy. A clearnet Space keeps them all.
func TestApplyOnionCSPStripsAllExternalOrigins(t *testing.T) {
	prev := config.Cfg.OnionMode
	defer func() { config.Cfg.OnionMode = prev }()

	// Video-embed frame origins must survive on clearnet, vanish on onion.
	config.Cfg.OnionMode = false
	clear := BuildCSP("n1", []string{"https://www.youtube-nocookie.com"})
	if !strings.Contains(clear, "https://www.youtube-nocookie.com") {
		t.Errorf("clearnet CSP must keep the video frame origin:\n%s", clear)
	}
	config.Cfg.OnionMode = true
	onion := BuildCSP("n1", []string{"https://www.youtube-nocookie.com"})
	if strings.Contains(onion, "https://") || strings.Contains(onion, "https:") {
		t.Errorf("Tor-mode CSP must strip the video frame origin (L8):\n%s", onion)
	}

	// The AdSense CSP must carry Google origins on clearnet and none on onion.
	config.Cfg.OnionMode = false
	adClear := BuildAdCSP("n2", nil)
	if !strings.Contains(adClear, "googlesyndication.com") {
		t.Errorf("clearnet ad CSP must include Google origins:\n%s", adClear)
	}
	config.Cfg.OnionMode = true
	adOnion := BuildAdCSP("n2", nil)
	if strings.Contains(adOnion, "https://") || strings.Contains(adOnion, "google") {
		t.Errorf("Tor-mode ad CSP must contain no external origin (M6):\n%s", adOnion)
	}
	// The nonce and the strict skeleton survive the stripping.
	if !strings.Contains(adOnion, "'nonce-n2'") || !strings.Contains(adOnion, "frame-ancestors 'none'") {
		t.Errorf("Tor-mode ad CSP must preserve nonce + skeleton:\n%s", adOnion)
	}
}
