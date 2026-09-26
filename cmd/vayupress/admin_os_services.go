// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_services.go — Site › Outside services: what a site's public pages
// may load from other sites (render.SitePolicy). The primary's is at
// /os/website/services, a hosted site's at /os/d/{id}/services.

import (
	"context"
	"encoding/json"
	htmpl "html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
)

// cspDirectiveWords names each directive the way the page speaks of it.
var cspDirectiveWords = map[string]string{
	"script-src": "Scripts", "connect-src": "Connections", "style-src": "Styles", "font-src": "Fonts",
	"img-src": "Images", "media-src": "Audio and video", "frame-src": "Embeds", "form-action": "Form destinations",
}

// sitePolicyIn reads a site's policy from its settings scope. Anything
// unreadable, or in a mode this build does not know, is the strict baseline.
// Its sources need no second check here: Apply admits only what the grammar
// allows, whatever is stored.
func (a *App) sitePolicyIn(ctx context.Context, sc settings.Scope) render.SitePolicy {
	strict := render.SitePolicy{Mode: render.PolicyStrict}
	if a.siteSettings == nil {
		return strict
	}
	raw := strings.TrimSpace(a.siteSettings.Get(ctx, sc, settings.KeyCSPPolicy))
	if raw == "" {
		return strict
	}
	var p render.SitePolicy
	if json.Unmarshal([]byte(raw), &p) != nil {
		return strict
	}
	switch p.Mode {
	case render.PolicyCustom, render.PolicyReport:
		return p
	}
	return strict
}

// servicesSite is the site a services page or save acts on: its settings
// scope, the hosts its reports come from, where it saves, and its name.
func (a *App) servicesSite(r *http.Request) (sc settings.Scope, hosts []string, save, name string) {
	if d, ok := osScopedDomain(r); ok {
		return settings.ForDomain(d.ID), []string{d.Host}, "/os/d/" + d.ID + "/api/csp", d.Host
	}
	host := strings.ToLower(strings.TrimSpace(config.Cfg.Domain))
	return settings.ForPrimary(), []string{host, "www." + host}, "/os/api/website/csp", host
}

// cspBlockAction is what the page offers for one blocked origin: Allow when
// a typed source may name it, turning on the service that covers it, or why
// neither is possible.
func cspBlockAction(b cspBlock) ui.HTML {
	if render.ValidateSource(b.Directive, b.Origin) == nil {
		return ui.HTML(`<button type="button" class="btn btn--sm" data-csp-allow="` + string(ui.Text(b.Directive)) + `" data-origin="` + string(ui.Text(b.Origin)) + `">Allow</button>`)
	}
	if s, ok := render.ServiceFor(b.Directive, b.Origin); ok {
		return ui.HTML(`<button type="button" class="btn btn--sm" data-csp-turn-on="` + string(ui.Text(s.ID)) + `">Turn on ` + string(ui.Text(s.Name)) + `</button>`)
	}
	switch {
	case b.Origin == "inline":
		return ui.HTML(`<span class="muted">Code written into the page is never allowed. Move it into a file.</span>`)
	case b.Origin == "eval":
		return ui.HTML(`<span class="muted">Building code from text is never allowed here.</span>`)
	case b.Directive == "script-src" || b.Directive == "connect-src":
		return ui.HTML(`<span class="muted">Scripts come only from the services above.</span>`)
	}
	return ui.HTML(`<span class="muted">Not an address a site can allow.</span>`)
}

