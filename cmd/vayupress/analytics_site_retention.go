// SPDX-License-Identifier: Apache-2.0

package main

// analytics_site_retention.go — how long a hosted site keeps its readers'
// analytics.
//
// The install's retention (AnalyticsRetainDays) is what the privacy
// disclosure publishes, so a site may keep its data for less time than that
// and never for more: a longer setting would keep data the install has said
// it deletes. Empty inherits the install's. The site's purge runs with the
// install's, and its last run is kept with the site's settings so the page can
// say when it ran and what it removed.

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
)

// siteRetainDays is the days site keeps its analytics, and whether that is
// its own setting rather than the install's.
func (a *App) siteRetainDays(ctx context.Context, site string) (int, bool) {
	install := config.Cfg.AnalyticsRetainDays
	if a.siteSettings == nil {
		return install, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(a.siteSettings.Get(ctx, settings.ForDomain(site), settings.KeyAnalyticsSiteRetainDays)))
	if err != nil || n <= 0 || (install > 0 && n >= install) {
		return install, false
	}
	return n, true
}

// purgeSitesAnalytics runs each hosted site's own purge, for the sites that
// keep their data for less time than the install, and records each run.
func (a *App) purgeSitesAnalytics(ctx context.Context) {
	if a.domains == nil || a.siteSettings == nil {
		return
	}
	list, err := a.domains.List(ctx)
	if err != nil {
		return
	}
	for _, d := range list {
		if d.IsPrimary {
			continue
		}
		days, own := a.siteRetainDays(ctx, d.ID)
		if !own {
			continue
		}
		var removed int64
		if a.analytics != nil {
			n, err := a.analytics.PurgeSite(ctx, d.ID, days)
			if err != nil {
				logging.LogInfo("analytics", "site purge for "+d.Host+" failed: "+err.Error())
				continue
			}
			removed += n
		}
		if a.vaEngagement != nil {
			n, _ := a.vaEngagement.PurgeSite(ctx, d.ID, days)
			removed += n
		}
		_ = a.siteSettings.SetMany(ctx, settings.ForDomain(d.ID), map[string]string{
			settings.KeyAnalyticsSiteRetainLast: time.Now().UTC().Format(time.RFC3339) + " " + strconv.FormatInt(removed, 10),
		})
		if removed > 0 {
			logging.LogInfo("analytics", fmt.Sprintf("purged %d rows of %s older than %dd", removed, d.Host, days))
		}
	}
}

// handleOSScopedRetention saves how long the URL's site keeps its analytics.
func (a *App) handleOSScopedRetention(w http.ResponseWriter, r *http.Request) {
	d, posted, ok := a.scopedSiteBody(w, r)
	if !ok {
		return
	}
	v := strings.TrimSpace(posted[settings.KeyAnalyticsSiteRetainDays])
	install := config.Cfg.AnalyticsRetainDays
	if v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeAPIError(w, r, http.StatusBadRequest, "invalid-days", "a number of days, or empty to keep the install's", "")
			return
		}
		if install > 0 && n > install {
			writeAPIError(w, r, http.StatusBadRequest, "longer-than-install",
				"this install deletes analytics after "+countOf(install, "day")+", as its privacy notice says; a site may keep its data for less, never for more", "")
			return
		}
		v = strconv.Itoa(n) // the install's own number reads as inherited (siteRetainDays)
	}
	if err := a.siteSettings.SetMany(r.Context(), settings.ForDomain(d.ID), map[string]string{settings.KeyAnalyticsSiteRetainDays: v}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-failed", err.Error(), "")
		return
	}
	dbpkg.AuditLog("analytics.site_retention", dbpkg.AuditActor(r), d.Host, "days="+v)
	writeJSON(w, r, http.StatusOK, map[string]any{"status": "ok"})
}

// siteRetentionSection is the setting on the site's Analytics page, with its
// last run.
func (a *App) siteRetentionSection(ctx context.Context, d domain.Domain, nonce string) string {
	if a.siteSettings == nil || d.IsPrimary {
		return ""
	}
	install := config.Cfg.AnalyticsRetainDays
	days, own := a.siteRetainDays(ctx, d.ID)
	value := ""
	state := "Inherited: the install deletes analytics after " + countOf(install, "day") + "."
	if own {
		value = strconv.Itoa(days)
		state = "Set for this site: kept " + countOf(days, "day") + ", where the install keeps " + countOf(install, "day") + "."
	}
	last := "Not run yet for this site. It runs a few minutes after start and every day."
	if v := strings.Fields(a.siteSettings.Get(ctx, settings.ForDomain(d.ID), settings.KeyAnalyticsSiteRetainLast)); len(v) == 2 {
		if at, err := time.Parse(time.RFC3339, v[0]); err == nil {
			n, _ := strconv.Atoi(v[1])
			last = "Last run " + humanAgo(at, time.Now()) + ", removing " + countOf(n, "row") + "."
		}
	}
	control := `<input id="site-retain-days" type="number" min="1" max="` + strconv.Itoa(install) + `" class="input" value="` + esc(value) +
		`" placeholder="` + strconv.Itoa(install) + `" data-retain-site="` + esc(d.ID) + `">`
	rows := ui.Rows(ui.Row{Label: "Days kept", ID: "site-retain-days", Control: ui.HTML(control),
		Hint: state + " A site may keep its readers' data for less time than the install, never for more. " + last})
	return string(ui.Fold("How long this site's data is kept", "Views, visits, events and engagement",
		ui.HTML(string(rows)+`<div class="sa-list__sheet-actions"><button type="button" class="btn btn--primary btn--sm" data-retain-save>Save</button>`+
			`<span class="text-xs muted" id="site-retain-status" role="status" aria-live="polite"></span></div>`))) +
		siteRetentionScript(nonce)
}

// siteRetentionScript saves the setting and says what the server answered.
func siteRetentionScript(nonce string) string {
	return `<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
var input=document.getElementById('site-retain-days');
var btn=document.querySelector('[data-retain-save]');
var st=document.getElementById('site-retain-status');
if(!input||!btn)return;
var ID=input.getAttribute('data-retain-site');
btn.addEventListener('click',function(){
  var body={domain_id:ID,settings:{}};body.settings['` + settings.KeyAnalyticsSiteRetainDays + `']=input.value;
  btn.disabled=true; if(st)st.textContent='Saving…';
  fetch('/os/d/'+encodeURIComponent(ID)+'/api/analytics/retention',{method:'POST',
    headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify(body)})
    .then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};});})
    .then(function(res){btn.disabled=false; if(st)st.textContent=res.ok?'Saved':((res.j&&res.j.message)||'Could not save');})
    .catch(function(e){btn.disabled=false; if(st)st.textContent='Error: '+e;});
});
})();
</script>`
}
