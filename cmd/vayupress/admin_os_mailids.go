// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_mailids.go — the Premium Mail-ID management console (under Growth →
// Monetization). The operator SEES every premium (vanity) address sale, APPROVES
// a pending/offline order or DISAPPROVES (revokes) any grant, and manages the
// list of localparts sold as premium (held back from the free member claim). It
// complements the price + terms editor on the Monetization page.

import (
	"encoding/json"
	"html"
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/render"
)

// handleOSMailIDs renders the Premium Mail-ID management console.
func (a *App) handleOSMailIDs(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	ctx := r.Context()
	lang := i18n.Lang(ctx)

	var grants []members.PremiumGrant
	var names []string
	pending, paid, claimed := 0, 0, 0
	if a.members != nil {
		grants, _ = a.members.AllPremiumGrants(ctx, 200)
		names, _ = a.members.ListPremiumLocalparts(ctx)
		pending, paid, claimed = a.members.PremiumGrantCounts(ctx)
	}
	price := priceLabel(a.payCurrency(ctx), a.premiumMailIDPriceCents(ctx))

	// The two management cards are also served on their own, so an approve /
	// disapprove / add / remove refreshes THEM instead of reloading the whole
	// console (M-17). The old script called location.reload() after every action,
	// which threw away the page position and re-fetched every stat.
	strong := `<strong>` + esc(i18n.T(lang, "Approve")) + `</strong>`
	panel := `<div class="card">
  <div class="settings-block-title">` + esc(i18n.T(lang, "Sales & grants")) + `</div>
  <p class="text-sm muted mb-4">` + escFill(i18n.T(lang, "Every premium address a member has bought. {approve} confirms an offline or pending order (the buyer can then activate it); {disapprove} cancels a grant.",
		"approve", htmlMark(0), "disapprove", htmlMark(1)), strong, `<strong>`+esc(i18n.T(lang, "Disapprove"))+`</strong>`) + `</p>
  ` + mailIDGrantsAdminTable(lang, grants) + `
</div>

<div class="card">
  <div class="settings-block-title">` + esc(i18n.T(lang, "Premium names")) + `</div>
  <p class="text-sm muted mb-4">` + esc(i18n.T(lang, "Names listed here (plus ultra-short and well-known handles) are held back from the free member claim and sold at the premium price. Add any localpart you want to reserve as premium.")) + `</p>
  <div class="vm-row vm-row--tight vm-row--end">
    <label class="field vm-grow"><span class="field-label">` + esc(i18n.T(lang, "Add a premium name")) + `</span>
      <input id="mid-add-name" class="input" type="text" placeholder="founder" autocomplete="off" spellcheck="false" aria-label="` + esc(i18n.T(lang, "Premium name to add")) + `"></label>
    <button type="button" class="btn btn--primary btn--sm" id="mid-add-btn">` + esc(i18n.T(lang, "Add")) + `</button>
  </div>
  ` + premiumNamesList(lang, names) + `
</div>`

	if r.URL.Query().Get("fragment") == "1" {
		writeOSFragment(w, panel)
		return
	}

	stat := func(label, value string) string {
		return `<div class="stat-card"><div class="stat-card__label">` + esc(label) + `</div><div class="stat-card__value">` + value + `</div></div>`
	}
	// The script's messages come from the page, in its language: each is an
	// attribute of the status line, with {name} where the name goes.
	msgs := map[string]string{
		"data-revoked":   i18n.T(lang, "Grant cancelled."),
		"data-approved":  i18n.T(lang, "Grant approved."),
		"data-failed":    i18n.T(lang, "That did not work — please try again."),
		"data-ask-title": i18n.T(lang, "Disapprove this premium address"),
		"data-ask-text":  i18n.T(lang, "The grant will be cancelled. The buyer stops being able to activate this address."),
		"data-ask-ok":    i18n.T(lang, "Disapprove"),
		"data-empty":     i18n.T(lang, "Enter a name first"),
		"data-added":     i18n.T(lang, "“{name}” is now a premium name.", "name", "{name}"),
		"data-removed":   i18n.T(lang, "“{name}” is available to members again.", "name", "{name}"),
	}
	var attrs strings.Builder
	for _, k := range []string{"data-revoked", "data-approved", "data-failed", "data-ask-title", "data-ask-text", "data-ask-ok", "data-empty", "data-added", "data-removed"} {
		attrs.WriteString(" " + k + `="` + esc(msgs[k]) + `"`)
	}
	body := `<div class="page-header">
  <h1>` + esc(i18n.T(lang, "Premium Mail IDs")) + `</h1>
  <div class="page-actions"><a class="btn btn--ghost btn--sm" href="/os/growth">` + esc(i18n.T(lang, "← Growth")) + `</a></div>
</div>
<p class="text-sm muted mb-4">` + escLinked(i18n.T(lang, "See every premium (vanity) address sale, approve an offline order or disapprove any grant, and choose which names are sold as premium. Set the price and terms on the {link} page.", "link", linkMark), "/os/monetization", i18n.T(lang, "Monetization")) + `</p>

<div class="stat-grid">
  ` + stat(i18n.T(lang, "Active addresses"), strconv.Itoa(claimed)) + `
  ` + stat(i18n.T(lang, "Awaiting activation"), strconv.Itoa(paid)) + `
  ` + stat(i18n.T(lang, "Awaiting payment"), strconv.Itoa(pending)) + `
  ` + stat(i18n.T(lang, "Price per address"), html.EscapeString(price)) + `
</div>

<div id="mid-panel" hx-get="/os/monetization/mailids?fragment=1" hx-trigger="mid-refresh from:body" hx-swap="innerHTML">
` + panel + `
</div>

<div id="mid-msg" role="status" aria-live="polite" class="action-msg"` + attrs.String() + `></div>
<script nonce="` + nonce + `">` + mailIDsScript + `</script>`

	writeOSHTML(w, r, adminOSLayout(nonce, i18n.T(lang, "Premium Mail IDs"), "growth", cfg, htmpl.HTML(body)))
}

