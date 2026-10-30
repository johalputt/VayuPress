// SPDX-License-Identifier: Apache-2.0

package main

// analytics_clickmap.go — a site's click map: where readers clicked on a
// page, for a site whose operator turned it on.
//
// It is the one thing Analytics records that a page view does not carry, so
// it is off until an administrator turns it on for a site, and the server,
// not the page, decides whether a site's clicks are taken: the script that
// records them is served only to a site that has it on, and the endpoint
// refuses every other site's beacon, so a copy of the script cached before
// the operator turned it off records nothing. What is kept is counts
// (internal/analytics/heat.go), never a reader's clicks.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
)

// clickMapSamples are the shares of page loads a click map can record: one
// in N.
var clickMapSamples = []int{1, 4, 10}

// clickMapSample is the N of site's click map ("" the primary), recording one
// page load in N, or 0 when it is off.
func (a *App) clickMapSample(ctx context.Context, site string) int {
	if a.siteSettings == nil || site == analytics.ScopeAllDomains {
		return 0
	}
	sc := settings.ForPrimary()
	if site != "" {
		sc = settings.ForDomain(site)
	}
	n, err := strconv.Atoi(strings.TrimSpace(a.siteSettings.Get(ctx, sc, settings.KeyAnalyticsClickMap)))
	if err != nil || n < 1 {
		return 0
	}
	return n
}

// clickMapJS records one page load in n: each click's element, as its path
// of tags and their order from the body (no text, no class, no attribute),
// and the tenth of it across and down, and sends them as the page is left.
// Clicks in a field, a text area or an editable region are never taken, nor
// are those past fifty a page.
func clickMapJS(n int) string {
	return `!function(){"use strict";if(Math.random()*` + strconv.Itoa(n) + `>=1)return;
var W=innerWidth<640?"phone":innerWidth<1024?"tablet":"desktop",C=[],last="",at=0,run=0;
function path(el){var p=[];while(el&&el.nodeType===1&&el!==document.body){if(p.length===16)return"";var t=el.tagName.toLowerCase(),i=1,s=el;while((s=s.previousElementSibling))if(s.tagName===el.tagName)i++;p.unshift(i>1?t+":"+i:t);el=el.parentElement}return el===document.body?p.join(">"):""}
document.addEventListener("click",function(e){var el=e.target;if(!el||el.nodeType!==1||C.length>=50||el.closest("input,textarea,select,[contenteditable]:not([contenteditable=false])"))return;var p=path(el);if(!p)return;var r=el.getBoundingClientRect();if(!r.width||!r.height)return;
var x=Math.max(0,Math.min(9,Math.floor((e.clientX-r.left)/r.width*10))),y=Math.max(0,Math.min(9,Math.floor((e.clientY-r.top)/r.height*10))),now=Date.now();run=p===last&&now-at<1000?run+1:1;last=p;at=now;
C.push({e:p,x:x,y:y,r:run===3,d:!el.closest("a[href],button,label,summary,[role=button],[role=link],[onclick],[tabindex]")})},true);
function send(){if(!C.length)return;var b=JSON.stringify({u:location.pathname,w:W,c:C});C=[];try{navigator.sendBeacon("/api/v1/analytics/clicks",new Blob([b],{type:"application/json"}))}catch(x){}}
addEventListener("pagehide",send);document.addEventListener("visibilitychange",function(){if(document.visibilityState==="hidden")send()});
}();`
}

// handleAnalyticsClicks takes a page's clicks, from a site whose click map is
// on. Public and unauthenticated, as the collector is, and under its rules:
// the site is the host this install served, a browser's opt-out is honoured,
// one address has the collector's rate, and only a reader is counted.
func (a *App) handleAnalyticsClicks(w http.ResponseWriter, r *http.Request) {
	site := a.contentScope(r)
	if a.analytics == nil || dntHonored(r) || a.clickMapSample(r.Context(), site) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !analyticsLimiter.allow(loginClientIP(r)) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	var body struct {
		U string `json:"u"`
		W string `json:"w"`
		C []struct {
			E    string `json:"e"`
			X, Y int
			R, D bool
		} `json:"c"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&body); err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if a.vayuShield != nil && !countsAsReader(a.vayuShield.Classify(r).Result.ClientType, true) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if a.analyticsGeoRefused(geoFromHeaders(r).Country) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	clicks := make([]analytics.Click, 0, len(body.C))
	for _, c := range body.C {
		clicks = append(clicks, analytics.Click{Element: c.E, X: c.X, Y: c.Y, Rage: c.R, Dead: c.D})
	}
	a.analytics.RecordClicks(site, body.U, body.W, clicks)
	w.WriteHeader(http.StatusNoContent)
}

// handleOSClickMapSave turns the click map of the address's site (the
// primary's, under /os/analytics) on at a share of page loads, or off.
func (a *App) handleOSClickMapSave(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "settings-unavailable", "settings are not available", "")
		return
	}
	sc, v, host := settings.ForPrimary(), primaryView, a.primaryHost(r.Context())
	if d, ok := osScopedDomain(r); ok {
		if d.IsPrimary {
			writeAPIError(w, r, http.StatusNotFound, "unknown-domain", "no such hosted site", "")
			return
		}
		sc, v, host = settings.ForDomain(d.ID), hostedView(d), d.Host
	}
	var body struct {
		Sample int `json:"sample"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid JSON", "")
		return
	}
	value := ""
	if body.Sample != 0 {
		ok := false
		for _, n := range clickMapSamples {
			ok = ok || n == body.Sample
		}
		if !ok {
			writeAPIError(w, r, http.StatusBadRequest, "invalid-sample", "every page load, one in 4 or one in 10, or 0 for off", "")
			return
		}
		value = strconv.Itoa(body.Sample)
	}
	if err := a.siteSettings.SetMany(r.Context(), sc, map[string]string{settings.KeyAnalyticsClickMap: value}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-failed", err.Error(), "")
		return
	}
	dbpkg.AuditLog("analytics.click_map", dbpkg.AuditActor(r), host, "sample="+value)
	a.refreshSiteAnalytics(v, host)
	writeJSON(w, r, http.StatusOK, map[string]any{"status": "ok"})
}

