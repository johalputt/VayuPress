package main

// admin_os_newsletter.go — the VayuOS Newsletter console (/os/newsletter).
//
// Until now the "Newsletter" sidebar item dead-ended: subscriber management and
// broadcasts existed only as JSON endpoints with no page. This file delivers a
// full operator console:
//
//   - audience health (total / active / pending double-opt-in / unsubscribed),
//     30-day growth sparkline and confirmation rate,
//   - subscriber table with status-segment tabs, instant search, CSV export and
//     per-row delete (GDPR erasure / spam cleanup),
//   - a broadcast composer (subject + text + optional HTML) with a "send test"
//     action and a one-click send to every confirmed subscriber,
//   - a persisted broadcast history with per-send delivery tallies.
//
// CSP posture is inherited (no inline styles, no innerHTML with untrusted data,
// the only inline script is nonce-gated). All dynamic values are escaped with
// html.EscapeString before emission; the table/composer behaviour lives in the
// same-origin /os/static/js/admin-os-newsletter.js.

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/newsletter"
	"github.com/johalputt/vayupress/internal/render"
)

// ── Page ─────────────────────────────────────────────────────────────────────

func (a *App) handleOSNewsletter(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	if a.newsletterStore == nil {
		body := `<div class="page-header"><h1>Newsletter</h1></div>
<div class="card empty-state"><div class="empty-icon">✉️</div>
<div class="empty-title">Newsletter unavailable</div>
<div class="empty-sub">The newsletter store is not initialised.</div></div>`
		writeOSHTML(w, adminOSLayout(nonce, "Newsletter", "newsletter", cfg, htmpl.HTML(body)))
		return
	}

	ctx := r.Context()
	stats, _ := a.newsletterStore.Stats(ctx)
	if stats == nil {
		stats = &newsletter.Stats{}
	}
	growth, _ := a.newsletterStore.GrowthByDay(ctx, 30)
	subs, _ := a.newsletterStore.List(ctx, "all", "", 500)
	broadcasts, _ := a.newsletterStore.ListBroadcasts(ctx, 10)
	esc := html.EscapeString

	// ── SMTP status banner ────────────────────────────────────────────────────
	smtpReady := a.mailer != nil && a.mailer.Enabled()
	banner := ""
	if !smtpReady {
		banner = `<div class="card settings-callout mb-6">
  <strong>SMTP is not configured.</strong>
  <span class="text-sm muted">Subscribers can still sign up and confirm, but broadcasts and test emails cannot be delivered until you set <code>SMTP_HOST</code> (and related variables). See the Email settings.</span>
</div>`
	}

	// ── Stat cards ────────────────────────────────────────────────────────────
	growthTotal := 0
	for _, v := range growth {
		growthTotal += v
	}
	statGrid := `<div class="stat-grid mb-6">` +
		nlStatCard("Subscribers", strconv.Itoa(stats.Total), "all records") +
		nlStatCard("Active", strconv.Itoa(stats.Active), "confirmed &amp; subscribed") +
		nlStatCard("Pending", strconv.Itoa(stats.Pending), "awaiting opt-in") +
		nlStatCard("Unsubscribed", strconv.Itoa(stats.Unsubscribed), "opted out") +
		nlStatCard("New · 30 days", "+"+strconv.Itoa(stats.NewLast30), "recent signups") +
		nlStatCard("Confirm rate", nlPercent(stats.ConfirmRate), "double opt-in") +
		`</div>`

	// ── Growth sparkline ──────────────────────────────────────────────────────
	growthCard := `<div class="card mb-6">
  <div class="flex justify-between items-center">
    <div class="card-title">Audience growth</div>
    <span class="text-xs muted">` + strconv.Itoa(growthTotal) + ` new in last 30 days</span>
  </div>
  <div class="sparkline-wrap">` + osSparkline(growth) + `</div>
</div>`

	// ── Broadcast composer ────────────────────────────────────────────────────
	disabledAttr := ""
	if !smtpReady {
		disabledAttr = " disabled"
	}
	composer := `<div class="card mb-6">
  <div class="card-title">Compose broadcast</div>
  <p class="text-sm muted mb-4">Send an email to all <strong>` + strconv.Itoa(stats.Active) + `</strong> confirmed subscribers. An unsubscribe link is appended automatically to every message.</p>
  <div class="field"><label class="field-label" for="nl-subject">Subject</label>
    <input id="nl-subject" class="input" type="text" maxlength="200" placeholder="What's new this week"` + disabledAttr + `></div>
  <div class="field"><label class="field-label" for="nl-text">Plain text <span class="muted text-xs">(required)</span></label>
    <textarea id="nl-text" class="textarea" rows="8" placeholder="Write your update…"` + disabledAttr + `></textarea></div>
  <div class="field"><label class="field-label" for="nl-html">HTML <span class="muted text-xs">(optional — sent as multipart/alternative)</span></label>
    <textarea id="nl-html" class="textarea font-mono" rows="6" placeholder="&lt;h1&gt;Hello&lt;/h1&gt;"` + disabledAttr + `></textarea></div>
  <div class="settings-row" style="gap:.5rem;flex-wrap:wrap">
    <input id="nl-test-to" class="input input--sm" type="email" placeholder="you@example.com" aria-label="Test recipient" style="max-width:16rem"` + disabledAttr + `>
    <button type="button" class="btn btn--ghost btn--sm" id="nl-send-test"` + disabledAttr + `>Send test</button>
    <span class="topbar-spacer" style="flex:1"></span>
    <button type="button" class="btn btn--primary" id="nl-send-broadcast"` + disabledAttr + `>Send to ` + strconv.Itoa(stats.Active) + ` subscribers</button>
  </div>
  <div id="nl-compose-msg" role="status" aria-live="polite" class="action-msg"></div>
</div>`

	// ── Broadcast history ─────────────────────────────────────────────────────
	historyCard := `<div class="card mb-6"><div class="card-title">Broadcast history</div>` +
		nlBroadcastsTable(broadcasts) + `</div>`

	// ── Subscriber table ──────────────────────────────────────────────────────
	rows := ""
	for _, s := range subs {
		seg := "active"
		statusBadge := `<span class="badge badge--ok">active</span>`
		if s.Status == "inactive" {
			seg = "unsubscribed"
			statusBadge = `<span class="badge badge--muted">unsubscribed</span>`
		} else if !s.Confirmed {
			seg = "pending"
			statusBadge = `<span class="badge badge--warn">pending</span>`
		}
		confirmed := `<span class="badge badge--muted">no</span>`
		if s.Confirmed {
			confirmed = `<span class="badge badge--ok">yes</span>`
		}
		rows += `<tr data-sub-row data-seg="` + seg + `" data-search="` + esc(strings.ToLower(s.Email)) + `">
  <td class="row-title">` + esc(s.Email) + `</td>
  <td>` + statusBadge + `</td>
  <td>` + confirmed + `</td>
  <td class="row-meta">` + s.SubscribedAt.UTC().Format("2 Jan 2006") + `</td>
  <td class="row-actions"><button type="button" class="btn btn--xs btn--danger" data-sub-delete data-id="` + esc(s.ID) + `" data-email="` + esc(s.Email) + `">Delete</button></td>
</tr>`
	}
	table := `<div class="empty-state">No subscribers yet.</div>`
	if rows != "" {
		table = `<div class="table-wrap"><table class="table">
  <thead><tr><th>Email</th><th>Status</th><th>Confirmed</th><th>Subscribed</th><th></th></tr></thead>
  <tbody data-subs-body>` + rows + `</tbody></table></div>`
	}
	subsCard := `<div class="card">
  <div class="toolbar-row">
    <div class="seg-filter" role="tablist" aria-label="Filter subscribers">
      <button type="button" class="seg-btn is-active" data-sub-filter="all">All <span class="muted">` + strconv.Itoa(stats.Total) + `</span></button>
      <button type="button" class="seg-btn" data-sub-filter="active">Active <span class="muted">` + strconv.Itoa(stats.Active) + `</span></button>
      <button type="button" class="seg-btn" data-sub-filter="pending">Pending <span class="muted">` + strconv.Itoa(stats.Pending) + `</span></button>
      <button type="button" class="seg-btn" data-sub-filter="unsubscribed">Unsubscribed <span class="muted">` + strconv.Itoa(stats.Unsubscribed) + `</span></button>
    </div>
    <input class="input input--sm" type="search" placeholder="Search email…" data-sub-search aria-label="Search subscribers" style="max-width:16rem">
  </div>
  <div class="table-empty" data-subs-empty hidden>No subscribers match this filter.</div>
  ` + table + `
</div>`

	body := `<div class="page-header">
  <h1>Newsletter</h1>
  <div class="page-actions">
    <a class="btn btn--ghost btn--sm" href="/os/api/newsletter/export.csv" download>Export CSV</a>
  </div>
</div>` + banner + statGrid + growthCard + composer + historyCard + subsCard +
		`<script nonce="` + nonce + `" src="/os/static/js/admin-os-newsletter.js?v=` + assetVer("js/admin-os-newsletter.js") + `"></script>`

	writeOSHTML(w, adminOSLayout(nonce, "Newsletter", "newsletter", cfg, htmpl.HTML(body)))
}

