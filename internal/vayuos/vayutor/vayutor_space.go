package vayutor

// vayutor_space.go — the ONE dedicated onion for the VayuOS Anonymous Tor Space
// (ADR-0141). Unlike the per-domain onions (which mirror clearnet hosts and are
// reaped when a host goes away), this onion fronts the isolated Tor-Space child
// instance on a loopback port. It is minted with its OWN persistent key under a
// RESERVED store host that never appears in the served-domain set, and is kept
// out of the per-domain map — so the reconcile reap loop can never tear it down
// and its address stays stable across restarts.

import (
	"context"
	"strings"
)

// reservedSpaceHost is the store key for the dedicated onion's persistent
// identity. It is not a real domain and never enters the want/served set, so the
// domain reconcile never mints, reaps, or collides with it.
const reservedSpaceHost = "__vayuos_tor_space"

// spaceOnionOp is the action reconcile should take for the dedicated onion.
type spaceOnionOp int

const (
	spaceOnionNoop spaceOnionOp = iota
	spaceOnionAdd
	spaceOnionRemove
)

// spaceOnionPlan is the pure decision core: given the desired target, the
// currently-live dedicated onion host, and any persisted key, decide what to do
// and (for an add) which key blob to use. Pure and fully unit-testable.
func spaceOnionPlan(target, liveHost, persistedKey string) (spaceOnionOp, string) {
	if target == "" {
		if liveHost != "" {
			return spaceOnionRemove, ""
		}
		return spaceOnionNoop, ""
	}
	if liveHost != "" {
		return spaceOnionNoop, "" // already live for this session
	}
	if persistedKey != "" {
		return spaceOnionAdd, persistedKey // restore the SAME address
	}
	return spaceOnionAdd, "NEW:ED25519-V3"
}

// SpaceOnion returns the live Anonymous Tor Space onion address, or "".
func (e *Engine) SpaceOnion() string {
	if e == nil {
		return ""
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.spaceOnionHost
}

// reconcileSpaceOnion brings the dedicated onion into line with the desired
// target each reconcile. No-op when the feature is off (SpaceOnionTarget nil).
func (e *Engine) reconcileSpaceOnion(ctx context.Context) {
	if e.cfg.SpaceOnionTarget == nil {
		return
	}
	target := strings.TrimSpace(e.cfg.SpaceOnionTarget())

	e.mu.RLock()
	live := e.spaceOnionHost
	ctrl := e.ctrl
	e.mu.RUnlock()
	if ctrl == nil {
		return
	}

	// Look up the persisted key for a stable address.
	persistedKey := ""
	if e.cfg.Store != nil {
		if recs, lerr := e.cfg.Store.LoadOnions(ctx); lerr == nil {
			for _, r := range recs {
				if normHost(r.Host) == reservedSpaceHost {
					persistedKey = r.PrivateKey
					break
				}
			}
		}
	}

	op, blob := spaceOnionPlan(target, live, persistedKey)
	switch op {
	case spaceOnionRemove:
		_ = ctrl.delOnion(serviceIDOf(live))
		e.mu.Lock()
		e.spaceOnionHost = ""
		e.mu.Unlock()
	case spaceOnionAdd:
		on, aerr := ctrl.addOnion(blob, target)
		if aerr != nil {
			e.setErr("tor-space onion: " + aerr.Error())
			return
		}
		e.mu.Lock()
		e.spaceOnionHost = on.host
		e.mu.Unlock()
		// Persist a freshly-minted identity so the address survives restarts.
		if on.privateKey != "" && e.cfg.Store != nil {
			_ = e.cfg.Store.SaveOnion(ctx, OnionRecord{Host: reservedSpaceHost, Address: on.host, PrivateKey: on.privateKey})
		}
		if e.cfg.SpaceOnionReady != nil {
			e.cfg.SpaceOnionReady(on.host)
		}
	case spaceOnionNoop:
	}
}
