// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// newPruneTestStore builds a store on a scratch database. ensureAppPasswords
// creates the table this test needs, so the schema comes from the product
// rather than from a restatement of it here.
func newPruneTestStore(t *testing.T) *AccountStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := &AccountStore{db: db}
	if err := s.ensureAppPasswords(); err != nil {
		t.Fatalf("ensureAppPasswords: %v", err)
	}
	return s
}

// The lockout this closes: every sign-in registers a device row, nothing
// dedupes them, and the mailbox has a hard ceiling. A phone re-added a few
// times over its life reaches that ceiling on its own — and once there the
// server refuses registration, the app reports it, and only an operator can
// clear it. Somebody locked out of their own mail because they signed in too
// many times is a worse outcome than the credential sprawl the cap prevents.
//
// A pending row was never approved, so it never synced anything and reclaiming
// it costs nobody anything. An approved row is somebody's working mail and a
// blocked row is a decision an operator made on purpose; reaping either would
// be a fix that causes a worse bug than it closes.
func TestPrunePendingDevicesReclaimsOnlyUnapprovedOnes(t *testing.T) {
	s := newPruneTestStore(t)
	ctx := context.Background()
	const email = "someone@example.test"

	seed := func(deviceID, status string, age time.Duration) {
		t.Helper()
		if _, err := s.CreateDevice(ctx, email, "phone", "android", deviceID, "hash", status); err != nil {
			t.Fatalf("seed %s: %v", deviceID, err)
		}
		if _, err := s.db.ExecContext(ctx,
			`UPDATE vayumail_app_passwords SET created_at=? WHERE device_id=?`,
			time.Now().UTC().Add(-age), deviceID); err != nil {
			t.Fatalf("age %s: %v", deviceID, err)
		}
	}

	seed("old-pending", DeviceStatusPending, 48*time.Hour)
	seed("new-pending", DeviceStatusPending, time.Minute)
	seed("old-approved", DeviceStatusApproved, 48*time.Hour)
	seed("old-blocked", DeviceStatusBlocked, 48*time.Hour)

	n, err := s.PrunePendingDevices(ctx, email, 24*time.Hour)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d rows, want exactly 1 (the old pending one)", n)
	}

	// Read device ids straight from the table: the credential struct the auth
	// path uses deliberately carries only id/hash/status.
	left := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT device_id FROM vayumail_app_passwords WHERE email=?`, email)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		left[id] = true
	}
	_ = rows.Err()
	if left["old-pending"] {
		t.Error("the stale unapproved device survived, so the ceiling can still lock the mailbox out")
	}
	if !left["new-pending"] {
		t.Error("a device registered a minute ago was reclaimed — an operator who steps away " +
			"mid-approval would find it gone before they got back")
	}
	if !left["old-approved"] {
		t.Error("an APPROVED device was deleted. That is somebody's working mail, silently " +
			"disconnected by a cleanup they never asked for.")
	}
	if !left["old-blocked"] {
		t.Error("a BLOCKED device was deleted, quietly undoing an operator's deliberate decision")
	}
}
