// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// TestAPasswordResetRemovesEveryPasskey — a passkey signs in with no
// password, so one added by whoever had the mailbox would sign them back
// in after the reset. The reset removes every passkey, and passkey only
// with them, so the new password is the way in; the notice counts them.
func TestAPasswordResetRemovesEveryPasskey(t *testing.T) {
	a := passkeyApp(t)
	ctx := context.Background()
	principal := "vmail:" + lockedMailbox
	k := newSoftKey(t)
	handle := addPasskey(t, a, holderOf(lockedMailbox), k)
	addPasskey(t, a, holderOf(lockedMailbox), newSoftKey(t))
	if err := a.passkeys.SetOnly(ctx, principal, true); err != nil {
		t.Fatal(err)
	}
	deps, ok := a.mailResetDepsFor()
	if !ok {
		t.Fatal("no reset pipeline")
	}
	var notice string
	deps.FileToMailbox = func(_ context.Context, _, _, body string) error { notice = body; return nil }
	out, err := applyMailPasswordReset(ctx, deps, lockedMailbox, "a-new-password-entirely", mailResetByCode, lockedMailbox)
	if err != nil {
		t.Fatal(err)
	}
	if out.PasskeysRemoved != 2 || len(out.Problems) != 0 {
		t.Errorf("the outcome: %+v", out)
	}
	if rec := signInWith(t, a, k, handle, "198.51.100.150"); rec.Code == http.StatusOK {
		t.Errorf("a passkey signed in after the reset: %s", rec.Body.String())
	}
	if a.passkeys.Only(ctx, principal) {
		t.Error("passkey only outlived the passkeys, so the new password signs in no one")
	}
	if !strings.Contains(notice, "2 passkeys removed") {
		t.Errorf("the notice does not count the passkeys:\n%s", notice)
	}
}

// TestADeletedMailboxsWaysInDoNotPassToTheAddresssNextHolder — a mailbox is
// deleted and its address given to someone else. The last holder's passkey,
// console session and tool key were held under the address, and each is
// checked only for an account there: none may work on the new mailbox.
func TestADeletedMailboxsWaysInDoNotPassToTheAddresssNextHolder(t *testing.T) {
	a := passkeyApp(t)
	a.apiKeys = apikeys.New(dbpkg.DB)
	ctx := context.Background()
	principal := "vmail:" + lockedMailbox
	k := newSoftKey(t)
	handle := addPasskey(t, a, holderOf(lockedMailbox), k)
	session, err := a.sessions.Create(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := a.apiKeys.CreateWithPermissions(ctx, principal, "A tool", apikeys.NewPermissions(), nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.deleteMailbox(ctx, lockedMailbox); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashSecretArgon2id("the-next-holders-own-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.vayuMail.Accounts().Create(ctx, lockedMailbox, hash, "Next", "mailbox"); err != nil {
		t.Fatal(err)
	}

	if rec := signInWith(t, a, k, handle, "198.51.100.151"); rec.Code == http.StatusOK {
		t.Errorf("the last holder's passkey signed in to the new mailbox: %s", rec.Body.String())
	}
	if id, err := a.sessions.Validate(ctx, session); err == nil {
		t.Errorf("the last holder's session opens the new mailbox as %q", id)
	}
	if a.apiKeys.Verify(key) {
		t.Error("the last holder's tool key still works")
	}
}

// TestEveryMailboxIsDeletedThroughDeleteMailbox — a handler that called the
// engine's DeleteMailbox itself would delete the mailbox and leave its
// passkeys, sessions and tool keys for the address's next holder.
func TestEveryMailboxIsDeletedThroughDeleteMailbox(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "mail_delete.go" {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), ".DeleteMailbox(") {
			t.Errorf("%s deletes a mailbox without a.deleteMailbox", name)
		}
	}
}