// mailIDsScript runs the page's moderation and name controls. It is code;
// every word it shows comes from #mid-msg's attributes, in the page's
// language.
const mailIDsScript = `
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?m[1]:'';}
var msg=document.getElementById('mid-msg');
function say(k,name){var t=(msg&&msg.getAttribute('data-'+k))||'';return name==null?t:t.split('{name}').join(name);}
function show(t,e){if(!msg)return;msg.textContent=t;msg.classList.toggle('is-error',!!e);msg.classList.add('visible');}
function jpost(url,body){return fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:body?JSON.stringify(body):null}).then(function(r){return r.json().catch(function(){return{};}).then(function(d){return{ok:r.ok,d:d};});});}
function refresh(){if(window.htmx){htmx.trigger(document.body,'mid-refresh');}}
function reason(res){return (res.d.error&&res.d.error.message)||res.d.detail||say('failed');}
document.querySelectorAll('[data-grant-action]').forEach(function(b){
  b.addEventListener('click',function(){
    var act=b.getAttribute('data-grant-action'),id=b.getAttribute('data-id');
    function go(){
      b.disabled=true;
      jpost('/os/api/mailids/'+encodeURIComponent(id)+'/'+act).then(function(res){
        b.disabled=false;
        if(res.ok){show(act==='revoke'?say('revoked'):say('approved'));refresh();}
        else{show(reason(res),true);}
      });
    }
    if(act==='revoke'){
      // The house dialog, not the browser's: it can say what disapprove does.
      vpConfirm({title:say('ask-title'),message:say('ask-text'),confirm:say('ask-ok')},go);
      return;
    }
    go();
  });
});
var addBtn=document.getElementById('mid-add-btn');
if(addBtn)addBtn.addEventListener('click',function(){
  var inp=document.getElementById('mid-add-name'),v=(inp.value||'').trim().toLowerCase();
  if(!v){show(say('empty'),true);return;}
  addBtn.disabled=true;
  jpost('/os/api/mailids/premium-names/add',{localpart:v}).then(function(res){
    addBtn.disabled=false;
    if(res.ok){if(inp)inp.value='';show(say('added',v));refresh();}
    else{show(reason(res),true);}
  });
});
document.querySelectorAll('[data-remove-name]').forEach(function(b){
  b.addEventListener('click',function(){
    var name=b.getAttribute('data-remove-name');
    b.disabled=true;
    jpost('/os/api/mailids/premium-names/remove',{localpart:name}).then(function(res){
      b.disabled=false;
      if(res.ok){show(say('removed',name));refresh();}
      else{show(reason(res),true);}
    });
  });
});
})();
`

