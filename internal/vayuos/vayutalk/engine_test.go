package vayutalk

import (
	"context"
	"testing"
	"time"
)

func fakeEngine(t *testing.T, ok bool) *Engine {
	t.Helper()
	e := NewEngine(Config{
		Enabled: true,
		Verify:  func(_ context.Context, _, _ string) bool { return ok },
		PubKey:  func(email string) (string, string, error) { return "ARMOR:" + email, "FPR", nil },
	})
	return e
}

func TestConnectAndAuthenticate(t *testing.T) {
	e := fakeEngine(t, true)
	token, ok := e.Connect(context.Background(), "a@x", "pw")
	if !ok || token == "" {
		t.Fatalf("connect ok=%v token=%q", ok, token)
	}
	email, ok := e.Authenticate(token)
	if !ok || email != "a@x" {
		t.Fatalf("authenticate = (%q,%v)", email, ok)
	}
	if _, ok := e.Authenticate("garbage"); ok {
		t.Fatal("garbage token authenticated")
	}
}

func TestConnectBadCredential(t *testing.T) {
	e := fakeEngine(t, false)
	if _, ok := e.Connect(context.Background(), "a@x", "pw"); ok {
		t.Fatal("connect succeeded with a rejecting verifier")
	}
}

func TestTokenExpiry(t *testing.T) {
	ts := newTokenStore()
	now := time.Unix(1000, 0)
	tok := ts.mint("a@x", now)
	if _, ok := ts.lookup(tok, now.Add(TokenTTL-time.Second)); !ok {
		t.Fatal("valid token rejected before expiry")
	}
	if _, ok := ts.lookup(tok, now.Add(TokenTTL+time.Second)); ok {
		t.Fatal("expired token accepted")
	}
	// Sweep removes it.
	ts.sweep(now.Add(TokenTTL + time.Second))
	if _, present := ts.m[tok]; present {
		t.Fatal("swept token still present")
	}
}

func TestSendLiveOfflineDropped(t *testing.T) {
	e := fakeEngine(t, true)
	id, delivered, queued, err := e.Send("a@x", "offline@x", []byte("ct"), 60, "live")
	if err != nil {
		t.Fatal(err)
	}
	if delivered || queued || id == "" {
		t.Fatalf("live offline: delivered=%v queued=%v id=%q", delivered, queued, id)
	}
	if e.store.Len() != 0 {
		t.Fatalf("live mode queued despite offline: len=%d", e.store.Len())
	}
}

func TestSendStoreOfflineQueued(t *testing.T) {
	e := fakeEngine(t, true)
	id, delivered, queued, err := e.Send("a@x", "offline@x", []byte("ct"), 60, "store")
	if err != nil {
		t.Fatal(err)
	}
	if delivered || !queued || id == "" {
		t.Fatalf("store offline: delivered=%v queued=%v", delivered, queued)
	}
	if e.store.Len() != 1 {
		t.Fatalf("store not queued: len=%d", e.store.Len())
	}
}

func TestSendDeliversLiveWhenOnline(t *testing.T) {
	e := fakeEngine(t, true)
	queued, ch, cancel, err := e.Subscribe("b@x")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if len(queued) != 0 {
		t.Fatalf("unexpected queued on fresh subscribe: %d", len(queued))
	}
	id, delivered, wasQueued, err := e.Send("a@x", "b@x", []byte("ct"), 60, "store")
	if err != nil {
		t.Fatal(err)
	}
	if !delivered || wasQueued {
		t.Fatalf("online store: delivered=%v queued=%v", delivered, wasQueued)
	}
	evt := recvEvent(t, ch)
	if evt.Type != "envelope" {
		t.Fatalf("event = %+v", evt)
	}
	_ = id
}

func TestAckEmitsReadReceiptToSender(t *testing.T) {
	e := fakeEngine(t, true)
	// Sender A is streaming; B is offline so the message is queued.
	_, senderCh, cancelA, err := e.Subscribe("a@x")
	if err != nil {
		t.Fatal(err)
	}
	defer cancelA()
	id, _, queued, err := e.Send("a@x", "b@x", []byte("ct"), 60, "store")
	if err != nil || !queued {
		t.Fatalf("send: queued=%v err=%v", queued, err)
	}
	// B comes online, drains the queue, acks.
	drained, _, cancelB, err := e.Subscribe("b@x")
	if err != nil {
		t.Fatal(err)
	}
	defer cancelB()
	if len(drained) != 1 || drained[0].ID != id {
		t.Fatalf("drain = %+v, want the queued envelope", drained)
	}
	e.Ack(id)
	evt := recvEvent(t, senderCh)
	p, ok := evt.Payload.(ReceiptPayload)
	if evt.Type != "receipt" || !ok || p.ID != id || p.Status != "read" {
		t.Fatalf("sender receipt = %+v", evt)
	}
}

func TestPurgeLoopEmitsExpiredReceipt(t *testing.T) {
	e := fakeEngine(t, true)
	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Stop(context.Background()) }()
	// Sender streaming; recipient offline -> queued with the minimum TTL. We
	// backdate expiry directly so the ~2s purge tick collects it promptly.
	_, senderCh, cancelA, err := e.Subscribe("a@x")
	if err != nil {
		t.Fatal(err)
	}
	defer cancelA()
	env, _ := NewEnvelope("a@x", "b@x", []byte("ct"), 60, "store", time.Now())
	env.ExpiresAt = time.Now().Add(-time.Second)
	if err := e.store.Enqueue(env); err != nil {
		t.Fatal(err)
	}
	select {
	case evt := <-senderCh:
		p, ok := evt.Payload.(ReceiptPayload)
		if evt.Type != "receipt" || !ok || p.ID != env.ID || p.Status != "expired" {
			t.Fatalf("expired receipt = %+v", evt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no expired receipt within 5s")
	}
}

func TestPubKey(t *testing.T) {
	e := fakeEngine(t, true)
	armored, fpr, err := e.PubKey("b@x")
	if err != nil || armored != "ARMOR:b@x" || fpr != "FPR" {
		t.Fatalf("PubKey = (%q,%q,%v)", armored, fpr, err)
	}
}

func TestDisabledEngine(t *testing.T) {
	e := NewEngine(Config{Enabled: false})
	if e.Enabled() {
		t.Fatal("disabled engine reports Enabled")
	}
	if err := e.Start(context.Background()); err == nil {
		t.Fatal("disabled Start returned nil error")
	}
	if _, ok := e.Connect(context.Background(), "a@x", "pw"); ok {
		t.Fatal("disabled Connect succeeded")
	}
}
