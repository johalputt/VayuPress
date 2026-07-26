// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"
)

// TestRenderMemberSecurityCard guards the member 2FA card structure: the
// disabled state must offer a scannable QR + manual key + verify, and the
// enabled state must offer a code-gated disable (and never re-offer enrolment).
func TestRenderMemberSecurityCard(t *testing.T) {
	off := renderMemberSecurityCard(false)
	for _, want := range []string{"data-totp-card", "data-totp-begin", "data-totp-qr", "data-totp-key", "data-totp-uri", "data-totp-verify", "Off"} {
		if !strings.Contains(off, want) {
			t.Errorf("disabled 2FA card missing %q", want)
		}
	}
	on := renderMemberSecurityCard(true)
	for _, want := range []string{"data-totp-card", "data-totp-disable", "data-totp-code", "On"} {
		if !strings.Contains(on, want) {
			t.Errorf("enabled 2FA card missing %q", want)
		}
	}
	if strings.Contains(on, "data-totp-begin") {
		t.Error("enabled 2FA card should not re-offer enrolment")
	}
}

// TestMemberAccountInlineJS verifies the inline script carries the nonce, hits
// exactly the member-scoped endpoints, and contains no backtick (which would
// prematurely close the Go raw string it lives in).
func TestMemberAccountInlineJS(t *testing.T) {
	js := memberAccountInlineJS("NONCE123")
	for _, want := range []string{
		`nonce="NONCE123"`,
		"/api/v1/members/totp/begin",
		"/api/v1/members/totp/verify",
		"/api/v1/members/totp/disable",
		"/api/v1/members/mailbox/claim",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("inline JS missing %q", want)
		}
	}
	if strings.Contains(js, "`") {
		t.Error("inline JS must not contain a backtick — it would break the Go raw string")
	}
}

// TestFreePaidBenefitsFallback checks the benefit lists are never empty (they
// fall back to sensible defaults when no tiers are configured), so the
// Free-vs-Premium comparison always renders something useful.
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
