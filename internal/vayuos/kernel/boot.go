package kernel

import (
	"context"
	"fmt"
)

// Subsystem is anything VayuOS boots in order.
type Subsystem interface {
	Name() string
	Start(ctx context.Context) error
}

// Step is one entry in the boot sequence. Critical steps abort boot on failure;
// non-critical steps degrade gracefully (logged, boot continues).
type Step struct {
	Sub      Subsystem
	Critical bool
}

// BootResult records the outcome of a single step.
type BootResult struct {
	Name     string
	Started  bool
	Critical bool
	Err      error
}

// Boot starts each step in order. It stops and returns an error as soon as a
// critical step fails; non-critical failures are collected but do not abort.
// The log callback (may be nil) receives a human-readable line per step.
func Boot(ctx context.Context, steps []Step, log func(string)) ([]BootResult, error) {
	results := make([]BootResult, 0, len(steps))
	for _, s := range steps {
		err := s.Sub.Start(ctx)
		res := BootResult{Name: s.Sub.Name(), Started: err == nil, Critical: s.Critical, Err: err}
		results = append(results, res)
		if log != nil {
			if err == nil {
				log(fmt.Sprintf("VayuOS: %s started", s.Sub.Name()))
			} else if s.Critical {
				log(fmt.Sprintf("VayuOS: %s FAILED (critical): %v", s.Sub.Name(), err))
			} else {
				log(fmt.Sprintf("VayuOS: %s unavailable (degraded): %v", s.Sub.Name(), err))
			}
		}
		if err != nil && s.Critical {
			return results, fmt.Errorf("vayuos: critical subsystem %s failed: %w", s.Sub.Name(), err)
		}
	}
	return results, nil
}
