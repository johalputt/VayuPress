package main

// admin_v3_security.go — Admin v3 Members page + TOTP two-factor (ADR-0068, Phase 5).
//
// TOTP enrolment is a two-step ceremony so a half-finished setup never locks an
// operator out: (1) begin → a secret is generated and stored disabled, the
// otpauth URI + manual key are shown; (2) verify → the operator's authenticator
// code is checked and only then is 2FA enabled. Disable clears the secret.
//
// Sign-in enforcement lives in verifyTOTPForLogin and is wired into both the v2
// and v3 login submit handlers, so an enrolled account cannot bypass 2FA via the
// older surface.

import (
	"context"
	"encoding/json"
	"html"
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"

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

func (a *App) handleV3Members(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getV3Settings(r.Context())

	if a.members == nil {
		body := `<div class="page-header"><h1>Members</h1></div>
<div class="empty-state">Memberships are not enabled on this instance.</div>`
		writeV3HTML(w, adminV3Layout(nonce, "Members", "members", cfg, htmpl.HTML(body)))
		return
	}

	counts, _ := a.members.Count(r.Context())
	total := 0
	for _, n := range counts {
		total += n
	}
	list, _ := a.members.List(r.Context(), 100)

	stat := func(label string, n int) string {
		return `<div class="stat-card"><div class="stat-card__label">` + html.EscapeString(label) +
			`</div><div class="stat-card__value">` + strconv.Itoa(n) + `</div></div>`
	}

	rows := ""
	for _, m := range list {
		rows += `<tr>
  <td class="row-title">` + html.EscapeString(m.Email) + `</td>
  <td><span class="badge badge--ok">` + html.EscapeString(m.Tier) + `</span></td>
  <td class="muted text-sm">` + m.CreatedAt.UTC().Format("2 Jan 2006") + `</td>
</tr>`
	}
	tableHTML := `<div class="empty-state">No members yet.</div>`
	if rows != "" {
		tableHTML = `<div class="table-wrap"><table class="table">
  <thead><tr><th>Email</th><th>Tier</th><th>Joined</th></tr></thead>
  <tbody>` + rows + `</tbody></table></div>`
	}

	body := `<div class="page-header"><h1>Members</h1></div>
<div class="stat-grid mb-6">` +
		stat("Total", total) +
		stat("Free", counts["free"]) +
		stat("Paid", counts["paid"]) +
		`</div>
<div class="card">` + tableHTML + `</div>`

	writeV3HTML(w, adminV3Layout(nonce, "Members", "members", cfg, htmpl.HTML(body)))
}

// ── Security page (TOTP) ────────────────────────────────────────────────────

func (a *App) handleV3Security(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getV3Settings(r.Context())

	u := currentUser(r)
	if u == nil || a.userStore == nil {
		// API-key session (no user record): 2FA is per-account, so explain.
		body := `<div class="page-header"><h1>Security</h1></div>
<div class="card"><p class="muted">Two-factor authentication applies to password accounts. You are signed in with an API key.</p></div>`
		writeV3HTML(w, adminV3Layout(nonce, "Security", "security", cfg, htmpl.HTML(body)))
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
<script nonce="` + nonce + `" src="/os/static/js/admin-v3-security.js"></script>`

	writeV3HTML(w, adminV3Layout(nonce, "Security", "security", cfg, htmpl.HTML(body)))
}

// handleV3TOTPBegin generates a fresh secret (stored disabled) and returns the
// provisioning URI + manual key for the current user.
func (a *App) handleV3TOTPBegin(w http.ResponseWriter, r *http.Request) {
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

// handleV3TOTPVerify checks the submitted code against the pending secret and,
// on success, enables 2FA.
func (a *App) handleV3TOTPVerify(w http.ResponseWriter, r *http.Request) {
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

// handleV3TOTPDisable clears 2FA for the current user.
func (a *App) handleV3TOTPDisable(w http.ResponseWriter, r *http.Request) {
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
