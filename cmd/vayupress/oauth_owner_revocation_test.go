// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/users"

	_ "github.com/mattn/go-sqlite3"
)

// SECTION 3 AUDIT FINDING — deleting an administrator does not take back what
// they granted.
//
// In the operator's voice, because this is the moment they believe they are
// closing a door:
//
//	One of my administrators connected an MCP client to this site with Full
//	control. They have left. I delete their account, which is the strongest
//	thing the panel offers me, and I consider the matter closed.
//
//	Their connector still works. It holds a scoped key with *:* over the whole
//	install — posts, pages, media, settings, mail — and it renews itself: the
//	access token is short-lived on purpose, but the refresh token rotates it
//	forever and nothing in that path ever looks at who owns it.
//
//	The account is gone from every screen I have. The access is not.
//
// The chain is short and every link checks something other than this:
//
//   - users.Delete is a single-table DELETE. vayu_api_keys.owner_user_id is a
//     plain column with no foreign key, so nothing cascades.
//   - apikeys refresh() loads keys WHERE revoked=0 AND active=1 AND not expired.
//     There is no join to users and no owner check.
//   - oauthTokenFromRefresh rotates the key on presentation of a refresh token
//     and never revisits the grant's owner.
//
// This is the Section 2 device finding one level up: there, disabling a mailbox
// left its phones syncing; here, deleting an administrator leaves their
// connector running the site.
//
// The fix must NOT be "refuse a key whose owner is missing" applied blindly.
// owner_user_id is '' for operator/system-owned keys by design (migration 062
// says so in as many words), and those must keep working — the same trap as the
// CMS-user mailboxes in Section 2, where the obvious stricter rule was an
// outage.

// keyStoreWithUsers builds an apikeys store and a users store on one scratch
// database carrying the shipped schema for both.
func keyStoreWithUsers(t *testing.T) (*apikeys.Store, *users.Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, m := range []string{
		"020-users-sessions", "038-user-profiles", "039-user-mailbox",
		"050-user-must-change-password", "051-user-username", "079-client-domain",
		"041-api-keys", "042-api-keys-envelope", "062-api-keys-permissions",
	} {
		applyMigration(t, db, m)
	}
	return apikeys.New(db), users.New(db), db
}

// deleteVia runs the product's own account-deletion root. Calling
// users.Store.Delete directly would prove only that a store deletes a row —
// the defect was that nothing downstream of it revoked anything, so the test
// has to go through the path the handler actually uses.
func deleteVia(t *testing.T, ks *apikeys.Store, us *users.Store, email string) {
	t.Helper()
	app := &App{userStore: us, apiKeys: ks}
	if _, err := app.deleteUserAccount(context.Background(), email); err != nil {
		t.Fatalf("delete %s: %v", email, err)
	}
}

// fullControlKeyFor mints an OAuth-shaped connector key owned by ownerID and
// returns its raw token — the same call oauthIssueTokens makes.
func fullControlKeyFor(t *testing.T, ks *apikeys.Store, ownerID string) string {
	t.Helper()
	perms := apikeys.NewPermissions()
	sec, act, ok := apikeys.ParseCapability("*:*")
	if !ok {
		t.Fatal("*:* is not a capability this build understands; the fixture is wrong")
	}
	perms.Grant(sec, act)
	_, raw, err := ks.CreateWithPermissions(context.Background(), ownerID,
		"Claude via OAuth (Full control)", perms, nil, 0)
	if err != nil {
		t.Fatalf("mint key: %v", err)
	}
	if !ks.Verify(raw) {
		t.Fatal("the freshly minted key does not authenticate; the fixture is wrong")
	}
	return raw
}

func TestDeletingAnAdministratorRevokesTheirConnector(t *testing.T) {
	ks, us, _ := keyStoreWithUsers(t)
	ctx := context.Background()

	u, err := us.Create(ctx, "leaver@example.com", "The Leaver", "a-long-password", "admin")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	raw := fullControlKeyFor(t, ks, u.ID)

	deleteVia(t, ks, us, "leaver@example.com")

	if ks.Verify(raw) {
		t.Error("the departed administrator's connector still authenticates.\n\n" +
			"It carries *:* over the whole install and renews itself through its " +
			"refresh token, so deleting the account took nothing away. The operator " +
			"has no screen left on which to see it.")
	}
}

