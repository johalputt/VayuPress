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
	"fmt"
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
	signups, _ := a.members.SignupsByDay(ctx, 30)
	revenue, _ := a.members.RevenueByTier(ctx)
	activity, _ := a.members.RecentEvents(ctx, 12)

	esc := html.EscapeString

	stat := func(label, value, sub string) string {
		subHTML := ""
		if sub != "" {
			subHTML = `<div class="stat-card__sub">` + sub + `</div>`
		}
		return `<div class="stat-card"><div class="stat-card__label">` + esc(label) +
			`</div><div class="stat-card__value">` + value + `</div>` + subHTML + `</div>`
	}

	// Net MRR movement (last 30 days), coloured by direction.
	movement := priceLabel(stats.Currency, stats.NetMRRMovementCents) + " · 30d"
	if stats.NetMRRMovementCents > 0 {
		movement = `<span class="trend trend--up">▲ ` + priceLabel(stats.Currency, stats.NetMRRMovementCents) + `</span> · 30d`
	} else if stats.NetMRRMovementCents < 0 {
		movement = `<span class="trend trend--down">▼ ` + priceLabel(stats.Currency, -stats.NetMRRMovementCents) + `</span> · 30d`
	}

	// ── Stat cards — the revenue & retention picture at a glance ──────────────
	statGrid := `<div class="stat-grid mb-6">` +
		stat("MRR", priceLabel(stats.Currency, stats.MRRCents), movement) +
		stat("ARR", priceLabel(stats.Currency, stats.ARRCents), "annual run-rate") +
		stat("Paid members", strconv.Itoa(stats.Paid), strconv.Itoa(stats.Trialing)+" in trial") +
		stat("Total members", strconv.Itoa(stats.Total), "+"+strconv.Itoa(stats.NewLast30)+" · 30d") +
		stat("Conversion", formatPercent(stats.ConversionRate), "free → paid") +
		stat("Churn · 30d", formatPercent(stats.ChurnRate30), strconv.Itoa(stats.CanceledLast30)+" canceled") +
		stat("ARPU", priceLabel(stats.Currency, stats.ARPUCents), "per paid member") +
		stat("LTV", priceLabel(stats.Currency, stats.LTVCents), "est. lifetime value") +
		`</div>`

	// ── Insights: growth sparkline + revenue by tier ──────────────────────────
	insightsCard := `<div class="card mb-6">
  <div class="card-head"><h2 class="card-title">Growth &amp; revenue</h2>
    <a class="btn btn--sm btn--ghost" href="/os/api/members/export.csv" download>Export CSV</a></div>
  <div class="grid grid-2 gap-4">
    <div>
      <div class="field-label">New members · last 30 days</div>
      ` + sparklineSVG(signups) + `
    </div>
    <div>
      <div class="field-label">Monthly recurring revenue by tier</div>
      ` + revenueByTierHTML(revenue, stats.Currency, stats.MRRCents) + `
    </div>
  </div>
</div>`

	// ── Recent activity feed ──────────────────────────────────────────────────
	activityCard := `<div class="card mb-6"><h2 class="card-title">Recent activity</h2>` +
		activityFeedHTML(activity, stats.Currency) + `</div>`

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
		if t.TrialDays > 0 {
			price += ` <span class="badge badge--muted">` + strconv.Itoa(t.TrialDays) + `-day trial</span>`
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
			data-trial="` + strconv.Itoa(t.TrialDays) + `" data-stripe-monthly="` + esc(t.StripeMonthlyPrice) + `" data-stripe-yearly="` + esc(t.StripeYearlyPrice) + `"
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
		actions := `<span class="row-meta">—</span>`
		if m.IsPaid() {
			actions = `<button type="button" class="btn btn--xs btn--danger" data-cancel-member data-email="` + esc(m.Email) + `">Cancel</button>`
		}
		// data-search lets the client-side filter match on email, name and labels.
		searchKey := esc(strings.ToLower(m.Email + " " + m.Name + " " + strings.Join(m.Labels, " ")))
		rows += `<tr data-member-row data-search="` + searchKey + `">
  <td class="row-title">` + esc(m.Email) + `</td>
  <td>` + name + `</td>
  <td>` + badge + `</td>
  <td><select class="select input--sm" data-member-tier data-email="` + esc(m.Email) + `">` + tierOptions(m.Tier) + `</select></td>
  <td>` + labelChips + `</td>
  <td class="row-meta">` + lastSeen + `</td>
  <td class="row-meta">` + m.CreatedAt.UTC().Format("2 Jan 2006") + `</td>
  <td class="row-actions">` + actions + `</td>
</tr>`
	}
	membersTable := `<div class="empty-state">No members yet.</div>`
	if rows != "" {
		membersTable = `<div class="table-wrap"><table class="table">
  <thead><tr><th>Email</th><th>Name</th><th>Tier</th><th>Plan</th><th>Labels</th><th>Last seen</th><th>Joined</th><th></th></tr></thead>
  <tbody data-members-body>` + rows + `</tbody></table></div>`
	}
	membersCard := `<div class="card">
  <div class="card-head"><h2 class="card-title">Members</h2>
    <input class="input input--sm" type="search" placeholder="Search members…" data-member-search aria-label="Search members" style="max-width:16rem">
  </div>
  <div class="empty-state" data-members-empty hidden>No members match your search.</div>` + membersTable + `</div>`

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
        <div class="field mt-3"><label class="field-label" for="tier-trial">Free trial (days)</label>
          <input class="input" id="tier-trial" type="number" min="0" value="0">
          <p class="field-hint">New members on this tier get full access for this many days before being charged. 0 disables the trial.</p></div>
        <div class="grid grid-2 gap-3 mt-3">
          <div class="field"><label class="field-label" for="tier-stripe-monthly">Stripe monthly price ID</label>
            <input class="input" id="tier-stripe-monthly" type="text" maxlength="80" placeholder="price_…"></div>
          <div class="field"><label class="field-label" for="tier-stripe-yearly">Stripe yearly price ID</label>
            <input class="input" id="tier-stripe-yearly" type="text" maxlength="80" placeholder="price_…"></div>
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
		statGrid + insightsCard + activityCard + tiersCard + teamCard + membersCard + modal +
		`<script nonce="` + nonce + `" src="/os/static/js/admin-os-members.js?v=` + assetVer("js/admin-os-members.js") + `"></script>`

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

