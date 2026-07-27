// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"net"
	"sync"
	"time"
)

// connLimiter bounds concurrent protocol connections, globally and per source
// address.
//
// # WHY THIS IS A SECURITY CONTROL AND NOT MERELY TIDINESS
//
// AuthThrottle defends password guessing with a decaying DELAY, capped at
// authDelayMax (2s), deliberately never locking anyone out. That is the right
// shape for a single attacker on a single connection — and it is worth almost
// nothing without this file, because a delay only rate-limits an attacker who
// waits. With unlimited concurrent connections the attacker does not wait: they
// open N sockets, each sleeps its 2s, and the aggregate guess rate is N/2 per
// second. A thousand sockets turns a "crushed to under one attempt per second"
// defence into roughly five hundred guesses per second against one mailbox.
// Serialising the attempts is what makes the delay mean what its comment claims.
//
// It also closes the plain resource exhaustion: each accepted connection holds a
// bufio.Reader sized against MaxMessageBytes and a five-minute deadline, so
// unbounded accepts are an unauthenticated memory and file-descriptor DoS on
// ports that must face the public internet.
//
// The per-source cap is what keeps one host from consuming the global budget and
// denying service to everyone else, while the global cap bounds a distributed
// flood. Both are needed: either alone leaves the other case open.
type connLimiter struct {
	mu      sync.Mutex
	perAddr map[string]int
	total   int

	maxTotal   int
	maxPerAddr int
}

// Connection-limit defaults. Chosen to be far above any legitimate load for a
// single-VPS install — a busy mail server sees single-digit concurrent
// connections — while still bounding the worst case to something a modest box
// survives.
const (
	defaultMaxConns        = 256
	defaultMaxConnsPerAddr = 16
)

func newConnLimiter(maxTotal, maxPerAddr int) *connLimiter {
	if maxTotal <= 0 {
		maxTotal = defaultMaxConns
	}
	if maxPerAddr <= 0 {
		maxPerAddr = defaultMaxConnsPerAddr
	}
	return &connLimiter{
		perAddr:    make(map[string]int),
		maxTotal:   maxTotal,
		maxPerAddr: maxPerAddr,
	}
}

// acquire reserves a slot for addr. It returns false when either cap is reached,
// in which case the caller must close the connection without servicing it.
func (l *connLimiter) acquire(addr net.Addr) (release func(), ok bool) {
	key := addrKey(addr)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total >= l.maxTotal || l.perAddr[key] >= l.maxPerAddr {
		return nil, false
	}
	l.total++
	l.perAddr[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.total--
			if n := l.perAddr[key] - 1; n > 0 {
				l.perAddr[key] = n
			} else {
				// Delete rather than leave a zero — otherwise the map grows once per
				// distinct source address seen, which is itself a memory leak on a
				// public port.
				delete(l.perAddr, key)
			}
		})
	}, true
}

// addrKey reduces a remote address to its host, so many ports from one host
// share a budget. An unparseable address collapses to a single shared key rather
// than becoming unlimited.
func addrKey(addr net.Addr) string {
	if addr == nil {
		return "?"
	}
	if ta, ok := addr.(*net.TCPAddr); ok && ta.IP != nil {
		return ta.IP.String()
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil || host == "" {
		return "?"
	}
	return host
}

// acceptBackoff converts a persistent Accept() failure into a bounded pause.
//
// Without it, an accept loop that only `continue`s spins at 100% CPU the moment
// Accept starts failing — and the way it starts failing in practice is EMFILE,
// file-descriptor exhaustion, which an attacker causes on purpose. That turns a
// resource-exhaustion attack into a full CPU denial of service, on every
// listener at once, and the box stops answering long before the descriptors free
// up. The delay doubles from 5ms to a 1s ceiling and resets on the first
// successful accept.
type acceptBackoff struct{ d time.Duration }

func (b *acceptBackoff) pause() {
	const (
		min = 5 * time.Millisecond
		max = 1 * time.Second
	)
	if b.d == 0 {
		b.d = min
	} else if b.d < max {
		b.d *= 2
		if b.d > max {
			b.d = max
		}
	}
	time.Sleep(b.d)
}

func (b *acceptBackoff) reset() { b.d = 0 }