// THE CONTROL that the obvious version of this fix fails.
//
// owner_user_id is ” for operator/system-owned keys — migration 062 states
// that explicitly. A rule of "the owner must exist in users" refuses every one
// of them, which is the whole API surface of an install that provisions keys
// outside a user account.
func TestSystemOwnedKeysWithNoOwnerKeepWorking(t *testing.T) {
	ks, _, _ := keyStoreWithUsers(t)
	raw := fullControlKeyFor(t, ks, "") // '' = operator/system-owned

	if !ks.Verify(raw) {
		t.Error("a system-owned key (owner_user_id='') stopped authenticating.\n\n" +
			"Migration 062 defines '' as operator/system-owned. Requiring an owner row " +
			"to exist takes out every provisioned key on the install.")
	}
}

// THE OTHER CONTROL. Deleting one administrator must not disturb anybody else's
// connector — a revocation that reaches past its target is its own incident.
func TestDeletingOneAdministratorLeavesOtherConnectorsAlone(t *testing.T) {
	ks, us, _ := keyStoreWithUsers(t)
	ctx := context.Background()

	leaver, err := us.Create(ctx, "leaver@example.com", "Leaver", "a-long-password", "admin")
	if err != nil {
		t.Fatalf("create leaver: %v", err)
	}
	stayer, err := us.Create(ctx, "stayer@example.com", "Stayer", "a-long-password", "admin")
	if err != nil {
		t.Fatalf("create stayer: %v", err)
	}
	leaverKey := fullControlKeyFor(t, ks, leaver.ID)
	stayerKey := fullControlKeyFor(t, ks, stayer.ID)
	systemKey := fullControlKeyFor(t, ks, "")

	deleteVia(t, ks, us, "leaver@example.com")

	if ks.Verify(leaverKey) {
		t.Error("the deleted administrator's key survived")
	}
	if !ks.Verify(stayerKey) {
		t.Error("deleting one administrator revoked ANOTHER administrator's connector.\n\n" +
			"Their integrations stop without warning and nothing on the panel explains why.")
	}
	if !ks.Verify(systemKey) {
		t.Error("deleting a user revoked the system-owned keys")
	}
}

// The empty-owner refusal, tested directly.
//
// deleteUserAccount only calls RevokeOwnedBy with a resolved, non-empty id, so
// the guard is unreachable from that path and removing it survives every test
// above — which is exactly why it needs one of its own. It exists for the call
// site that does not exist yet: ” is operator/system-owned, so a blank id
// treated as a wildcard would revoke the install's entire provisioned API
// surface in a single statement.
func TestRevokingByAnEmptyOwnerIsRefused(t *testing.T) {
	ks, _, _ := keyStoreWithUsers(t)
	system := fullControlKeyFor(t, ks, "")
	owned := fullControlKeyFor(t, ks, "u-someone")

	n, err := ks.RevokeOwnedBy(context.Background(), "  ")
	if err == nil {
		t.Errorf("RevokeOwnedBy(\"\") was accepted and revoked %d key(s).\n\n"+
			"An empty owner is not 'no filter', it is the operator/system-owned "+
			"bucket. Treating it as a wildcard takes the whole install's API surface "+
			"out in one statement.", n)
	}
	if !ks.Verify(system) {
		t.Error("the system-owned key was revoked by a blank-owner call")
	}
	if !ks.Verify(owned) {
		t.Error("an unrelated owner's key was revoked by a blank-owner call")
	}
}

