package slo_test

import (
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/slo"
)

func TestBudgetFullOnEmpty(t *testing.T) {
	tr := slo.NewTracker(slo.SLO{Name: "test", Target: 0.99, Window: time.Hour})
	if got := tr.Budget(); got != 1.0 {
		t.Errorf("empty tracker budget = %f, want 1.0", got)
	}
}

func TestBudgetExhaustedAfterFailures(t *testing.T) {
	tr := slo.NewTracker(slo.SLO{Name: "test", Target: 0.99, Window: time.Hour})
	// 100 events, all bad → budget exhausted.
	for i := 0; i < 100; i++ {
		tr.Record(false)
	}
	if !tr.BudgetExhausted() {
		t.Error("expected budget exhausted after 100 failures")
	}
}

func TestBudgetFullAfterAllSuccess(t *testing.T) {
	tr := slo.NewTracker(slo.SLO{Name: "test", Target: 0.99, Window: time.Hour})
	for i := 0; i < 100; i++ {
		tr.Record(true)
	}
	if tr.BudgetExhausted() {
		t.Error("budget should not be exhausted after all successes")
	}
	if got := tr.Budget(); got <= 0 {
		t.Errorf("budget = %f, want > 0", got)
	}
}

func TestRegistryNoDuplicates(t *testing.T) {
	r := slo.NewRegistry()
	r.Register(slo.SLO{Name: "unique", Target: 0.999, Window: time.Hour})
	defer func() {
		if rec := recover(); rec == nil {
			t.Error("expected panic on duplicate SLO registration")
		}
	}()
	r.Register(slo.SLO{Name: "unique", Target: 0.999, Window: time.Hour})
}

func TestRegistrySnapshot(t *testing.T) {
	r := slo.NewRegistry()
	r.Register(slo.SLO{Name: "slo.a", Target: 0.99, Window: time.Hour})
	r.Register(slo.SLO{Name: "slo.b", Target: 0.999, Window: time.Hour})
	snaps := r.Snapshot()
	if len(snaps) != 2 {
		t.Errorf("expected 2 snapshots, got %d", len(snaps))
	}
}