// nlStatCard renders one stat card (label, big value, sub-label).
func nlStatCard(label, value, sub string) string {
	return `<div class="stat-card">
  <div class="stat-card__label">` + html.EscapeString(label) + `</div>
  <div class="stat-card__value">` + value + `</div>
  <div class="stat-card__sub">` + sub + `</div>
</div>`
}

// nlPercent renders a 0..1 ratio as a one-decimal percentage.
func nlPercent(f float64) string {
	if f < 0 {
		f = 0
	}
	return fmt.Sprintf("%.1f%%", f*100)
}

// nlBroadcastsTable renders the broadcast history table.
func nlBroadcastsTable(list []newsletter.Broadcast) string {
	if len(list) == 0 {
		return `<div class="empty-state">No broadcasts sent yet. Compose one above.</div>`
	}
	esc := html.EscapeString
	rows := ""
	for _, b := range list {
		status := `<span class="badge badge--warn">sending</span>`
		if b.Status == "complete" {
			status = `<span class="badge badge--ok">complete</span>`
		}
		failed := strconv.Itoa(b.Failed)
		if b.Failed > 0 {
			failed = `<span class="badge badge--danger">` + failed + `</span>`
		}
		rows += `<tr>
  <td class="row-title">` + esc(b.Subject) + `</td>
  <td>` + status + `</td>
  <td class="row-meta">` + strconv.Itoa(b.Recipients) + `</td>
  <td class="row-meta">` + strconv.Itoa(b.Sent) + `</td>
  <td>` + failed + `</td>
  <td class="row-meta">` + b.CreatedAt.UTC().Format("2 Jan 2006 15:04") + `</td>
</tr>`
	}
	return `<div class="table-wrap"><table class="table">
  <thead><tr><th>Subject</th><th>Status</th><th>Recipients</th><th>Sent</th><th>Failed</th><th>When</th></tr></thead>
  <tbody>` + rows + `</tbody></table></div>`
}

