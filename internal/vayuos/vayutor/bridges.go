package vayutor

// bridges.go — VayuTor's answer to networks that block Tor at the IP level
// (a VPS that null-routes public relay IPs, or a censor doing DPI). When a
// managed-tor bootstrap can't reach the network directly, VayuTor escalates to
// Tor BRIDGES: unlisted entry points whose IPs aren't on any public-relay
// blocklist, optionally wrapped in the obfs4 pluggable transport so the traffic
// doesn't look like Tor at all. Bridges only change how the SERVER's own Tor
// circuits leave the box; they touch nothing on the visitor→site path, and the
// onion services stay E2E-encrypted, so VayuTor's "count-only, nothing tracked"
// privacy posture is unchanged. See docs/adr/ADR-0138.

import "strings"

// escLevel is VayuTor's one-shot bootstrap-escalation ladder. Each rung is tried
// once (never flapped) when the rung below stalls; escBridges is terminal.
type escLevel int

const (
	escDirect  escLevel = iota // no reachability restriction, no bridges (rung 0)
	escFascist                 // ReachableAddresses *:80,*:443            (rung 1)
	escBridges                 // UseBridges 1 + Bridge lines (+ obfs4 PT)  (rung 2, terminal)
)

// defaultObfs4Bridges is an OPTIONAL built-in obfs4 bridge set used only when the
// operator sets no VAYUOS_TOR_BRIDGES and the direct + firewall-friendly rungs
// have both failed. It is intentionally EMPTY: VayuPress does not ship live
// third-party bridge infrastructure (those bridges are provisioned for Tor
// Browser, would concentrate every install onto the same guards, and go stale as
// they are blocked). Operator-supplied bridges are the first-class path — fresh,
// working lines from https://bridges.torproject.org (or bridges@torproject.org
// by email). A distributor who wants a zero-config default may paste current
// `obfs4 IP:443 <FINGERPRINT> cert=<...> iat-mode=0` lines here verbatim; the
// cert/fingerprint values are self-authenticating and must be exact.
var defaultObfs4Bridges = []string{}

// classifyBridges normalizes raw bridge lines (strips a leading "Bridge "
// keyword, drops blanks) and reports whether any line names a pluggable
// transport (its first token is a PT name like "obfs4", not an IP:port), which
// means obfs4proxy must be present.
func classifyBridges(in []string) (out []string, needsPT bool) {
	for _, l := range in {
		l = strings.TrimSpace(l)
		l = strings.TrimPrefix(l, "Bridge ")
		l = strings.TrimPrefix(l, "bridge ")
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if f := strings.Fields(l); len(f) > 0 {
			if t := f[0]; !strings.Contains(t, ":") && !strings.HasPrefix(t, "[") {
				needsPT = true
			}
		}
		out = append(out, l)
	}
	return out, needsPT
}

// vanillaOnly keeps only the non-pluggable-transport bridge lines (a bare
// IP:port [fingerprint]), used as a fallback when obfs4proxy is unavailable.
func vanillaOnly(lines []string) []string {
	var out []string
	for _, l := range lines {
		if f := strings.Fields(l); len(f) > 0 {
			if t := f[0]; strings.Contains(t, ":") || strings.HasPrefix(t, "[") {
				out = append(out, l)
			}
		}
	}
	return out
}

// logHasNoRoute reports whether tor's log shows an IP-level routing block — the
// signature ("No route to host" / NOROUTE) that means direct AND firewall-
// friendly connections are futile, so VayuTor should jump straight to bridges.
func logHasNoRoute(tail string) bool {
	low := strings.ToLower(tail)
	return strings.Contains(low, "no route to host") || strings.Contains(low, "noroute")
}

// escalationNote is the status line shown when VayuTor moves to a new rung.
func escalationNote(l escLevel) string {
	switch l {
	case escFascist:
		return "bootstrap stalled — retrying Tor via firewall-friendly ports (80/443)"
	case escBridges:
		return "bootstrap blocked (no route to Tor relays) — retrying Tor over bridges"
	}
	return ""
}

// transportLabel is a human name for the current escalation rung, for the
// admin status ("direct" / "80/443 only" / "bridges (obfs4)").
func transportLabel(l escLevel, obfs4 bool) string {
	switch l {
	case escFascist:
		return "ports 80/443 only"
	case escBridges:
		if obfs4 {
			return "bridges (obfs4)"
		}
		return "bridges"
	}
	return "direct"
}