// mailIDGrantsAdminTable renders the sales table with per-row moderation actions.
func mailIDGrantsAdminTable(lang string, grants []members.PremiumGrant) string {
	if len(grants) == 0 {
		return `<div class="table-empty">` + esc(i18n.T(lang, "No premium addresses sold yet. Sales appear here as members buy vanity IDs.")) + `</div>`
	}
	rows := ""
	for i := range grants {
		g := grants[i]
		actions := ""
		if g.Status == members.GrantPending {
			actions += `<button type="button" class="btn btn--primary btn--sm" data-grant-action="approve" data-id="` + html.EscapeString(g.ID) + `">` + esc(i18n.T(lang, "Approve")) + `</button> `
		}
		if g.Status != members.GrantRevoked {
			actions += `<button type="button" class="btn btn--ghost btn--sm" data-grant-action="revoke" data-id="` + html.EscapeString(g.ID) + `">` + esc(i18n.T(lang, "Disapprove")) + `</button>`
		}
		rows += `<tr>` +
			`<td class="row-title"><code>` + html.EscapeString(g.Address()) + `</code></td>` +
			`<td class="muted text-sm">` + html.EscapeString(g.Email) + `</td>` +
			`<td>` + premiumGrantPill(lang, g.Status) + `</td>` +
			`<td class="muted text-sm">` + i18n.Date(lang, config.InSite(g.CreatedAt), i18n.DayMonthYearShort) + `</td>` +
			`<td class="row-actions">` + actions + `</td>` +
			`</tr>`
	}
	return `<div class="table-wrap"><table class="table">` +
		`<thead><tr><th>` + esc(i18n.T(lang, "Address")) + `</th><th>` + esc(i18n.T(lang, "Buyer")) + `</th><th>` + esc(i18n.T(lang, "Status")) + `</th><th>` + esc(i18n.T(lang, "Purchased")) + `</th><th></th></tr></thead>` +
		`<tbody>` + rows + `</tbody></table></div>`
}

// premiumNamesList renders the operator's premium localparts as removable chips.
func premiumNamesList(lang string, names []string) string {
	if len(names) == 0 {
		return `<p class="text-sm muted">` + esc(i18n.T(lang, "No custom premium names yet. Ultra-short and well-known handles are premium automatically.")) + `</p>`
	}
	chips := ""
	for _, n := range names {
		chips += `<span class="status-pill" style="margin:.15rem .3rem .15rem 0"><code>` + html.EscapeString(n) + `</code> <button type="button" class="btn btn--ghost btn--sm" data-remove-name="` + html.EscapeString(n) + `" style="padding:0 .35rem;margin-left:.25rem" aria-label="` + esc(i18n.T(lang, "Remove {name}", "name", n)) + `">✕</button></span>`
	}
	return `<div style="display:flex;flex-wrap:wrap;align-items:center">` + chips + `</div>`
}

// ── Moderation + premium-name APIs ────────────────────────────────────────────

func (a *App) handleOSMailIDApprove(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", i18n.T(i18n.Lang(r.Context()), "Memberships not initialised"), "")
		return
	}
	if err := a.members.ApprovePremiumGrant(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "approve-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "approved"})
}

func (a *App) handleOSMailIDRevoke(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", i18n.T(i18n.Lang(r.Context()), "Memberships not initialised"), "")
		return
	}
	if err := a.members.RevokePremiumGrant(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "revoke-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "revoked"})
}

func (a *App) handleOSMailIDNameAdd(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", i18n.T(i18n.Lang(r.Context()), "Memberships not initialised"), "")
		return
	}
	var in struct {
		Localpart string `json:"localpart"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", i18n.T(i18n.Lang(r.Context()), "Invalid request body"), "")
		return
	}
	if err := a.members.AddPremiumLocalpart(r.Context(), strings.ToLower(strings.TrimSpace(in.Localpart))); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "add-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "added"})
}

func (a *App) handleOSMailIDNameRemove(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", i18n.T(i18n.Lang(r.Context()), "Memberships not initialised"), "")
		return
	}
	var in struct {
		Localpart string `json:"localpart"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", i18n.T(i18n.Lang(r.Context()), "Invalid request body"), "")
		return
	}
	if err := a.members.RemovePremiumLocalpart(r.Context(), strings.ToLower(strings.TrimSpace(in.Localpart))); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "remove-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "removed"})
}
