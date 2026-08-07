// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The finding: Argon2id here runs at 64 MiB, t=3, 4 threads, and the
// anti-enumeration decoy forces a full derivation even for an address that does
// not exist — so a failing unauthenticated request costs exactly as much as a
// succeeding one. Nothing bounded how many could run at once, and the per-mailbox
// "throttle" is a time.Sleep, which delays one caller and bounds nothing about a
// thousand arriving together. A few hundred concurrent posts to any credential
// endpoint were a few hundred × 64 MiB of live allocation with every core
// saturated.
//
// These tests live IN the package on purpose. The first version was written from
// outside and counted concurrency around the call rather than inside the
// derivation — so it passed with the ceiling deleted, which is the one thing a
// test of a ceiling must not do. len(argonSlots) is the number of slots
// currently held, and only an in-package test can read it.

// TestTheCeilingActuallyHoldsUnderAFlood drives the primitive directly.
func TestTheCeilingActuallyHoldsUnderAFlood(t *testing.T) {
	limit := cap(argonSlots)

	var inFlight, peak int64
	var wg sync.WaitGroup
	for i := 0; i < limit*8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			withArgonSlot(func() {
				n := atomic.AddInt64(&inFlight, 1)
				for {
					old := atomic.LoadInt64(&peak)
					if n <= old || atomic.CompareAndSwapInt64(&peak, old, n) {
						break
					}
				}
				time.Sleep(2 * time.Millisecond) // hold the slot long enough to collide
				atomic.AddInt64(&inFlight, -1)
			})
		}()
	}
	wg.Wait()

	got := int(atomic.LoadInt64(&peak))
	if got > limit {
		t.Errorf("%d derivations ran at once against a ceiling of %d.\n\n"+
			"Each one is 64 MiB and every core; the whole point of the bound is that the "+
			"worst case is a number the operator can reason about rather than a function of "+
			"how many requests arrived.", got, limit)
	}
	if got < 2 {
		t.Errorf("peak concurrency was %d — the calls never overlapped, so this run proves "+
			"nothing about the ceiling", got)
	}
}

// TestVerifyGoesThroughTheCeiling is the mutation-killer, and it exists because
// the test it replaces did not. A ceiling that is implemented but not APPLIED to
// the derivation bounds nothing, and no assertion about wall-clock or observed
// parallelism can tell the difference. Watching the slot table can: if
// VerifySecretArgon2id stops acquiring, the table stays empty while work runs.
func TestVerifyGoesThroughTheCeiling(t *testing.T) {
	hash, err := HashSecretArgon2id("a secret worth hashing")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	stop := make(chan struct{})
	var sawHeld int64
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if len(argonSlots) > 0 {
				atomic.StoreInt64(&sawHeld, 1)
				return
			}
			time.Sleep(200 * time.Microsecond)
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < cap(argonSlots)*4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			VerifySecretArgon2id("a secret worth hashing", hash)
		}()
	}
	wg.Wait()
	close(stop)

	if atomic.LoadInt64(&sawHeld) == 0 {
		t.Error("dozens of concurrent verifications ran and not one slot was ever held.\n\n" +
			"VerifySecretArgon2id is not going through the ceiling, so the bound exists in the " +
			"source and nowhere in the running program — which is the exact shape of defect " +
			"this repository keeps paying for: a claim rather than a control.")
	}
}

// TestHashGoesThroughTheCeiling — the same, for the write side. A password change
// costs the same 64 MiB as a sign-in, and counting only one of them would leave
// the bound describing half the work.
func TestHashGoesThroughTheCeiling(t *testing.T) {
	stop := make(chan struct{})
	var sawHeld int64
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if len(argonSlots) > 0 {
				atomic.StoreInt64(&sawHeld, 1)
				return
			}
			time.Sleep(200 * time.Microsecond)
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < cap(argonSlots)*4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = HashSecretArgon2id("another secret")
		}()
	}
	wg.Wait()
	close(stop)

	if atomic.LoadInt64(&sawHeld) == 0 {
		t.Error("concurrent hashing never held a slot; the ceiling does not cover the write side")
	}
}

// The controls. A bound that breaks correctness, or that serialises every
// sign-in, is worse than the flood it prevents.
func TestTheCeilingLeavesVerificationCorrect(t *testing.T) {
	hash, err := HashSecretArgon2id("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	var wg sync.WaitGroup
	problems := make(chan string, 128)
	for i := 0; i < cap(argonSlots)*4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !VerifySecretArgon2id("correct horse battery staple", hash) {
				problems <- "a CORRECT secret failed to verify under load — sign-in breaks exactly when the install is busy"
			}
			if VerifySecretArgon2id("wrong", hash) {
				problems <- "a WRONG secret verified — the ceiling is returning the wrong answer, not merely a slow one"
			}
		}()
	}
	wg.Wait()
	close(problems)
	for msg := range problems {
		t.Error(msg)
	}
}

func TestTheCeilingIsNotOne(t *testing.T) {
	if n := ArgonConcurrencyLimit(); n < 2 {
		t.Errorf("ceiling = %d; a bound below 2 serialises every credential check on the "+
			"install behind one derivation, which is an outage rather than a limit", n)
	}
}
