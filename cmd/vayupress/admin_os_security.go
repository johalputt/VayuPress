// SPDX-License-Identifier: Apache-2.0

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
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/totp"
	"github.com/johalputt/vayupress/internal/ui"
)

// verifyTOTPForLogin decides whether a login may proceed. It returns required=true
// when the account has 2FA enabled; ok reflects whether the supplied code is
// valid AND newly consumed — a code that already authenticated once is refused
// even inside its validity window (audit: TOTP replay). When 2FA is not
// enabled, required=false and ok=true (no code needed).
func (a *App) verifyTOTPForLogin(ctx context.Context, email, code string) (ok, required bool) {
	if a.userStore == nil {
		return true, false
	}
	secret, enabled, err := a.userStore.TOTPSecretByEmail(ctx, email)
	if err != nil || !enabled || secret == "" {
		return true, false
	}
	step, matched := totp.MatchAt(secret, code, time.Now())
	if !matched {
		return false, true
	}
	consumed, cerr := a.userStore.ConsumeTOTPStep(ctx, email, int64(step))
	if cerr != nil || !consumed {
		return false, true
	}
	return true, true
}

// ── Members page ───────────────────────────────────────────────────────────

// handleOSMembers renders Audience › Members as the Overview kind (render 03):
// how many there are and how many pay, the month's growth beside what happened
// lately, then the tiers. The people themselves are a list one click away.
func (a *App) handleOSMembers(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	if a.members == nil {
		writeOSHTML(w, r, adminOSLayout(nonce, "Members", "members", cfg, ui.Overview(ui.OverviewPage{Title: "Members"},
			ui.Band{Title: "Growth", Aside: ui.Empty("audience", "Memberships are off", "This install was started without the members store.", "")})))
		return
	}

	ctx := r.Context()
	stats, _ := a.members.Stats(ctx)
	if stats == nil {
		stats = &members.Stats{ByTier: map[string]int{}, Currency: "USD"}
	}
	tiers, _ := a.members.ListTiers(ctx, true)
	signups, _ := a.members.SignupsByDay(ctx, 30)
	activity, _ := a.members.RecentEvents(ctx, 6)

	state := "No members yet"
	if stats.Total > 0 {
		state = countOf(stats.Total, "member")
		if stats.Paid > 0 {
			state += ", " + strconv.Itoa(stats.Paid) + " paying"
		}
	}

	// A figure shows only when it says something: an install with no sign-ups
	// this month has no growth to report, and "0" in large type says less
	// than its absence.
	var figs []ui.Figure
	if stats.NewLast30 > 0 {
		figs = append(figs, ui.Figure{Value: "+" + strconv.Itoa(stats.NewLast30), Label: "New members"})
	}
	if stats.MRRCents > 0 {
		figs = append(figs, ui.Figure{Value: priceLabel(stats.Currency, stats.MRRCents), Label: "Monthly revenue"})
	}
	if stats.Paid > 0 {
		figs = append(figs, ui.Figure{Value: formatPercent(stats.ConversionRate), Label: "Pay after joining"})
	}
	chart := ui.HTML(`<p class="table-empty">Nobody has joined in the last 30 days.</p>`)
	if stats.NewLast30 > 0 {
		chart = ui.HTML(sparklineSVG(signups))
	}

	var tierRows []ui.Row
	for _, t := range tiers {
		price := "Free"
		if !t.IsFree() {
			price = priceLabel(t.Currency, t.MonthlyCents) + " a month"
			if t.MonthlyCents == 0 && t.YearlyCents > 0 {
				price = priceLabel(t.Currency, t.YearlyCents) + " a year"
			}
		}
		if t.TrialDays > 0 {
			price += " · " + strconv.Itoa(t.TrialDays) + "-day trial"
		}
		if len(t.Benefits) > 0 {
			price += " · " + strings.Join(t.Benefits, ", ")
		}
		count := "No members yet"
		if n := stats.ByTier[t.Slug]; n > 0 {
			count = countOf(n, "member")
			if !t.IsFree() {
				count = strconv.Itoa(n) + " paying"
			}
		}
		st := ui.State("ok", "Public")
		switch {
		case !t.Active:
			st = ui.State("neutral", "Archived")
		case t.Visibility != "public":
			st = ui.State("neutral", titleFirst(t.Visibility))
		}
		control := `<span class="sa-row__count">` + esc(count) + `</span>` + string(st) +
			`<button class="btn btn--sm btn--ghost" type="button" data-edit-tier
			data-id="` + esc(t.ID) + `" data-name="` + esc(t.Name) + `" data-description="` + esc(t.Description) + `"
			data-monthly="` + strconv.Itoa(t.MonthlyCents) + `" data-yearly="` + strconv.Itoa(t.YearlyCents) + `"
			data-currency="` + esc(t.Currency) + `" data-visibility="` + esc(t.Visibility) + `"
			data-trial="` + strconv.Itoa(t.TrialDays) + `" data-stripe-monthly="` + esc(t.StripeMonthlyPrice) + `" data-stripe-yearly="` + esc(t.StripeYearlyPrice) + `"
			data-mail-enabled="` + zeroOne(t.MailEnabled) + `" data-mail-quota="` + strconv.Itoa(t.MailQuotaMB) + `"
			data-benefits="` + esc(strings.Join(t.Benefits, "\n")) + `">Edit</button>`
		if t.Active && t.Slug != members.TierFree && t.Slug != members.TierPaid {
			control += `<button class="btn btn--sm btn--ghost" type="button" data-archive-tier data-id="` + esc(t.ID) + `">Archive</button>`
		}
		tierRows = append(tierRows, ui.Row{Label: t.Name, Hint: price, Control: ui.HTML(control)})
	}
	tiersBody := ui.HTML(`<p class="table-empty">No tiers yet.</p>`)
	if len(tierRows) > 0 {
		tiersBody = ui.Rows(tierRows...)
	}
	sections := []ui.HTML{ui.Section("Tiers", "On your public pricing page", tiersBody)}

	// Unconfirmed leftovers from the old pre-send signup path, shown only while
	// there is something to clean up. On a count error nothing is shown and the
	// failure is logged: a wrong number would be worse than none, and the purge
	// endpoint re-lists authoritatively.
	if n, err := a.members.CountUnverified(ctx); err != nil {
		logging.LogError("members", "unconfirmed-member count failed; cleanup hidden", err.Error())
	} else if body := unverifiedMembersCardHTML(n); body != "" {
		sections = append(sections, ui.Section("Unconfirmed addresses", "", ui.HTML(body)))
	}

	// The tier editor rises in a sheet. Its title's id, tier-modal-title, is the
	// one the members script sets to "New tier" or "Edit tier".
	modal := string(ui.Sheet("tier-modal", "New tier", ui.HTML(`<form id="tier-form">
  <input type="hidden" id="tier-id">
  <div class="field"><label class="field-label" for="tier-name">Name</label>
    <input class="input" id="tier-name" type="text" required maxlength="60" placeholder="Premium"></div>
  <div class="field"><label class="field-label" for="tier-desc">Description</label>
    <input class="input" id="tier-desc" type="text" maxlength="200" placeholder="One line for the pricing page"></div>
  <div class="field"><label class="field-label" for="tier-monthly">Monthly price, in cents</label>
    <input class="input" id="tier-monthly" type="number" min="0" value="0"></div>
  <div class="field"><label class="field-label" for="tier-yearly">Yearly price, in cents</label>
    <input class="input" id="tier-yearly" type="number" min="0" value="0"></div>
  <div class="field"><label class="field-label" for="tier-currency">Currency</label>
    <input class="input" id="tier-currency" type="text" maxlength="3" value="USD"></div>
  <div class="field"><label class="field-label" for="tier-visibility">Visibility</label>
    <select class="select" id="tier-visibility"><option value="public">Public</option><option value="hidden">Hidden</option></select></div>
  <div class="field"><label class="field-label" for="tier-trial">Free trial, in days</label>
    <input class="input" id="tier-trial" type="number" min="0" value="0">
    <p class="field-hint">Full access for this many days before the first charge. 0 is none.</p></div>
  <div class="field"><label class="field-label" for="tier-stripe-monthly">Stripe monthly price ID</label>
    <input class="input" id="tier-stripe-monthly" type="text" maxlength="80" placeholder="price_…"></div>
  <div class="field"><label class="field-label" for="tier-stripe-yearly">Stripe yearly price ID</label>
    <input class="input" id="tier-stripe-yearly" type="text" maxlength="80" placeholder="price_…"></div>
  <label class="cz-check"><input type="checkbox" id="tier-mail-enabled"> A VayuMail mailbox with this tier</label>
  <div class="field"><label class="field-label" for="tier-mail-quota">Mailbox size, in MB</label>
    <input class="input" id="tier-mail-quota" type="number" min="0" value="0">
    <p class="field-hint">0 is unlimited; 1024 is 1 GB.</p></div>
  <div class="field"><label class="field-label" for="tier-benefits">Benefits, one a line</label>
    <textarea class="textarea" id="tier-benefits" rows="4" placeholder="Every premium post&#10;The members' newsletter"></textarea></div>
  <div class="mt-3 sa-list__sheet-actions"><button class="btn btn--primary btn--sm" type="submit" id="tier-save">Save tier</button>
    <button class="btn btn--ghost btn--sm" type="button" id="tier-cancel">Cancel</button></div>
</form>`)))
	body := string(ui.Overview(ui.OverviewPage{
		Title: "Members",
		State: ui.Text(state),
		Actions: ui.HTML(`<a class="btn btn--ghost" href="/os/api/members/export.csv" download>Export</a>` +
			`<a class="btn" href="/os/members/people">Everyone</a>` +
			`<button class="btn btn--primary" type="button" data-new-tier>` + saIcon("plus") + ` New tier</button>`),
	}, ui.Band{Title: "Growth", Hint: "Last 30 days", Figures: figs, Chart: chart,
		Aside: ui.Section("Recent", "", ui.HTML(activityFeedHTML(activity, stats.Currency)))}, sections...)) +
		modal +
		`<script nonce="` + nonce + `" src="/os/static/js/admin-os-members.js?v=` + assetVer("js/admin-os-members.js") + `"></script>`

	writeOSHTML(w, r, adminOSLayout(nonce, "Members", "members", cfg, htmpl.HTML(body)))
}

