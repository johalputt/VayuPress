// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

// cspViolation is one recent Content-Security-Policy violation, kept in a small
// in-memory ring so frontend-governance signals can surface in the Unified
// Operational Timeline alongside mode transitions and faults. It is intentionally
// ephemeral (bounded, process-local): the durable record is the structured log
// line and the vayupress_csp_violations_total metric.
type cspViolation struct {
	When       time.Time
	Directive  string
	BlockedURI string
}

const cspRingMax = 10

var (
	cspRingMu sync.Mutex
	cspRing   []cspViolation
)

// recordCSPViolation appends a violation to the bounded ring (newest last).
func recordCSPViolation(directive, blocked string) {
	cspRingMu.Lock()
	cspRing = append(cspRing, cspViolation{When: time.Now().UTC(), Directive: directive, BlockedURI: blocked})
	if len(cspRing) > cspRingMax {
		cspRing = cspRing[len(cspRing)-cspRingMax:]
	}
	cspRingMu.Unlock()
}

// recentCSPViolations returns a copy of the current ring (oldest first).
func recentCSPViolations() []cspViolation {
	cspRingMu.Lock()
	defer cspRingMu.Unlock()
	out := make([]cspViolation, len(cspRing))
	copy(out, cspRing)
	return out
}

// cspEnforcementMode returns the human-readable CSP enforcement posture. The
// enforcement posture is operational state, so it is surfaced in the timeline,
// the governance dashboard, and the stats/health JSON — not hidden in an env var.
func cspEnforcementMode() string {
	if config.Cfg.CSPReportOnly {
		return "report-only"
	}
	return "enforcing"
}

// cspBlock is one origin a site's policy blocked, as the Outside services page
// lists it: which site, which kind of resource, how often, and from how many
// visitors.
type cspBlock struct {
	Host, Directive, Origin string
	Count                   int
	Last                    time.Time
	visitors                map[string]bool // hashed client addresses, capped
}

const (
	cspBlocksMax     = 500
	cspBlockVisitors = 8
	// cspBlockShownAt is how many visitors must report an origin before the
	// page offers it. Reports are unauthenticated: one forged report could
	// otherwise put an origin of an attacker's choosing in front of the
	// operator with an Allow button beside it.
	cspBlockShownAt = 2
)

var (
	cspBlocksMu sync.Mutex
	cspBlocks   = map[string]*cspBlock{}
)

// cspDirectiveBase folds the element and attribute variants a browser reports
// (script-src-elem, style-src-attr) into the directive a policy names.
func cspDirectiveBase(d string) string {
	d = strings.ToLower(strings.TrimSpace(strings.Fields(d + " ")[0]))
	return strings.TrimSuffix(strings.TrimSuffix(d, "-elem"), "-attr")
}

// cspBlockedOrigin is what was blocked, as an origin a policy could name, or
// the keyword a browser reports for what no origin covers (inline, eval).
func cspBlockedOrigin(blocked string) string {
	u, err := url.Parse(strings.TrimSpace(blocked))
	if err != nil || u.Host == "" {
		return strings.ToLower(strings.TrimSpace(blocked))
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// recordCSPBlock counts one report against the site it came from.
func recordCSPBlock(documentURI, directive, blocked, client string, now time.Time) {
	doc, err := url.Parse(documentURI)
	if err != nil || doc.Hostname() == "" {
		return
	}
	b := cspBlock{Host: strings.ToLower(doc.Hostname()), Directive: cspDirectiveBase(directive), Origin: cspBlockedOrigin(blocked)}
	if b.Directive == "" || b.Origin == "" {
		return
	}
	sum := sha256.Sum256([]byte(client))
	visitor := hex.EncodeToString(sum[:8])
	key := b.Host + "|" + b.Directive + "|" + b.Origin

	cspBlocksMu.Lock()
	defer cspBlocksMu.Unlock()
	cur, ok := cspBlocks[key]
	if !ok {
		if len(cspBlocks) >= cspBlocksMax {
			var oldest string
			for k, v := range cspBlocks {
				if oldest == "" || v.Last.Before(cspBlocks[oldest].Last) {
					oldest = k
				}
			}
			delete(cspBlocks, oldest)
		}
		b.visitors = map[string]bool{}
		cur = &b
		cspBlocks[key] = cur
	}
	cur.Count++
	cur.Last = now
	if len(cur.visitors) < cspBlockVisitors {
		cur.visitors[visitor] = true
	}
}

// cspBlocksFor lists what the given hosts' policies blocked, reported by
// enough visitors to be offered, most recent first.
func cspBlocksFor(hosts []string) []cspBlock {
	want := map[string]bool{}
	for _, h := range hosts {
		want[strings.ToLower(h)] = true
	}
	cspBlocksMu.Lock()
	var out []cspBlock
	for _, b := range cspBlocks {
		if want[b.Host] && len(b.visitors) >= cspBlockShownAt {
			c := *b
			c.visitors = nil
			out = append(out, c)
		}
	}
	cspBlocksMu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}
