// SPDX-License-Identifier: Apache-2.0

package vayutalk

// web_cursor_test.go — the per-reader cursor (ADR-0142 follow-on).
//
// The web console and the phone app stream the SAME identity's queue. Before the
// cursor, the console had only two options and both were wrong: ack on display
// (which deleted the queued copy the app had not yet received) or never ack
// (which meant every reconnect re-flushed messages the user had already watched
// burn — "disappears when read" was not true in the browser). These tests pin the
// third option: the console keeps its own cursor, and the app's copy is untouched.

import "testing"

func TestWebCursorStopsReflushWithoutDestroyingTheAppsCopy(t *testing.T) {
	e := fakeEngine(t, true)
	const alice, bob = "alice@x", "bob@x"

	// Bob sends while Alice is offline, so it waits in her queue.
	id, _, queued, err := e.Send(bob, alice, []byte("ct"), 300, "store")
	if err != nil || !queued {
		t.Fatalf("send: queued=%v err=%v", queued, err)
	}
	if id == "" {
		t.Fatal("no envelope id")
	}

	// The app stream sees the pending envelope (unchanged behaviour).
	appQ, _, cancelApp, err := e.Subscribe(alice)
	if err != nil {
		t.Fatalf("app subscribe: %v", err)
	}
	defer cancelApp()
	if len(appQ) != 1 {
		t.Fatalf("app should see the pending envelope, got %d", len(appQ))
	}

	// The console sees it too, and records that it has shown it.
	webQ, _, cancelWeb, err := e.SubscribeWeb(alice)
	if err != nil {
		t.Fatalf("web subscribe: %v", err)
	}
	defer cancelWeb()
	if len(webQ) != 1 {
		t.Fatalf("console should see the pending envelope first, got %d", len(webQ))
	}
	if !e.MarkWebReadAs(id, alice) {
		t.Fatal("the recipient must be able to set her own read cursor")
	}
	// Idempotent: a second signal is still a success and changes nothing.
	if !e.MarkWebReadAs(id, alice) {
		t.Error("setting the cursor twice must be harmless")
	}

	// Reconnect: the console must NOT be re-sent a message it already displayed.
	webAgain, _, cancelWeb2, err := e.SubscribeWeb(alice)
	if err != nil {
		t.Fatalf("web resubscribe: %v", err)
	}
	defer cancelWeb2()
	if len(webAgain) != 0 {
		t.Fatalf("console was re-sent %d already-displayed message(s) — a burned message must not resurrect", len(webAgain))
	}

	// …while the app still receives its copy, because the cursor destroys nothing.
	appAgain, _, cancelApp2, err := e.Subscribe(alice)
	if err != nil {
		t.Fatalf("app resubscribe: %v", err)
	}
	defer cancelApp2()
	if len(appAgain) != 1 || appAgain[0].ID != id {
		t.Fatalf("the web cursor stole the app's copy: app got %d envelope(s)", len(appAgain))
	}

	// The app remains the authoritative reader: its ack is what ends the message.
	if !e.AckAs(id, alice) {
		t.Fatal("the app ack must still read-destroy the envelope")
	}
	final, _, cancelFinal, err := e.Subscribe(alice)
	if err != nil {
		t.Fatalf("final subscribe: %v", err)
	}
	defer cancelFinal()
	if len(final) != 0 {
		t.Fatalf("after the app acked, nothing should remain; got %d", len(final))
	}
}

func TestWebCursorIsOwnershipChecked(t *testing.T) {
	e := fakeEngine(t, true)
	const alice, bob, mallory = "alice@x", "bob@x", "mallory@x"

	id, _, _, err := e.Send(bob, alice, []byte("ct"), 300, "store")
	if err != nil {
		t.Fatal(err)
	}
	// Someone else's id must not let a third party silence Alice's notification.
	if e.MarkWebReadAs(id, mallory) {
		t.Fatal("a non-recipient must not be able to set another mailbox's read cursor")
	}
	if e.MarkWebReadAs("not-a-real-id", alice) {
		t.Error("an unknown id must not report success")
	}
	if e.MarkWebReadAs("", alice) || e.MarkWebReadAs(id, "") {
		t.Error("empty id or claimant must be refused")
	}
	// And the envelope is still pending for its real recipient.
	web, _, cancel, err := e.SubscribeWeb(alice)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if len(web) != 1 {
		t.Fatalf("the envelope must still be pending after a refused cursor, got %d", len(web))
	}
}

func TestWebCursorOnALiveMessageIsHarmless(t *testing.T) {
	e := fakeEngine(t, true)
	const alice, bob = "alice@x", "bob@x"

	// A live-mode message is never stored (it is delivered only if the recipient
	// is online and is never queued), so there is no cursor to set. What matters
	// is that the call is harmless: no error path, and no phantom queue entry.
	id, _, _, err := e.Send(bob, alice, []byte("ct"), 300, "live")
	if err != nil {
		t.Fatal(err)
	}
	if e.MarkWebReadAs(id, alice) {
		t.Error("a live envelope is never stored, so there should be nothing to mark")
	}
	for _, reader := range []struct {
		name string
		get  func(string) ([]*Envelope, <-chan Event, func(), error)
	}{{"web", e.SubscribeWeb}, {"app", e.Subscribe}} {
		queued, _, cancel, err := reader.get(alice)
		if err != nil {
			t.Fatalf("%s subscribe: %v", reader.name, err)
		}
		if len(queued) != 0 {
			t.Fatalf("%s reader: a live message must never appear in a queue, got %d", reader.name, len(queued))
		}
		cancel()
	}
}
