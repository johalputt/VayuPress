// SPDX-License-Identifier: Apache-2.0

package main

// analytics_alerts.go — the alerts on the install's Analytics page and on each
// hosted site's: the check that runs them once a minute, the mail a firing
// sends, the bell that shows firings until they are marked seen, and the
// section that lists, adds and deletes them with their history.
//
// Adding or deleting an alert is for an administrator alone, although an
// editor reads Analytics: an alert mails the address it is given, and an
// editor who could set one could send the install's mail anywhere.

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/alerts"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/ui"
)

// alertsHistoryShown is how many firings the section lists.
const alertsHistoryShown = 20

// startAlerts runs the check once a minute, the finest window's own step.
func (a *App) startAlerts(done <-chan struct{}) {
	if a.alerts == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
			}
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
			a.checkAlerts(ctx)
			cancel()
		}
	}()
}

// alertHosts is each hosted site's host by id; only those sites' alerts are
// counted, so a removed site's alerts stop.
func (a *App) alertHosts(ctx context.Context) map[string]string {
	hosts := map[string]string{}
	if a.domains == nil {
		return hosts
	}
	list, err := a.domains.List(ctx)
	if err != nil {
		return hosts
	}
	for _, d := range list {
		if !d.IsPrimary {
			hosts[d.ID] = d.Host
		}
	}
	return hosts
}

// checkAlerts runs every alert once and wakes the bells when one fired.
func (a *App) checkAlerts(ctx context.Context) {
	hosts := a.alertHosts(ctx)
	fired, err := a.alerts.Check(ctx, func(site string) bool { _, ok := hosts[site]; return ok }, a.mailAlert(hosts))
	if err != nil {
		logging.LogInfo("alerts", "check stopped: "+err.Error())
	}
	for _, f := range fired {
		logging.LogInfo("alerts", alertWhere(f.Site, hosts)+": "+f.Name+" fired at "+strconv.Itoa(f.Value)+" ("+f.Condition+")")
	}
	if len(fired) > 0 {
		a.bellLive.fire()
	}
}

// alertWhere names an alert's scope in a sentence.
func alertWhere(site string, hosts map[string]string) string {
	if site == alerts.Install {
		return "this install"
	}
	if h, ok := hosts[site]; ok {
		return h
	}
	return "a removed site"
}

// alertPage is the console page that lists site's alerts.
func alertPage(site string) string {
	if site == alerts.Install {
		return "/os/analytics#alerts"
	}
	return "/os/d/" + site + "/analytics#alerts"
}

// mailAlert sends a firing to its alert's address and says what became of it,
// which the history shows beside the firing.
func (a *App) mailAlert(hosts map[string]string) alerts.Mailer {
	return func(_ context.Context, al alerts.Alert, value int) string {
		if a.mailer == nil || !a.mailer.Enabled() {
			return "not sent: this install has no outgoing mail set up"
		}
		where := alertWhere(al.Site, hosts)
		text := strconv.Itoa(value) + " " + al.Noun() + " on " + where + " in the last " + alerts.Minutes(al.WindowMin) + ".\n\n" +
			"Alert \"" + al.Name + "\": " + al.Condition() + ". It will not fire again for " + alerts.Minutes(al.CooldownMin) + "."
		if config.Cfg.Domain != "" {
			text += "\n\nhttps://" + config.Cfg.Domain + alertPage(al.Site)
		}
		if err := a.mailer.Send(email.Message{To: al.MailTo, Subject: "[" + where + "] " + al.Name, Text: text}); err != nil {
			return "not sent: " + err.Error()
		}
		return "sent to " + al.MailTo
	}
}

// alertNotices are the bell's lines for firings not yet seen, one per site.
func (a *App) alertNotices(ctx context.Context) []osNotification {
	if a.alerts == nil {
		return nil
	}
	unseen, err := a.alerts.UnseenBySite(ctx)
	if err != nil || len(unseen) == 0 {
		return nil
	}
	hosts := a.alertHosts(ctx)
	out := make([]osNotification, 0, len(unseen))
	for _, u := range unseen {
		if _, ok := hosts[u.Site]; !ok && u.Site != alerts.Install {
			continue
		}
		detail := "alert" + plural(u.Count) + " fired on " + alertWhere(u.Site, hosts)
		out = append(out, osNotification{Title: "Alerts", Detail: detail, Href: alertPage(u.Site), Count: u.Count, Kind: "alert", Severity: u.Severity})
	}
	return out
}

