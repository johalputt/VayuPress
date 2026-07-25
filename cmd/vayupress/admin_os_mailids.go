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
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/render"
)

// handleOSMailIDs renders the Premium Mail-ID management console.
func (a *App) handleOSMailIDs(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	ctx := r.Context()

	var grants []members.PremiumGrant
	var names []string
	pending, paid, claimed := 0, 0, 0
	if a.members != nil {
		grants, _ = a.members.AllPremiumGrants(ctx, 200)
		names, _ = a.members.ListPremiumLocalparts(ctx)
		pending, paid, claimed = a.members.PremiumGrantCounts(ctx)
	}
	price := priceLabel(a.payCurrency(ctx), a.premiumMailIDPriceCents(ctx))

	body := `<div class="page-header">
  <h1>Premium Mail IDs</h1>
  <div class="page-actions"><a class="btn btn--ghost btn--sm" href="/os/growth">← Growth</a></div>
</div>
<p class="text-sm muted mb-4">See every premium (vanity) address sale, approve an offline order or disapprove any grant, and choose which names are sold as premium. Set the price &amp; terms on the <a href="/os/monetization">Monetization</a> page.</p>

<div class="stat-grid">
  <div class="stat-card"><div class="stat-card__label">Active addresses</div><div class="stat-card__value">` + strconv.Itoa(claimed) + `</div></div>
  <div class="stat-card"><div class="stat-card__label">Awaiting activation</div><div class="stat-card__value">` + strconv.Itoa(paid) + `</div></div>
  <div class="stat-card"><div class="stat-card__label">Awaiting payment</div><div class="stat-card__value">` + strconv.Itoa(pending) + `</div></div>
  <div class="stat-card"><div class="stat-card__label">Price per address</div><div class="stat-card__value">` + html.EscapeString(price) + `</div></div>
</div>

<div class="card">
  <div class="settings-block-title">Sales &amp; grants</div>
  <p class="text-sm muted mb-4">Every premium address a member has bought. <strong>Approve</strong> confirms an offline/pending order (the buyer can then activate it); <strong>Disapprove</strong> cancels a grant.</p>
  ` + mailIDGrantsAdminTable(grants) + `
</div>

<div class="card">
  <div class="settings-block-title">Premium names</div>
  <p class="text-sm muted mb-4">Names listed here (plus ultra-short and well-known handles) are held back from the free member claim and sold at the premium price. Add any localpart you want to reserve as premium.</p>
  <div class="field" style="display:flex;gap:.5rem;align-items:flex-end;flex-wrap:wrap">
    <div style="flex:1;min-width:12rem"><label class="field-label" for="mid-add-name">Add a premium name</label>
    <input id="mid-add-name" class="input" type="text" placeholder="founder" autocomplete="off" spellcheck="false" style="text-transform:lowercase"></div>
    <button type="button" class="btn btn--primary btn--sm" id="mid-add-btn">Add</button>
  </div>
  ` + premiumNamesList(names) + `
</div>

<div id="mid-msg" role="status" aria-live="polite" class="action-msg"></div>
<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?m[1]:'';}
var msg=document.getElementById('mid-msg');
function show(t,e){if(!msg)return;msg.textContent=t;msg.classList.toggle('is-error',!!e);msg.classList.add('visible');}
function jpost(url,body){return fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:body?JSON.stringify(body):null}).then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});});}
document.querySelectorAll('[data-grant-action]').forEach(function(b){
  b.addEventListener('click',function(){
    var act=b.getAttribute('data-grant-action'),id=b.getAttribute('data-id');
    if(act==='revoke'&&!confirm('Disapprove this premium address? The grant will be cancelled.'))return;
    b.disabled=true;
    jpost('/os/api/mailids/'+encodeURIComponent(id)+'/'+act).then(function(res){
      if(res.ok){location.reload();}else{b.disabled=false;show((res.d.error&&res.d.error.message)||res.d.detail||'Error',true);}
    });
  });
});
var addBtn=document.getElementById('mid-add-btn');
if(addBtn)addBtn.addEventListener('click',function(){
  var inp=document.getElementById('mid-add-name'),v=(inp.value||'').trim().toLowerCase();
  if(!v){show('Enter a name first',true);return;}
  addBtn.disabled=true;
  jpost('/os/api/mailids/premium-names/add',{localpart:v}).then(function(res){addBtn.disabled=false;if(res.ok){location.reload();}else{show((res.d.error&&res.d.error.message)||'Error',true);}});
});
document.querySelectorAll('[data-remove-name]').forEach(function(b){
  b.addEventListener('click',function(){
    b.disabled=true;
    jpost('/os/api/mailids/premium-names/remove',{localpart:b.getAttribute('data-remove-name')}).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;show('Error',true);}});
  });
});
})();
</script>`

	writeOSHTML(w, r, adminOSLayout(nonce, "Premium Mail IDs", "growth", cfg, htmpl.HTML(body)))
}

