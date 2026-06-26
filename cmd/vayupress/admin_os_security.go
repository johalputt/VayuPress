package main

// admin_os_security.go — VayuOS Members page + TOTP two-factor (ADR-0068, Phase 5).
//
// TOTP enrolment is a two-step ceremony so a half-finished setup never locks an
// operator out: (1) begin → a secret is generated and stored disabled, the
// otpauth URI + manual key are shown; (2) verify → the operator's authenticator
// code is checked and only then is 2FA enabled. Disable clears the secret.
//
// Sign-in enforcement lives in verifyTOTPForLogin and is wired into both the v2
// and os login submit handlers, so an enrolled account cannot bypass 2FA via the
// older surface.

import (
	"context"
	"encoding/json"
	"html"
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/totp"
)

// verifyTOTPForLogin decides whether a login may proceed. It returns required=true
// when the account has 2FA enabled; ok reflects whether the supplied code is
// valid. When 2FA is not enabled, required=false and ok=true (no code needed).
func (a *App) verifyTOTPForLogin(ctx context.Context, email, code string) (ok, required bool) {
	if a.userStore == nil {
		return true, false
	}
	secret, enabled, err := a.userStore.TOTPSecretByEmail(ctx, email)
	if err != nil || !enabled || secret == "" {
		return true, false
	}
	return totp.Validate(secret, code), true
}

// ── Members page ───────────────────────────────────────────────────────────

