// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"runtime"
	"time"
)

// A process-wide ceiling on concurrent Argon2id work.
//
// AUDIT FINDING (Section 1). Argon2id here runs at 64 MiB, t=3, 4 threads, and
// the anti-enumeration decoy forces a full derivation even for addresses that do
// not exist — so an unauthenticated request that fails is exactly as expensive
// as one that succeeds. Nothing bounded how many could run at once. A few
// hundred concurrent POSTs to any credential endpoint is a few hundred × 64 MiB
// of live allocation plus every core saturated, which takes the host down
// without a single valid password.
//
// The per-mailbox throttles that exist are `time.Sleep` calls. A sleep delays
// one caller; it does not bound a thousand of them arriving together, and each
// sleeping goroutine still holds its request. Rate limits are per IP, and the
// attacker chooses how many IPs to be. Neither is a ceiling, so this is one.
//
// WHY A CEILING AND NOT A REJECTION. Blocking is the behaviour that keeps the
// install correct under load: work queues instead of failing, and a legitimate
// sign-in during a burst is slow rather than refused. Refusing would make the
// flood succeed at denying service to everyone else, which is the outcome being
// prevented.
//
// The deadline exists so the queue cannot grow without limit. A caller that has
// waited this long is behind a queue nothing will drain in time, and its own
// client has almost certainly given up; returning at that point sheds the load
// rather than holding a goroutine and a request buffer for it. It is set far
// above any honest wait — a full slot table at ~100 ms per derivation drains
// many times over inside the window — so reaching it means the box is already
// under a flood.
var (
	argonSlots   = make(chan struct{}, argonCeiling())
	argonMaxWait = 15 * time.Second
)

// argonCeiling sizes the slot table. Argon2id already parallelises internally
// across argonThreads, so running one derivation per core on top of that
// oversubscribes badly; half the cores, floored at 2, keeps a multi-core host
// responsive while still hashing far faster than any real sign-in rate needs.
// The memory ceiling follows from it: slots × 64 MiB, which is what makes the
// worst case a number an operator can reason about instead of a function of how
// many requests arrived.
func argonCeiling() int {
	n := runtime.NumCPU() / 2
	if n < 2 {
		n = 2
	}
	if n > 8 {
		n = 8
	}
	return n
}

// withArgonSlot runs fn holding one slot. It reports false when the wait
// deadline passed without a slot, in which case fn did NOT run — every caller
// treats that as a failed verification, which is the safe direction: under a
// flood the answer is "no", never "yes".
func withArgonSlot(fn func()) bool {
	timer := time.NewTimer(argonMaxWait)
	defer timer.Stop()
	select {
	case argonSlots <- struct{}{}:
		defer func() { <-argonSlots }()
		fn()
		return true
	case <-timer.C:
		return false
	}
}

// ArgonConcurrencyLimit reports the ceiling, so a posture page can state the
// bound rather than assert that one exists.
func ArgonConcurrencyLimit() int { return cap(argonSlots) }