// clickMapNotice is the line an operator can put in the site's privacy
// notice: the install writes none of its own.
const clickMapNotice = "On some visits, this site counts where on a page readers click: the part of the page clicked and the size of the screen, added to a daily count. Nothing typed is recorded, no cookie is set, and the counts cannot be traced to a reader."

// clickMapSection is the setting on a site's Goals, for an administrator.
func (a *App) clickMapSection(ctx context.Context, v analyticsView, nonce string) string {
	if a.siteSettings == nil || v.all() {
		return ""
	}
	n := a.clickMapSample(ctx, v.scope)
	opts := `<option value="0"` + selectedIf(n == 0) + `>Off</option>`
	for _, s := range clickMapSamples {
		label := "One page load in " + strconv.Itoa(s)
		if s == 1 {
			label = "Every page load"
		}
		opts += `<option value="` + strconv.Itoa(s) + `"` + selectedIf(n == s) + `>` + label + `</option>`
	}
	save := primaryAnalyticsBase + "/clickmap"
	if v.hosted {
		save = "/os/d/" + v.d.ID + "/api/analytics/clickmap"
	}
	rows := ui.Rows(ui.Row{Label: "Record clicks", ID: "click-map-sample",
		Control: ui.HTML(`<select id="click-map-sample" class="input" data-click-map-save="` + esc(save) + `">` + opts + `</select>`),
		Hint:    "On, a share of page loads records where readers click: the element (its place among the page's tags, never its text), the tenth of it clicked and the screen's width class, added to a daily count kept as long as the site's other analytics. Nothing typed is recorded, and no reader's clicks are kept apart. Each page's report then shows its click map."})
	return `<div id="click-map">` + string(ui.Section("Click map", "Off unless turned on, for this site alone",
		ui.HTML(string(rows)+`<p class="settings-row-hint">For the site's privacy notice: “`+esc(clickMapNotice)+`”</p>`+
			`<div class="sa-list__sheet-actions"><button type="button" class="btn btn--primary btn--sm" data-click-map-button>Save</button>`+
			`<span class="text-xs muted" id="click-map-status" role="status" aria-live="polite"></span></div>`))) + `</div>` +
		`<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
var sel=document.getElementById('click-map-sample'),btn=document.querySelector('[data-click-map-button]'),st=document.getElementById('click-map-status');
if(!sel||!btn)return;
btn.addEventListener('click',function(){
  btn.disabled=true; if(st)st.textContent='Saving…';
  fetch(sel.getAttribute('data-click-map-save'),{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({sample:parseInt(sel.value,10)})})
    .then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};});})
    .then(function(res){btn.disabled=false; if(st)st.textContent=res.ok?'Saved':((res.j&&res.j.message)||'Could not save');})
    .catch(function(e){btn.disabled=false; if(st)st.textContent='Error: '+e;});
});
})();
</script>`
}

// selectedIf is an option's selected attribute when on.
func selectedIf(on bool) string {
	if on {
		return " selected"
	}
	return ""
}

// clickWidthOf is the width class the address asks a click map for, desktop
// when it names none the map keeps.
func clickWidthOf(r *http.Request) string {
	w := r.URL.Query().Get("cw")
	for _, x := range analytics.Widths {
		if w == x {
			return w
		}
	}
	return "desktop"
}

// clickMapShown is how many elements a page's click map lists and draws.
const clickMapShown = 30

