// SPDX-License-Identifier: Apache-2.0

// Package geoip provides an offline, embedded, IP → ISO-3166 country lookup.
//
// It exists so that self-hosted, sovereign deployments — a VayuPress binary on
// a bare VPS with no CDN or reverse proxy injecting geo headers — can still show
// visitor countries in analytics without contacting any external service. The
// lookup table ships compiled into the binary (github.com/phuslu/iploc), is a
// few hundred kilobytes, performs no network I/O, and is queried purely in
// memory. This keeps the "no telemetry, no phone-home" posture intact: nothing
// leaves the host, and (unlike proxy headers) the visitor IP is used only for
// this in-process lookup and is never persisted — analytics stores the country
// code alone (see ADR-0097).
//
// Resolution is coarse country-level only. Private, loopback, reserved and
// unknown addresses resolve to the empty string, as do all lookups when the
// operator sets ANALYTICS_GEOIP=off to opt out entirely.
package geoip

import (
	"net"
	"os"
	"strings"

	"github.com/phuslu/iploc"
)

// Enabled reports whether offline country resolution is active. The lookup is on
// by default so sovereign deployments get countries out of the box; operators
// who prefer to record nothing derived from the visitor IP set ANALYTICS_GEOIP=off.
// The environment is read on each call so a restart is not required to toggle it
// (the cost is one getenv per rate-limited analytics beacon).
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ANALYTICS_GEOIP"))) {
	case "off", "false", "0", "no", "disabled":
		return false
	default:
		return true
	}
}

// Country returns the uppercase ISO-3166 alpha-2 country code for ip, or "" when
// resolution is disabled, the address is unparseable/private/reserved, or the
// database has no entry. The visitor IP is never stored — only the returned code
// is (by the caller). "ZZ" (iploc's unknown/reserved sentinel) maps to "".
func Country(ip string) string {
	if !Enabled() {
		return ""
	}
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil || parsed.IsPrivate() || parsed.IsLoopback() ||
		parsed.IsUnspecified() || parsed.IsLinkLocalUnicast() {
		return ""
	}
	code := strings.ToUpper(iploc.Country(parsed))
	if len(code) != 2 || code == "ZZ" || code == "XX" {
		return ""
	}
	return code
}
