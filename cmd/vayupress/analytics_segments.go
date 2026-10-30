// SPDX-License-Identifier: Apache-2.0

package main

// analytics_segments.go — segments on the console: saved from the Explorer's
// filters, applied there with its own, listed on the Analytics page with the
// visitors each held over the period, and deleted from the Explorer. A site's
// segments are its own; the install's count every site.

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/ui"
)

// segmentScope is the scope a segment request acts on, with its site's host:
// the URL's hosted site under /os/d/{id}, else the install's.
func segmentScope(r *http.Request) (scope, host string) {
	if d, ok := osScopedDomain(r); ok {
		return d.ID, d.Host
	}
	return analytics.ScopeAllDomains, ""
}

// handleOSSegmentCreate saves a segment for the install or the URL's site.
func (a *App) handleOSSegmentCreate(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	scope, host := segmentScope(r)
	var body struct {
		DomainID string                    `json:"domain_id"`
		Name     string                    `json:"name"`
		Filters  []analytics.ExploreFilter `json:"filters"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid JSON", "")
		return
	}
	if scope != analytics.ScopeAllDomains && !requireScopeMatchesPath(r, body.DomainID) {
		writeAPIError(w, r, http.StatusBadRequest, "scope-mismatch",
			"this request names a different site from the one in its address; nothing was saved", "")
		return
	}
	id, err := a.analytics.CreateSegment(r.Context(), scope, body.Name, body.Filters)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid-segment", err.Error(), "")
		return
	}
	a.refreshDefinitionReports(scope, host)
	dbpkg.AuditLog("analytics.segment.create", dbpkg.AuditActor(r), id, goalAuditSite(scope)+segmentWords(body.Filters))
	writeJSON(w, r, http.StatusCreated, map[string]string{"id": id})
}

// handleOSSegmentDelete removes one of the scope's segments; another site's
// is not found from here.
func (a *App) handleOSSegmentDelete(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	scope, host := segmentScope(r)
	id := chi.URLParam(r, "sid")
	if err := a.analytics.DeleteSegment(r.Context(), scope, id); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, analytics.ErrNoSegment) {
			code = http.StatusNotFound
		}
		writeAPIError(w, r, code, "not-deleted", err.Error(), "")
		return
	}
	a.refreshDefinitionReports(scope, host)
	dbpkg.AuditLog("analytics.segment.delete", dbpkg.AuditActor(r), id, goalAuditSite(scope))
	writeJSON(w, r, http.StatusOK, map[string]bool{"deleted": true})
}

// filterWords is one filter in words: `page starts with "/blog/"`.
func filterWords(f analytics.ExploreFilter) string {
	op := map[string]string{analytics.FilterIs: "is", analytics.FilterIsNot: "is not", analytics.FilterPrefix: "starts with",
		analytics.FilterAtLeast: "at least", analytics.FilterAtMost: "at most"}[f.Op]
	return strings.ToLower(exploreDimLabel(f.Dim)) + " " + op + " " + strconv.Quote(f.Value)
}

// segmentWords is a segment's filters in words, joined by "and".
func segmentWords(fs []analytics.ExploreFilter) string {
	parts := make([]string, 0, len(fs))
	for _, f := range fs {
		parts = append(parts, filterWords(f))
	}
	return strings.Join(parts, " and ")
}

// segmentFocus is a segment's filters as the address writes them.
func segmentFocus(fs []analytics.ExploreFilter) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, encodeFocus(f))
	}
	return out
}

// osSegmentsSection is the Analytics page's segments: each with the
// visitors it held over the period and their share of all of them, and the
// way into the Explorer with it applied: v's segments, counted over v's
// visits. Counted in the page's background cache, which a segment saved or
// deleted refreshes.
func (a *App) osSegmentsSection(ctx context.Context, v analyticsView, days int, p analyticsSpan) string {
	scope, explore := v.scope, v.base+"/explore"
	list, err := a.analytics.ListSegments(ctx, v.defs)
	if err != nil || len(list) == 0 {
		return string(ui.Section("Segments", "Saved filters, none yet", ui.HTML(`<p class="table-empty">No segments yet. Save one from the <a href="`+
			html.EscapeString(explore+"?days="+strconv.Itoa(days))+`">Explorer</a>.</p>`)))
	}
	all, err := a.analytics.Explore(ctx, analytics.ExploreQuery{Scope: scope, Metric: analytics.ExploreVisitors, Days: days})
	if err != nil {
		return string(ui.Section("Segments", "", ui.Callout("danger", ui.Text("The segments could not be counted: "+err.Error()))))
	}
	rows := make([][]ui.HTML, 0, len(list))
	for _, g := range list {
		q := analytics.ExploreQuery{Scope: scope, Metric: analytics.ExploreVisitors, Segment: g.Filters, Days: days}
		count, share := "—", ""
		if res, err := a.analytics.Explore(ctx, q); err == nil {
			count, share = strconv.FormatFloat(res.Total, 'f', 0, 64), osShareLabel(int(res.Total), int(all.Total))
		}
		// A segment opens as the page narrowed to it, when its filters are
		// ones a page takes; the Explorer has it either way.
		link := explore + "?seg=" + g.ID + "&metric=visitors&by=day&days=" + strconv.Itoa(days)
		name := `<a href="` + html.EscapeString(link) + `">` + html.EscapeString(g.Name) + `</a>`
		if in := (url.Values{focusParam: segmentFocus(g.Filters)}); len(g.Filters) <= analytics.ExploreMaxFilters && len(focusOf(in)) == len(g.Filters) {
			np := p
			np.focus = focusOf(in)
			name = `<a href="` + html.EscapeString(analyticsHref(v.base, "overview", np)) + `">` + html.EscapeString(g.Name) + `</a> <a class="muted text-xs" href="` + html.EscapeString(link) + `">Explorer</a>`
		}
		rows = append(rows, []ui.HTML{
			ui.HTML(name + `<br><span class="muted text-xs">` + html.EscapeString(segmentWords(g.Filters)) + `</span>`),
			ui.Text(count), ui.HTML(share),
		})
	}
	return string(ui.Section("Segments", countOf(len(list), "segment")+", visitors in each", ui.Table([]string{"Segment", "Visitors", "Share"}, rows, "")))
}

