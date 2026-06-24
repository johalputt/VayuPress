// Package kernel — subsystem boot orchestration.
//
// Handles ordered startup and shutdown of all VayuOS subsystems:
// VayuPGP → VayuTLS → VayuMail → DNS → VayuOS Panel.
package kernel

import (
	"context"
	"fmt"
)

// Subsystem is a named component that can be started and stopped.
type Subsystem interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// BootOrchestrator manages ordered subsystem lifecycle.
type BootOrchestrator struct {
	subsystems []Subsystem
}

func NewBootOrchestrator() *BootOrchestrator {
	return &BootOrchestrator{}
}

func (o *BootOrchestrator) Register(s Subsystem) {
	o.subsystems = append(o.subsystems, s)
}

func (o *BootOrchestrator) Start(ctx context.Context) error {
	for _, s := range o.subsystems {
		if err := s.Start(ctx); err != nil {
			return fmt.Errorf("subsystem %s: %w", s.Name(), err)
		}
	}
	return nil
}

func (o *BootOrchestrator) Stop(ctx context.Context) error {
	for i := len(o.subsystems) - 1; i >= 0; i-- {
		if err := o.subsystems[i].Stop(ctx); err != nil {
			return fmt.Errorf("subsystem %s: %w", o.subsystems[i].Name(), err)
		}
	}
	return nil
}