func (a *App) handleOSServices(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	sc, hosts, save, name := a.servicesSite(r)
	p := a.sitePolicyIn(r.Context(), sc)
	now := time.Now().UTC()
	mode := p.Enforced(now)

	var b strings.Builder
	// Every change saves as it is made, the way Add and Allow always did, so
	// there is no unsaved state to lose and no bar to find on a phone.
	b.WriteString(`<div data-csp-save="` + save + `">`)
	b.WriteString(string(ui.Page("Outside services", "What "+name+"'s public pages may load from other sites. Strict is the default: nothing from outside loads.", "")))
	if config.Cfg.OnionMode {
		b.WriteString(string(ui.Callout("info", "This is the Tor world. Every outside address is removed before a page is sent, whatever is chosen here, so a visitor's browser never leaves the onion.")))
	}

	option := func(value, title, hint string) string {
		checked := ""
		if mode == value {
			checked = " checked"
		}
		return `<label class="csp-mode"><input type="radio" name="csp-mode" value="` + value + `"` + checked + `><span><strong>` + title + `</strong><span class="muted">` + hint + `</span></span></label>`
	}
	state := ""
	switch {
	case mode == render.PolicyReport:
		state = `<p class="csp-note">Report only until <time datetime="` + p.ReportUntil.Format(time.RFC3339) + `">` + p.ReportUntil.Format("2 Jan 2006, 15:04 UTC") + `</time>. Nothing is blocked on this site until then.</p>`
	case p.Mode == render.PolicyReport:
		state = `<p class="csp-note">Report only ended on ` + p.ReportUntil.Format("2 Jan 2006") + `; the allowances below are enforced.</p>`
	}
	b.WriteString(string(ui.Section("Policy", "", ui.HTML(`<div class="csp-modes" role="radiogroup" aria-label="Policy">`+
		option(render.PolicyStrict, "Strict", "Only this site's own files load.")+
		option(render.PolicyCustom, "Allow what I choose", "The services and sources below load too.")+
		option(render.PolicyReport, "Report only, for 7 days", "Nothing is blocked; everything blocked is listed below. Then it enforces again on its own.")+
		`</div>`+state+`<p class="muted csp-note">Sign-in, member, checkout and mail pages, and this console, always keep the strict policy.`+
		string(ui.Tip("A service you allow runs on your public pages, never where someone's session could be read."))+`</p>`))))

	on := map[string]bool{}
	for _, id := range p.Services {
		on[id] = true
	}
	var rows []ui.Row
	for _, s := range render.CSPServices() {
		var dirs []string
		for d := range s.Sources {
			dirs = append(dirs, strings.ToLower(cspDirectiveWords[d]))
		}
		sort.Strings(dirs)
		hint := "Adds " + strings.Join(dirs, ", ")
		if s.RunsCode() {
			hint = "Runs code on your pages. " + hint
		}
		checked := ""
		if on[s.ID] {
			checked = " checked"
		}
		rows = append(rows, ui.Row{Label: s.Name, Hint: hint, ID: "csp-svc-" + s.ID,
			Control: ui.HTML(`<input type="checkbox" class="toggle" role="switch" id="csp-svc-` + s.ID + `" data-csp-service="` + s.ID + `"` + checked + `>`)})
	}
	b.WriteString(string(ui.Section("Services", "", ui.Rows(rows...))))

	var srcRows [][]ui.HTML
	for _, d := range render.CustomDirectives {
		for _, src := range p.Sources[d] {
			srcRows = append(srcRows, []ui.HTML{ui.Text(cspDirectiveWords[d]),
				ui.HTML(`<code>` + string(ui.Text(src)) + `</code>`),
				ui.HTML(`<button type="button" class="btn btn--ghost btn--sm" data-csp-source="` + d + `" data-origin="` + string(ui.Text(src)) + `" data-csp-remove>Remove</button>`)})
		}
	}
	opts := ""
	for _, d := range render.CustomDirectives {
		opts += `<option value="` + d + `">` + cspDirectiveWords[d] + `</option>`
	}
	inline := ""
	if p.InlineStyles {
		inline = " checked"
	}
	b.WriteString(string(ui.Section("Your own sources", "", ui.Join(
		ui.Table([]string{"For", "Address", ""}, srcRows, "None yet."),
		ui.HTML(`<div class="csp-add"><select class="input" data-csp-add-dir aria-label="For">`+opts+`</select>`+
			`<input type="text" class="input" data-csp-add-src placeholder="https://images.example.com" aria-label="Address" autocomplete="off" spellcheck="false">`+
			`<button type="button" class="btn" data-csp-add>Add</button></div>`),
		ui.Rows(ui.Row{Label: "Inline styles", Hint: "Many embedded widgets style themselves this way. Styles cannot run code.", ID: "csp-inline",
			Control: ui.HTML(`<input type="checkbox" class="toggle" role="switch" id="csp-inline" data-csp-inline` + inline + `>`)}),
	))))

	var blockRows [][]ui.HTML
	for _, bl := range cspBlocksFor(hosts) {
		what := cspDirectiveWords[bl.Directive]
		if what == "" {
			what = bl.Directive
		}
		blockRows = append(blockRows, []ui.HTML{ui.Text(what), ui.HTML(`<code>` + string(ui.Text(bl.Origin)) + `</code>`),
			ui.Text(itoaSafe(bl.Count) + " time" + plural(bl.Count)),
			ui.HTML(`<time datetime="` + bl.Last.UTC().Format(time.RFC3339) + `">` + bl.Last.UTC().Format("2 Jan, 15:04") + `</time>`),
			cspBlockAction(bl)})
	}
	b.WriteString(string(ui.Section("Blocked on this site", "", ui.Join(
		ui.Table([]string{"What", "From", "Seen", "Last", ""}, blockRows, "Nothing blocked here has been reported by more than one visitor."),
		ui.HTML(`<p class="muted csp-note">Reported by visitors' browsers since the server started.`+
			string(ui.Tip("Anyone can send a report, so an address shows here only once more than one visitor has reported it. Allow only what you recognise."))+`</p>`),
	))))

	sample := strings.Replace(p.Apply(render.BuildCSP("NONCE", nil)), "'nonce-NONCE'", "'nonce-…'", 1)
	b.WriteString(string(ui.Disclosure(ui.Icon("shield"), "The policy your pages are sent", "As saved", "", false,
		ui.HTML(`<pre class="csp-sample">`+string(ui.Text(strings.ReplaceAll(sample, "; ", ";\n")))+`</pre>`))))

	b.WriteString(`</div>`)
	b.WriteString(`<script nonce="` + nonce + `">` + servicesScript + `</script>`)
	writeOSHTML(w, r, adminOSLayout(nonce, "Outside services", "website", cfg, htmpl.HTML(b.String())))
}

