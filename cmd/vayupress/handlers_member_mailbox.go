package main

// handlers_member_mailbox.go — a paid member self-provisions the VayuMail mailbox
// their tier includes (Phase 2b). The member picks a GENERIC address (reserved
// admin/role names are refused — see internal/vayuos/mail/reserved.go), sets a
// password, and gets a real mailbox with an auto PGP keypair + WKD and their
// tier's storage quota. The member cookie is SameSite=Lax, so a cross-site POST
// never carries it (CSRF-safe), matching the other member endpoints.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/vayuos/mail"
)

// mailboxEntitlement returns the member's tier and quota (bytes) when it includes
// a mailbox; ok=false for a free tier or a paid tier without a mailbox.
func (a *App) mailboxEntitlement(ctx context.Context, m *members.Member) (tier *members.Tier, quotaBytes int64, ok bool) {
	if m == nil || a.members == nil || !m.IsPaid() {
		return nil, 0, false
	}
	t, err := a.members.GetTier(ctx, m.Tier)
	if err != nil || t == nil || !t.MailEnabled {
		return nil, 0, false
	}
	return t, t.MailQuotaBytes(), true
}

// mailHost returns the primary VayuMail domain member mailboxes are created on,
// or "" when mail is unavailable.
func (a *App) mailHost() string {
	if a.vayuMail == nil {
		return ""
	}
	return a.vayuMail.Config().Domain
}

// mailboxLocalpartCheck validates a desired localpart for a member self-claim,
// returning ok plus a human reason when not.
func (a *App) mailboxLocalpartCheck(ctx context.Context, local string) (bool, string) {
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		return false, "Mail is not available."
	}
	if !mail.ValidLocalpart(local) {
		return false, "Use letters, numbers and . _ - only (no spaces)."
	}
	if mail.IsReservedLocalpart(local) {
		return false, "That address is reserved."
	}
	// Premium (vanity) names are the operator's sellable inventory and are not
	// handed out on the free claim path — they are purchased (Phase 3 marketplace).
	if mail.IsPremiumLocalpart(local) {
		return false, "That's a premium address — it isn't included on the free claim."
	}
	host := a.mailHost()
	if host == "" {
		return false, "Mail is not available."
	}
	if a.vayuMail.Accounts().Exists(ctx, local+"@"+host) {
		return false, "That address is already taken."
	}
	return true, ""
}

// GET /api/v1/members/mailbox — the member's mailbox entitlement + current state.
func (a *App) handleMemberMailboxStatus(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "members-only", "Please sign in", "")
		return
	}
	host := a.mailHost()
	tier, _, ok := a.mailboxEntitlement(r.Context(), m)
	resp := map[string]interface{}{
		"entitled":    ok && host != "",
		"domain":      host,
		"has_mailbox": false,
		"address":     "",
		"quota_mb":    0,
	}
	if ok {
		resp["quota_mb"] = tier.MailQuotaMB
	}
	if a.members != nil {
		if addr := a.members.MailAddressFor(r.Context(), m.Email); addr != "" {
			resp["has_mailbox"] = true
			resp["address"] = addr
		}
	}
	writeJSON(w, r, http.StatusOK, resp)
}

// GET /api/v1/members/mailbox/available?localpart=X — live availability check.
func (a *App) handleMemberMailboxAvailable(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "members-only", "Please sign in", "")
		return
	}
	local := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("localpart")))
	available, reason := a.mailboxLocalpartCheck(r.Context(), local)
	// premium flags a well-formed, non-reserved vanity name so the portal can
	// surface it as sellable inventory rather than a plain "unavailable".
	premium := mail.ValidLocalpart(local) && !mail.IsReservedLocalpart(local) && mail.IsPremiumLocalpart(local)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"localpart": local, "available": available, "reason": reason, "premium": premium})
}

// POST /api/v1/members/mailbox/claim {localpart, password} — provisions the
// mailbox the member's tier includes.
func (a *App) handleMemberMailboxClaim(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "members-only", "Please sign in", "")
		return
	}
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	tier, quotaBytes, ok := a.mailboxEntitlement(r.Context(), m)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "not-entitled", "Your plan does not include a mailbox.", "")
		return
	}
	if a.members.MailAddressFor(r.Context(), m.Email) != "" {
		writeAPIError(w, r, http.StatusConflict, "already-claimed", "You already have a mailbox.", "")
		return
	}
	var in struct {
		Localpart string `json:"localpart"`
		Password  string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	local := strings.ToLower(strings.TrimSpace(in.Localpart))
	if okc, reason := a.mailboxLocalpartCheck(r.Context(), local); !okc {
		writeAPIError(w, r, http.StatusBadRequest, "bad-localpart", reason, "")
		return
	}
	if len(in.Password) < 8 {
		writeAPIError(w, r, http.StatusBadRequest, "weak-password", "Password must be at least 8 characters.", "")
		return
	}
	hash, err := auth.HashSecretArgon2id(in.Password)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "hash-failed", "Could not secure the password", "")
		return
	}
	email, perr := a.provisionMailbox(r.Context(), "", local, a.mailHost(), hash, m.DisplayName(), mail.RoleMailbox, quotaBytes)
	if perr != nil {
		writeAPIError(w, r, http.StatusBadRequest, "provision-failed", perr.Error(), "")
		return
	}
	_ = a.members.SetMailAddress(r.Context(), m.Email, email)
	logging.LogInfo("members", "member claimed mailbox "+email)
	writeJSON(w, r, http.StatusCreated, map[string]interface{}{"address": email, "quota_mb": tier.MailQuotaMB})
}
