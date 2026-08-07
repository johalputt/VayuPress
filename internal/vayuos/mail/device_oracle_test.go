// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
)

// The audit finding, in the attacker's voice:
//
//	I do not need a password. I need to know which of your addresses have a
//	phone attached, and your server tells me for free.
//
//	  POST /api/v1/members/vayumail-device-reset
//	  {"email":"<candidate>","app_password":"x","new_password":"aaaaaaaa"}
//
//	VerifyApprovedDevice only reached auth.VerifySecretArgon2id from inside the
//	loop, so an address with no enrolled devices never ran the KDF and never
//	reached the empty-hash decoy. A sub-millisecond 401 meant "nobody enrolled";
//	tens to hundreds of milliseconds meant "at least one device". One request per
//	address, no statistics required — the decoy path measures around 193 ms
//	against a microsecond zero-row SELECT.
//
// The handler's own comment promised the opposite — "one uniform failure for
// every rejection below" — which is why nothing was looking for it. A claim, not
// a control.
//
// (Precisely: this was never a MAILBOX-existence oracle. An unknown address and
// a real mailbox with no devices both return zero rows and both answered fast.
// What leaked is enrolment.)

func oracleStore(t *testing.T) *AccountStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	// The production constructor, not a hand-built struct. Every AccountStore the
	// product creates comes from here, and both callers exit if it fails — so a
	// store without vayumail_accounts is a shape that only ever existed in this
	// harness. The credential query reads that table to enforce mailbox state,
	// and a harness missing it fails devices for a reason production cannot have.
	s, err := NewAccountStore(db)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := s.ensureAppPasswords(); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return s
}

// timeVerify returns how long one rejected verification takes. The median of a
// few runs, because a single sample on a shared machine is noise — but the
// signal being measured is two orders of magnitude, so this needs no more care
// than that.
func timeVerify(t *testing.T, s *AccountStore, email string) time.Duration {
	t.Helper()
	var best time.Duration
	for i := 0; i < 3; i++ {
		start := time.Now()
		if _, ok := s.VerifyApprovedDevice(context.Background(), email, "not-the-right-secret"); ok {
			t.Fatal("a wrong secret verified")
		}
		if d := time.Since(start); best == 0 || d < best {
			best = d
		}
	}
	return best
}

func TestARejectionCostsTheSameWhetherOrNotADeviceIsEnrolled(t *testing.T) {
	s := oracleStore(t)
	ctx := context.Background()

	hash, err := auth.HashSecretArgon2id("a real device credential")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO vayumail_app_passwords(email,label,hash,status,created_at)
		 VALUES(?,?,?,'approved',?)`,
		"enrolled@example.com", "phone", hash, time.Now().UTC()); err != nil {
		t.Fatalf("seed device: %v", err)
	}

	withDevice := timeVerify(t, s, "enrolled@example.com")
	without := timeVerify(t, s, "nobody@example.com")

	// The premise: a real verification has to be expensive, or there is no
	// timing signal to hide and this test would pass on a stub.
	if withDevice < 5*time.Millisecond {
		t.Fatalf("verifying against a real credential took %v — Argon2id is not running, so "+
			"this test cannot observe the difference it exists to close", withDevice)
	}

	// The gap the attacker reads. A factor of four is far tighter than the ~1000×
	// the defect gave and far looser than scheduler noise on a busy machine.
	if withDevice > without*4 {
		t.Errorf("an address with an enrolled device costs %v and one without costs %v.\n\n"+
			"That difference is readable from a single request, so anyone can walk a list of "+
			"your published addresses and learn which people have a phone attached. The "+
			"handler's own comment promises one uniform failure for every rejection.",
			withDevice, without)
	}
}

// The control: the decoy must not make a CORRECT credential fail, and a blocked
// device must still be refused. A "spend the decoy" fix that also stopped
// answering correctly would be an outage.
func TestAnApprovedDeviceStillVerifies(t *testing.T) {
	s := oracleStore(t)
	ctx := context.Background()

	hash, err := auth.HashSecretArgon2id("the-right-secret")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO vayumail_app_passwords(email,label,hash,status,created_at)
		 VALUES(?,?,?,'approved',?)`,
		"holder@example.com", "phone", hash, time.Now().UTC()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, ok := s.VerifyApprovedDevice(ctx, "holder@example.com", "the-right-secret"); !ok {
		t.Fatal("an approved device with the correct credential was refused — trusted-device " +
			"recovery is broken for everyone holding one")
	}
}

func TestABlockedDeviceIsStillRefused(t *testing.T) {
	s := oracleStore(t)
	ctx := context.Background()

	hash, err := auth.HashSecretArgon2id("the-right-secret")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO vayumail_app_passwords(email,label,hash,status,created_at)
		 VALUES(?,?,?,'blocked',?)`,
		"blocked@example.com", "stolen phone", hash, time.Now().UTC()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, ok := s.VerifyApprovedDevice(ctx, "blocked@example.com", "the-right-secret"); ok {
		t.Fatal("a device the operator BLOCKED still recovered the mailbox — blocking is the " +
			"control for a stolen phone and it has to mean something")
	}
}

// A mailbox with several devices must still find the right one wherever it sits
// in the list. Capping the loop was the obvious way to bound the compute
// amplifier and it would have introduced exactly this lockout, because the
// credential query carries no ORDER BY.
func TestALaterEnrolledDeviceIsStillFound(t *testing.T) {
	s := oracleStore(t)
	ctx := context.Background()

	for i, secret := range []string{"first", "second", "third", "fourth", "fifth", "sixth", "seventh"} {
		hash, err := auth.HashSecretArgon2id(secret)
		if err != nil {
			t.Fatalf("hash %d: %v", i, err)
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO vayumail_app_passwords(email,label,hash,status,created_at)
			 VALUES(?,?,?,'approved',?)`,
			"many@example.com", secret, hash, time.Now().UTC()); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	if _, ok := s.VerifyApprovedDevice(ctx, "many@example.com", "seventh"); !ok {
		t.Fatal("the most recently enrolled of seven devices could not recover the mailbox.\n\n" +
			"A person holding a phone they enrolled last week would be told their credential " +
			"is wrong — a lockout on the one path that exists to undo a lockout.")
	}
}