// servicesScript saves the page's policy on every change. Allow and Turn on
// edit the page's own controls first, so what is saved is always what the
// page shows; the reload then shows the policy as the server stored it.
const servicesScript = `(function(){
  var page=document.querySelector('[data-csp-save]'); if(!page)return;
  function policy(){
    var m=document.querySelector('input[name="csp-mode"]:checked');
    var p={mode:m?m.value:'strict',services:[],sources:{},inline_styles:!!(document.querySelector('[data-csp-inline]')||{}).checked};
    Array.prototype.forEach.call(document.querySelectorAll('[data-csp-service]'),function(c){if(c.checked)p.services.push(c.getAttribute('data-csp-service'));});
    Array.prototype.forEach.call(document.querySelectorAll('[data-csp-source]'),function(s){var d=s.getAttribute('data-csp-source');(p.sources[d]=p.sources[d]||[]).push(s.getAttribute('data-origin'));});
    return p;
  }
  function save(p,btn){
    window.vpPost(page.getAttribute('data-csp-save'),p,function(d){
      window.vpToast(d.detail||'Saved.','ok'); setTimeout(function(){location.reload();},400);
    },function(d,msg){window.vpToast(msg||'Not saved.','error');});
  }
  page.addEventListener('change',function(e){
    var t=e.target;
    if(t.name==='csp-mode'||t.hasAttribute('data-csp-service')||t.hasAttribute('data-csp-inline'))save(policy(),t);
  });
  var add=document.querySelector('[data-csp-add]');
  if(add)add.addEventListener('click',function(){
    var src=(document.querySelector('[data-csp-add-src]').value||'').trim(); if(!src)return;
    var p=policy(),d=document.querySelector('[data-csp-add-dir]').value;
    if(p.mode==='strict')p.mode='custom';
    (p.sources[d]=p.sources[d]||[]).push(src); save(p,add);
  });
  Array.prototype.forEach.call(document.querySelectorAll('[data-csp-remove]'),function(b){
    b.addEventListener('click',function(){b.removeAttribute('data-csp-source');var r=b.closest('tr');if(r)r.remove();save(policy(),b);});
  });
  Array.prototype.forEach.call(document.querySelectorAll('[data-csp-allow]'),function(b){
    b.addEventListener('click',function(){
      var d=b.getAttribute('data-csp-allow'),o=b.getAttribute('data-origin');
      window.vpConfirm({title:'Allow '+o+'?',message:'Pages on this site will load '+o+' as '+b.closest('tr').cells[0].textContent.toLowerCase()+'. Allow it only if you recognise it: anyone can send the reports this list is built from.',confirm:'Allow'},function(){
        var p=policy(); if(p.mode==='strict')p.mode='custom'; (p.sources[d]=p.sources[d]||[]).push(o); save(p,b);
      });
    });
  });
  Array.prototype.forEach.call(document.querySelectorAll('[data-csp-turn-on]'),function(b){
    b.addEventListener('click',function(){
      var c=document.querySelector('[data-csp-service="'+b.getAttribute('data-csp-turn-on')+'"]'); if(c)c.checked=true;
      var p=policy(); if(p.mode==='strict')p.mode='custom'; save(p,b);
    });
  });
})();`