// The other half of the same finding: DEMOTION.
//
// Deleting the account is the loud version. The quiet one is an operator moving
// an administrator down to author — which the panel presents as taking their
// administration away — while the connector they approved as an admin keeps its
// *:* grant. The key is a credential in its own right; its capabilities were
// fixed at mint time and nothing revisits them, so the demoted person now holds
// more through their connector than their own session allows.
//
// The rule is narrowed to a REDUCTION in access. A promotion leaves the keys
// alone: they are already within the wider authority the operator just granted,
// and revoking them would break working integrations for no security reason.
func TestDemotingAnAdministratorRevokesTheirConnector(t *testing.T) {
	ks, us, _ := keyStoreWithUsers(t)
	ctx := context.Background()

	u, err := us.Create(ctx, "demoted@example.com", "Demoted", "a-long-password", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	raw := fullControlKeyFor(t, ks, u.ID)

	app := &App{userStore: us, apiKeys: ks}
	if err := app.setUserRole(ctx, "demoted@example.com", "author"); err != nil {
		t.Fatalf("demote: %v", err)
	}

	if ks.Verify(raw) {
		t.Error("an administrator demoted to author kept a *:* connector.\n\n" +
			"The panel told the operator it had taken their administration away. The " +
			"connector still runs the whole site, and it renews itself.")
	}
}

// THE CONTROL. Promotion must not disturb anything: the keys are already inside
// the authority the operator just widened.
func TestPromotingAUserLeavesTheirKeysAlone(t *testing.T) {
	ks, us, _ := keyStoreWithUsers(t)
	ctx := context.Background()

	u, err := us.Create(ctx, "rising@example.com", "Rising", "a-long-password", "author")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	raw := fullControlKeyFor(t, ks, u.ID)

	app := &App{userStore: us, apiKeys: ks}
	if err := app.setUserRole(ctx, "rising@example.com", "admin"); err != nil {
		t.Fatalf("promote: %v", err)
	}

	if !ks.Verify(raw) {
		t.Error("promoting a user revoked their working connector.\n\n" +
			"Their integrations stop the moment they are given MORE authority, which " +
			"is a change nobody would connect to the cause.")
	}
	// And a no-op role write (admin → admin) is not a demotion either.
	if err := app.setUserRole(ctx, "rising@example.com", "admin"); err != nil {
		t.Fatalf("re-set same role: %v", err)
	}
	if !ks.Verify(raw) {
		t.Error("re-setting the SAME role revoked the user's keys")
	}
}

// THE PROPERTY BOTH FIXES REST ON, found by attacking them rather than the code
// they replaced.
//
// Revoking the key is only half a revocation if the connector can rotate it back.
// The OAuth refresh path takes a refresh token, resolves the key id it is bound
// to, and rotates that key — and the refresh-token row is NOT deleted when the
// key is revoked, so a departed administrator's client still holds a valid one
// and will present it within the hour as a matter of routine.
//
// RotateWithExpiry updates WHERE id=? AND revoked=0, so the rotation finds no
// row and the token endpoint answers "the connector key no longer exists". That
// is the correct outcome and it was already true; nothing tested it, and it is
// the single condition that makes revoking-on-delete and revoking-on-demote real
// rather than cosmetic.
func TestARevokedKeyCannotBeRotatedBackToLife(t *testing.T) {
	ks, us, _ := keyStoreWithUsers(t)
	ctx := context.Background()

	u, err := us.Create(ctx, "leaver@example.com", "Leaver", "a-long-password", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	perms := apikeys.NewPermissions()
	sec, act, _ := apikeys.ParseCapability("*:*")
	perms.Grant(sec, act)
	key, raw, err := ks.CreateWithPermissions(ctx, u.ID, "Claude via OAuth (Full control)", perms, nil, 0)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if !ks.Verify(raw) {
		t.Fatal("fixture: the minted key does not authenticate")
	}

	deleteVia(t, ks, us, "leaver@example.com")

	// Exactly what oauthTokenFromRefresh does once the refresh token checks out.
	fresh, rerr := ks.RotateWithExpiry(ctx, key.ID, nil)
	if rerr == nil {
		t.Fatalf("a revoked key was rotated and handed back a working token (%d chars).\n\n"+
			"The refresh-token row outlives the revocation, so the departed "+
			"administrator's client presents it on its normal renewal and gets its "+
			"access back. Revoking the key would be cosmetic.", len(fresh))
	}
	if ks.Verify(raw) {
		t.Error("the original token still authenticates after revocation")
	}
	if fresh != "" && ks.Verify(fresh) {
		t.Error("the rotated token authenticates")
	}
}
