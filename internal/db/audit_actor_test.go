// SPDX-License-Identifier: Apache-2.0

package db

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// AuditActor used to read X-Real-IP, then the FIRST X-Forwarded-For entry, with
// no peer check at all — and the left-most XFF entry is the one a client
// prepends. Anyone able to reach the origin directly could therefore write
// whatever actor they liked into the audit log. A trail an attacker can author
// is worse than none: it does not merely fail to record them, it records someone
// else.
func TestAuditActorRejectsForgedHeaders(t *testing.T) {
	saved := config.Cfg.TrustedProxies
	t.Cleanup(func() { config.Cfg.TrustedProxies = saved })
	_, lo, _ := net.ParseCIDR("127.0.0.0/8")
	config.Cfg.TrustedProxies = []*net.IPNet{lo}

	r := httptest.NewRequest(http.MethodPost, "/os/x", nil)
	r.RemoteAddr = "198.51.100.7:4444" // direct visitor, NOT a trusted proxy
	r.Header.Set("X-Real-IP", "10.0.0.1")
	r.Header.Set("X-Forwarded-For", "10.0.0.1")
	if got := AuditActor(r); got != "198.51.100.7" {
		t.Errorf("AuditActor = %q, want 198.51.100.7 — an attacker can author the audit log", got)
	}
}

// TestAuditActorHonoursATrustedProxy — the legitimate case must still work, or
// every audit entry behind nginx records 127.0.0.1 and the log is useless.
func TestAuditActorHonoursATrustedProxy(t *testing.T) {
	saved := config.Cfg.TrustedProxies
	t.Cleanup(func() { config.Cfg.TrustedProxies = saved })
	_, lo, _ := net.ParseCIDR("127.0.0.0/8")
	config.Cfg.TrustedProxies = []*net.IPNet{lo}

	r := httptest.NewRequest(http.MethodPost, "/os/x", nil)
	r.RemoteAddr = "127.0.0.1:5555" // the local reverse proxy
	r.Header.Set("X-Real-IP", "203.0.113.50")
	if got := AuditActor(r); got != "203.0.113.50" {
		t.Errorf("AuditActor = %q, want the real visitor 203.0.113.50", got)
	}
}
