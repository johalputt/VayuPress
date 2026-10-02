// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_newsletter.go — the VayuOS Newsletter console (/os/newsletter): an
// overview of the audience and what was sent, the subscribers, a composer in a
// sheet, and the JSON actions behind them.
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
	"github.com/johalputt/vayupress/internal/ui"
)

// ── Page ─────────────────────────────────────────────────────────────────────

// handleOSNewsletter renders Audience › Newsletter as the Overview kind: how
// many subscribers there are, the month's growth beside what was sent, then
// the subscribers. Composing a broadcast rises in a sheet. With no relay and
// nobody yet it is the Setup page.
func (a *App) handleOSNewsletter(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	if a.newsletterStore == nil {
		writeOSHTML(w, r, adminOSLayout(nonce, "Newsletter", "newsletter", cfg, ui.Overview(ui.OverviewPage{Title: "Newsletter"},
			ui.Band{Title: "Growth", Aside: ui.Empty("send", "The newsletter is off", "This install was started without the newsletter store.", "")})))
		return
	}

	ctx := r.Context()
	stats, _ := a.newsletterStore.Stats(ctx)
	if stats == nil {
		stats = &newsletter.Stats{}
	}
	growth, _ := a.newsletterStore.GrowthByDay(ctx, 30)
	subs, _ := a.newsletterStore.List(ctx, "all", "", 500)
	broadcasts, _ := a.newsletterStore.ListBroadcasts(ctx, 6)
	esc := html.EscapeString

	smtpReady := a.mailer != nil && a.mailer.Enabled()
	// No relay and nobody yet is a newsletter that has not started: a setup
	// page. Once there are subscribers or past broadcasts the page shows them,
	// relay or not.
	if !smtpReady && stats.Total == 0 && len(broadcasts) == 0 {
		writeOSHTML(w, r, adminOSLayout(nonce, "Newsletter", "newsletter", cfg, htmpl.HTML(newsletterSetup())))
		return
	}

	state := countOf(stats.Active, "subscriber")
	if stats.Active == 0 {
		state = "No subscriber yet"
	}
	if stats.Pending > 0 {
		state += ", " + strconv.Itoa(stats.Pending) + " waiting to confirm"
	}
	// Without a relay the state says so, and the page's action is the setting
	// that fixes it rather than a composer that cannot send.
	action := `<button type="button" class="btn btn--primary" data-sheet="nl-compose">` + saIcon("send") + ` Compose</button>`
	if !smtpReady {
		state += ". Sending is off: no mail relay is set"
		action = `<a class="btn btn--primary" href="/os/settings/email">Set up sending</a>`
	}

	// A figure shows only when it says something.
	var figs []ui.Figure
	if stats.NewLast30 > 0 {
		figs = append(figs, ui.Figure{Value: "+" + strconv.Itoa(stats.NewLast30), Label: "New subscribers"})
	}
	if stats.Active+stats.Pending > 0 && stats.ConfirmRate > 0 {
		figs = append(figs, ui.Figure{Value: nlPercent(stats.ConfirmRate), Label: "Confirm by email"})
	}
	if stats.Unsubscribed > 0 {
		figs = append(figs, ui.Figure{Value: strconv.Itoa(stats.Unsubscribed), Label: "Have left"})
	}
	chart := ui.HTML(`<p class="table-empty">Nobody has subscribed in the last 30 days.</p>`)
	if stats.NewLast30 > 0 {
		chart = ui.HTML(`<div class="sparkline-wrap">` + osSparkline(growth, "New subscribers a day, last 30 days") + `</div>`)
	}

	var rows strings.Builder
	for _, s := range subs {
		seg, st := "active", ui.State("ok", "Subscribed")
		switch {
		case s.Status == "inactive":
			seg, st = "unsubscribed", ui.State("neutral", "Left")
		case !s.Confirmed:
			seg, st = "pending", ui.State("warn", "Waiting to confirm")
		}
		rows.WriteString(`<tr data-sub-row data-seg="` + seg + `" data-search="` + esc(strings.ToLower(s.Email)) + `">` +
			`<td class="post-row__name">` + esc(s.Email) + `</td><td>` + string(st) + `</td>` +
			`<td class="post-row__date">` + config.FormatSite(s.SubscribedAt, "2 Jan 2006") + `</td>` +
			`<td class="row-actions"><button type="button" class="btn btn--xs btn--ghost" data-sub-delete data-id="` + esc(s.ID) + `" data-email="` + esc(s.Email) + `">Delete</button></td></tr>`)
	}
	subsBody := `<p class="table-empty">No subscriber yet.</p>`
	if rows.Len() > 0 {
		subsBody = `<div class="toolbar-row"><div class="seg-filter" role="tablist" aria-label="Show subscribers">` +
			`<button type="button" class="seg-btn is-active" data-sub-filter="all">All <span class="muted">` + strconv.Itoa(stats.Total) + `</span></button>` +
			`<button type="button" class="seg-btn" data-sub-filter="active">Subscribed <span class="muted">` + strconv.Itoa(stats.Active) + `</span></button>` +
			`<button type="button" class="seg-btn" data-sub-filter="pending">Waiting <span class="muted">` + strconv.Itoa(stats.Pending) + `</span></button>` +
			`<button type="button" class="seg-btn" data-sub-filter="unsubscribed">Left <span class="muted">` + strconv.Itoa(stats.Unsubscribed) + `</span></button></div>` +
			string(ui.Search(ui.SearchBox{Placeholder: "Search subscribers", Hook: "data-sub-search"})) + `</div>` +
			`<p class="table-empty" data-subs-empty hidden>No subscriber matches that.</p>` +
			`<div class="table-wrap"><table class="table post-table"><thead><tr><th>Email</th><th class="sa-col--state">State</th><th class="sa-col--date">Subscribed</th><th class="sa-col--action"></th></tr></thead><tbody>` +
			rows.String() + `</tbody></table></div>`
	}

	// The composer is the one form on the page, so it rises in a sheet.
	composer := string(ui.Sheet("nl-compose", "Compose a broadcast", ui.HTML(`<p class="text-sm muted">To `+countOf(stats.Active, "confirmed subscriber")+`. Every message carries a link to leave.</p>
  <div class="field"><label class="field-label" for="nl-subject">Subject</label>
    <input id="nl-subject" class="input" type="text" maxlength="200" placeholder="What's new this week"></div>
  <div class="field"><label class="field-label" for="nl-text">Text</label>
    <textarea id="nl-text" class="textarea" rows="8" placeholder="Write your update"></textarea></div>
  <div class="field"><label class="field-label" for="nl-html">HTML, if you want one</label>
    <textarea id="nl-html" class="textarea font-mono" rows="6" placeholder="&lt;h1&gt;Hello&lt;/h1&gt;"></textarea>
    <p class="field-hint">Sent beside the text, for mail apps that show HTML.</p></div>
  <div class="field"><label class="field-label" for="nl-test-to">Send a test to</label>
    <input id="nl-test-to" class="input" type="email" placeholder="you@example.com"></div>
  <div class="mt-3 sa-list__sheet-actions"><button type="button" class="btn btn--primary btn--sm" id="nl-send-broadcast">Send to `+countOf(stats.Active, "subscriber")+`</button>
    <button type="button" class="btn btn--ghost btn--sm" id="nl-send-test">Send the test</button></div>
  <div id="nl-compose-msg" role="status" aria-live="polite" class="action-msg"></div>`)))

	body := string(ui.Overview(ui.OverviewPage{
		Title:   "Newsletter",
		State:   ui.Text(state),
		Actions: ui.HTML(`<a class="btn btn--ghost" href="/os/api/newsletter/export.csv" download>Export</a>` + action),
	}, ui.Band{Title: "Growth", Hint: "Last 30 days", Figures: figs, Chart: chart,
		Aside: ui.Section("Sent", "", ui.HTML(nlBroadcasts(broadcasts)))},
		ui.Section("Subscribers", "", ui.HTML(subsBody)))) +
		composer +
		`<script nonce="` + nonce + `" src="/os/static/js/admin-os-newsletter.js?v=` + assetVer("js/admin-os-newsletter.js") + `"></script>`

	writeOSHTML(w, r, adminOSLayout(nonce, "Newsletter", "newsletter", cfg, htmpl.HTML(body)))
}

