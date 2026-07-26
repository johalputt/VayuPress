// SPDX-License-Identifier: Apache-2.0

package vayutor

// health.go — VayuTor onion health tracking + alerting. The reconcile loop
// already knows everything needed to judge health (control connection, bootstrap
// %, how many of the wanted domains actually have a live onion); this turns that
// into a single coarse state with a short transition history and fires an
// operator alert (via an injected Notify callback → webhooks) when onions go
// down and again when they recover. Debounced so a brief blip or the normal
// startup climb never pages anyone. No visitor data is involved — health is
// purely about the server's own onion publication.

import "time"

// Health states (coarse, operator-facing).
const (
	HealthOff      = "off"      // VayuTor deactivated
	HealthStarting = "starting" // active, tor still bootstrapping (expected, not an alert)
	HealthHealthy  = "healthy"  // connected, bootstrapped, all wanted onions published
	HealthDegraded = "degraded" // connected but some onions are missing
	HealthDown     = "down"     // control port unreachable / tor not connected
)

const (
	// healthAlertDebounce is how long a bad state must persist before it counts
	// as an outage (and fires an alert). Benign states commit immediately.
	healthAlertDebounce = 90 * time.Second
	// healthLogMax bounds the retained transition history shown on the page.
	healthLogMax = 20
)

// HealthEvent is one state transition for the admin history.
type HealthEvent struct {
	State  string
	Reason string
	At     time.Time
}

// computeHealth derives the raw health state from the current engine snapshot.
// Caller holds e.mu.
func (e *Engine) computeHealth() (state, reason string) {
	if !e.active() {
		return HealthOff, ""
	}
	if !e.connected {
		r := "Tor control port not reachable"
		if e.lastErr != "" {
			r = e.lastErr
		}
		return HealthDown, r
	}
	if e.bootPct < 100 {
		r := "Tor is joining the network"
		if e.bootNote != "" {
			r = e.bootNote
		}
		return HealthStarting, r
	}
	if e.wantCount > 0 && len(e.onionByHost) < e.wantCount {
		return HealthDegraded, "only some domains have a published onion"
	}
	return HealthHealthy, ""
}

// evalHealth recomputes health, commits state transitions (benign states
// immediately, outage states after a debounce), maintains the history, and fires
// down/recovered alerts. Safe to call at every reconcile exit (via defer).
func (e *Engine) evalHealth() {
	var notifyEvent string
	var notifyPayload map[string]any

	e.mu.Lock()
	raw, reason := e.computeHealth()
	now := time.Now()
	switch {
	case raw == e.health:
		// Stable — just keep the reason line fresh and clear any pending change.
		e.healthReason = reason
		e.healthPending = ""
	default:
		benign := raw == HealthHealthy || raw == HealthStarting || raw == HealthOff
		commit := benign
		if !benign {
			// Outage state: require it to persist before committing (anti-flap).
			if e.healthPending != raw {
				e.healthPending = raw
				e.healthPendingSince = now
			} else if now.Sub(e.healthPendingSince) >= healthAlertDebounce {
				commit = true
			}
		}
		if commit {
			prev := e.health
			e.health, e.healthReason, e.healthSince = raw, reason, now
			e.healthPending = ""
			e.healthLog = append(e.healthLog, HealthEvent{State: raw, Reason: reason, At: now})
			if len(e.healthLog) > healthLogMax {
				e.healthLog = e.healthLog[len(e.healthLog)-healthLogMax:]
			}
			// Alert on the healthy⇄outage boundary, tracked with a latch so a
			// down→starting→healthy recovery still fires exactly one "recovered".
			switch raw {
			case HealthDown, HealthDegraded:
				if !e.healthAlerted {
					e.healthAlerted = true
					notifyEvent = "tor.onion_down"
					notifyPayload = e.healthPayload(raw, reason)
				}
			case HealthHealthy:
				if e.healthAlerted {
					e.healthAlerted = false
					notifyEvent = "tor.onion_recovered"
					notifyPayload = e.healthPayload(raw, reason)
				}
			case HealthOff:
				e.healthAlerted = false // deactivation is deliberate — never alert
			}
			_ = prev
		}
	}
	e.mu.Unlock()

	if notifyEvent != "" && e.cfg.Notify != nil {
		e.cfg.Notify(notifyEvent, notifyPayload)
	}
}

// healthPayload builds the PII-free alert body. Caller holds e.mu.
func (e *Engine) healthPayload(state, reason string) map[string]any {
	return map[string]any{
		"state":     state,
		"reason":    reason,
		"onions":    len(e.onionByHost),
		"domains":   e.wantCount,
		"bootstrap": e.bootPct,
	}
}

// healthSnapshot returns the current health for the admin page. Caller holds
// e.mu (read).
func (e *Engine) healthSnapshot() (state, reason string, since time.Time, log []HealthEvent) {
	state = e.health
	if state == "" {
		state = HealthOff
	}
	log = make([]HealthEvent, len(e.healthLog))
	copy(log, e.healthLog)
	return state, e.healthReason, e.healthSince, log
}