func (a *App) handleOSMembers(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	if a.members == nil {
		body := `<div class="page-header"><h1>Members</h1></div>
<div class="empty-state">Memberships are not enabled on this instance.</div>`
		writeOSHTML(w, adminOSLayout(nonce, "Members", "members", cfg, htmpl.HTML(body)))
		return
	}

	ctx := r.Context()
	stats, _ := a.members.Stats(ctx)
	if stats == nil {
		stats = &members.Stats{ByTier: map[string]int{}, Currency: "USD"}
	}
	tiers, _ := a.members.ListTiers(ctx, true)
	list, _ := a.members.List(ctx, 100)

	esc := html.EscapeString

	stat := func(label, value string) string {
		return `<div class="stat-card"><div class="stat-card__label">` + esc(label) +
			`</div><div class="stat-card__value">` + value + `</div></div>`
	}

	// ── Stat cards ──────────────────────────────────────────────────────────
	statGrid := `<div class="stat-grid mb-6">` +
		stat("Total members", strconv.Itoa(stats.Total)) +
		stat("Free", strconv.Itoa(stats.Free)) +
		stat("Paid", strconv.Itoa(stats.Paid)) +
		stat("MRR", priceLabel(stats.Currency, stats.MRRCents)) +
		stat("New · 30 days", strconv.Itoa(stats.NewLast30)) +
		`</div>`

	// ── Tiers card ──────────────────────────────────────────────────────────
	tierRows := ""
	for _, t := range tiers {
		price := "Free"
		if !t.IsFree() {
			price = priceLabel(t.Currency, t.MonthlyCents) + " / mo"
			if t.MonthlyCents == 0 && t.YearlyCents > 0 {
				price = priceLabel(t.Currency, t.YearlyCents) + " / yr"
			}
		}
		vis := `<span class="badge badge--muted">` + esc(t.Visibility) + `</span>`
		status := `<span class="badge badge--ok">active</span>`
		if !t.Active {
			status = `<span class="badge badge--muted">archived</span>`
		}
		actions := `<button class="btn btn--sm btn--ghost" type="button" data-edit-tier
			data-id="` + esc(t.ID) + `" data-name="` + esc(t.Name) + `" data-description="` + esc(t.Description) + `"
			data-monthly="` + strconv.Itoa(t.MonthlyCents) + `" data-yearly="` + strconv.Itoa(t.YearlyCents) + `"
			data-currency="` + esc(t.Currency) + `" data-visibility="` + esc(t.Visibility) + `"
			data-benefits="` + esc(strings.Join(t.Benefits, "\n")) + `">Edit</button>`
		if t.Slug != members.TierFree && t.Slug != members.TierPaid {
			actions += ` <button class="btn btn--sm btn--danger" type="button" data-archive-tier data-id="` + esc(t.ID) + `">Archive</button>`
		}
		tierRows += `<tr>
  <td class="row-title">` + esc(t.Name) + ` <span class="row-meta">` + esc(t.Slug) + `</span></td>
  <td>` + esc(price) + `</td>
  <td>` + vis + `</td>
  <td>` + status + `</td>
  <td class="row-actions">` + actions + `</td>
</tr>`
	}
	tierTable := `<div class="empty-state">No tiers yet.</div>`
	if tierRows != "" {
		tierTable = `<div class="table-wrap"><table class="table">
  <thead><tr><th>Plan</th><th>Price</th><th>Visibility</th><th>Status</th><th></th></tr></thead>
  <tbody>` + tierRows + `</tbody></table></div>`
	}
	tiersCard := `<div class="card mb-6">
  <div class="card-head">
    <h2 class="card-title">Membership tiers</h2>
    <button class="btn btn--primary btn--sm" type="button" data-new-tier>+ New tier</button>
  </div>
  ` + tierTable + `
  <p class="field-hint mt-3">Tiers appear on your public <a href="/pricing">pricing page</a>. The built-in Free and Premium plans cannot be removed.</p>
</div>`

	// ── Members table ───────────────────────────────────────────────────────
	tierOptions := func(current string) string {
		opts := ""
		seen := map[string]bool{}
		for _, t := range tiers {
			sel := ""
			if t.Slug == current {
				sel = " selected"
			}
			opts += `<option value="` + esc(t.Slug) + `"` + sel + `>` + esc(t.Name) + `</option>`
			seen[t.Slug] = true
		}
		if !seen[current] && current != "" {
			opts += `<option value="` + esc(current) + `" selected>` + esc(current) + `</option>`
		}
		return opts
	}
	rows := ""
	for _, m := range list {
		badge := `<span class="badge badge--free">free</span>`
		if m.IsPaid() {
			badge = `<span class="badge badge--paid">` + esc(m.Tier) + `</span>`
		}
		labelChips := ""
		for _, l := range m.Labels {
			labelChips += `<span class="chip chip--removable">` + esc(l) +
				`<button type="button" data-remove-label data-email="` + esc(m.Email) + `" data-label="` + esc(l) + `" aria-label="Remove label">×</button></span> `
		}
		labelChips += `<button type="button" class="btn btn--xs btn--ghost" data-add-label data-email="` + esc(m.Email) + `">+ label</button>`
		lastSeen := `<span class="row-meta">never</span>`
		if m.LastSeenAt != nil {
			lastSeen = m.LastSeenAt.Format("2 Jan 2006")
		}
		name := esc(m.Name)
		if name == "" {
			name = `<span class="row-meta">—</span>`
		}
		rows += `<tr>
  <td class="row-title">` + esc(m.Email) + `</td>
  <td>` + name + `</td>
  <td>` + badge + `</td>
  <td><select class="select input--sm" data-member-tier data-email="` + esc(m.Email) + `">` + tierOptions(m.Tier) + `</select></td>
  <td>` + labelChips + `</td>
  <td class="row-meta">` + lastSeen + `</td>
  <td class="row-meta">` + m.CreatedAt.UTC().Format("2 Jan 2006") + `</td>
</tr>`
	}
	membersTable := `<div class="empty-state">No members yet.</div>`
	if rows != "" {
		membersTable = `<div class="table-wrap"><table class="table">
  <thead><tr><th>Email</th><th>Name</th><th>Tier</th><th>Plan</th><th>Labels</th><th>Last seen</th><th>Joined</th></tr></thead>
  <tbody>` + rows + `</tbody></table></div>`
	}
	membersCard := `<div class="card"><h2 class="card-title">Members</h2>` + membersTable + `</div>`

	// ── Team & roles (admin-only; staff accounts, not readers) ────────────────
	teamCard := a.teamCardHTML(r)

	// ── Tier editor modal ─────────────────────────────────────────────────────
	modal := `<div class="modal-backdrop" id="tier-modal" hidden>
  <div class="modal-panel">
    <div class="modal-header"><h3 class="modal-title" id="tier-modal-title">New tier</h3>
      <button class="modal-close" type="button" id="tier-cancel" aria-label="Close">×</button></div>
    <form id="tier-form">
      <div class="modal-body">
        <input type="hidden" id="tier-id">
        <div class="field"><label class="field-label" for="tier-name">Name</label>
          <input class="input" id="tier-name" type="text" required maxlength="60" placeholder="e.g. Premium"></div>
        <div class="field mt-3"><label class="field-label" for="tier-desc">Description</label>
          <input class="input" id="tier-desc" type="text" maxlength="200" placeholder="Short summary shown on the pricing page"></div>
        <div class="grid grid-2 gap-3 mt-3">
          <div class="field"><label class="field-label" for="tier-monthly">Monthly price (cents)</label>
            <input class="input" id="tier-monthly" type="number" min="0" value="0"></div>
          <div class="field"><label class="field-label" for="tier-yearly">Yearly price (cents)</label>
            <input class="input" id="tier-yearly" type="number" min="0" value="0"></div>
        </div>
        <div class="grid grid-2 gap-3 mt-3">
          <div class="field"><label class="field-label" for="tier-currency">Currency</label>
            <input class="input" id="tier-currency" type="text" maxlength="3" value="USD"></div>
          <div class="field"><label class="field-label" for="tier-visibility">Visibility</label>
            <select class="select" id="tier-visibility"><option value="public">Public</option><option value="hidden">Hidden</option></select></div>
        </div>
        <div class="field mt-3"><label class="field-label" for="tier-benefits">Benefits (one per line)</label>
          <textarea class="textarea" id="tier-benefits" rows="4" placeholder="Full access to premium posts&#10;Members-only newsletter"></textarea></div>
      </div>
      <div class="modal-footer">
        <button class="btn btn--ghost" type="button" id="tier-cancel-2">Cancel</button>
        <button class="btn btn--primary" type="submit" id="tier-save">Save tier</button>
      </div>
    </form>
  </div>
</div>`

	body := `<div class="page-header"><h1>Members</h1></div>` +
		statGrid + tiersCard + teamCard + membersCard + modal +
		`<script nonce="` + nonce + `" src="/os/static/js/admin-os-members.js"></script>`

	writeOSHTML(w, adminOSLayout(nonce, "Members", "members", cfg, htmpl.HTML(body)))
}

