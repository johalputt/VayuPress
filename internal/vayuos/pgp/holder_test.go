// SPDX-License-Identifier: Apache-2.0

package pgp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
)

const holderPW = "the holder's password"

// heldEngine is an engine whose dana@example.com key its holder keeps, the
// recovery code, and a message sealed to the mailbox.
func heldEngine(t *testing.T) (*Engine, string, []byte) {
	t.Helper()
	e := newTestEngine(t)
	if _, err := e.GenerateKeypair(&PGPUser{UserID: "dana@example.com", Name: "Dana", Email: "dana@example.com"}); err != nil {
		t.Fatal(err)
	}
	code, err := e.LockToHolder("dana@example.com", holderPW)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	sealed, err := e.SealTo("dana@example.com", []byte("quarterly figures"))
	if err != nil {
		t.Fatal(err)
	}
	return e, code, sealed
}

// restarted is the same key store opened afresh, as after a restart: nothing
// held in memory.
func restarted(t *testing.T, e *Engine) *Engine {
	t.Helper()
	re := NewEngine(&e.cfg)
	if err := re.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return re
}

// everyKeyInTheStoreIsLocked opens every key file with the install's key, as
// someone holding the master secret and the disk would, and reports any
// private key that comes out usable.
func everyKeyInTheStoreIsLocked(t *testing.T, e *Engine) {
	t.Helper()
	entries, err := os.ReadDir(e.cfg.StorageDir)
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	for _, f := range entries {
		if !strings.HasSuffix(f.Name(), ".json") || (!strings.Contains(f.Name(), ".key.") && !strings.Contains(f.Name(), ".arch.")) {
			continue
		}
		rec, err := e.ks.readNamed(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		files++
		for _, copyOf := range [][2]string{{rec.PrivateNonce, rec.PrivateCT}, {rec.RecoveryNonce, rec.RecoveryCT}} {
			armored, err := e.ks.open(copyOf[0], copyOf[1])
			if err != nil {
				t.Fatalf("%s: a copy is missing or unreadable: %v", f.Name(), err)
			}
			ring, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(armored))
			if err != nil {
				t.Fatal(err)
			}
			for _, ent := range ring {
				if !ent.PrivateKey.Encrypted {
					t.Errorf("%s: the install's key opens a usable private key", f.Name())
				}
				for _, sk := range ent.Subkeys {
					if !sk.PrivateKey.Encrypted {
						t.Errorf("%s: the install's key opens a usable subkey", f.Name())
					}
				}
			}
		}
	}
	if files == 0 {
		t.Fatal("no key files read")
	}
}

// TestTheInstallCannotOpenAHoldersKey — with the master secret and the disk,
// and nothing the holder knows, no key opens and no message reads.
func TestTheInstallCannotOpenAHoldersKey(t *testing.T) {
	e, _, sealed := heldEngine(t)
	everyKeyInTheStoreIsLocked(t, e)
	re := restarted(t, e)
	if _, err := re.DecryptForEmail(sealed, "dana@example.com"); !errors.Is(err, ErrHolderLocked) {
		t.Fatalf("decrypting after a restart gave %v, want ErrHolderLocked", err)
	}
	if _, err := re.SignDetachedFromEmail([]byte("x"), "dana@example.com"); !errors.Is(err, ErrHolderLocked) {
		t.Fatalf("signing after a restart gave %v, want ErrHolderLocked", err)
	}
}

// TestThePasswordHoldsTheKeyOpenUntilClosed — and nothing else does.
func TestThePasswordHoldsTheKeyOpenUntilClosed(t *testing.T) {
	e, code, sealed := heldEngine(t)
	if got, err := e.DecryptForEmail(sealed, "dana@example.com"); err != nil || string(got) != "quarterly figures" {
		t.Fatalf("right after locking, the holder reads %q, %v", got, err)
	}
	re := restarted(t, e)
	for _, wrong := range []string{"not it", code, ""} {
		if err := re.HoldOpen("dana@example.com", wrong); !errors.Is(err, ErrNotUnlocked) {
			t.Fatalf("HoldOpen(%q) gave %v, want ErrNotUnlocked", wrong, err)
		}
	}
	if re.HeldOpen("dana@example.com") {
		t.Fatal("a wrong password held the key open")
	}
	if err := re.HoldOpen("dana@example.com", holderPW); err != nil {
		t.Fatalf("the password: %v", err)
	}
	if got, err := re.DecryptForEmail(sealed, "dana@example.com"); err != nil || string(got) != "quarterly figures" {
		t.Fatalf("held open, the holder reads %q, %v", got, err)
	}
	re.CloseHeld("dana@example.com")
	if _, err := re.DecryptForEmail(sealed, "dana@example.com"); !errors.Is(err, ErrHolderLocked) {
		t.Fatalf("after closing: %v, want ErrHolderLocked", err)
	}
}

