// Package fault provides a fault injection framework for adversarial and
// crash-consistency testing. Faults are named, probabilistic, and scoped to
// test builds via build tags or explicit activation — they are no-ops in
// production unless explicitly enabled.
package fault

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
)

// Kind classifies the type of fault.
type Kind string

const (
	KindError   Kind = "error"   // return an error from the injection point
	KindPanic   Kind = "panic"   // panic at the injection point (tests recover)
	KindLatency Kind = "latency" // block for a configurable duration (not yet implemented)
)

// Fault is a named injection point with a trigger probability and optional error.
type Fault struct {
	Name         string
	Kind         Kind
	Probability  float64 // 0.0 = never, 1.0 = always
	Err          error   // returned when Kind==KindError and fault fires
	TriggerCount atomic.Int64
}

// Injector holds a registry of named faults.
// All methods are safe for concurrent use.
type Injector struct {
	mu     sync.RWMutex
	faults map[string]*Fault
	rng    *rand.Rand
	seed   int64
}

// New returns an Injector with a fixed seed for deterministic tests.
func New(seed int64) *Injector {
	return &Injector{
		faults: make(map[string]*Fault),
		rng:    rand.New(rand.NewSource(seed)), //nolint:gosec
		seed:   seed,
	}
}

// Global is the process-wide injector. Disabled (empty) by default.
var Global = New(0)

// Register adds a fault to the injector. Panics on duplicate name.
func (inj *Injector) Register(f *Fault) {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	if _, exists := inj.faults[f.Name]; exists {
		panic(fmt.Sprintf("fault: duplicate name %q", f.Name))
	}
	inj.faults[f.Name] = f
}

// SetProbability adjusts the trigger probability for a named fault.
// No-op if the fault is not registered.
func (inj *Injector) SetProbability(name string, p float64) {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	if f, ok := inj.faults[name]; ok {
		f.Probability = p
	}
}

// Check evaluates the named fault. Returns a non-nil error if the fault fires
// and its Kind is KindError. Returns nil if the fault is not registered or does
// not trigger. Panics if the fault fires and its Kind is KindPanic.
func (inj *Injector) Check(name string) error {
	inj.mu.RLock()
	f, ok := inj.faults[name]
	inj.mu.RUnlock()
	if !ok || f.Probability == 0 {
		return nil
	}

	inj.mu.Lock()
	roll := inj.rng.Float64() //nolint:gosec
	inj.mu.Unlock()

	if roll >= f.Probability {
		return nil
	}

	f.TriggerCount.Add(1)

	switch f.Kind {
	case KindPanic:
		panic(fmt.Sprintf("fault injection: %s fired (panic)", name))
	case KindError:
		if f.Err != nil {
			return f.Err
		}
		return fmt.Errorf("fault injection: %s fired", name)
	}
	return nil
}

// Trigger manually fires the named fault point, incrementing its counter and
// registering it on first use. Unlike Check it is independent of probability —
// it exists for operator-driven fault simulation from the admin Fault Engine.
// Returns the new trigger count.
func (inj *Injector) Trigger(name string) int64 {
	inj.mu.Lock()
	f, ok := inj.faults[name]
	if !ok {
		f = &Fault{Name: name, Kind: KindError, Probability: 0}
		inj.faults[name] = f
	}
	inj.mu.Unlock()
	return f.TriggerCount.Add(1)
}

// TriggerCount returns how many times the named fault has fired. Returns 0 if
// the fault is not registered.
func (inj *Injector) TriggerCount(name string) int64 {
	inj.mu.RLock()
	f, ok := inj.faults[name]
	inj.mu.RUnlock()
	if !ok {
		return 0
	}
	return f.TriggerCount.Load()
}

// Reset clears all faults — intended for test teardown.
func (inj *Injector) Reset() {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	inj.faults = make(map[string]*Fault)
	inj.rng = rand.New(rand.NewSource(inj.seed)) //nolint:gosec
}

// Named fault points used across the platform.
const (
	FaultWALWrite          = "db.wal.write"
	FaultMigrationApply    = "migrations.apply"
	FaultSigningSign       = "signing.sign"
	FaultFederationDeliver = "federation.deliver"
	FaultPluginInvoke      = "sandbox.plugin.invoke"
	FaultOutboxCommit      = "outbox.commit"
)