// ── Security page (TOTP) ────────────────────────────────────────────────────

func (a *App) handleOSSecurity(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	u := currentUser(r)
	if u == nil || a.userStore == nil {
		// API-key session (no user record): 2FA is per-account, so explain.
		body := `<div class="page-header"><h1>Security</h1></div>
<div class="card"><p class="muted">Two-factor authentication applies to password accounts. You are signed in with an API key.</p></div>`
		writeOSHTML(w, adminOSLayout(nonce, "Security", "security", cfg, htmpl.HTML(body)))
		return
	}

	_, enabled, _ := a.userStore.TOTPStatus(r.Context(), u.ID)

	var section string
	if enabled {
		section = `<div class="settings-row">
    <div class="settings-row-info">
      <div class="settings-row-label">Two-factor authentication</div>
      <div class="settings-row-hint">Active — a code from your authenticator is required at sign-in.</div>
    </div>
    <span class="badge badge--ok">Enabled</span>
  </div>
  <div class="mt-4">
    <button type="button" class="btn btn--danger btn--sm" data-totp-disable>Disable 2FA</button>
  </div>`
	} else {
		section = `<div class="settings-row">
    <div class="settings-row-info">
      <div class="settings-row-label">Two-factor authentication</div>
      <div class="settings-row-hint">Add a time-based one-time code (TOTP) from any authenticator app.</div>
    </div>
    <span class="badge badge--warn">Disabled</span>
  </div>
  <div class="mt-4">
    <button type="button" class="btn btn--primary btn--sm" data-totp-begin>Set up 2FA</button>
  </div>
  <div class="totp-enroll" data-totp-enroll hidden>
    <div class="section-divider"></div>
    <div class="settings-block-title">Scan or enter this key</div>
    <p class="text-sm muted">Add this account to your authenticator app, then enter the 6-digit code to confirm.</p>
    <div class="totp-key"><code data-totp-key class="font-mono"></code></div>
    <div class="totp-uri text-xs muted"><a data-totp-uri href="#" rel="noopener">Open in authenticator</a></div>
    <div class="field mt-3">
      <label class="field-label" for="totp-code">Verification code</label>
      <input id="totp-code" class="input" type="text" inputmode="numeric" autocomplete="one-time-code"
        maxlength="6" placeholder="000000" data-totp-code>
    </div>
    <button type="button" class="btn btn--primary btn--sm" data-totp-verify>Verify &amp; enable</button>
  </div>`
	}

	body := `<div class="page-header"><h1>Security</h1></div>
<div class="card" data-totp-card>` + section + `</div>
<script nonce="` + nonce + `" src="/os/static/js/admin-os-security.js"></script>`

	writeOSHTML(w, adminOSLayout(nonce, "Security", "security", cfg, htmpl.HTML(body)))
}

// handleOSTOTPBegin generates a fresh secret (stored disabled) and returns the
// provisioning URI + manual key for the current user.
func (a *App) handleOSTOTPBegin(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || a.userStore == nil {
		writeAPIError(w, r, http.StatusForbidden, "no-account", "2FA requires a password account", "")
		return
	}
	secret, err := totp.GenerateSecret()
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "secret-error", err.Error(), "")
		return
	}
	if err := a.userStore.SetTOTPSecret(r.Context(), u.ID, secret); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "store-error", err.Error(), "")
		return
	}
	uri := totp.ProvisioningURI(secret, "VayuPress", u.Email)
	writeJSON(w, r, http.StatusOK, map[string]string{"secret": secret, "uri": uri})
}

// handleOSTOTPVerify checks the submitted code against the pending secret and,
// on success, enables 2FA.
func (a *App) handleOSTOTPVerify(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || a.userStore == nil {
		writeAPIError(w, r, http.StatusForbidden, "no-account", "2FA requires a password account", "")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	secret, _, err := a.userStore.TOTPStatus(r.Context(), u.ID)
	if err != nil || secret == "" {
		writeAPIError(w, r, http.StatusBadRequest, "no-pending", "Start 2FA setup first", "")
		return
	}
	if !totp.Validate(secret, strings.TrimSpace(body.Code)) {
		writeAPIError(w, r, http.StatusBadRequest, "bad-code", "That code is not valid — try again", "")
		return
	}
	if err := a.userStore.EnableTOTP(r.Context(), u.ID); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "enable-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "enabled"})
}

// handleOSTOTPDisable clears 2FA for the current user.
func (a *App) handleOSTOTPDisable(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || a.userStore == nil {
		writeAPIError(w, r, http.StatusForbidden, "no-account", "2FA requires a password account", "")
		return
	}
	if err := a.userStore.DisableTOTP(r.Context(), u.ID); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "disable-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "disabled"})
}