// ── JSON / action handlers (session-authed under /os/api/newsletter/*) ─────────

// GET /os/api/newsletter/stats → audience snapshot + 30-day growth series.
func (a *App) handleOSNewsletterStats(w http.ResponseWriter, r *http.Request) {
	if a.newsletterStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "newsletter-disabled", "Newsletter not initialised", "")
		return
	}
	stats, err := a.newsletterStore.Stats(r.Context())
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	growth, _ := a.newsletterStore.GrowthByDay(r.Context(), 30)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"stats": stats, "growth": growth})
}

// GET /os/api/newsletter/subscribers?filter=&q= → filtered subscriber list.
func (a *App) handleOSNewsletterSubscribers(w http.ResponseWriter, r *http.Request) {
	if a.newsletterStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "newsletter-disabled", "Newsletter not initialised", "")
		return
	}
	filter := r.URL.Query().Get("filter")
	q := r.URL.Query().Get("q")
	subs, err := a.newsletterStore.List(r.Context(), filter, q, 1000)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"subscribers": subs})
}

// GET /os/api/newsletter/export.csv → download the full subscriber list.
func (a *App) handleOSNewsletterExport(w http.ResponseWriter, r *http.Request) {
	if a.newsletterStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "newsletter-disabled", "Newsletter not initialised", "")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="newsletter-subscribers.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	if err := a.newsletterStore.ExportCSV(r.Context(), w); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "export-error", err.Error(), "")
	}
}

// GET /os/api/newsletter/broadcasts → recent broadcast history.
func (a *App) handleOSNewsletterBroadcasts(w http.ResponseWriter, r *http.Request) {
	if a.newsletterStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "newsletter-disabled", "Newsletter not initialised", "")
		return
	}
	list, err := a.newsletterStore.ListBroadcasts(r.Context(), 25)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"broadcasts": list})
}

