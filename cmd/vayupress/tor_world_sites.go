// SPDX-License-Identifier: Apache-2.0

package main

// tor_world_sites.go — the CHILD (Tor world) side of "one-click add Tor site"
// (ADR-0141). A Tor site is just a secondary domain in the Tor world's registry;
// it is created with a placeholder host, and the PARENT (which owns the tor
// control port) mints its dedicated .onion and calls back here to rewrite the
// host to that address. These endpoints are how the parent enumerates sites and
// assigns their onions; they only exist in the Tor world (OnionMode) and are
// reached by the parent over the child's own API key (bearer → CSRF-exempt).

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/domain"
)

// torSitePending is the placeholder host prefix a freshly-added Tor site carries
// until the parent assigns its real .onion. It is a syntactically valid host
// (".onion"-less) so the registry accepts it, and unique per site so two pending
// sites never collide before their onions land.
const torSitePending = "pending-"

// newPendingHost mints a unique placeholder host for a just-added Tor site. The
// parent rewrites it to the real .onion once minted; until then it is a harmless,
// non-routable hostname that only the Tor world's own registry ever sees.
func newPendingHost() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return torSitePending + hex.EncodeToString(b) + ".local"
}

// handleTorWorldAddSite is the one-click "Add Tor site" action (ADR-0141). In the
// Tor world the operator never types a host — a new site is registered with a
// placeholder host and the parent mints its dedicated .onion automatically. This
// only creates the registry row and picks what it serves (blog and/or mail); the
// onion is assigned asynchronously by the engine's reconcile via handleTorWorldAssign.
func (a *App) handleTorWorldAddSite(w http.ResponseWriter, r *http.Request) {
	if !config.Cfg.OnionMode || a.domains == nil {
		writeAPIError(w, r, http.StatusNotFound, "not-tor", "Tor sites exist only in the Tor world", "")
		return
	}
	var req struct {
		SiteType    string `json:"site_type"`
		MailEnabled bool   `json:"mail_enabled"`
	}
	// Body is optional — an empty POST creates a plain blog site.
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req)
	siteType := strings.TrimSpace(req.SiteType)
	switch siteType {
	case domain.SiteBlog, domain.SiteBusiness, domain.SiteBusinessSubpath, domain.SiteStatic, domain.SiteMailOnly:
	default:
		siteType = domain.SiteBlog
	}
	d, err := a.domains.Create(r.Context(), newPendingHost(), siteType, req.MailEnabled)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "create_failed", err.Error(), "")
		return
	}
	// The dedicated .onion is minted by the PARENT's tor engine (the only process
	// with a control port) on its next reconcile, then handed back via
	// handleTorWorldAssign — nothing to do here but record the pending row.
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "id": d.ID, "host": d.Host})
}

// handleTorWorldSites lists every Tor site (secondary domain) so the parent can
// mint an onion for each. Tor world only; admin/bearer-authed.
func (a *App) handleTorWorldSites(w http.ResponseWriter, r *http.Request) {
	if !config.Cfg.OnionMode || a.domains == nil {
		writeAPIError(w, r, http.StatusNotFound, "not-tor", "Tor sites exist only in the Tor world", "")
		return
	}
	all, err := a.domains.List(r.Context())
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "list_failed", "could not list sites", "")
		return
	}
	type site struct {
		ID          string `json:"id"`
		Host        string `json:"host"`
		SiteType    string `json:"site_type"`
		MailEnabled bool   `json:"mail_enabled"`
		Pending     bool   `json:"pending"`
	}
	sites := make([]site, 0, len(all))
	for _, d := range all {
		if d.IsPrimary {
			continue // the primary is the child's own dedicated onion, minted separately
		}
		sites = append(sites, site{
			ID: d.ID, Host: d.Host, SiteType: d.SiteType, MailEnabled: d.MailEnabled,
			Pending: strings.HasPrefix(d.Host, torSitePending),
		})
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"sites": sites})
}

// handleTorWorldAssign rewrites a Tor site's host to its freshly-minted .onion and
// makes sure it is active. Idempotent (assigning the same host again is a no-op).
// Tor world only; admin/bearer-authed.
func (a *App) handleTorWorldAssign(w http.ResponseWriter, r *http.Request) {
	if !config.Cfg.OnionMode || a.domains == nil {
		writeAPIError(w, r, http.StatusNotFound, "not-tor", "Tor sites exist only in the Tor world", "")
		return
	}
	var req struct {
		ID   string `json:"id"`
		Host string `json:"host"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "id and host are required", "")
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	req.Host = strings.TrimSpace(strings.ToLower(req.Host))
	if req.ID == "" || !strings.HasSuffix(req.Host, ".onion") {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "id and a .onion host are required", "")
		return
	}
	if err := a.domains.SetHost(r.Context(), req.ID, req.Host); err != nil {
		// A duplicate host (already assigned this onion) is fine — treat as success.
		if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeAPIError(w, r, http.StatusBadRequest, "assign_failed", err.Error(), "")
			return
		}
	}
	_ = a.domains.SetStatus(r.Context(), req.ID, domain.StatusActive)
	_ = a.domains.SetSyncState(r.Context(), req.ID, domain.SyncApproved)
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true})
}
