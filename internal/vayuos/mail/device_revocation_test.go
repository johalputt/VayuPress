// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"testing"

	"github.com/johalputt/vayupress/internal/auth"
)

// SECTION 2 AUDIT FINDING — disabling or deleting a mailbox cut nothing.
//
// In the operator's voice, which is the right one here because this is a tool
// they reach for in an emergency:
//
//	Someone left, or a phone was stolen. I disable the mailbox. I delete it.
//	Either way I believe I have cut that device off.
//
//	I have not. SetActive writes one column of vayumail_accounts and Delete is a
//	single-table DELETE. HashFor is the ONLY query carrying "AND active=1", so
//	the raw password does stop working — which is exactly what makes this
//	convincing. AppPasswordCredentials selects "WHERE email=?" with no join and
//	no active predicate, so every enrolled device keeps authenticating over
//	IMAP, POP3 and submission, and the Maildir is never removed, so it keeps
//	serving the mail that is still on disk.
//
// The delete case has a second edge: the credential rows are orphaned rather
// than removed, so creating a mailbox at the same address later hands the new
// occupant's mail to the previous holder's phone.

func revocationStore(t *testing.T) *AccountStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	// The production constructor, not a hand-built struct: it is what creates
	// vayumail_accounts, and the credential query below now reads that table.
	s, err := NewAccountStore(db)
	if err != nil {
		t.Fatalf("accounts schema: %v", err)
	}
	if err := s.ensureAppPasswords(); err != nil {
		t.Fatalf("app-password schema: %v", err)
	}
	return s
}

// enrolledMailbox creates an active mailbox holding one approved device.
func enrolledMailbox(t *testing.T, s *AccountStore, email, deviceSecret string) {
	t.Helper()
	ctx := context.Background()
	pw, err := auth.HashSecretArgon2id("the account password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := s.Create(ctx, email, pw, "Holder", "mailbox"); err != nil {
		t.Fatalf("create account: %v", err)
	}
	dh, err := auth.HashSecretArgon2id(deviceSecret)
	if err != nil {
		t.Fatalf("hash device: %v", err)
	}
	if _, err := s.CreateAppPassword(ctx, email, "phone", dh); err != nil {
		t.Fatalf("create app password: %v", err)
	}
	// Sanity: the device authenticates while the mailbox is healthy, or every
	// assertion below would pass for the wrong reason.
	if _, ok := s.VerifyApprovedDevice(ctx, email, deviceSecret); !ok {
		t.Fatal("the seeded device does not authenticate; the fixture is wrong")
	}
}

func TestDisablingAMailboxCutsOffItsDevices(t *testing.T) {
	s := revocationStore(t)
	ctx := context.Background()
	enrolledMailbox(t, s, "holder@example.com", "device-secret")

	if err := s.SetActive(ctx, "holder@example.com", false); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if _, ok := s.VerifyApprovedDevice(ctx, "holder@example.com", "device-secret"); ok {
		t.Error("the device still authenticates against a DISABLED mailbox.\n\n" +
			"Disabling is what an operator reaches for when a phone is stolen or someone " +
			"leaves. The account password stops working, which makes it look like it worked, " +
			"while every enrolled device keeps syncing the mail.")
	}
	if creds := s.AppPasswordCredentials(ctx, "holder@example.com"); len(creds) != 0 {
		t.Errorf("a disabled mailbox still offers %d credential(s) to the login path", len(creds))
	}
}

