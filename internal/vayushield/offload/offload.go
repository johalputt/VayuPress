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
	mu      sync.Mutex
	dir     string
	entries map[string]time.Time // ip -> expiry
	dirty   bool
	now     func() time.Time
}

// New builds an exporter that writes into dir (the VayuShield control
// directory). The directory is created if missing so the root agent has a
// place to read from; failures are silent — offload is best-effort by design.
func New(dir string) *Exporter {
	_ = os.MkdirAll(dir, 0o750)
	return &Exporter{dir: dir, entries: make(map[string]time.Time), now: time.Now}
}

// Ban records that ip should be dropped in-kernel until now+ttl. Invalid IPs
// and non-positive TTLs are ignored. The write itself happens on the next
// debounced flush.
func (e *Exporter) Ban(ip string, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return
	}
	exp := e.now().Add(ttl)
	key := addr.String() // canonical form
	e.mu.Lock()
	defer e.mu.Unlock()
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