// mailIDGrantsAdminTable renders the sales table with per-row moderation actions.
func mailIDGrantsAdminTable(grants []members.PremiumGrant) string {
	if len(grants) == 0 {
		return `<div class="table-empty">No premium addresses sold yet. Sales appear here as members buy vanity IDs.</div>`
	}
	rows := ""
	for i := range grants {
		g := grants[i]
		actions := ""
		if g.Status == members.GrantPending {
			actions += `<button type="button" class="btn btn--primary btn--sm" data-grant-action="approve" data-id="` + html.EscapeString(g.ID) + `">Approve</button> `
		}
		if g.Status != members.GrantRevoked {
			actions += `<button type="button" class="btn btn--ghost btn--sm" data-grant-action="revoke" data-id="` + html.EscapeString(g.ID) + `">Disapprove</button>`
		}
		rows += `<tr>` +
			`<td class="row-title"><code>` + html.EscapeString(g.Address()) + `</code></td>` +
			`<td class="muted text-sm">` + html.EscapeString(g.Email) + `</td>` +
			`<td>` + premiumGrantPill(g.Status) + `</td>` +
			`<td class="muted text-sm">` + config.FormatSite(g.CreatedAt, "2 Jan 2006") + `</td>` +
			`<td class="row-actions">` + actions + `</td>` +
			`</tr>`
	}
	return `<div class="table-wrap"><table class="table">` +
		`<thead><tr><th>Address</th><th>Buyer</th><th>Status</th><th>Purchased</th><th></th></tr></thead>` +
		`<tbody>` + rows + `</tbody></table></div>`
}

// premiumNamesList renders the operator's premium localparts as removable chips.
func premiumNamesList(names []string) string {
	if len(names) == 0 {
		return `<p class="text-sm muted">No custom premium names yet. Ultra-short and well-known handles are premium automatically.</p>`
	}
	chips := ""
	for _, n := range names {
		chips += `<span class="status-pill" style="margin:.15rem .3rem .15rem 0"><code>` + html.EscapeString(n) + `</code> <button type="button" class="btn btn--ghost btn--sm" data-remove-name="` + html.EscapeString(n) + `" style="padding:0 .35rem;margin-left:.25rem" aria-label="Remove ` + html.EscapeString(n) + `">✕</button></span>`
	}
	return `<div style="display:flex;flex-wrap:wrap;align-items:center">` + chips + `</div>`
}

// ── Moderation + premium-name APIs ────────────────────────────────────────────

func (a *App) handleOSMailIDApprove(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
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
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
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
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	var in struct {
		Localpart string `json:"localpart"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
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
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	var in struct {
		Localpart string `json:"localpart"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	if err := a.members.RemovePremiumLocalpart(r.Context(), strings.ToLower(strings.TrimSpace(in.Localpart))); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "remove-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "removed"})
}
