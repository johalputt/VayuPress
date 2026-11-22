// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// Device approval is required of every mailbox. A mailbox an earlier release
// let an administrator switch off is switched on when the store opens, and
// reads as required from then on.
func TestAMailboxSwitchedOffIsSwitchedOnAtStart(t *testing.T) {
	t.Parallel()
	db, _ := sql.Open("sqlite3", ":memory:")
	db.SetMaxOpenConns(1)
	defer db.Close()
	ctx := context.Background()
	s, err := NewAccountStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, "dana@example.com", "x", "Dana", RoleMailbox); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE vayumail_accounts SET require_device_approval = 0`); err != nil {
		t.Fatal(err)
	}
	if s.RequireDeviceApproval(ctx, "dana@example.com") {
		t.Fatal("the switched-off state was not written, so this test proves nothing")
	}
	s, err = NewAccountStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if !s.RequireDeviceApproval(ctx, "dana@example.com") {
		t.Fatal("a mailbox switched off before the upgrade is still off after the store opens")
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 || !list[0].RequireDeviceApproval {
		t.Fatalf("the mailbox lists as %+v (%v)", list, err)
	}
}