// ── Security page (TOTP) ────────────────────────────────────────────────────

func (a *App) handleOSSecurity(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	tabs := saTabsFor(cfg, "shield", "/os/security")

	u := currentUser(r)
	if u == nil || a.userStore == nil {
		// API-key session (no user record): 2FA is per-account, so explain.
		body := ui.Status(ui.StatusPage{Title: "Shield", Tabs: tabs, Tone: "neutral",
			State:  "Two-factor sign-in is for password accounts",
			Detail: ui.Text("You are signed in with an API key, which has no second factor to add.")})
		writeOSHTML(w, r, adminOSLayout(nonce, "Sign-in security", "security", cfg, body))
		return
	}

	_, enabled, _ := a.userStore.TOTPStatus(r.Context(), u.ID)
	body := string(securityPage(enabled, tabs)) +
		`<script nonce="` + nonce + `" src="/os/static/js/admin-os-security.js?v=` + assetVer("js/admin-os-security.js") + `"></script>`
	writeOSHTML(w, r, adminOSLayout(nonce, "Sign-in security", "security", cfg, htmpl.HTML(body)))
}

// securityPage is Sign-in security as a status page: whether a second factor
// guards this account, said first, with the one control that changes it. Set
// up rises in a sheet, as every form in the console does. One wrapper round
// the page and its sheet: the script binds every 2FA control through it.
func securityPage(enabled bool, tabs ui.HTML) ui.HTML {
	return `<div data-totp-card>` + securityBody(enabled, tabs) + `</div>`
}

