// SPDX-License-Identifier: Apache-2.0

// Package offload is VayuShield's Aegis L1: it exports the shield's live
// verdicts (jailed IPs) down to the kernel, so a confirmed attacker's packets
// are dropped by nftables/XDP at line rate — before a TCP connection, TLS
// handshake, goroutine or byte of userspace work exists for them.
//
// Privilege separation (ADR-0123): the unprivileged web app NEVER touches the
// firewall. This package only maintains a plain-text ban file in the app-owned
// control directory (<control>/banlist.txt, lines of "<ip> <unix-expiry>").
// The root reconcile agent (deploy/vayushield-agent.sh) — installed once by
// the operator — polls that file, revalidates every line against a strict
// parser, and reconciles a kernel nftables timeout-set (and an XDP filter
// when available) to match. If the agent is not installed the file is written
// and simply never consumed: the in-binary gates (L0/L2/L5 + blocklist) keep
// enforcing on their own, so kernel offload is a pure acceleration, never a
// dependency.
//
// The file is written atomically (tmp + rename), debounced to at most one
// write per flush interval, pruned of expired entries on every flush, and
// hard-capped — bounded I/O and memory no matter how many IPs get jailed.
package offload

import (
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	fileName      = "banlist.txt"
	maxEntries    = 10000
	flushInterval = 2 * time.Second
)

// Exporter maintains the kernel ban file. Safe for concurrent use.
type Exporter struct {
	mu        sync.Mutex
	dir       string
	entries   map[string]time.Time // ip -> expiry
	protected map[string]time.Time // operator IPs immune from banning
	// refused counts bans declined because the address must never be dropped
	// in-kernel (see neverBannable). Surfaced so a real-IP misconfiguration —
	// which makes every visitor arrive as 127.0.0.1 — is visible rather than
	// silently swallowed.
	refused map[string]int
	dirty   bool
	now     func() time.Time
}

// New builds an exporter that writes into dir (the VayuShield control
// directory). The directory is created if missing so the root agent has a
// place to read from; failures are silent — offload is best-effort by design.
func New(dir string) *Exporter {
	_ = os.MkdirAll(dir, 0o750)
	return &Exporter{
		dir:       dir,
		entries:   make(map[string]time.Time),
		protected: make(map[string]time.Time),
		refused:   make(map[string]int),
		now:       time.Now,
	}
}

