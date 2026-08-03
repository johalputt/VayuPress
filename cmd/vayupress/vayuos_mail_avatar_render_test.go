// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// TestMailAvatarImg verifies the sender/recipient chip renders the uploaded
// profile picture only for local mailboxes that have one, and falls back to the
// colored-initials chip otherwise (no broken <img> for external senders).
func TestMailAvatarImg(t *testing.T) {
	set := map[string]bool{"ankush@example.test": true}

	// A mailbox with a picture → an <img> pointing at the avatar serve route.
	withPic := mailAvatarImg("Ankush <ankush@example.test>", set)
	if !strings.Contains(withPic, "<img") {
		t.Errorf("expected an <img> for a mailbox with a picture, got: %s", withPic)
	}
	if !strings.Contains(withPic, "/os/vayumail/accounts/avatar?email=") {
		t.Errorf("avatar img must point at the serve route, got: %s", withPic)
	}
	if !strings.Contains(withPic, "ankush%40example.test") && !strings.Contains(withPic, "ankush@example.test") {
		t.Errorf("avatar img must carry the mailbox email, got: %s", withPic)
	}

	// A mailbox WITHOUT a picture → the initials chip, never an <img>.
	noPic := mailAvatarImg("gangu@example.test", set)
	if strings.Contains(noPic, "<img") {
		t.Errorf("a mailbox without a picture must fall back to initials, got: %s", noPic)
	}
	if !strings.Contains(noPic, "vm-av") {
		t.Errorf("expected the initials chip, got: %s", noPic)
	}

	// An external sender (not a local mailbox) → initials, never an <img>.
	external := mailAvatarImg("Someone <someone@gmail.com>", set)
	if strings.Contains(external, "<img") {
		t.Errorf("an external sender must not render an avatar img, got: %s", external)
	}
}
