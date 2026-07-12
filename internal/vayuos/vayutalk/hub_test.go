package vayutalk

import (
	"testing"
	"time"
)

func recvEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}
	}
}

func TestSubscribePerUserCapEvictsOldest(t *testing.T) {
	h := NewHub()
	chans := make([]<-chan Event, 0, MaxStreamsPerUser)
	cancels := make([]func(), 0, MaxStreamsPerUser)
	for i := 0; i < MaxStreamsPerUser; i++ {
		ch, cancel, err := h.Subscribe("b@x")
		if err != nil {
			t.Fatalf("subscribe %d: %v", i, err)
		}
		chans = append(chans, ch)
		cancels = append(cancels, cancel)
	}
	// One more must SUCCEED (not be rejected) by evicting the oldest — so a
	// client whose stream keeps dropping always reconnects instead of spiralling.
	_, extraCancel, err := h.Subscribe("b@x")
	if err != nil {
		t.Fatalf("over-cap subscribe should evict, not reject: %v", err)
	}
	// The oldest subscriber's channel is closed, which is how its handler learns
	// to stop.
	if _, open := <-chans[0]; open {
		t.Fatalf("oldest stream should have been closed on eviction")
	}
	// The user is still at the cap (evict + add), and a different user is fine.
	_, c, err := h.Subscribe("c@x")
	if err != nil {
		t.Fatalf("other user rejected: %v", err)
	}
	c()
	extraCancel()
	for _, c := range cancels[1:] {
		c()
	}
}

func TestSubscribeGlobalCap(t *testing.T) {
	h := NewHub()
	var cancels []func()
	// Fill to the global cap using distinct users (well under per-user cap each).
	for i := 0; i < MaxGlobalStreams; i++ {
		user := "u" + string(rune('a'+i%26)) + "-" + itoa(i) + "@x"
		_, cancel, err := h.Subscribe(user)
		if err != nil {
			t.Fatalf("subscribe %d: %v", i, err)
		}
		cancels = append(cancels, cancel)
	}
	if _, _, err := h.Subscribe("fresh@x"); err != ErrGlobalStreamLimit {
		t.Fatalf("over-global err = %v, want ErrGlobalStreamLimit", err)
	}
	for _, c := range cancels {
		c()
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestPublishLiveDelivery(t *testing.T) {
	h := NewHub()
	ch, cancel, err := h.Subscribe("b@x")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	env := &Envelope{ID: "id1", From: "a@x", To: "b@x", Ciphertext: []byte("z"), CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Mode: "live"}
	if !h.Publish(env) {
		t.Fatal("Publish reported not delivered to a live subscriber")
	}
	evt := recvEvent(t, ch)
	if evt.Type != "envelope" {
		t.Fatalf("event type = %q, want envelope", evt.Type)
	}
	p, ok := evt.Payload.(EnvelopePayload)
	if !ok || p.ID != "id1" || p.From != "a@x" || p.Mode != "live" {
		t.Fatalf("payload = %+v", evt.Payload)
	}
}

func TestPublishNoSubscriber(t *testing.T) {
	h := NewHub()
	env := &Envelope{ID: "id", To: "offline@x", ExpiresAt: time.Now().Add(time.Minute)}
	if h.Publish(env) {
		t.Fatal("Publish to offline user reported delivered")
	}
	if h.Online("offline@x") {
		t.Fatal("Online reported true with no subscribers")
	}
}

func TestReceiptFanOut(t *testing.T) {
	h := NewHub()
	ch1, c1, _ := h.Subscribe("a@x")
	ch2, c2, _ := h.Subscribe("a@x")
	defer c1()
	defer c2()
	h.PublishReceipt("a@x", "mid", "read")
	for i, ch := range []<-chan Event{ch1, ch2} {
		evt := recvEvent(t, ch)
		p, ok := evt.Payload.(ReceiptPayload)
		if evt.Type != "receipt" || !ok || p.ID != "mid" || p.Status != "read" {
			t.Fatalf("subscriber %d got %+v", i, evt)
		}
	}
}

func TestCancelStopsDelivery(t *testing.T) {
	h := NewHub()
	_, cancel, err := h.Subscribe("b@x")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if h.Online("b@x") {
		t.Fatal("Online true after cancel")
	}
	env := &Envelope{ID: "x", To: "b@x", ExpiresAt: time.Now().Add(time.Minute)}
	if h.Publish(env) {
		t.Fatal("delivered to a cancelled subscriber")
	}
	// cancel is idempotent.
	cancel()
}