// TestTheHoldEnds — a key held open closes by itself.
func TestTheHoldEnds(t *testing.T) {
	e, _, sealed := heldEngine(t)
	userID, _ := e.holderID("dana@example.com")
	e.mu.Lock()
	h := e.held[userID]
	h.until = h.until.Add(-HoldOpenFor - 1)
	e.held[userID] = h
	e.mu.Unlock()
	if _, err := e.DecryptForEmail(sealed, "dana@example.com"); !errors.Is(err, ErrHolderLocked) {
		t.Fatalf("past its time the key still reads: %v", err)
	}
}

// TestARecoveryCodeOpensTheKeyOnce — it locks the key under the password
// given and a new code; the used code no longer opens anything.
func TestARecoveryCodeOpensTheKeyOnce(t *testing.T) {
	e, code, sealed := heldEngine(t)
	re := restarted(t, e)
	next, err := re.RecoverHolder("dana@example.com", " "+strings.ToLower(code)+" ", "a new password")
	if err != nil {
		t.Fatalf("the recovery code, typed loosely: %v", err)
	}
	if next == "" || next == code {
		t.Fatalf("recovery gave code %q, want a new one", next)
	}
	again := restarted(t, re)
	if _, err := again.RecoverHolder("dana@example.com", code, "x"); !errors.Is(err, ErrNotUnlocked) {
		t.Fatalf("the used code gave %v, want ErrNotUnlocked", err)
	}
	if err := again.HoldOpen("dana@example.com", holderPW); !errors.Is(err, ErrNotUnlocked) {
		t.Fatalf("the old password gave %v, want ErrNotUnlocked", err)
	}
	if err := again.HoldOpen("dana@example.com", "a new password"); err != nil {
		t.Fatalf("the new password: %v", err)
	}
	if got, _ := again.DecryptForEmail(sealed, "dana@example.com"); string(got) != "quarterly figures" {
		t.Fatalf("after recovery the mail reads %q", got)
	}
	if _, err := restarted(t, again).RecoverHolder("dana@example.com", next, "x"); err != nil {
		t.Fatalf("the new code: %v", err)
	}
	everyKeyInTheStoreIsLocked(t, again)
}

// TestAPasswordChangeRelocksTheKey — the old password stops opening it; the
// recovery code still does.
func TestAPasswordChangeRelocksTheKey(t *testing.T) {
	e, code, _ := heldEngine(t)
	if err := e.ChangeHolderPassword("dana@example.com", "not it", "new"); !errors.Is(err, ErrNotUnlocked) {
		t.Fatalf("a change from a wrong password gave %v", err)
	}
	if err := e.ChangeHolderPassword("dana@example.com", holderPW, "the new password"); err != nil {
		t.Fatal(err)
	}
	re := restarted(t, e)
	if err := re.HoldOpen("dana@example.com", holderPW); !errors.Is(err, ErrNotUnlocked) {
		t.Fatalf("the old password gave %v", err)
	}
	if err := re.HoldOpen("dana@example.com", "the new password"); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted(t, re).RecoverHolder("dana@example.com", code, "the new password"); err != nil {
		t.Fatalf("the recovery code after a password change: %v", err)
	}
	everyKeyInTheStoreIsLocked(t, re)
}

