// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"
)

// Two-factor opens in place under its row: set-up offers the QR, the key and a
// code to confirm; once it is on, the row offers only a code-gated way off,
// never set-up again.
func TestTwoFactorOpensInPlace(t *testing.T) {
	on := memberTwoFactorOn()
	for _, want := range []string{"data-totp-card", "data-totp-begin", "data-totp-qr", "data-totp-key", "data-totp-uri", "data-totp-verify", "data-totp-code"} {
		if !strings.Contains(on, want) {
			t.Errorf("two-factor set-up is missing %q", want)
		}
	}
	off := memberTwoFactorOff()
	for _, want := range []string{"data-totp-card", "data-totp-disable", "data-totp-code"} {
		if !strings.Contains(off, want) {
			t.Errorf("turning two-factor off is missing %q", want)
		}
	}
	if strings.Contains(off, "data-totp-begin") {
		t.Error("with two-factor on, the row offers set-up again")
	}
}

// The page's script carries the nonce, talks only to the member endpoints,
// never writes a response into the page as markup, and holds no backtick (it
// lives in a Go raw string).
func TestMemberAccountInlineJS(t *testing.T) {
	js := memberAccountInlineJS("NONCE123")
	for _, want := range []string{
		`nonce="NONCE123"`,
		"/api/v1/members/totp/begin", "/api/v1/members/totp/verify", "/api/v1/members/totp/disable",
		"/api/v1/members/avatar/choose", "'/api/v1/members/avatar'",
		"data-acct-open", "data-acct-switch", "window.vpPortalOpen",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("the page's script is missing %q", want)
		}
	}
	if strings.Contains(js, "innerHTML") {
		t.Error("the page's script writes markup; response data goes in with textContent")
	}
	if strings.Contains(js, "`") {
		t.Error("the page's script contains a backtick, which ends the Go raw string")
	}
}

// TestFreePaidBenefitsFallback checks the benefit lists are never empty (they
// fall back to sensible defaults when no tiers are configured), so the free
// member's Plan row always says what Premium adds.
func TestFreePaidBenefitsFallback(t *testing.T) {
	a := &App{} // members == nil → defaults
	free, paid := a.freePaidBenefits(context.Background())
	if len(free) == 0 || len(paid) == 0 {
		t.Fatalf("expected non-empty default benefit lists, got free=%d paid=%d", len(free), len(paid))
	}
	if !strings.Contains(strings.Join(paid, " "), "VayuMail") {
		t.Errorf("paid benefits should mention the VayuMail address, got %v", paid)
	}
}
