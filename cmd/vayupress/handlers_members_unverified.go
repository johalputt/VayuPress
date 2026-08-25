// SPDX-License-Identifier: Apache-2.0

package main

// handlers_members_unverified.go — clearing members who never confirmed.
//
// Membership now begins at verification: the member row is created when the
// emailed magic link comes back (handleMemberVerify), never when it is merely
// requested. Rows created by the older pre-send behaviour can therefore hold
// addresses nobody ever proved control of — a typo, a throwaway domain, or an
// address whose mail bounced outright. They are the only members whose address
// may not be real, so the console lists them and offers removal, individually or
// all at once.
//
// Both endpoints are admin-only and CSRF-checked at the route, and the store
// refuses to delete a verified member, so this can never remove a real account.

import (
	"errors"
	"html"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/members"
)

// unverifiedMembersCardHTML renders the cleanup card. It returns "" when nothing
// is unconfirmed, so a healthy install never carries a warning it cannot act on.
func unverifiedMembersCardHTML(n int) string {
	if n <= 0 {
		return ""
	}
	noun, verb := "address", "was"
	if n != 1 {
		noun, verb = "addresses", "were"
	}
	count := strconv.Itoa(n)
	return `<div class="card mb-6">
  <div class="card-head"><h2 class="card-title">Unconfirmed addresses</h2>
    <button type="button" class="btn btn--sm btn--danger" data-purge-unverified data-count="` + count + `">Remove all ` + count + `</button></div>
  <p class="text-sm muted">` + count + ` ` + html.EscapeString(noun) + ` ` + verb + ` added as members before the sign-in link had been used, so nobody ever proved they control them — a mistyped or undeliverable address ends up here. New signups can no longer reach this state: a member is created only when the emailed link is opened.</p>
  <p class="text-sm muted">Removing one deletes the member record, its sessions and any pending sign-in link. If the person is real and later opens a link, they simply join then. Verified members are never affected.</p>
</div>`
}

// handleMemberDeleteAdmin removes a single never-confirmed member.
// DELETE /api/v1/admin/members/{email}
func (a *App) handleMemberDeleteAdmin(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	email := chi.URLParam(r, "email")
	if err := a.members.Delete(r.Context(), email); err != nil {
		// A verified member is a refusal, not a failure to find them: a confirmed
		// account is a real person and is not this endpoint's business. Matched
		// by sentinel — a string comparison here silently converted a deliberate
		// 409 into a misleading 404 when wording drifted (audit).
		if errors.Is(err, members.ErrVerified) {
			writeAPIError(w, r, http.StatusConflict, "member-verified",
				"That member confirmed their address, so this cannot remove them", "")
			return
		}
		writeAPIError(w, r, http.StatusNotFound, "not-found", "No unconfirmed member with that email", "")
		return
	}
	logging.LogInfo("members", "removed unconfirmed member: "+email)
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "removed"})
}

// handleMembersPurgeUnverified removes every never-confirmed member.
// POST /api/v1/admin/members/unverified/purge
func (a *App) handleMembersPurgeUnverified(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	list, err := a.members.Unverified(r.Context(), 500)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", "Could not list unconfirmed members", "")
		return
	}
	removed, refused, failed := 0, 0, 0
	for _, m := range list {
		switch err := a.members.Delete(r.Context(), m.Email); {
		case err == nil:
			removed++
		case errors.Is(err, members.ErrVerified):
			// Confirmed between listing and delete (opened their link in the
			// meantime) — a refusal, not a failure, but never silent on a
			// destructive bulk path.
			refused++
		default:
			failed++
			logging.LogError("members", "purge could not remove "+m.Email, err.Error())
		}
	}
	logging.LogInfo("members", "purged unconfirmed members: "+strconv.Itoa(removed)+
		" (refused: "+strconv.Itoa(refused)+", failed: "+strconv.Itoa(failed)+")")
	remaining, cerr := a.members.CountUnverified(r.Context())
	if cerr != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", "Purged, but the remaining count could not be read", "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]int{"removed": removed, "refused": refused, "failed": failed, "remaining": remaining})
}
