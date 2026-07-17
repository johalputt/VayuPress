package vayutor

// vanity.go — generate a custom-prefix ("vanity") v3 onion address for a hosted
// domain. A v3 address is base32(pubkey ‖ checksum ‖ version) of an ed25519
// public key, so a chosen prefix is found by brute force: mint random keypairs
// until the resulting address starts with the wanted characters. This runs
// entirely as the unprivileged service user in the background, is cancellable,
// and — when it lands — persists the new identity and republishes the domain's
// onion with it (the old random address is replaced). No key ever leaves the box.

import (
	"context"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"filippo.io/edwards25519"
	"golang.org/x/crypto/sha3"
)

const (
	// onionVersion is the v3 onion address version byte.
	onionVersion = 0x03
	// vanityMaxPrefix caps the prefix length. Each base32 char is 5 bits, so the
	// expected work is 32^len keypairs — length 6 already averages ~1 billion.
	vanityMaxPrefix = 7
	// vanityRecommendedMax is the longest prefix we don't warn about (32^5 ≈ 33M,
	// a few minutes across a handful of cores).
	vanityRecommendedMax = 5
)

// onionB32 is tor's address encoding: RFC 4648 base32, lower-cased, no padding.
var onionB32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// vanityState is the live status of a running (or just-finished) search.
type vanityState struct {
	host      string
	prefix    string
	tries     int64 // atomic
	startedAt time.Time
	cancel    context.CancelFunc

	done    atomic.Bool
	found   atomic.Bool
	addr    atomic.Value // string
	failMsg atomic.Value // string
}

// VanityStatus is the admin-facing snapshot of a vanity search.
type VanityStatus struct {
	Active  bool
	Host    string
	Prefix  string
	Tries   int64
	Found   bool
	Address string
	Error   string
	Elapsed time.Duration
	Warn    bool // prefix long enough to take a while
	MaxLen  int
	RecLen  int
}

// Vanity search errors, surfaced verbatim to the admin page.
var (
	errVanityPrefix = errors.New("prefix must be 1–7 base32 characters (a–z, 2–7)")
	errVanityHost   = errors.New("that domain has no live onion yet — activate VayuTor first")
	errVanityBusy   = errors.New("a vanity search is already running — cancel it first")
)

// deriveOnion computes the v3 onion address and tor "ED25519-V3:<base64>" key
// blob for a 32-byte ed25519 seed. The blob is the 64-byte tor expanded secret
// (clamped scalar ‖ nonce), which ADD_ONION accepts to pin this exact address.
func deriveOnion(seed []byte) (addr, keyBlob string) {
	h := sha512.Sum512(seed)
	// Clamp the low 32 bytes per RFC 8032 §5.1.5 — this is the scalar tor stores.
	var a [32]byte
	copy(a[:], h[:32])
	a[0] &= 248
	a[31] &= 127
	a[31] |= 64
	// Public key A = a·B. SetBytesWithClamping clamps + reduces internally; the
	// resulting point is identical to using the (un-reduced) clamped bytes.
	s, err := edwards25519.NewScalar().SetBytesWithClamping(h[:32])
	if err != nil {
		return "", ""
	}
	pub := edwards25519.NewIdentityPoint().ScalarBaseMult(s).Bytes()
	addr = onionAddressFromPub(pub)
	expanded := make([]byte, 0, 64)
	expanded = append(expanded, a[:]...)
	expanded = append(expanded, h[32:64]...)
	keyBlob = "ED25519-V3:" + base64.StdEncoding.EncodeToString(expanded)
	return addr, keyBlob
}

// onionAddressFromPub builds the v3 onion address from a 32-byte ed25519 public
// key: base32(pub ‖ checksum ‖ version) + ".onion", where
// checksum = SHA3-256(".onion checksum" ‖ pub ‖ version)[:2].
func onionAddressFromPub(pub []byte) string {
	hash := sha3.New256()
	_, _ = hash.Write([]byte(".onion checksum"))
	_, _ = hash.Write(pub)
	_, _ = hash.Write([]byte{onionVersion})
	sum := hash.Sum(nil)
	buf := make([]byte, 0, 35)
	buf = append(buf, pub...)
	buf = append(buf, sum[:2]...)
	buf = append(buf, onionVersion)
	return strings.ToLower(onionB32.EncodeToString(buf)) + ".onion"
}

