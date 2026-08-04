// SPDX-License-Identifier: Apache-2.0

package vayutor

// vanity_order_test.go — "found" must mean the identity is DURABLE.
//
// THE BUG THIS PINS, which reached CI as a one-in-many flake in the race job:
//
//	--- FAIL: TestVanitySearchFindsAndApplies
//	    persisted address "" != found "amdzqz…yd.onion"
//
// The winning worker published found, and only then persisted:
//
//	vs.found.Store(true); vs.done.Store(true); e.applyVanity(...)
//
// VanityStatus().Found reads that flag, so between the two lines a status read
// saw a completed search whose key was nowhere. Everything downstream treats
// found as "this address exists and survives a restart" — the panel says the
// vanity onion is ready, and CancelVanity's own comment asserted it was
// "persisted the moment it was found". That was the one claim that was untrue,
// and a cancel or a crash in that window discarded a key that had cost a long
// search on a machine's CPU.
//
// The existing test can only catch it by losing a race, which is why it took
// until CI to appear and why re-breaking the code does not reliably fail it.
// This one does not race: it holds the store open and asserts the announcement
// has NOT happened while the write is still in flight.

import (
	"context"
	"testing"
	"time"
)

// blockingStore wraps memStore and holds SaveOnion open until released, so the
// window between "found" and "persisted" can be inspected rather than raced for.
type blockingStore struct {
	*memStore
	entered chan struct{}
	release chan struct{}
}

func (b *blockingStore) SaveOnion(ctx context.Context, rec OnionRecord) error {
	close(b.entered)
	<-b.release
	return b.memStore.SaveOnion(ctx, rec)
}

func TestVanityIsNotAnnouncedBeforeItIsPersisted(t *testing.T) {
	base := newMemStore()
	store := &blockingStore{
		memStore: base,
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	e := NewEngine(Config{Enabled: true, Store: store})

	// StartVanity refuses a domain with no live onion, so seed one — the search
	// replaces an existing random address with a vanity one.
	e.mu.Lock()
	e.onionByHost["blog.in"] = "oldrandomaddressxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx.onion"
	e.hostByOnion["oldrandomaddressxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx.onion"] = "blog.in"
	e.mu.Unlock()

	// A 1-char prefix lands in ~32 tries, so the search reaches the save quickly.
	if err := e.StartVanity("blog.in", "a"); err != nil {
		t.Fatalf("StartVanity: %v", err)
	}

	select {
	case <-store.entered:
	case <-time.After(20 * time.Second):
		t.Fatal("the search never reached the persistence step")
	}

	// THE ASSERTION. The write is in flight and has not returned. Nothing may be
	// telling anyone the identity is ready, because right now it is not: kill the
	// process here and the key is gone.
	if st := e.VanityStatus(); st.Found {
		t.Fatal("the search reported Found while the key was still being written. " +
			"Everything downstream reads Found as 'this address exists and will " +
			"survive a restart' — a cancel or a crash in this window loses a key " +
			"that cost a long search")
	}

	close(store.release)

	deadline := time.Now().Add(20 * time.Second)
	for {
		st := e.VanityStatus()
		if st.Found {
			// And once announced, the store must genuinely hold it — the whole
			// point of the ordering.
			recs, _ := base.LoadOnions(context.Background())
			for _, r := range recs {
				if r.Host == "blog.in" && r.Address == st.Address {
					return
				}
			}
			t.Fatalf("Found was announced but the store does not hold %q", st.Address)
		}
		if time.Now().After(deadline) {
			t.Fatal("the search never announced a result after the write completed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
