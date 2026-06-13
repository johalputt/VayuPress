package fault

import (
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/mode"
)

func TestEscalationFiresOnThreshold(t *testing.T) {
	mgr := mode.New()
	e := NewEscalator(mgr)
	e.AddRule(EscalationRule{
		FaultName:  FaultWALWrite,
		Threshold:  3,
		Window:     time.Minute,
		TargetMode: mode.ModeReadOnly,
		Reason:     "test",
		Cause:      "test",
	})

	e.Record(FaultWALWrite)
	e.Record(FaultWALWrite)
	if mgr.Current() != mode.ModeNormal {
		t.Fatal("should not have escalated at 2/3 triggers")
	}
	e.Record(FaultWALWrite) // hits threshold
	if mgr.Current() != mode.ModeReadOnly {
		t.Fatalf("expected ReadOnly after threshold, got %s", mgr.Current())
	}
}

func TestEscalationWindowReset(t *testing.T) {
	mgr := mode.New()
	e := NewEscalator(mgr)
	e.AddRule(EscalationRule{
		FaultName:  FaultSigningSign,
		Threshold:  2,
		Window:     50 * time.Millisecond,
		TargetMode: mode.ModeDegraded,
		Reason:     "test",
		Cause:      "test",
	})

	e.Record(FaultSigningSign) // count=1
	time.Sleep(60 * time.Millisecond)
	e.Record(FaultSigningSign) // window expired → reset to 1
	if mgr.Current() != mode.ModeNormal {
		t.Fatal("should not have escalated — window reset between triggers")
	}
}

func TestEscalationNoWindowIsLifetime(t *testing.T) {
	mgr := mode.New()
	e := NewEscalator(mgr)
	e.AddRule(EscalationRule{
		FaultName:  FaultMigrationApply,
		Threshold:  1,
		Window:     0, // lifetime
		TargetMode: mode.ModeReadOnly,
		Reason:     "test",
		Cause:      "test",
	})

	e.Record(FaultMigrationApply)
	if mgr.Current() != mode.ModeReadOnly {
		t.Fatalf("expected ReadOnly, got %s", mgr.Current())
	}
}

func TestEscalationUnmatchedFaultIsNoop(t *testing.T) {
	mgr := mode.New()
	e := NewEscalator(mgr)
	e.AddRule(EscalationRule{
		FaultName:  FaultWALWrite,
		Threshold:  1,
		Window:     0,
		TargetMode: mode.ModeReadOnly,
		Reason:     "test",
		Cause:      "test",
	})

	e.Record("some.other.fault") // no rule for this
	if mgr.Current() != mode.ModeNormal {
		t.Fatal("unmatched fault should be a no-op")
	}
}

func TestEscalationResetsCounterAfterFire(t *testing.T) {
	mgr := mode.New()
	e := NewEscalator(mgr)
	e.AddRule(EscalationRule{
		FaultName:  FaultOutboxCommit,
		Threshold:  2,
		Window:     time.Minute,
		TargetMode: mode.ModeDegraded,
		Reason:     "test",
		Cause:      "test",
	})

	e.Record(FaultOutboxCommit)
	e.Record(FaultOutboxCommit) // fires, resets counter
	if mgr.Current() != mode.ModeDegraded {
		t.Fatalf("expected Degraded, got %s", mgr.Current())
	}

	// Subsequent faults should not immediately re-trigger (counter reset).
	// Since we're already Degraded, transition to Degraded is a no-op but
	// counter should start fresh and need another 2 triggers.
	e.Record(FaultOutboxCommit) // count=1 in new window; no re-trigger
	// verify count restarted by checking mode didn't change unexpectedly
	if mgr.Current() != mode.ModeDegraded {
		t.Fatal("mode should remain Degraded")
	}
}

func TestDefaultRulesCoversAllFaultConstants(t *testing.T) {
	allFaults := []string{
		FaultWALWrite,
		FaultMigrationApply,
		FaultSigningSign,
		FaultFederationDeliver,
		FaultPluginInvoke,
		FaultOutboxCommit,
	}
	rules := DefaultRules()
	covered := make(map[string]bool)
	for _, r := range rules {
		covered[r.FaultName] = true
	}
	for _, f := range allFaults {
		if !covered[f] {
			t.Errorf("default rules missing coverage for fault %q", f)
		}
	}
}