// handleOSServicesSave stores a site's policy. The policy is validated here
// and again whenever it is read, so neither a stale page nor a hand-edited
// setting can widen anything the grammar refuses.
func (a *App) handleOSServicesSave(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "settings-error", "settings not initialised", "")
		return
	}
	var body render.SitePolicy
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	p, err := body.Normalize()
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid-policy", err.Error(), "")
		return
	}
	sc, _, _, name := a.servicesSite(r)
	detail := "Saved. " + name + " is strict again: only its own files load."
	switch p.Mode {
	case render.PolicyReport:
		// Set here, never by the client: how long a site blocks nothing is
		// not the page's to choose.
		p.ReportUntil = time.Now().UTC().Add(render.ReportOnlyFor).Truncate(time.Minute)
		detail = "Saved. Nothing is blocked on " + name + " until " + p.ReportUntil.Format("2 Jan, 15:04 UTC") + "."
	case render.PolicyCustom:
		detail = "Saved. " + name + "'s pages now load what you chose."
	}
	raw, err := json.Marshal(p)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-error", err.Error(), "")
		return
	}
	if err := a.siteSettings.SetMany(r.Context(), sc, map[string]string{settings.KeyCSPPolicy: string(raw)}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-error", err.Error(), "")
		return
	}
	dbpkg.AuditLog("csp.policy", dbpkg.AuditActor(r), name, string(raw))
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "detail": detail})
}

// cspReportOnlyNotices are the bell's reminders that a site blocks nothing:
// report-only is a trial, and a trial nobody remembers is an open door.
func (a *App) cspReportOnlyNotices(ctx context.Context) []osNotification {
	var out []osNotification
	now := time.Now().UTC()
	check := func(sc settings.Scope, host, href string) {
		p := a.sitePolicyIn(ctx, sc)
		if p.Enforced(now) == render.PolicyReport {
			out = append(out, osNotification{Title: "Outside services: report only", Href: href, Count: 1, Kind: "security", Severity: "warn",
				Detail: "Nothing is blocked on " + host + " until " + p.ReportUntil.Format("2 Jan, 15:04 UTC")})
		}
	}
	check(settings.ForPrimary(), strings.ToLower(config.Cfg.Domain), "/os/website/services")
	if a.domains != nil {
		if list, err := a.domains.List(ctx); err == nil {
			for _, d := range list {
				if !d.IsPrimary {
					check(settings.ForDomain(d.ID), d.Host, "/os/d/"+d.ID+"/services")
				}
			}
		}
	}
	return out
}
