// SPDX-License-Identifier: Apache-2.0

package main

// tor_egress.go — the opt-in "route outbound over Tor" mode for a Tor Space
// (ADR-0143). By default a Tor Space REFUSES every clearnet connection (the safe
// default). When the operator opts in with VAYUTOR_ROUTE_EGRESS and points
// VAYUOS_TOR_SOCKS_ADDR at a Tor SOCKS proxy, outbound clearnet is instead
// ROUTED through Tor: features that need the internet (update checks, AI, etc.)
// keep working while the server's real IP stays hidden. The SOCKS5 dialer
// resolves hostnames REMOTELY (inside Tor), so no DNS query leaks locally.

import (
	"context"
	"net"
	"strings"

	"golang.org/x/net/proxy"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/safefetch"
)

// envTruthy reports whether an env value reads as on.
func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// configureTorEgressRouting installs the opt-in Tor egress route when enabled and
// a Tor SOCKS proxy is configured. Fail-safe: if the opt-in is on but no usable
// SOCKS proxy is available, it stays in clearnet-BLOCK mode (never falls back to
// leaking) and warns.
func configureTorEgressRouting() {
	if !config.Cfg.OnionMode || !envTruthy(config.EnvOr("VAYUTOR_ROUTE_EGRESS", "")) {
		return
	}
	socks := strings.TrimSpace(config.EnvOr("VAYUOS_TOR_SOCKS_ADDR", ""))
	if socks == "" {
		logging.LogWarn("anonymity", "VAYUTOR_ROUTE_EGRESS is set but VAYUOS_TOR_SOCKS_ADDR is empty — staying in clearnet-BLOCK mode. Point it at your Tor SOCKS proxy (e.g. 127.0.0.1:9050) to route outbound over Tor.")
		return
	}
	fwd, err := proxy.SOCKS5("tcp", socks, nil, proxy.Direct)
	if err != nil {
		logging.LogError("anonymity", "invalid VAYUOS_TOR_SOCKS_ADDR for egress routing ("+socks+") — staying in block mode", err.Error())
		return
	}
	cd, ok := fwd.(proxy.ContextDialer)
	if !ok {
		logging.LogError("anonymity", "Tor SOCKS dialer does not support context — staying in block mode", "")
		return
	}
	// Route every guarded clearnet dial through Tor. proxy.SOCKS5 hands the
	// hostname to Tor for REMOTE resolution, so nothing resolves (or dials) from
	// the real IP.
	safefetch.SetTorEgressDialer(func(ctx context.Context, network, addr string) (net.Conn, error) {
		return cd.DialContext(ctx, network, addr)
	})
	logging.LogInfo("anonymity", "Tor Space egress ROUTED over Tor via "+socks+" (opt-in): outbound features work over Tor and the real IP stays hidden")
}
