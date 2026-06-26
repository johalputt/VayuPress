package kernel

import (
	"sync"
	"time"
)

// CheckFunc reports whether a component is healthy plus a short detail string.
type CheckFunc func() (ok bool, detail string)

// ComponentHealth is the health of one registered component.
type ComponentHealth struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// HealthSnapshot is the overall VayuOS health at a point in time.
type HealthSnapshot struct {
	OK         bool              `json:"ok"`
	Components []ComponentHealth `json:"components"`
	CheckedAt  time.Time         `json:"checked_at"`
}

// HealthMonitor aggregates component health checks for the VayuOS panel.
type HealthMonitor struct {
	mu     sync.RWMutex
	checks map[string]CheckFunc
	order  []string
}

// NewHealthMonitor creates an empty monitor.
func NewHealthMonitor() *HealthMonitor {
	return &HealthMonitor{checks: make(map[string]CheckFunc)}
}

// Register adds (or replaces) a named health check.
func (h *HealthMonitor) Register(name string, fn CheckFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.checks[name]; !exists {
		h.order = append(h.order, name)
	}
	h.checks[name] = fn
}

// Snapshot runs every check and returns the aggregate state.
func (h *HealthMonitor) Snapshot() HealthSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	snap := HealthSnapshot{OK: true, CheckedAt: time.Now().UTC()}
	for _, name := range h.order {
		ok, detail := h.checks[name]()
		if !ok {
			snap.OK = false
		}
		snap.Components = append(snap.Components, ComponentHealth{Name: name, OK: ok, Detail: detail})
	}
	return snap
}
