// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// These tests are written from the position of the person the promise is made
// to: a client who has been told "after we hand your mailbox over, our staff can
// no longer open it from the admin panel."
//
// Each one tries to make that sentence false.

func handoverDB(t *testing.T) *AccountStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, line := range strings.Split(migration081(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		if _, err := db.Exec(line); err != nil {
			t.Fatalf("migration: %v (%s)", err, line)
		}
	}
	s := &AccountStore{db: db}
	s.SetDefaultDomain("studio.test")
	return s
}

// migration081 is the shipped migration, read at test time rather than restated,
// so a test cannot pass against a schema the product does not have.
func migration081() string {
	b, err := readRepoFile("../../db/migrations/081-mail-handover.up.sql")
	if err != nil {
		return ""
	}
	return b
}

func TestAHandedOverMailboxRefusesTheOperatorEverywhereItCounts(t *testing.T) {
	s := handoverDB(t)
	ctx := context.Background()
	const mbox = "bob@client.test"

	if s.IsHandedOver(mbox) {
		t.Fatal("a mailbox is handed over before anyone handed it over")
	}
	if err := s.HandOver(ctx, mbox, "operator@studio.test", "bob-personal@johal.in"); err != nil {
		t.Fatal(err)
	}
	if !s.IsHandedOver(mbox) {
		t.Fatal("handover did not take effect")
	}

	// The two moves that made "we cannot get into your account" false.
	if err := s.DisableTOTP(ctx, mbox); err != errHandedOverAccount {
		t.Errorf("clearing the second factor returned %v — an operator who can clear the code "+
			"and reset the password can simply sign in as the client", err)
	}
	// Forwarding reads the mailbox without opening it, by copying every future
	// message somewhere the operator chooses.
	if err := s.SetForward(ctx, mbox, "operator@studio.test"); err != errHandedOverAccount {
		t.Errorf("setting auto-forward returned %v — this is the cheapest way to defeat a "+
			"handover and it leaves the archive untouched, so nothing looks wrong", err)
	}
	// Clearing forwarding is not exfiltration, so it must stay available.
	if err := s.SetForward(ctx, mbox, ""); err == errHandedOverAccount {
		t.Error("clearing forwarding was refused; turning a leak OFF must never be blocked")
	}
}

// A mailbox asked for two ways must give one answer. A handover that can be
// bypassed by using a bare local part instead of a full address is not one.
func TestHandoverCannotBeBypassedByHowTheMailboxIsNamed(t *testing.T) {
	s := handoverDB(t)
	ctx := context.Background()
	if err := s.HandOver(ctx, "alice@studio.test", "op", ""); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"alice@studio.test", "alice", "ALICE@STUDIO.TEST", "  alice  "} {
		if !s.IsHandedOver(key) {
			t.Errorf("asked as %q the mailbox reads as not handed over — the same mailbox "+
				"must give one answer however it is named", key)
		}
	}
}

// Handover is one-way and the record is permanent, enforced by the DATABASE.
// A promise that the party running the database can quietly undo is not one.
func TestHandoverCannotBeUndoneEvenByTheOperator(t *testing.T) {
	s := handoverDB(t)
	ctx := context.Background()
	const mbox = "carol@client.test"
	if err := s.HandOver(ctx, mbox, "op", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE mail_handover SET handed_at=NULL WHERE mailbox=?`, mbox); err == nil {
		t.Error("the operator un-did a handover with one UPDATE")
	}
	if _, err := s.db.Exec(`DELETE FROM mail_handover WHERE mailbox=?`, mbox); err == nil {
		t.Error("the operator deleted a handover record")
	}
	if !s.IsHandedOver(mbox) {
		t.Error("after the failed attempts the mailbox is no longer handed over")
	}
}

// The ledger is append-only in the database, and tampering is detectable.
//
// Chaining does NOT make tampering impossible — the operator owns the database.
// It makes it show up, which is the honest property and the one the client is
// told about. A record that could be quietly rewritten would be worse than none,
// because it would be believed.
func TestTheAccessRecordIsAppendOnlyAndTamperEvident(t *testing.T) {
	s := handoverDB(t)
	ctx := context.Background()
	const mbox = "dan@client.test"
	for _, a := range []string{"break-glass", "handover", "break-glass"} {
		if err := s.AppendLedger(ctx, mbox, "op@studio.test", a, "detail"); err != nil {
			t.Fatal(err)
		}
	}
	if bad, err := s.VerifyLedger(ctx); err != nil || bad != 0 {
		t.Fatalf("a freshly written chain does not verify: bad=%d err=%v", bad, err)
	}
	if _, err := s.db.Exec(`UPDATE mail_access_ledger SET actor='someone-else' WHERE seq=2`); err == nil {
		t.Error("a ledger row was edited in place")
	}
	if _, err := s.db.Exec(`DELETE FROM mail_access_ledger WHERE seq=2`); err == nil {
		t.Error("a ledger row was deleted")
	}
	// The chain must still verify after the refused attempts, and the client must
	// be able to read their own record.
	if bad, _ := s.VerifyLedger(ctx); bad != 0 {
		t.Errorf("the chain broke at %d after refused tampering", bad)
	}
	ents, err := s.Ledger(ctx, mbox, 10)
	if err != nil || len(ents) != 3 {
		t.Fatalf("the client's own record shows %d entries (err %v), want 3", len(ents), err)
	}
}

// If the chain IS broken — by someone with direct database access, which is
// exactly who the client is warned about — verification must say where.
func TestTamperingWithTheRecordIsDetected(t *testing.T) {
	s := handoverDB(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := s.AppendLedger(ctx, "e@client.test", "op", "break-glass", "d"); err != nil {
			t.Fatal(err)
		}
	}
	// Drop the trigger, as someone with direct database access would, and rewrite
	// an entry. This is the scenario the honest claim admits is possible.
	if _, err := s.db.Exec(`DROP TRIGGER mail_access_ledger_is_append_only`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE mail_access_ledger SET detail='nothing to see' WHERE seq=2`); err != nil {
		t.Fatal(err)
	}
	bad, err := s.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bad != 2 {
		t.Errorf("verification reported %d, want 2 — a rewritten entry must be locatable, "+
			"or 'tampering shows up' is a marketing sentence rather than a checkable one", bad)
	}
}

// A database that cannot answer must not answer "no" — except in the one case
// where the answer is knowable anyway.
func TestAnUnanswerableHandoverQuestionFailsClosed(t *testing.T) {
	// A table that has NEVER existed cannot have recorded a handover. An install
	// mid-upgrade must keep working, and this is certainty rather than optimism.
	fresh := handoverDB(t)
	if _, err := fresh.db.Exec(`DROP TABLE mail_handover`); err != nil {
		t.Fatal(err)
	}
	if fresh.IsHandedOver("frank@client.test") {
		t.Error("with no handover table at all every mailbox reads as handed over, which " +
			"locks an operator out of their own install for the duration of an upgrade")
	}

	// Any OTHER failure leaves the question genuinely open, and an open question
	// must not be answered "no": that opens a client's mail on a transient error
	// nobody would ever notice.
	broken := handoverDB(t)
	if err := broken.db.Close(); err != nil {
		t.Fatal(err)
	}
	if !broken.IsHandedOver("frank@client.test") {
		t.Error("with the database unusable the mailbox reads as NOT handed over — a " +
			"transient failure would open every handed-over mailbox to the operator")
	}
}

// readRepoFile loads a file relative to this package, so a test reads the
// SHIPPED artefact rather than a copy that can drift from it.
func readRepoFile(rel string) (string, error) {
	b, err := os.ReadFile(rel)
	return string(b), err
}