// exploreSegments is the Explorer's part for segments: the scope's
// segments, each to use or delete, the ones to start from, and saving the
// filters set now as a segment.
func exploreSegments(base, api, site string, list []analytics.Segment, x exploreAsk, nonce string) string {
	var b strings.Builder
	rows := make([][]ui.HTML, 0, len(list))
	for _, g := range list {
		use := exploreAsk{q: x.q, bars: x.bars, seg: g.ID}
		rows = append(rows, []ui.HTML{
			ui.HTML(`<a href="` + html.EscapeString(base+"?"+use.values().Encode()) + `">` + html.EscapeString(g.Name) + `</a>`),
			ui.Text(segmentWords(g.Filters)),
			ui.HTML(`<button type="button" class="btn btn--ghost btn--sm" data-segment-delete="` + html.EscapeString(g.ID) + `">Delete</button>`),
		})
	}
	b.WriteString(string(ui.Table([]string{"Segment", "Who is in it", ""}, rows, "No segments yet.")))
	var presets []string
	for _, p := range analytics.SegmentPresets() {
		start := exploreAsk{q: x.q, bars: x.bars}
		start.q.Filters = p.Filters
		presets = append(presets, `<a href="`+html.EscapeString(base+"?"+start.values().Encode())+`">`+html.EscapeString(p.Name)+`</a>`)
	}
	b.WriteString(`<p class="settings-row-hint">Start from: ` + strings.Join(presets, " · ") + `. Adjust the filters, count, then save them.</p>`)
	if len(x.q.Filters) > 0 {
		b.WriteString(`<div class="page-lead"><button type="button" class="btn btn--sm" data-sheet="segment-new">Save these filters as a segment</button></div>`)
		b.WriteString(string(segmentSaveSheet(x.q.Filters, "")))
	}
	b.WriteString(`<span class="text-xs muted" id="segment-status" role="status" aria-live="polite"></span>`)
	return string(ui.Section("Segments", "Saved filters, kept for this site", ui.HTML(`<div id="segments" data-segments-api="`+html.EscapeString(api)+
		`" data-segments-site="`+html.EscapeString(site)+`"></div>`+b.String()))) + segmentsScript(nonce)
}

// segmentsScript saves and deletes segments, then reloads to show the
// server's own list.
func segmentsScript(nonce string) string {
	return `<script nonce="` + nonce + `">
(function(){'use strict';
var ctx=document.getElementById('segments'); if(!ctx)return;
var API=ctx.getAttribute('data-segments-api'), SITE=ctx.getAttribute('data-segments-site');
var st=document.getElementById('segment-status');
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
function call(method,path,body){
  if(st)st.textContent='Saving…';
  var o={method:method,headers:{'X-CSRF-Token':csrf()}};
  if(body){o.headers['Content-Type']='application/json';o.body=JSON.stringify(body);}
  fetch(API+path,o).then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};});})
    .then(function(res){if(res.ok){location.reload();return;} if(st)st.textContent=(res.j&&res.j.message)||'Could not save';})
    .catch(function(e){if(st)st.textContent='Error: '+e;});
}
var save=document.querySelector('[data-segment-save]');
if(save)save.addEventListener('click',function(){
  var n=document.getElementById('segment-name');
  call('POST','',{domain_id:SITE,name:n?n.value:'',filters:JSON.parse(save.getAttribute('data-filters'))});
});
document.querySelectorAll('[data-segment-delete]').forEach(function(b){b.addEventListener('click',function(){
  vpConfirm({title:'Delete this segment?',message:'The figures it was used for are not changed.',confirm:'Delete'},function(){
    call('DELETE','/'+encodeURIComponent(b.getAttribute('data-segment-delete')),null);
  });
});});
})();
</script>`
}
