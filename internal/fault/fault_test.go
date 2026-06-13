package fault

import (
	"errors"
	"testing"
)

func TestCheckUnregisteredIsNil(t *testing.T) {
	inj := New(42)
	if err := inj.Check("nonexistent"); err != nil {
		t.Errorf("unregistered fault should return nil, got %v", err)
	}
}

func TestCheckZeroProbabilityNeverFires(t *testing.T) {
	inj := New(42)
	inj.Register(&Fault{Name: "never", Kind: KindError, Probability: 0.0})
	for i := 0; i < 1000; i++ {
		if err := inj.Check("never"); err != nil {
			t.Fatalf("zero probability fault fired at iteration %d", i)
		}
	}
}

func TestCheckAlwaysFires(t *testing.T) {
	inj := New(42)
	sentinel := errors.New("sentinel")
	inj.Register(&Fault{Name: "always", Kind: KindError, Probability: 1.0, Err: sentinel})
	err := inj.Check("always")
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

func TestTriggerCountAccumulates(t *testing.T) {
	inj := New(42)
	inj.Register(&Fault{Name: "counter", Kind: KindError, Probability: 1.0})
	for i := 0; i < 5; i++ {
		inj.Check("counter") //nolint:errcheck
	}
	if got := inj.TriggerCount("counter"); got != 5 {
		t.Errorf("expected 5 triggers, got %d", got)
	}
}

func TestPanicKindPanics(t *testing.T) {
	inj := New(42)
	inj.Register(&Fault{Name: "boom", Kind: KindPanic, Probability: 1.0})
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic, got none")
		}
	}()
	inj.Check("boom") //nolint:errcheck
}

func TestDuplicateNamePanics(t *testing.T) {
	inj := New(42)
	inj.Register(&Fault{Name: "dup", Kind: KindError, Probability: 0})
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	inj.Register(&Fault{Name: "dup", Kind: KindError, Probability: 0})
}

func TestSetProbability(t *testing.T) {
	inj := New(42)
	inj.Register(&Fault{Name: "adj", Kind: KindError, Probability: 0.0})
	if err := inj.Check("adj"); err != nil {
		t.Fatal("should not fire at 0.0")
	}
	inj.SetProbability("adj", 1.0)
	if err := inj.Check("adj"); err == nil {
		t.Error("should fire at 1.0")
	}
}

func TestReset(t *testing.T) {
	inj := New(42)
	inj.Register(&Fault{Name: "tmp", Kind: KindError, Probability: 1.0})
	inj.Reset()
	if err := inj.Check("tmp"); err != nil {
		t.Error("fault should not exist after reset")
	}
	if inj.TriggerCount("tmp") != 0 {
		t.Error("trigger count should be 0 after reset")
	}
}