// nlPercent renders a 0..1 ratio as a one-decimal percentage.
func nlPercent(f float64) string {
	if f < 0 {
		f = 0
	}
	return fmt.Sprintf("%.1f%%", f*100)
}

// nlBroadcasts lists what was sent lately: when, the subject, and how it went.
// A failure is said in words on its row rather than as a red badge.
func nlBroadcasts(list []newsletter.Broadcast) string {
	if len(list) == 0 {
		return `<p class="table-empty">Nothing sent yet.</p>`
	}
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString(`<ul class="sa-activity">`)
	for _, br := range list {
		note := "Sending"
		if br.Status == "complete" {
			note = "Sent to " + strconv.Itoa(br.Sent)
			if br.Failed > 0 {
				note += ", " + strconv.Itoa(br.Failed) + " failed"
			}
		}
		b.WriteString(`<li><span class="sa-activity__when">` + esc(config.FormatSite(br.CreatedAt, "2 Jan, 15:04")) + `</span>` +
			`<span class="sa-activity__what">` + esc(br.Subject) + `</span><span class="sa-activity__note">` + esc(note) + `</span></li>`)
	}
	b.WriteString(`</ul>`)
	return b.String()
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

// newsletterSetup is the Newsletter before it can send or has anyone to send
// to. Broadcasts need an SMTP relay, which only the environment sets; the
// Email delivery settings say what is set now and how to change it.
func newsletterSetup() string {
	return string(ui.Setup(ui.SetupPage{
		Icon:  "send",
		Title: "The newsletter isn't set up yet",
		What:  "Email your readers from your own server, with no mailing-list service in between.",
		Steps: []ui.SetupStep{
			{Title: "A mail relay for broadcasts", Detail: "A broadcast goes to every subscriber at once, through an SMTP relay named by SMTP_HOST and its companions in /etc/vayupress/env, read when VayuPress starts."},
			{Title: "Subscribers", Detail: "Each confirms by email before they count, and every broadcast carries a link to leave. Confirmations go out through Mail when no relay is set."},
		},
		Action: `<a class="btn btn--primary" href="/os/settings/email">Email delivery settings</a>`,
	}))
}
