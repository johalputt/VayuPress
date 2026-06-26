package kernel

import (
	"context"
	"errors"
	"testing"
)

func TestEventBusDispatch(t *testing.T) {
	t.Parallel()
	b := NewBus()
	var gotUser string
	var otherCalled bool
	b.Subscribe(UserCreated{}, func(_ context.Context, ev Event) {
		gotUser = ev.(UserCreated).Email
	})
	b.Subscribe(DomainAdded{}, func(_ context.Context, _ Event) {
		otherCalled = true
	})
	b.Publish(context.Background(), UserCreated{UserID: "1", Email: "a@b.com"})
	if gotUser != "a@b.com" {
		t.Fatalf("handler not invoked, got %q", gotUser)
	}
	if otherCalled {
		t.Fatalf("unrelated handler must not fire")
	}
}

func TestEventBusMultipleHandlersInOrder(t *testing.T) {
	t.Parallel()
	b := NewBus()
	var order []int
	b.Subscribe(UserCreated{}, func(_ context.Context, _ Event) { order = append(order, 1) })
	b.Subscribe(UserCreated{}, func(_ context.Context, _ Event) { order = append(order, 2) })
	b.Publish(context.Background(), UserCreated{})
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatalf("handlers not in order: %v", order)
	}
}

type fakeSub struct {
	name string
	err  error
}

func (f fakeSub) Name() string                  { return f.name }
func (f fakeSub) Start(_ context.Context) error { return f.err }

func TestBootCriticalAborts(t *testing.T) {
	t.Parallel()
	steps := []Step{
		{Sub: fakeSub{name: "ok"}, Critical: true},
		{Sub: fakeSub{name: "bad", err: errors.New("boom")}, Critical: true},
		{Sub: fakeSub{name: "never"}, Critical: true},
	}
	res, err := Boot(context.Background(), steps, nil)
	if err == nil {
		t.Fatalf("expected critical failure")
	}
	if len(res) != 2 {
		t.Fatalf("boot should stop at failing critical step, got %d results", len(res))
	}
}

func TestBootDegradedContinues(t *testing.T) {
	t.Parallel()
	steps := []Step{
		{Sub: fakeSub{name: "ok"}, Critical: true},
		{Sub: fakeSub{name: "soft", err: errors.New("meh")}, Critical: false},
		{Sub: fakeSub{name: "after"}, Critical: true},
	}
	res, err := Boot(context.Background(), steps, nil)
	if err != nil {
		t.Fatalf("degraded failure must not abort: %v", err)
	}
	if len(res) != 3 || res[2].Name != "after" || !res[2].Started {
		t.Fatalf("boot did not continue past degraded step: %+v", res)
	}
}

func TestHealthMonitor(t *testing.T) {
	t.Parallel()
	h := NewHealthMonitor()
	h.Register("a", func() (bool, string) { return true, "fine" })
	h.Register("b", func() (bool, string) { return false, "down" })
	snap := h.Snapshot()
	if snap.OK {
		t.Fatalf("overall should be unhealthy when one check fails")
	}
	if len(snap.Components) != 2 {
		t.Fatalf("expected 2 components, got %d", len(snap.Components))
	}
}