// DELETE /os/api/newsletter/subscribers/{id} → permanently remove a subscriber.
func (a *App) handleOSNewsletterDelete(w http.ResponseWriter, r *http.Request) {
	if a.newsletterStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "newsletter-disabled", "Newsletter not initialised", "")
		return
	}
	id := chi.URLParam(r, "id")
	if strings.TrimSpace(id) == "" {
		writeAPIError(w, r, http.StatusBadRequest, "missing-id", "subscriber id is required", "")
		return
	}
	if err := a.newsletterStore.Delete(r.Context(), id); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "delete-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "deleted"})
}

// POST /os/api/newsletter/test  {to, subject, text, html}
// Sends a single test message so the operator can preview a broadcast before
// committing it to the whole audience.
func (a *App) handleOSNewsletterSendTest(w http.ResponseWriter, r *http.Request) {
	if a.newsletterStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "newsletter-disabled", "Newsletter not initialised", "")
		return
	}
	if a.mailer == nil || !a.mailer.Enabled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "email-disabled", "SMTP not configured — set SMTP_HOST to send", "")
		return
	}
	var body struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		Text    string `json:"text"`
		HTML    string `json:"html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	to := strings.TrimSpace(body.To)
	if to == "" {
		writeAPIError(w, r, http.StatusBadRequest, "missing-to", "a test recipient address is required", "")
		return
	}
	if strings.TrimSpace(body.Subject) == "" || strings.TrimSpace(body.Text) == "" {
		writeAPIError(w, r, http.StatusBadRequest, "missing-fields", "subject and text are required", "")
		return
	}
	if err := a.mailer.Send(email.Message{
		To: to, Subject: "[TEST] " + body.Subject, Text: body.Text, HTML: body.HTML,
	}); err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "send-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "sent", "to": to})
}

// POST /os/api/newsletter/broadcast  {subject, text, html}
// Records the broadcast, then delivers to every confirmed subscriber in the
// background, persisting the final sent/failed tallies.
func (a *App) handleOSNewsletterBroadcastSend(w http.ResponseWriter, r *http.Request) {
	if a.newsletterStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "newsletter-disabled", "Newsletter not initialised", "")
		return
	}
	if a.mailer == nil || !a.mailer.Enabled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "email-disabled", "SMTP not configured — set SMTP_HOST to send broadcasts", "")
		return
	}
	var body struct {
		Subject string `json:"subject"`
		Text    string `json:"text"`
		HTML    string `json:"html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	if strings.TrimSpace(body.Subject) == "" || strings.TrimSpace(body.Text) == "" {
		writeAPIError(w, r, http.StatusBadRequest, "missing-fields", "subject and text are required", "")
		return
	}
	subs, err := a.newsletterStore.ListActive(r.Context())
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	if len(subs) == 0 {
		writeAPIError(w, r, http.StatusBadRequest, "no-recipients", "There are no confirmed subscribers to send to yet", "")
		return
	}
	id, err := a.newsletterStore.CreateBroadcast(r.Context(), strings.TrimSpace(body.Subject), len(subs))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	go a.deliverBroadcastTracked(id, subs, body.Subject, body.Text, body.HTML)
	writeJSON(w, r, http.StatusAccepted, map[string]interface{}{
		"queued": len(subs), "broadcast_id": id, "status": "sending",
	})
}

// deliverBroadcastTracked mirrors deliverBroadcast but persists the final
// delivery tallies against the broadcast record for the console's history view.
func (a *App) deliverBroadcastTracked(broadcastID string, subs []newsletter.Subscriber, subject, text, htmlBody string) {
	var sent, failed int
	for _, s := range subs {
		unsub := "https://" + config.Cfg.Domain + "/api/v1/newsletter/unsubscribe?token=" + s.Token
		ftext := text + "\r\n\r\n---\r\nUnsubscribe: " + unsub
		fhtml := htmlBody
		if fhtml != "" {
			fhtml += `<hr><p style="color:#888;font-size:12px"><a href="` + html.EscapeString(unsub) + `">Unsubscribe</a></p>`
		}
		if err := a.mailer.Send(email.Message{To: s.Email, Subject: subject, Text: ftext, HTML: fhtml}); err != nil {
			failed++
		} else {
			sent++
		}
	}
	if err := a.newsletterStore.FinishBroadcast(context.Background(), broadcastID, sent, failed); err != nil {
		logging.LogError("newsletter", "finish broadcast failed", err.Error())
	}
	logging.LogInfo("newsletter", fmt.Sprintf("broadcast %s complete — sent=%d failed=%d", broadcastID, sent, failed))
}
