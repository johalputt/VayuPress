package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	vtor "github.com/johalputt/vayupress/internal/vayuos/vayutor"
)

// TestTorOnionMiddlewareOnionPrimary covers the ADR-0141 onion-primary rule: in
// a Tor Space (VAYUOS_MODE=tor) an incoming .onion Host is the site's own domain
// and must survive to downstream resolution — the clearnet Host rewrite is
// suppressed — while the clearnet Space path is unchanged.
func TestTorOnionMiddlewareOnionPrimary(t *testing.T) {
	prev := config.Cfg.OnionMode
	defer func() { config.Cfg.OnionMode = prev }()

	const onion = "f7ar4p3dbdezrdgj4zsaaolmxmvddq3rsmryf6zztltlgocliomn6qad.onion"

	// hostSeen wraps the middleware and reports the Host the downstream handler
	// actually receives.
	hostSeen := func(a *App, path string) (string, *App) {
		var got string
		h := a.torOnionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Host
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest(http.MethodGet, "http://"+onion+path, nil)
		req.Host = onion
		h.ServeHTTP(httptest.NewRecorder(), req)
		return got, a
	}

	t.Run("tor mode preserves the onion host and tallies the visit", func(t *testing.T) {
		config.Cfg.OnionMode = true
		a := &App{vayuTor: vtor.NewEngine(vtor.Config{Enabled: true})}
		got, a := hostSeen(a, "/articles/hello")
		if got != onion {
			t.Errorf("onion Host rewritten to %q; want it preserved as %q", got, onion)
		}
		if v := a.vayuTor.Visits(); v != 1 {
			t.Errorf("visit tally = %d, want 1", v)
		}
	})

	t.Run("clearnet mode leaves an unmapped onion untouched", func(t *testing.T) {
		config.Cfg.OnionMode = false
		a := &App{vayuTor: vtor.NewEngine(vtor.Config{Enabled: true})}
		got, _ := hostSeen(a, "/articles/hello")
		if got != onion {
			t.Errorf("unmapped onion Host = %q; want it unchanged as %q", got, onion)
		}
	})

	t.Run("disabled engine is a pass-through in both modes", func(t *testing.T) {
		for _, mode := range []bool{false, true} {
			config.Cfg.OnionMode = mode
			a := &App{vayuTor: vtor.NewEngine(vtor.Config{Enabled: false})}
			got, _ := hostSeen(a, "/articles/hello")
			if got != onion {
				t.Errorf("OnionMode=%v: disabled engine changed Host to %q; want %q", mode, got, onion)
			}
		}
	})
}

// TestOnionSafeBindAddr: a Tor Space binds loopback only (its content must never
// be reachable on the host's public clearnet IP — a deanonymisation vector),
// while a clearnet install binds all interfaces exactly as before (ADR-0141).
func TestOnionSafeBindAddr(t *testing.T) {
	if got := onionSafeBindAddr("8080", false); got != ":8080" {
		t.Errorf("clearnet bind = %q, want %q", got, ":8080")
	}
	if got := onionSafeBindAddr("8081", true); got != "127.0.0.1:8081" {
		t.Errorf("tor bind = %q, want %q (must be loopback-only)", got, "127.0.0.1:8081")
	}
}