func securityBody(enabled bool, tabs ui.HTML) ui.HTML {
	if enabled {
		return ui.Status(ui.StatusPage{Title: "Shield", Tabs: tabs, Tone: "ok",
			State:   "Two-factor sign-in is on",
			Detail:  ui.Text("A code from your authenticator app is needed at every sign-in, as well as your password."),
			Actions: `<button type="button" class="btn btn--sm" data-totp-disable>Turn off</button>`})
	}
	return ui.Join(ui.Status(ui.StatusPage{Title: "Shield", Tabs: tabs, Tone: "warn",
		State:   "Two-factor sign-in is off",
		Detail:  ui.Text("Your password alone signs you in. Add a code from any authenticator app, so a stolen password is not enough."),
		Actions: `<button type="button" class="btn btn--primary" data-sheet="totp-sheet" data-totp-begin>Set up two-factor sign-in</button>`}),
		ui.Sheet("totp-sheet", "Set up two-factor sign-in", `<div class="totp-enroll" data-totp-enroll hidden>
    <p class="text-sm muted">Scan the code with your authenticator app (Google Authenticator, Aegis, 1Password), then enter the six digits it shows. Can't scan? Enter the key by hand.</p>
    <img data-totp-qr alt="Two-factor setup code" width="180" height="180" class="totp-qr" hidden>
    <div class="totp-key mt-2">Key: <code data-totp-key class="font-mono"></code></div>
    <div class="totp-uri text-xs muted"><a data-totp-uri href="#" rel="noopener">Open in your authenticator app</a></div>
    <div class="field mt-3">
      <label class="field-label" for="totp-code">Verification code</label>
      <input id="totp-code" class="input" type="text" inputmode="numeric" autocomplete="one-time-code"
        maxlength="6" placeholder="000000" data-totp-code>
    </div>
    <button type="button" class="btn btn--primary btn--sm" data-totp-verify>Verify and turn on</button>
  </div>`))
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
	writeJSON(w, r, http.StatusOK, map[string]string{"secret": secret, "uri": uri, "qr": qrDataURI(uri)})
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
	step, matched := totp.MatchAt(secret, strings.TrimSpace(body.Code), time.Now())
	if !matched {
		writeAPIError(w, r, http.StatusBadRequest, "bad-code", "That code is not valid — try again", "")
		return
	}
	// Single-use (audit): consume the enabling code so it cannot be replayed.
	if consumed, cerr := a.userStore.ConsumeTOTPStep(r.Context(), u.Email, int64(step)); cerr != nil || !consumed {
		writeAPIError(w, r, http.StatusBadRequest, "bad-code", "That code has already been used — try the next one", "")
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
	for _, d := range series {
		if d.Count > max {
			max = d.Count
		}
	}
	n := len(series)
	bw := 100.0 / float64(n)
	bars := ""
	for i, d := range series {
		h := float64(d.Count) / float64(max) * 36.0
		x := float64(i) * bw
		y := 40.0 - h
		bars += fmt.Sprintf(
			`<rect class="mem-bar" x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="0.5"><title>%s: %d</title></rect>`,
			x+bw*0.15, y, bw*0.7, h, html.EscapeString(d.Day), d.Count)
	}
	return `<svg viewBox="0 0 100 40" preserveAspectRatio="none" width="100%" height="84" role="img" aria-label="New members per day">` +
		bars + `</svg>`
}

// activityFeedHTML renders recent member activity as rows: when, who and
// what, and the plan or amount it concerns. The event's kind is in the words,
// so it needs no colour of its own.
func activityFeedHTML(events []members.Event, currency string) string {
	if len(events) == 0 {
		return `<p class="table-empty">Sign-ups and plan changes will show here.</p>`
	}
	labels := map[string]string{
		members.EventSignup:          "joined",
		members.EventSubscribe:       "started paying",
		members.EventTrialStart:      "started a trial",
		members.EventUpgrade:         "upgraded",
		members.EventDowngrade:       "downgraded",
		members.EventRenew:           "renewed",
		members.EventCancel:          "cancelled",
		members.EventCancelScheduled: "cancels at period end",
		members.EventComp:            "was given a plan",
		members.EventPaymentFailed:   "had a payment fail",
	}
	var b strings.Builder
	b.WriteString(`<ul class="sa-activity">`)
	for _, e := range events {
		who := e.Email
		if who == "" {
			who = "A member"
		}
		label := labels[e.Type]
		if label == "" {
			label = strings.ReplaceAll(e.Type, "_", " ")
		}
		note := ""
		if e.AmountCents > 0 {
			note = priceLabel(currency, e.AmountCents) + " a month"
		}
		b.WriteString(`<li><span class="sa-activity__when">` + esc(config.FormatSite(e.CreatedAt, "2 Jan, 15:04")) + `</span>` +
			`<span class="sa-activity__what">` + esc(who) + ` ` + esc(label) + `</span>` +
			`<span class="sa-activity__note">` + esc(note) + `</span></li>`)
	}
	b.WriteString(`</ul>`)
	return b.String()
}