// Disabling is reversible, and must stay reversible: re-enabling restores the
// holder's own devices rather than making them re-enrol every phone they own.
func TestReEnablingAMailboxRestoresItsDevices(t *testing.T) {
	s := revocationStore(t)
	ctx := context.Background()
	enrolledMailbox(t, s, "holder@example.com", "device-secret")

	if err := s.SetActive(ctx, "holder@example.com", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := s.SetActive(ctx, "holder@example.com", true); err != nil {
		t.Fatalf("re-enable: %v", err)
	}

	if _, ok := s.VerifyApprovedDevice(ctx, "holder@example.com", "device-secret"); !ok {
		t.Error("re-enabling a mailbox did not restore its devices.\n\n" +
			"A reversible switch that silently destroys every enrolment is not reversible; " +
			"the holder would have to re-add every device they own.")
	}
}

func TestDeletingAMailboxCutsOffItsDevices(t *testing.T) {
	s := revocationStore(t)
	ctx := context.Background()
	enrolledMailbox(t, s, "gone@example.com", "device-secret")

	if err := s.Delete(ctx, "gone@example.com"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, ok := s.VerifyApprovedDevice(ctx, "gone@example.com", "device-secret"); ok {
		t.Error("a device still authenticates against a DELETED mailbox.\n\n" +
			"The account row is gone, the Maildir is not, and the credential rows are not — " +
			"so the departed holder's phone keeps downloading the mail still on disk.")
	}
}

// The sharp edge of the delete case: recreating the address must not resurrect
// the previous holder's devices.
func TestRecreatingAnAddressDoesNotResurrectTheOldDevices(t *testing.T) {
	s := revocationStore(t)
	ctx := context.Background()
	enrolledMailbox(t, s, "shared@example.com", "old-holders-device")

	if err := s.Delete(ctx, "shared@example.com"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// The address is reissued to somebody else — a role account changing hands.
	pw, err := auth.HashSecretArgon2id("a new person's password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := s.Create(ctx, "shared@example.com", pw, "New Holder", "mailbox"); err != nil {
		t.Fatalf("recreate: %v", err)
	}

	if _, ok := s.VerifyApprovedDevice(ctx, "shared@example.com", "old-holders-device"); ok {
		t.Error("the PREVIOUS holder's device authenticates against the recreated mailbox.\n\n" +
			"Orphaned credential rows outlive the account, so reissuing an address hands the " +
			"new occupant's mail to whoever held it before.")
	}
}

// THE CONTROL. None of the above may cost a healthy mailbox its devices.
func TestAnActiveMailboxKeepsItsDevices(t *testing.T) {
	s := revocationStore(t)
	ctx := context.Background()
	enrolledMailbox(t, s, "working@example.com", "device-secret")

	if _, ok := s.VerifyApprovedDevice(ctx, "working@example.com", "device-secret"); !ok {
		t.Fatal("an active mailbox's device stopped authenticating — every phone and mail " +
			"client on the install just lost access")
	}
	if creds := s.AppPasswordCredentials(ctx, "working@example.com"); len(creds) != 1 {
		t.Errorf("an active mailbox offers %d credentials, want 1 — the app-password cap and "+
			"the device list both read this", len(creds))
	}
}

// The device credential is not the only thing that outlived the account. A
// recovery code and a recovery token each reset the password on their address,
// so leaving them behind means the mailbox is deleted while two separate ways
// back into it are not — and both land on the NEW occupant once the address is
// reissued.
//
// This is the same defect as the device rows, and finding it is the reason
// Delete removes the whole per-address set rather than the one table this
// finding started from.
func TestRecoveryMaterialDoesNotOutliveTheMailbox(t *testing.T) {
	s := revocationStore(t)
	ctx := context.Background()
	enrolledMailbox(t, s, "shared@example.com", "device-secret")

	codes, err := s.GenerateRecoveryCodes(ctx, "shared@example.com")
	if err != nil {
		t.Fatalf("generate recovery codes: %v", err)
	}
	if len(codes) == 0 {
		t.Fatal("no recovery codes generated; the fixture is wrong")
	}
	token, err := s.CreateRecoveryToken(ctx, "shared@example.com")
	if err != nil {
		t.Fatalf("create recovery token: %v", err)
	}

	if err := s.Delete(ctx, "shared@example.com"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	pw, err := auth.HashSecretArgon2id("a new person's password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := s.Create(ctx, "shared@example.com", pw, "New Holder", "mailbox"); err != nil {
		t.Fatalf("recreate: %v", err)
	}

	if s.ConsumeRecoveryCode(ctx, "shared@example.com", codes[0]) {
		t.Error("the previous holder's recovery code still resets the password on the " +
			"reissued address — a full takeover of the new occupant's mailbox")
	}
	if addr, err := s.ConsumeRecoveryToken(ctx, token); err == nil {
		t.Errorf("the previous holder's recovery link still resolves to %q after the "+
			"mailbox was deleted and reissued", addr)
	}
}

// Deleting an account must not fail on an install that never used a feature.
// Most of the per-address tables are created lazily by their own ensure* helper,
// so a store where nobody ever enrolled a device or generated a recovery code
// simply has no such table — and a delete that errors on that leaves the
// operator unable to remove an account at all.
func TestDeletingAMailboxWorksOnAStoreWithNoOptionalTables(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	s, err := NewAccountStore(db) // accounts only — no ensureAppPasswords, no ensureRecovery
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	pw, err := auth.HashSecretArgon2id("password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := s.Create(context.Background(), "plain@example.com", pw, "Holder", "mailbox"); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := s.Delete(context.Background(), "plain@example.com"); err != nil {
		t.Fatalf("deleting an account on a store with no optional tables failed: %v\n\n"+
			"Every install that never enrolled a device is in this state. An operator "+
			"who cannot delete an account has lost the control this fix is about.", err)
	}
	if s.HashFor(context.Background(), "plain@example.com") != "" {
		t.Error("the account survived its own deletion")
	}
}

// THE OTHER CONTROL, and the one the obvious version of this fix fails.
//
// "Only serve credentials for an ACTIVE account" reads like the right rule and
// is an outage. A CMS user is a first-class mailbox holder here — verifyCredential
// authenticates them through userStore (branch 1) and GetUserByEmail resolves
// their mailbox — and they need no vayumail_accounts row at all. The bootstrap
// admin created by internal/users has exactly that shape.
//
// So the rule is deliberately narrower: refuse when a row EXISTS and is
// disabled. An address with no row is left exactly as it works today. Requiring
// a row instead would have logged every CMS-only holder out of their own mail on
// upgrade — a security fix taking away the access it was meant to protect.
func TestAnAddressWithNoMailAccountRowKeepsItsDevices(t *testing.T) {
	s := revocationStore(t)
	ctx := context.Background()

	dh, err := auth.HashSecretArgon2id("cms-users-device")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := s.CreateAppPassword(ctx, "admin@example.com", "laptop", dh); err != nil {
		t.Fatalf("create app password: %v", err)
	}

	if _, ok := s.VerifyApprovedDevice(ctx, "admin@example.com", "cms-users-device"); !ok {
		t.Error("a credential belonging to an address with no vayumail_accounts row stopped " +
			"authenticating.\n\n" +
			"CMS users hold mailboxes without a mail-account row. Gating on 'an active row " +
			"exists' rather than 'no disabled row exists' cuts off every one of them.")
	}
}