// osClickMap is the page's click map at the address's width: the elements
// most clicked, with the bursts and the clicks on nothing, and, for the
// primary's pages, the page itself with the clicks drawn over it. Every
// reader of the page is counted: a click is kept apart from its visit, so
// the page's filters cannot narrow it, and it says so.
func (a *App) osClickMap(r *http.Request, v analyticsView, per analyticsSpan, admin bool) string {
	ctx := r.Context()
	if v.all() || per.page == "" {
		return ""
	}
	width := clickWidthOf(r)
	els, err := a.analytics.ClickMapScoped(a.scoped(ctx, per), v.scope, per.page, width, per.days, clickMapShown)
	on := a.clickMapSample(ctx, v.scope) > 0
	if !on && len(els) == 0 {
		if !admin {
			return ""
		}
		return `<div data-part="clicks">` + string(ui.Section("Where readers clicked", "",
			ui.HTML(`<p class="table-empty">The click map is off for this site. <a href="`+esc(analyticsHref(v.base, "goals", per))+`#click-map">Turn it on in Goals</a>.</p>`))) + `</div>`
	}
	hint := "Clicks on this page " + per.when() + ", by element, on screens of this width"
	if len(per.focus) > 0 {
		hint += "; every reader is counted here, since a click is not kept with its visit"
	}
	var widths strings.Builder
	widths.WriteString(`<p class="sa-heat__widths">`)
	for i, w := range analytics.Widths {
		if i > 0 {
			widths.WriteString(" · ")
		}
		label := strings.ToUpper(w[:1]) + w[1:]
		if w == width {
			widths.WriteString(`<strong aria-current="true">` + label + `</strong>`)
			continue
		}
		widths.WriteString(`<a href="` + esc(analyticsHref(v.base, "page", per)+"&cw="+w) + `">` + label + `</a>`)
	}
	widths.WriteString(`</p>`)
	rows := make([][]ui.HTML, 0, len(els))
	for _, e := range els {
		rows = append(rows, []ui.HTML{
			ui.HTML(`<span title="` + esc(e.Element) + `">` + esc(elementLabel(e.Element)) + `</span>`),
			ui.Text(strconv.FormatInt(e.Clicks, 10)), ui.Text(strconv.FormatInt(e.Rage, 10)), ui.Text(strconv.FormatInt(e.Dead, 10)),
		})
	}
	empty := "No clicks recorded at this width " + per.when() + "."
	if err != nil {
		empty = "The clicks could not be read: " + err.Error()
	}
	body := widths.String()
	if !v.hosted && len(els) > 0 {
		body += clickOverlay(els, per.page, width)
	}
	body += string(ui.Table([]string{"Element", "Clicks", "In a burst", "On nothing"}, rows, empty))
	return `<div data-part="clicks">` + string(ui.Section("Where readers clicked", hint, ui.HTML(body))) + `</div>`
}

// elementLabel is an element's path as a reader names it: its last three
// steps, each tag with its place among its like ("p 3"), the rest elided.
func elementLabel(e string) string {
	steps := strings.Split(e, ">")
	lead := ""
	if len(steps) > 3 {
		steps, lead = steps[len(steps)-3:], "… › "
	}
	for i, s := range steps {
		steps[i] = strings.Replace(s, ":", " ", 1)
	}
	return lead + strings.Join(steps, " › ")
}

// clickFrameWidths is the width a page is drawn at for each width class.
var clickFrameWidths = map[string]int{"phone": 390, "tablet": 820, "desktop": 1280}

// clickOverlay is the page as the primary serves it, in a frame that runs
// none of its scripts (so drawing it records nothing), with each element's
// clicks for the console's script to draw over it.
func clickOverlay(els []analytics.HeatElement, path, width string) string {
	type drawn struct {
		E string  `json:"e"`
		C []int64 `json:"c"`
	}
	data := make([]drawn, 0, len(els))
	for _, e := range els {
		data = append(data, drawn{E: e.Element, C: e.Cells[:]})
	}
	j, _ := json.Marshal(data)
	return `<div class="sa-heat" data-heat="` + esc(string(j)) + `" data-heat-width="` + strconv.Itoa(clickFrameWidths[width]) + `" aria-hidden="true">` +
		`<div class="sa-heat__stage"><iframe class="sa-heat__frame" sandbox="allow-same-origin" loading="lazy" tabindex="-1" title="The page with its clicks" src="` +
		esc(primaryAnalyticsBase+"/frame?page="+url.QueryEscape(path)) + `"></iframe><div class="sa-heat__layer"></div></div></div>`
}

// handleOSClickMapFrame is one of the primary's pages as a reader is served
// it, for the click map's frame: rendered in-process, as the site preview
// does, and allowed to be framed by this console alone. Every public page
// refuses any frame; this answer is the console's, at its own address.
func (a *App) handleOSClickMapFrame(w http.ResponseWriter, r *http.Request) {
	page := pageOf(r)
	if page == "" {
		http.NotFound(w, r)
		return
	}
	body, rec := previewFetch(r.Context(), a.rootHandler(), a.primaryHost(r.Context()), page)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		http.NotFound(w, r)
		return
	}
	// The page's own policy, which its nonces were written for, framed by
	// this origin alone.
	csp := strings.Replace(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'", "frame-ancestors 'self'", 1)
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(body))
}
