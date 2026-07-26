// SPDX-License-Identifier: Apache-2.0

package vayutor

// vayutor_sites.go — EXTRA Tor-world site onions (ADR-0141, "one-click add Tor
// site"). Each extra site gets its OWN dedicated onion, minted exactly like the
// primary Anonymous Tor Space onion (reservedSpaceHost) but under a per-site
// reserved store key, all routing to the SAME child loopback target. The child
// distinguishes them by the .onion Host header via its domain registry. This
// path is fully separate from reconcileSpaceOnion, so the primary onion is
// unaffected.

import (
	"context"
	"strings"
)

// reservedSitePrefix namespaces each extra site's persistent onion key so it can
// never collide with a real domain or the primary space onion, and the per-domain
// reap loop never touches it.
const reservedSitePrefix = "__vayuos_tor_site_"

// SpaceSiteOnion returns the live onion for an extra Tor-world site, or "".
func (e *Engine) SpaceSiteOnion(siteID string) string {
	if e == nil {
		return ""
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.spaceSiteHosts[siteID]
}

// reconcileSpaceSites brings the set of extra-site onions into line with the
// desired site list each reconcile: it mints an onion for every wanted site not
// yet live, and tears down onions for sites no longer wanted. No-op when the
// feature is off (SpaceSites nil) or there is no live control connection.
func (e *Engine) reconcileSpaceSites(ctx context.Context) {
	if e.cfg.SpaceSites == nil || e.cfg.SpaceOnionTarget == nil {
		return
	}
	target := strings.TrimSpace(e.cfg.SpaceOnionTarget())

	e.mu.RLock()
	ctrl := e.ctrl
	live := make(map[string]string, len(e.spaceSiteHosts))
	for id, h := range e.spaceSiteHosts {
		live[id] = h
	}
	e.mu.RUnlock()
	if ctrl == nil {
		return // no control connection: nothing can be minted OR torn down this
		// tick, so don't even poll the child — we retry once tor reconnects.
	}

	// Decide which sites should have an onion. When the Space is disabled
	// (target==""), NONE are wanted → every live site onion is torn down below,
	// mirroring reconcileSpaceOnion's remove-on-disable. Otherwise ask the child
	// (an HTTP hop). A non-definitive answer (child briefly unreachable) leaves the
	// live onions untouched so a published .onion never blips on a transient hiccup.
	want := map[string]bool{}
	if target != "" {
		ids, ok := e.cfg.SpaceSites()
		if !ok {
			return
		}
		for _, id := range ids {
			if id = strings.TrimSpace(id); id != "" {
				want[id] = true
			}
		}
	}

	// Tear down onions for sites no longer wanted (all of them when the Space is off).
	for id, host := range live {
		if !want[id] {
			_ = ctrl.delOnion(serviceIDOf(host))
			e.mu.Lock()
			delete(e.spaceSiteHosts, id)
			e.mu.Unlock()
		}
	}

	if target == "" {
		return // Space disabled: teardown done, nothing to mint.
	}

	// Persisted per-site keys, for stable addresses across restarts.
	persisted := map[string]string{}
	if e.cfg.Store != nil {
		if recs, lerr := e.cfg.Store.LoadOnions(ctx); lerr == nil {
			for _, r := range recs {
				h := normHost(r.Host)
				if strings.HasPrefix(h, reservedSitePrefix) {
					persisted[strings.TrimPrefix(h, reservedSitePrefix)] = r.PrivateKey
				}
			}
		}
	}

	// Mint onions for wanted sites not already live.
	for id := range want {
		if live[id] != "" {
			continue
		}
		op, blob := spaceOnionPlan(target, "", persisted[id])
		if op != spaceOnionAdd {
			continue
		}
		on, aerr := ctrl.addOnion(blob, target)
		if aerr != nil {
			// One site failing must not block the others (or the primary onion), and
			// must NOT drop the control connection — record the error and move on.
			e.mu.Lock()
			e.lastErr = "tor-site onion: " + aerr.Error()
			e.mu.Unlock()
			continue
		}
		e.mu.Lock()
		if e.spaceSiteHosts == nil {
			e.spaceSiteHosts = map[string]string{}
		}
		e.spaceSiteHosts[id] = on.host
		e.mu.Unlock()
		if on.privateKey != "" && e.cfg.Store != nil {
			_ = e.cfg.Store.SaveOnion(ctx, OnionRecord{Host: reservedSitePrefix + id, Address: on.host, PrivateKey: on.privateKey})
		}
		if e.cfg.SpaceSiteReady != nil {
			e.cfg.SpaceSiteReady(id, on.host)
		}
	}
}