// validVanityPrefix reports whether p is a usable prefix (base32 alphabet, within
// the length cap). It also returns a normalized (lower-case) form.
func validVanityPrefix(p string) (string, bool) {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" || len(p) > vanityMaxPrefix {
		return "", false
	}
	for _, c := range p {
		if !((c >= 'a' && c <= 'z') || (c >= '2' && c <= '7')) {
			return "", false
		}
	}
	return p, true
}

// StartVanity begins searching for a vanity address for host. It fails if a
// search is already running, the prefix is invalid, or host has no live onion.
func (e *Engine) StartVanity(host, prefix string) error {
	host = normHost(host)
	np, ok := validVanityPrefix(prefix)
	if !ok {
		return errVanityPrefix
	}
	e.mu.RLock()
	_, served := e.onionByHost[host]
	e.mu.RUnlock()
	if !served {
		return errVanityHost
	}

	e.vanityMu.Lock()
	defer e.vanityMu.Unlock()
	if e.vanity != nil && !e.vanity.done.Load() {
		return errVanityBusy
	}
	ctx, cancel := context.WithCancel(context.Background())
	vs := &vanityState{host: host, prefix: np, startedAt: time.Now(), cancel: cancel}
	e.vanity = vs

	workers := runtime.NumCPU() - 1
	if workers < 1 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		go e.vanityWorker(ctx, vs)
	}
	return nil
}

// vanityWorker brute-forces keypairs until the prefix matches, the context is
// cancelled, or another worker wins.
func (e *Engine) vanityWorker(ctx context.Context, vs *vanityState) {
	seed := make([]byte, 32)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if vs.found.Load() {
			return
		}
		if _, err := rand.Read(seed); err != nil {
			continue
		}
		addr, blob := deriveOnion(seed)
		atomic.AddInt64(&vs.tries, 1)
		if addr != "" && strings.HasPrefix(addr, vs.prefix) {
			// First worker to set found() wins; the rest observe it and exit.
			if vs.found.CompareAndSwap(false, true) {
				vs.addr.Store(addr)
				vs.done.Store(true)
				vs.cancel()
				e.applyVanity(vs.host, addr, blob)
			}
			return
		}
	}
}

// applyVanity persists the new identity and tears down the domain's current
// onion so the next reconcile republishes it under the vanity address.
func (e *Engine) applyVanity(host, addr, blob string) {
	if e.cfg.Store != nil {
		_ = e.cfg.Store.SaveOnion(context.Background(), OnionRecord{Host: host, Address: addr, PrivateKey: blob})
	}
	e.mu.Lock()
	old := e.onionByHost[host]
	ctrl := e.ctrl
	if old != "" {
		delete(e.hostByOnion, normHost(old))
		delete(e.onionByHost, host)
	}
	e.mu.Unlock()
	if ctrl != nil && old != "" {
		_ = ctrl.delOnion(serviceIDOf(old))
	}
	e.Kick() // reconcile republishes host with the persisted vanity key
}

// CancelVanity stops any running search. The already-found result (if any) is
// kept — it was persisted the moment it was found.
func (e *Engine) CancelVanity() {
	e.vanityMu.Lock()
	vs := e.vanity
	e.vanityMu.Unlock()
	if vs != nil && !vs.done.Load() {
		vs.done.Store(true)
		if s := vs.failMsg.Load(); s == nil && !vs.found.Load() {
			vs.failMsg.Store("cancelled")
		}
		vs.cancel()
	}
}

// VanityStatus returns the current search status for the admin page.
func (e *Engine) VanityStatus() VanityStatus {
	if e == nil {
		return VanityStatus{MaxLen: vanityMaxPrefix, RecLen: vanityRecommendedMax}
	}
	e.vanityMu.Lock()
	vs := e.vanity
	e.vanityMu.Unlock()
	st := VanityStatus{MaxLen: vanityMaxPrefix, RecLen: vanityRecommendedMax}
	if vs == nil {
		return st
	}
	st.Host = vs.host
	st.Prefix = vs.prefix
	st.Tries = atomic.LoadInt64(&vs.tries)
	st.Found = vs.found.Load()
	st.Active = !vs.done.Load()
	st.Elapsed = time.Since(vs.startedAt)
	st.Warn = len(vs.prefix) > vanityRecommendedMax
	if a := vs.addr.Load(); a != nil {
		st.Address, _ = a.(string)
	}
	if m := vs.failMsg.Load(); m != nil {
		st.Error, _ = m.(string)
	}
	return st
}