// ── Members dashboard render helpers ─────────────────────────────────────────

// formatPercent renders a 0..1 ratio as a one-decimal percentage (e.g. "12.5%").
func formatPercent(f float64) string {
	if f < 0 {
		f = 0
	}
	return fmt.Sprintf("%.1f%%", f*100)
}

// sparklineSVG renders a daily signup series as a compact inline bar chart. The
// chart is resolution-independent (viewBox + preserveAspectRatio) so it scales
// to its container without a JS charting dependency.
func sparklineSVG(series []members.DayCount) string {
	if len(series) == 0 {
		return `<div class="empty-state">No data yet.</div>`
	}
	max := 1
	total := 0
	for _, d := range series {
		if d.Count > max {
			max = d.Count
		}
		total += d.Count
	}
	n := len(series)
	bw := 100.0 / float64(n)
	bars := ""
	for i, d := range series {
		h := float64(d.Count) / float64(max) * 36.0
		x := float64(i) * bw
		y := 40.0 - h
		bars += fmt.Sprintf(
			`<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="0.5" fill="#7c83ff" opacity="0.85"><title>%s: %d</title></rect>`,
			x+bw*0.15, y, bw*0.7, h, html.EscapeString(d.Day), d.Count)
	}
	return `<svg viewBox="0 0 100 40" preserveAspectRatio="none" width="100%" height="84" role="img" aria-label="New members per day">` +
		bars + `</svg><div class="row-meta mt-2">` + strconv.Itoa(total) + ` new members in the last 30 days</div>`
}

// revenueByTierHTML renders each tier's MRR contribution as a labelled bar.
func revenueByTierHTML(rev []members.TierRevenue, currency string, totalMRR int) string {
	if len(rev) == 0 {
		return `<div class="empty-state">No paying members yet.</div>`
	}
	esc := html.EscapeString
	out := `<div>`
	for _, t := range rev {
		pct := 0.0
		if totalMRR > 0 {
			pct = float64(t.MRRCents) / float64(totalMRR) * 100
		}
		cur := t.Currency
		if cur == "" {
			cur = currency
		}
		out += `<div class="mb-3">
  <div class="flex-between" style="display:flex;justify-content:space-between;font-size:.85rem">
    <span>` + esc(t.Name) + `</span>
    <span class="row-meta">` + priceLabel(cur, t.MRRCents) + ` · ` + strconv.Itoa(t.Members) + ` members</span>
  </div>
  <div style="background:rgba(124,131,255,.15);border-radius:4px;height:8px;overflow:hidden;margin-top:4px">
    <div style="height:100%;background:#7c83ff;width:` + fmt.Sprintf("%.1f", pct) + `%"></div>
  </div>
</div>`
	}
	out += `</div>`
	return out
}

// activityFeedHTML renders the recent member activity events as a timeline.
func activityFeedHTML(events []members.Event, currency string) string {
	if len(events) == 0 {
		return `<div class="empty-state">No activity yet. Member signups and subscription changes will appear here.</div>`
	}
	esc := html.EscapeString
	labels := map[string]string{
		members.EventSignup:          "joined as a free member",
		members.EventSubscribe:       "started a paid subscription",
		members.EventTrialStart:      "started a free trial",
		members.EventUpgrade:         "upgraded their plan",
		members.EventDowngrade:       "downgraded their plan",
		members.EventRenew:           "renewed their subscription",
		members.EventCancel:          "cancelled their subscription",
		members.EventCancelScheduled: "scheduled a cancellation",
		members.EventComp:            "was granted a complimentary plan",
		members.EventPaymentFailed:   "had a payment fail",
	}
	colors := map[string]string{
		members.EventSubscribe:     "#22c55e",
		members.EventTrialStart:    "#3b82f6",
		members.EventUpgrade:       "#22c55e",
		members.EventCancel:        "#ef4444",
		members.EventPaymentFailed: "#f59e0b",
		members.EventComp:          "#a855f7",
	}
	out := `<ul style="list-style:none;margin:0;padding:0">`
	for _, e := range events {
		who := e.Email
		if who == "" {
			who = "A member"
		}
		label := labels[e.Type]
		if label == "" {
			label = strings.ReplaceAll(e.Type, "_", " ")
		}
		dot := colors[e.Type]
		if dot == "" {
			dot = "#9ca3af"
		}
		amt := ""
		if e.AmountCents > 0 {
			amt = ` <span class="row-meta">(` + priceLabel(currency, e.AmountCents) + `/mo)</span>`
		}
		when := e.CreatedAt.UTC().Format("2 Jan 15:04")
		out += `<li style="display:flex;align-items:center;gap:.6rem;padding:.45rem 0;border-bottom:1px solid rgba(127,127,127,.12)">
  <span style="flex:none;width:8px;height:8px;border-radius:50%;background:` + dot + `"></span>
  <span style="flex:1;font-size:.9rem"><strong>` + esc(who) + `</strong> ` + esc(label) + amt + `</span>
  <span class="row-meta">` + esc(when) + `</span>
</li>`
	}
	out += `</ul>`
	return out
}
