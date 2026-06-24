// Package kernel — health monitor for VayuOS subsystems.
package kernel

import "time"

type HealthStatus struct {
	AllHealthy bool
	Subsystems map[string]ComponentHealth
	CheckedAt  time.Time
}

type ComponentHealth struct {
	Healthy bool
	Message string
	Since   time.Time
}

type HealthMonitor struct {
	components map[string]func() ComponentHealth
}

func NewHealthMonitor() *HealthMonitor {
	return &HealthMonitor{components: make(map[string]func() ComponentHealth)}
}

func (h *HealthMonitor) Register(name string, check func() ComponentHealth) {
	h.components[name] = check
}

func (h *HealthMonitor) Check() *HealthStatus {
	status := &HealthStatus{
		AllHealthy: true,
		Subsystems: make(map[string]ComponentHealth),
		CheckedAt:  time.Now().UTC(),
	}
	for name, check := range h.components {
		ch := check()
		status.Subsystems[name] = ch
		if !ch.Healthy {
			status.AllHealthy = false
		}
	}
	return status
}