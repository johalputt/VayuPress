// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/vayushield/policy"
)

// clockShield is a two-site shield whose clock the test moves. The dates are
// years ahead, so a mutation that reads the real clock instead is caught.
func clockShield(now *atomic.Int64) *Manager {
	m := New(Config{Enabled: true,
		Now: func() time.Time { return time.Unix(now.Load(), 0) },
		SiteFn: func(r *http.Request) string {
			if r.Host == "a.test" {
				return "sa"
			}
			return ""
		}})
	return m
}

func refused(m *Manager, host, ip string) bool {
	ok := false
	h := m.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ok = true }))
	req := httptest.NewRequest(http.MethodGet, "/article", nil)
	req.Host, req.RemoteAddr = host, ip+":40000"
	h.ServeHTTP(httptest.NewRecorder(), req)
	return !ok
}

// A refusal with an end is in force until it, the timer is set for it, and
// when it passes the refusal is gone and nothing is pending.
func TestAnEndedRefusalStopsAndNothingIsPending(t *testing.T) {
	var now atomic.Int64
	t0 := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	now.Store(t0.Unix())
	m := clockShield(&now)
	m.ApplySettings(Settings{Enabled: true, Policy: policy.Config{DenyCIDRs: []string{"198.51.100.0/24 until 2030-01-01T13:00"}}})
	if !refused(m, "b.test", "198.51.100.9") {
		t.Fatal("the refusal was not in force before its end")
	}
	if got := m.expiryAt.Load(); got != t0.Add(time.Hour).Unix() {
		t.Fatalf("the timer is set for %d, want %d", got, t0.Add(time.Hour).Unix())
	}
	now.Store(t0.Add(time.Hour).Unix())
	m.expirePolicy()
	if refused(m, "b.test", "198.51.100.9") {
		t.Error("the refusal outlived its end")
	}
	if m.expiryAt.Load() != 0 || len(m.ExpiredPolicy()) != 1 {
		t.Errorf("after the end: pending %d, expired %v; want nothing pending and the entry named", m.expiryAt.Load(), m.ExpiredPolicy())
	}
}

// A site's own entry with an end sets the timer when it is the soonest.
func TestASitesEntryWithTheSoonestEndSetsTheTimer(t *testing.T) {
	var now atomic.Int64
	t0 := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	now.Store(t0.Unix())
	m := clockShield(&now)
	m.ApplySettings(Settings{Enabled: true, Policy: policy.Config{DenyCIDRs: []string{"192.0.2.0/24 until 2030-01-01T14:00"}}})
	m.SetSiteSettings(map[string]SiteSettings{"sa": {Policy: policy.Config{DenyCIDRs: []string{"203.0.113.0/24 until 2030-01-01T12:30"}}}})
	if got, want := m.expiryAt.Load(), t0.Add(30*time.Minute).Unix(); got != want {
		t.Errorf("the timer is set for %d, want the site's end %d", got, want)
	}
	if !refused(m, "a.test", "203.0.113.4") {
		t.Error("the site's refusal was not in force before its end")
	}
	now.Store(t0.Add(30 * time.Minute).Unix())
	m.expirePolicy()
	if refused(m, "a.test", "203.0.113.4") {
		t.Error("the site's refusal outlived its end")
	}
	if got, want := m.expiryAt.Load(), t0.Add(2*time.Hour).Unix(); got != want {
		t.Errorf("after the site's end the timer is set for %d, want the install's %d", got, want)
	}
}

// The timer itself recompiles: an end already passed when the timer is set
// fires at once and the refusal stops without anything else being called.
func TestTheTimerRecompilesWhenAnEndPasses(t *testing.T) {
	var now atomic.Int64
	t0 := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	now.Store(t0.Unix())
	m := clockShield(&now)
	m.ApplySettings(Settings{Enabled: true, Policy: policy.Config{DenyCIDRs: []string{"198.51.100.0/24 until 2030-01-01T13:00"}}})
	now.Store(t0.Add(2 * time.Hour).Unix())
	m.armExpiry()
	deadline := time.Now().Add(2 * time.Second)
	for refused(m, "b.test", "198.51.100.9") {
		if time.Now().After(deadline) {
			t.Fatal("the timer did not recompile the lists after the end passed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
