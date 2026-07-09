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
	dirty     bool
	now       func() time.Time
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
