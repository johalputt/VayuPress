// Package lifecycle provides structured startup and shutdown orchestration (ADR-0051).
// Services register start/stop callbacks; the Manager runs them in registration
// order on startup and in reverse order on shutdown.
package lifecycle

import (
	"context"
	"fmt"
	"sync"

	"github.com/johalputt/vayupress/internal/logging"
)

type startFn func(ctx context.Context) error
type stopFn func(ctx context.Context)

type service struct {
	name  string
	start startFn
	stop  stopFn // may be nil
}

// Manager orchestrates ordered startup and shutdown of application services.
type Manager struct {
	mu       sync.Mutex
	services []service
}

// New returns an initialised Manager.
func New() *Manager { return &Manager{} }

// Register adds a named service with optional start and stop callbacks.
// start may be nil for services that require no async initialisation.
// stop may be nil for services that need no cleanup.
// Services run in registration order; shutdown runs in reverse order.
func (m *Manager) Register(name string, start startFn, stop stopFn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.services = append(m.services, service{name: name, start: start, stop: stop})
}

// Start runs all registered start functions in order, stopping on first error.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	svcs := m.services
	m.mu.Unlock()
	for _, s := range svcs {
		if s.start == nil {
			continue
		}
		logging.LogInfo("lifecycle", fmt.Sprintf("starting: %s", s.name))
		if err := s.start(ctx); err != nil {
			return fmt.Errorf("lifecycle: %s failed to start: %w", s.name, err)
		}
	}
	return nil
}

// Stop runs all registered stop functions in reverse order.
// Errors from stop functions are logged but do not halt the shutdown sequence.
func (m *Manager) Stop(ctx context.Context) {
	m.mu.Lock()
	svcs := m.services
	m.mu.Unlock()
	for i := len(svcs) - 1; i >= 0; i-- {
		s := svcs[i]
		if s.stop == nil {
			continue
		}
		logging.LogInfo("lifecycle", fmt.Sprintf("stopping: %s", s.name))
		func() {
			defer func() {
				if p := recover(); p != nil {
					logging.LogError("lifecycle", fmt.Sprintf("stop panic in %s", s.name), fmt.Sprint(p))
				}
			}()
			s.stop(ctx)
		}()
	}
}