// TestRotatedOutKeysGoWithTheHolder — mail sealed to an older key reads when
// held open, and the older key is locked like the current one.
func TestRotatedOutKeysGoWithTheHolder(t *testing.T) {
	e := newTestEngine(t)
	if _, err := e.GenerateKeypair(&PGPUser{UserID: "dana@example.com", Name: "Dana", Email: "dana@example.com"}); err != nil {
		t.Fatal(err)
	}
	old, err := e.Encrypt([]byte("from before the rotation"), "dana@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.RotateKeypair("dana@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.LockToHolder("dana@example.com", holderPW); err != nil {
		t.Fatal(err)
	}
	if n := len(e.ks.holderFiles("dana@example.com")); n != 2 {
		t.Fatalf("%d key files, want the key and its archive", n)
	}
	everyKeyInTheStoreIsLocked(t, e)
	re := restarted(t, e)
	if _, err := re.DecryptForEmail(old, "dana@example.com"); !errors.Is(err, ErrHolderLocked) {
		t.Fatalf("old mail without the password: %v", err)
	}
	if err := re.HoldOpen("dana@example.com", holderPW); err != nil {
		t.Fatal(err)
	}
	if got, err := re.DecryptForEmail(old, "dana@example.com"); err != nil || string(got) != "from before the rotation" {
		t.Fatalf("old mail held open: %q, %v", got, err)
	}
	sig, err := re.SignDetachedFromEmail([]byte("signed"), "dana@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := re.Verify([]byte("signed"), sig, "dana@example.com"); !ok {
		t.Fatalf("held open, the holder signs with a key other than the current one: %v", err)
	}
	if _, err := re.RotateKeypair("dana@example.com"); !errors.Is(err, ErrHolderKey) {
		t.Fatalf("rotating a holder's key gave %v, want ErrHolderKey", err)
	}
	if err := re.RevokeKeypair("dana@example.com"); !errors.Is(err, ErrHolderKey) || !re.HolderKeeps("dana@example.com") {
		t.Fatalf("revoking a holder's key gave %v; revoked, the address's next mail would go unsealed", err)
	}
}

// TestTheAppIsHandedTheLockedKey — what the key endpoint gives the phone is
// the key locked with the holder's password, which the password opens.
func TestTheAppIsHandedTheLockedKey(t *testing.T) {
	e, _, _ := heldEngine(t)
	armored, err := restarted(t, e).ArmoredPrivateKey("dana@example.com")
	if err != nil {
		t.Fatal(err)
	}
	ring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored))
	if err != nil || len(ring) == 0 || !ring[0].PrivateKey.Encrypted {
		t.Fatalf("the app was handed an unlocked key (%v)", err)
	}
	if _, err := openArmor([]byte(armored), []byte(holderPW)); err != nil {
		t.Fatalf("the password does not open what the app gets: %v", err)
	}
}

// TestADeletedMailboxsKeyIsNotHandedOn — once retired, the address is free:
// a new key is made for whoever holds it next, and the old one is still on
// disk for the retired mail.
func TestADeletedMailboxsKeyIsNotHandedOn(t *testing.T) {
	e, _, sealed := heldEngine(t)
	if err := e.RetireHolderKey("dana@example.com"); err != nil {
		t.Fatal(err)
	}
	if e.HolderKeeps("dana@example.com") || e.HeldOpen("dana@example.com") {
		t.Fatal("the retired key is still the address's key")
	}
	re := restarted(t, e)
	if _, err := re.EnsureKeypair(&PGPUser{UserID: "dana@example.com", Name: "Dana", Email: "dana@example.com"}); err != nil {
		t.Fatal(err)
	}
	if re.HolderKeeps("dana@example.com") {
		t.Fatal("the next holder of the address was given the last holder's locked key")
	}
	if _, err := re.SignDetachedFromEmail([]byte("x"), "dana@example.com"); err != nil {
		t.Fatalf("the next holder's key is not usable: %v", err)
	}
	entries, _ := os.ReadDir(e.cfg.StorageDir)
	kept := false
	for _, f := range entries {
		if strings.Contains(f.Name(), ".retired-") {
			kept = true
		}
	}
	if !kept {
		t.Error("the retired key was deleted; the retired mail could never be opened again")
	}
	_ = sealed
}

// TestALockStoppedPartWayIsFinished — a key file left unlocked by an
// interrupted lock is locked by the next one.
func TestALockStoppedPartWayIsFinished(t *testing.T) {
	e := newTestEngine(t)
	if _, err := e.GenerateKeypair(&PGPUser{UserID: "dana@example.com", Name: "Dana", Email: "dana@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RotateKeypair("dana@example.com"); err != nil {
		t.Fatal(err)
	}
	// Lock only the archive, as a crash after the first file would leave it.
	names, recs, rings, err := e.openHolderFiles("dana@example.com", []byte(holderPW), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.relock(names[:1], recs[:1], rings[:1], []byte(holderPW), []byte("CODE")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.LockToHolder("dana@example.com", holderPW); err != nil {
		t.Fatalf("finishing the lock: %v", err)
	}
	everyKeyInTheStoreIsLocked(t, e)
}
