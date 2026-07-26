// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// privateArmorHeader is the literal that must never appear in an admin-facing
// response. PGP armour is self-describing, so grepping for the header is an
// exact test rather than a heuristic.
const privateArmorHeader = "BEGIN PGP PRIVATE KEY BLOCK"

// TestAdminSurfaceNeverRendersPrivateKey is the guard on the property the whole
// VayuPGP admin surface rests on: an administrator can hand out any mailbox's
// PUBLIC key, and can obtain no mailbox's private key.
//
// The risk this defends is not theoretical. Every mailbox's private key is held
// server-side (AES-256-GCM at rest) so inbound mail can be decrypted, which means
// a single admin-facing template that rendered the private half would convert one
// stolen admin session into every mailbox's mail — retroactively, because the
// mail is already stored. The one legitimate release path is the owner's own
// device via /api/v1/members/vayumail-privkey, which authenticates as the mailbox
// under the MAIL-SYNC device scope, not as an administrator.
//
// This scans the VayuOS handler sources for any use of the private-key accessor
// or the armour header. A source-level test rather than a response-level one on
// purpose: it fails when the dangerous call is *written*, not when someone
// remembers to exercise the route that returns it.
func TestAdminSurfaceNeverRendersPrivateKey(t *testing.T) {
	// Files that make up the VayuOS admin surface. handlers_portal.go is
	// deliberately absent — it holds the owner-authenticated device endpoint,
	// which is the one place private key material is legitimately released.
	adminFiles := []string{
		"vayuos.go",
		"vayuos_mail.go",
		"vayuos_mail_accounts.go",
		"admin_os_ui.go",
	}

	// ArmoredPrivateKey is the only accessor that decrypts the private half.
	privAccessor := regexp.MustCompile(`ArmoredPrivateKey\s*\(`)

	for _, name := range adminFiles {
		b, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src := string(b)

		for i, line := range strings.Split(src, "\n") {
			// Comments explaining the posture are expected and must not trip this.
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if privAccessor.MatchString(line) {
				t.Errorf("%s:%d calls ArmoredPrivateKey in an admin handler.\n"+
					"An administrator must never be able to obtain another mailbox's private key: "+
					"the server already holds it, so exposing it here turns one compromised admin "+
					"session into every mailbox's stored mail. Use GetPublicKey.\n  %s",
					name, i+1, strings.TrimSpace(line))
			}
			if strings.Contains(line, privateArmorHeader) {
				t.Errorf("%s:%d emits a PGP PRIVATE KEY BLOCK header in an admin handler.\n  %s",
					name, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// TestAccountPubKeyRouteIsPublicHalfOnly pins the download route to the public
// accessor. If someone swaps GetPublicKey for ArmoredPrivateKey here, the route
// keeps compiling, keeps returning a .asc, and starts handing out private keys —
// a change that looks like a one-word refactor and is a total compromise.
func TestAccountPubKeyRouteIsPublicHalfOnly(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("vayuos_mail_accounts.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	src := string(b)

	start := strings.Index(src, "func (a *App) handleVayuOSAccountPubKey")
	if start < 0 {
		t.Fatal("handleVayuOSAccountPubKey not found — did the route move?")
	}
	end := strings.Index(src[start:], "\nfunc ")
	if end < 0 {
		end = len(src) - start
	}
	body := src[start : start+end]

	if !strings.Contains(body, "GetPublicKey(") {
		t.Error("the public-key download route no longer calls GetPublicKey")
	}
	if strings.Contains(body, "ArmoredPrivateKey") || strings.Contains(body, privateArmorHeader) {
		t.Error("the public-key download route touches private key material")
	}
	// Admin-gated: the route lists every mailbox's key material, so an
	// unauthenticated or non-admin caller must not reach it even though what it
	// returns is public.
	if !strings.Contains(body, "isAdminRequest") {
		t.Error("the public-key download route is not admin-gated")
	}
}

// TestPGPPanelExposesCopyableKeys is the functional half: the operator asked for
// a way to copy a mailbox's public key, and a panel that renders the button but
// not the key would satisfy no one.
func TestPGPPanelExposesCopyableKeys(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("vayuos_mail_accounts.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	src := string(b)
	for _, want := range []string{"data-pgp-armor", "data-pgp-copy", "accounts/pubkey"} {
		if !strings.Contains(src, want) {
			t.Errorf("account card is missing %q — the copy affordance is incomplete", want)
		}
	}
}
