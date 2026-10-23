// SPDX-License-Identifier: Apache-2.0

package vayushield

// policy_expiry.go — list entries that end ("203.0.113.0/24 until
// 2026-11-01"). The lists are compiled with the entries in force at the
// shield's clock, and recompiled the moment the soonest one ends, so a
// request never tests a date and an ended refusal stops on time without a
// save.

import (
	"time"

	"github.com/johalputt/vayupress/internal/vayushield/policy"
)

// compilePolicy compiles the install's lists at the shield's clock.
func (m *Manager) compilePolicy(c policy.Config) {
	c.Now = m.cfg.Now()
	rules, bad := policy.Compile(c)
	m.policy.Store(&rules)
	m.policyIssues.Store(&bad)
}

// armExpiry sets the timer for the soonest end among the install's entries
// and every site's, replacing any timer set before.
func (m *Manager) armExpiry() {
	next := m.Policy().NextExpiry()
	if all := m.sites.Load(); all != nil {
		for _, sp := range *all {
			if sp.rules == nil {
				continue
			}
			if t := sp.rules.NextExpiry(); !t.IsZero() && (next.IsZero() || t.Before(next)) {
				next = t
			}
		}
	}
	m.expiryMu.Lock()
	defer m.expiryMu.Unlock()
	if m.expiryTimer != nil {
		m.expiryTimer.Stop()
		m.expiryTimer = nil
	}
	if next.IsZero() {
		m.expiryAt.Store(0)
		return
	}
	m.expiryAt.Store(next.Unix())
	m.expiryTimer = time.AfterFunc(max(next.Sub(m.cfg.Now()), 0), m.expirePolicy)
}

// expirePolicy recompiles the install's lists and every site's at the
// shield's clock; compileSites arms the next timer.
func (m *Manager) expirePolicy() {
	if s := m.curSettings.Load(); s != nil {
		m.compilePolicy(s.Policy)
	}
	m.compileSites()
}

// ExpiredPolicy is the install's entries left out because their end passed.
func (m *Manager) ExpiredPolicy() []string { return m.Policy().Expired() }