// canonical strips any :port and normalizes the address, returning "" for
// anything that is not a plain IP.
func canonical(ip string) string {
	if h, _, err := net.SplitHostPort(ip); err == nil {
		ip = h
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	return addr.String()
}

// neverBannable reports addresses that must never reach the kernel ban set.
//
// # The outage this exists to prevent
//
// A kernel ban is a source-address drop applied by the root agent, and it runs
// in a chain that sits in front of everything else on the machine. Ban the
// loopback address and NOTHING on the host can talk to itself: nginx cannot
// reach this application, so every visitor gets 502; a local resolver stops
// answering, so name lookups fail; and because the rule is a DROP rather than
// a reject, every one of those failures is a TIMEOUT rather than an error.
// The site comes back by itself when the ban's TTL expires, minutes later,
// leaving a running process, a clean application log and no explanation.
//
// This is not hypothetical for a reverse-proxied install. VayuShield keys its
// verdicts by the resolved client address, and when the real-IP layer is not
// configured — or a proxy is not yet trusted — every visitor arrives as
// 127.0.0.1. One bad actor in that state jails the loopback address for the
// whole machine. Provisioning makes it likelier still: the certificate helper
// issues a burst of loopback pre-flight requests, one per domain, which is
// exactly the shape the rate limiter is built to punish.
//
// The unspecified address is refused for the same reason: 0.0.0.0 as a source
// match is a wildcard on some paths, and a wildcard drop is a dead machine.
//
// Deliberately NOT extended to private ranges. An operator running behind a
// LAN-facing proxy may have a genuine reason to ban 10.x, and refusing that
// would be this product overriding a decision that is theirs to make. Loopback
// is different in kind: banning it can never be what anybody wanted.
func neverBannable(addr netip.Addr) (bool, string) {
	switch {
	case addr.IsLoopback():
		return true, "loopback"
	case addr.IsUnspecified():
		return true, "unspecified"
	}
	return false, ""
}

// Protect marks ip as an operator address: it can never be banned, and any
// pending ban for it is withdrawn. A kernel drop is the one gate app-level
// operator immunity cannot override, so the ban must never be exported in the
// first place. Protection lasts 24 h from the most recent trusted request.
func (e *Exporter) Protect(ip string) {
	key := canonical(ip)
	if key == "" {
		return
	}
	now := e.now()
	e.mu.Lock()
	defer e.mu.Unlock()
	// Refresh at most once a minute so per-request calls stay a cheap map read.
	if until, ok := e.protected[key]; ok && until.Sub(now) > 23*time.Hour+59*time.Minute {
		return
	}
	e.protected[key] = now.Add(24 * time.Hour)
	if _, banned := e.entries[key]; banned {
		delete(e.entries, key)
		e.dirty = true
	}
}

// Unban withdraws any pending ban for ip (a pardon — e.g. the source solved a
// challenge). The agent mirrors the file exactly, so the kernel entry is
// removed within one reconcile poll.
func (e *Exporter) Unban(ip string) {
	key := canonical(ip)
	if key == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.entries[key]; ok {
		delete(e.entries, key)
		e.dirty = true
	}
}

// Ban records that ip should be dropped in-kernel until now+ttl. Invalid IPs,
// non-positive TTLs and protected (operator) addresses are ignored. The write
// itself happens on the next debounced flush.
func (e *Exporter) Ban(ip string, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	key := canonical(ip)
	if key == "" {
		return
	}
	// Never export a ban that would cut the machine off from itself. See
	// neverBannable: a loopback drop takes nginx away from this process and
	// every visitor gets 502 until the TTL runs out.
	if addr, err := netip.ParseAddr(key); err == nil {
		if refuse, why := neverBannable(addr); refuse {
			e.mu.Lock()
			e.refused[why]++
			e.mu.Unlock()
			return
		}
	}
	now := e.now()
	exp := now.Add(ttl)
	e.mu.Lock()
	defer e.mu.Unlock()
	if until, ok := e.protected[key]; ok && until.After(now) {
		return
	}
	if cur, ok := e.entries[key]; !ok || exp.After(cur) {
		e.entries[key] = exp
		e.dirty = true
	}
}

// Count returns the number of live (unexpired) bans (telemetry).
func (e *Exporter) Count() int {
	now := e.now()
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, exp := range e.entries {
		if exp.After(now) {
			n++
		}
	}
	return n
}

// Start launches the debounced background flusher. Call once at boot with the
// process shutdown channel.
func (e *Exporter) Start(done <-chan struct{}) {
	go func() {
		t := time.NewTicker(flushInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				e.Flush()
			}
		}
	}()
}

// Flush prunes expired entries, enforces the cap and atomically rewrites the
// ban file if anything changed. Errors are swallowed: kernel offload is an
// acceleration, and the in-binary gates keep enforcing regardless.
func (e *Exporter) Flush() {
	now := e.now()
	e.mu.Lock()
	pruned := false
	for ip, exp := range e.entries {
		if !exp.After(now) {
			delete(e.entries, ip)
			pruned = true
		}
	}
	if !e.dirty && !pruned {
		e.mu.Unlock()
		return
	}
	type ent struct {
		ip  string
		exp time.Time
	}
	list := make([]ent, 0, len(e.entries))
	for ip, exp := range e.entries {
		list = append(list, ent{ip, exp})
	}
	e.dirty = false
	e.mu.Unlock()

	// Keep the entries expiring FURTHEST out when over the cap (they represent
	// the most persistent offenders); deterministic order for stable diffs.
	sort.Slice(list, func(i, j int) bool { return list[i].exp.After(list[j].exp) })
	if len(list) > maxEntries {
		list = list[:maxEntries]
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ip < list[j].ip })

	var b strings.Builder
	b.WriteString("# VayuShield L1 kernel-offload ban list — written by vayupress, consumed by vayushield-agent.\n")
	b.WriteString("# Format: <ip> <unix-expiry>. Lines failing strict validation are ignored by the agent.\n")
	for _, en := range list {
		b.WriteString(en.ip)
		b.WriteByte(' ')
		b.WriteString(strconv.FormatInt(en.exp.Unix(), 10))
		b.WriteByte('\n')
	}
	path := filepath.Join(e.dir, fileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o640); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// RefusedBans reports bans declined because the address may never be dropped
// in-kernel, keyed by reason ("loopback", "unspecified").
//
// A non-zero loopback count is a real signal, not trivia: it means the shield
// decided a visitor was a bad actor and that visitor's resolved address was
// this machine. Almost always that is a reverse proxy whose real-IP layer is
// not configured, so the whole audience shares one key and one bad actor
// convicts everybody.
func (e *Exporter) RefusedBans() map[string]int {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]int, len(e.refused))
	for k, v := range e.refused {
		out[k] = v
	}
	return out
}