// alertScope is the scope a request to the alerts' API acts on: the URL's
// hosted site under /os/d/{id}, else the install's. It answers the refusal
// itself.
func (a *App) alertScope(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return "", false
	}
	if a.alerts == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "alerts-off", "this install was started without its database", "")
		return "", false
	}
	if !strings.HasPrefix(r.URL.Path, "/os/d/") {
		return alerts.Install, true
	}
	d, ok := osScopedDomain(r)
	if !ok || d.IsPrimary {
		writeAPIError(w, r, http.StatusNotFound, "unknown-domain", "no such hosted site", "")
		return "", false
	}
	return d.ID, true
}

// handleOSAlertCreate adds an alert to the install or the URL's site.
func (a *App) handleOSAlertCreate(w http.ResponseWriter, r *http.Request) {
	site, ok := a.alertScope(w, r)
	if !ok {
		return
	}
	var body struct {
		DomainID string       `json:"domain_id"`
		Alert    alerts.Draft `json:"alert"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid JSON", "")
		return
	}
	if site != alerts.Install && !requireScopeMatchesPath(r, body.DomainID) {
		writeAPIError(w, r, http.StatusBadRequest, "scope-mismatch",
			"this request names a different site from the one in its address; nothing was saved", "")
		return
	}
	al, err := body.Alert.Validate(site)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid-alert", err.Error(), "")
		return
	}
	id, err := a.alerts.Create(r.Context(), al)
	if errors.Is(err, alerts.ErrTooMany) {
		writeAPIError(w, r, http.StatusConflict, "too-many-alerts", err.Error(), "")
		return
	}
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-failed", err.Error(), "")
		return
	}
	dbpkg.AuditLog("analytics.alert_create", dbpkg.AuditActor(r), site, id+" "+al.Condition())
	writeJSON(w, r, http.StatusOK, map[string]any{"status": "ok", "id": id})
}

// handleOSAlertDelete removes one of the scope's alerts; another site's id
// matches nothing.
func (a *App) handleOSAlertDelete(w http.ResponseWriter, r *http.Request) {
	site, ok := a.alertScope(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "aid")
	found, err := a.alerts.Delete(r.Context(), site, id)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "delete-failed", err.Error(), "")
		return
	}
	if !found {
		writeAPIError(w, r, http.StatusNotFound, "unknown-alert", "no such alert here", "")
		return
	}
	dbpkg.AuditLog("analytics.alert_delete", dbpkg.AuditActor(r), site, id)
	writeJSON(w, r, http.StatusOK, map[string]any{"status": "ok"})
}

// handleOSAlertsSeen marks the scope's firings seen, which clears them from
// the bell in every open tab.
func (a *App) handleOSAlertsSeen(w http.ResponseWriter, r *http.Request) {
	site, ok := a.alertScope(w, r)
	if !ok {
		return
	}
	n, err := a.alerts.MarkSeen(r.Context(), site)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-failed", err.Error(), "")
		return
	}
	a.bellLive.fire()
	writeJSON(w, r, http.StatusOK, map[string]any{"status": "ok", "marked": n})
}

// alertsSection is the alerts of site (alerts.Install, or a hosted site's id)
// with their history. The form is drawn for an administrator alone, who is
// the one the API lets save it.
func (a *App) alertsSection(ctx context.Context, site, api string, admin bool, nonce string) string {
	if a.alerts == nil {
		return ""
	}
	list, err := a.alerts.List(ctx, site)
	if err != nil {
		return string(ui.Section("Alerts", "", ui.Callout("danger", ui.Text("The alerts could not be read: "+err.Error()))))
	}
	history, _ := a.alerts.History(ctx, site, alertsHistoryShown)
	now := time.Now()
	rows := make([][]ui.HTML, 0, len(list))
	for _, al := range list {
		last := "Not checked yet"
		if al.Checked {
			last = strconv.Itoa(al.LastValue) + " " + al.Noun() + ", " + humanAgo(al.CheckedAt, now)
		}
		fired := "Never"
		if !al.LastFiredAt.IsZero() {
			fired = humanAgo(al.LastFiredAt, now)
		}
		mail := "Bell only"
		if al.MailTo != "" {
			mail = "Bell and " + al.MailTo
		}
		cells := []ui.HTML{
			ui.HTML(`<strong>` + html.EscapeString(al.Name) + `</strong><br><span class="muted text-xs">` + html.EscapeString(al.Condition()) +
				`, at most once every ` + html.EscapeString(alerts.Minutes(al.CooldownMin)) + `</span>`),
			ui.State(al.Severity, severityWord[al.Severity]),
			ui.Text(mail), ui.Text(last), ui.Text(fired),
		}
		if admin {
			cells = append(cells, ui.HTML(`<button type="button" class="btn btn--ghost btn--sm" data-alert-delete="`+html.EscapeString(al.ID)+`">Delete</button>`))
		}
		rows = append(rows, cells)
	}
	headers := []string{"Alert", "Severity", "Tells", "Last count", "Last fired"}
	if admin {
		headers = append(headers, "")
	}
	empty := "No alerts yet."
	if admin {
		empty = "No alerts yet. Add one to be told when a figure crosses a threshold."
	}
	unseen := 0
	hrows := make([][]ui.HTML, 0, len(history))
	for _, f := range history {
		if !f.Seen {
			unseen++
		}
		mailed := f.Mailed
		if mailed == "" {
			mailed = "Bell only"
		}
		hrows = append(hrows, []ui.HTML{
			ui.Text(f.At.UTC().Format("2006-01-02 15:04") + " UTC"),
			ui.HTML(html.EscapeString(f.Name) + `<br><span class="muted text-xs">` + html.EscapeString(f.Condition) + `</span>`),
			ui.Text(strconv.Itoa(f.Value) + " " + f.Noun),
			ui.State(f.Severity, severityWord[f.Severity]),
			ui.Text(mailed),
		})
	}
	var b strings.Builder
	b.WriteString(`<div id="alerts" data-alerts-api="` + html.EscapeString(api) + `" data-alerts-site="` + html.EscapeString(site) + `"></div>`)
	if admin {
		b.WriteString(`<div class="page-lead"><button type="button" class="btn btn--primary btn--sm" data-sheet="alert-new">Add an alert</button></div>`)
	}
	b.WriteString(string(ui.Table(headers, rows, empty)))
	if len(history) > 0 {
		lead := `<h3 class="text-sm mt-3">Fired</h3>`
		if unseen > 0 && admin {
			lead += `<p class="text-sm">` + countOf(unseen, "firing") + ` not yet seen. <button type="button" class="btn btn--ghost btn--sm" data-alerts-seen>Mark as seen</button></p>`
		}
		b.WriteString(lead + string(ui.Table([]string{"When", "Alert", "Counted", "Severity", "Mail"}, hrows, "")))
	}
	b.WriteString(`<span class="text-xs muted" id="alerts-status" role="status" aria-live="polite"></span>`)
	if admin {
		b.WriteString(string(ui.Sheet("alert-new", "Add an alert", alertForm())))
	}
	out := string(ui.Section("Alerts", "Checked every minute", ui.HTML(b.String())))
	if admin {
		out += alertsScript(nonce)
	}
	return out
}

// severityWord is a severity as the page says it; its tone is the severity
// itself, and info, which ui.State does not know, reads as neutral.
var severityWord = map[string]string{"info": "Info", "warn": "Warn", "danger": "Danger"}

// alertForm is the new alert's fields.
func alertForm() ui.HTML {
	sel := func(id string, opts []alerts.Choice, pick string) string {
		var b strings.Builder
		b.WriteString(`<select class="input" id="` + id + `">`)
		for _, o := range opts {
			s := ""
			if o.Value == pick {
				s = " selected"
			}
			b.WriteString(`<option value="` + html.EscapeString(o.Value) + `"` + s + `>` + html.EscapeString(o.Label) + `</option>`)
		}
		b.WriteString(`</select>`)
		return b.String()
	}
	minutes := func(list []int) []alerts.Choice {
		out := make([]alerts.Choice, 0, len(list))
		for _, n := range list {
			out = append(out, alerts.Choice{Value: strconv.Itoa(n), Label: alerts.Minutes(n)})
		}
		return out
	}
	return ui.Rows(
		ui.Row{Label: "Name", ID: "alert-name", Control: ui.HTML(`<input class="input" id="alert-name" maxlength="80" placeholder="Traffic spike">`)},
		ui.Row{Label: "Counts", ID: "alert-metric", Hint: "Each is counted as the reports count it.", Control: ui.HTML(sel("alert-metric", alerts.MetricChoices(), "views"))},
		ui.Row{Label: "Fires when the count is", ID: "alert-op", Control: ui.HTML(sel("alert-op", []alerts.Choice{{Value: "above", Label: "Above"}, {Value: "below", Label: "Below"}}, "above"))},
		ui.Row{Label: "Threshold", ID: "alert-threshold", Control: ui.HTML(`<input class="input" id="alert-threshold" type="number" min="0" step="1" value="100">`)},
		ui.Row{Label: "Over the last", ID: "alert-window", Control: ui.HTML(sel("alert-window", minutes(alerts.Windows), "15"))},
		ui.Row{Label: "Then waits", ID: "alert-cooldown", Hint: "Before it may fire again, however long the count stays past the threshold.", Control: ui.HTML(sel("alert-cooldown", minutes(alerts.Cooldowns), "60"))},
		ui.Row{Label: "Severity", ID: "alert-severity", Hint: "The colour of its line in the bell.", Control: ui.HTML(sel("alert-severity", []alerts.Choice{{Value: "info", Label: severityWord["info"]}, {Value: "warn", Label: severityWord["warn"]}, {Value: "danger", Label: severityWord["danger"]}}, "warn"))},
		ui.Row{Label: "Also mail", ID: "alert-mail", Hint: "One address, or empty for the bell alone.", Control: ui.HTML(`<input class="input" id="alert-mail" type="email" maxlength="254" placeholder="ops@example.com">`)},
	) + ui.HTML(`<div class="mt-3"><button type="button" class="btn btn--primary btn--sm" data-alert-create>Add the alert</button> <span class="text-xs muted" id="alert-new-status" role="status" aria-live="polite"></span></div>`)
}

// alertsScript adds, deletes and marks seen, then reloads to show the
// server's own list.
func alertsScript(nonce string) string {
	return `<script nonce="` + nonce + `">
(function(){'use strict';
var ctx=document.getElementById('alerts'); if(!ctx)return;
var API=ctx.getAttribute('data-alerts-api'), SITE=ctx.getAttribute('data-alerts-site');
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
function call(method,path,body,st){
  if(st)st.textContent='Saving…';
  var o={method:method,headers:{'X-CSRF-Token':csrf()}};
  if(body){o.headers['Content-Type']='application/json';o.body=JSON.stringify(body);}
  fetch(API+path,o).then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};});})
    .then(function(res){if(res.ok){location.reload();return;} if(st)st.textContent=(res.j&&res.j.message)||'Could not save';})
    .catch(function(e){if(st)st.textContent='Error: '+e;});
}
function v(id){var el=document.getElementById(id);return el?el.value:'';}
var add=document.querySelector('[data-alert-create]');
if(add)add.addEventListener('click',function(){
  call('POST','',{domain_id:SITE==='*'?'':SITE,alert:{name:v('alert-name'),metric:v('alert-metric'),op:v('alert-op'),threshold:v('alert-threshold'),
    window:v('alert-window'),cooldown:v('alert-cooldown'),severity:v('alert-severity'),mail_to:v('alert-mail')}},document.getElementById('alert-new-status'));
});
document.querySelectorAll('[data-alert-delete]').forEach(function(b){b.addEventListener('click',function(){
  vpConfirm({title:'Delete this alert?',message:'Its firings stay in the history.',confirm:'Delete'},function(){
    call('DELETE','/'+encodeURIComponent(b.getAttribute('data-alert-delete')),null,document.getElementById('alerts-status'));
  });
});});
var seen=document.querySelector('[data-alerts-seen]');
if(seen)seen.addEventListener('click',function(){call('POST','/seen',null,document.getElementById('alerts-status'));});
})();
</script>`
}
