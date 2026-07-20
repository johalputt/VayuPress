package main

// admin_os_ads.go — VayuOS Advertising console (/os/ads).
//
// Manage the activation-gated ad surface: AdSense publisher id, the affiliate
// disclosure text, and the ad-slot catalogue (create / enable / disable /
// delete). Slots only render on the public site when the Advertising module is
// switched on (feature.ads) and the individual slot is enabled — and AdSense
// units additionally require the Google Ads module + a publisher id.

import (
	"encoding/json"
	"html"
	htmpl "html/template"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/ads"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// handleOSAds renders the Advertising console.
func (a *App) handleOSAds(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	ctx := r.Context()

	adsOn := a.adsEnabled(ctx)
	googleOn := a.siteSettings != nil && a.siteSettings.FeatureEnabled(ctx, settings.KeyFeatureGoogleAds)
	adsenseClient := a.adsenseClient(ctx)
	disclosure := ""
	if a.siteSettings != nil {
		disclosure = a.siteSettings.Get(ctx, settings.KeyAffiliateDisclosure)
	}

	var slots []ads.Slot
	var pendingAds []ads.Slot
	if a.ads != nil {
		slots, _ = a.ads.List(ctx)
		pendingAds, _ = a.ads.ListByStatus(ctx, ads.StatusPendingReview)
	}
	adPrice := a.adSlotPriceCents(ctx)

	banner := ""
	if !adsOn {
		banner = `<div class="settings-callout"><strong>Advertising is off.</strong> <span class="text-sm muted">Your slots are saved but nothing renders on the public site until you enable the Advertising module.</span> <a class="btn btn--primary btn--sm mt-2" href="/os/tools">Enable in Tools &amp; Plugins →</a></div>`
	}

	body := `<div class="page-header">
  <h1>Advertising</h1>
  <div class="page-actions"><span id="ads-status" role="status" aria-live="polite" class="text-xs muted"></span></div>
</div>
` + banner + `
<div class="card">
  <div class="settings-block-title">Member ad submissions` + memberAdBadge(len(pendingAds)) + `</div>
  <p class="text-sm muted mb-4">Members buy an ad from their account; each paid submission waits here for your review. <strong>Approve</strong> publishes it in its placement; <strong>Reject</strong> declines it. Only image ads are accepted from members — never member HTML.</p>
  ` + memberAdReviewTable(pendingAds) + `
</div>

<div class="card">
  <div class="settings-block-title">Member ad price</div>
  <p class="text-sm muted mb-4">The flat fee a member pays to submit one ad for review. Charged once per submission via your connected gateway (Stripe one-time, or the sovereign direct method). Set in whole currency units.</p>
  <div class="field" style="max-width:16rem"><label class="field-label" for="ad-price">Price per ad</label>
    <input id="ad-price" class="input" type="number" min="0" step="1" data-ads-price value="` + strconv.Itoa(adPrice/100) + `"></div>
  <button type="button" class="btn btn--primary btn--sm" id="ad-price-save">Save price</button>
</div>

<div class="card">
  <div class="settings-block-title">Ad slots</div>
  <p class="text-sm muted mb-4">Each slot targets a placement and renders a same-origin image+link, a sanitised HTML creative, or a Google AdSense unit. New slots are enabled by default.</p>
  ` + adsSlotsTable(slots) + `
</div>

<div class="card">
  <div class="settings-block-title">Add an ad slot</div>
  <div class="ads-form" style="display:grid;gap:.75rem;max-width:42rem">
    <div class="field"><label class="field-label" for="ad-name">Name</label>
      <input id="ad-name" class="input" type="text" placeholder="e.g. Below-post banner"></div>
    <div class="field"><label class="field-label" for="ad-placement">Placement</label>
      <select id="ad-placement" class="select">` + adsPlacementOptions() + `</select></div>
    <div class="field"><label class="field-label" for="ad-kind">Creative kind</label>
      <select id="ad-kind" class="select">
        <option value="image">Image + link (house / direct-sold)</option>
        <option value="html">HTML creative (sanitised)</option>
        <option value="adsense">Google AdSense unit</option>
      </select></div>
    <div class="field"><label class="field-label" for="ad-image">Image URL <span class="muted text-xs">(image kind)</span></label>
      <input id="ad-image" class="input" type="text" placeholder="/media/banner.png or https://…"></div>
    <div class="field"><label class="field-label" for="ad-link">Destination URL <span class="muted text-xs">(image kind)</span></label>
      <input id="ad-link" class="input" type="text" placeholder="https://sponsor.example"></div>
    <div class="field"><label class="field-label" for="ad-alt">Alt / label text</label>
      <input id="ad-alt" class="input" type="text" placeholder="Sponsored by …"></div>
    <div class="field"><label class="field-label" for="ad-html">HTML creative / AdSense unit id</label>
      <textarea id="ad-html" class="textarea font-mono" rows="3" placeholder="HTML for an HTML creative, or the numeric ad-unit id for an AdSense slot"></textarea></div>
    <div><button type="button" class="btn btn--primary" id="ad-create-btn">Add slot</button></div>
  </div>
</div>

<div class="card">
  <div class="settings-block-title">Google AdSense</div>
  <p class="text-sm muted mb-4">Optional. Enable the <strong>Google AdSense</strong> module in Tools &amp; Plugins, set your publisher id here, then create slots of kind <em>AdSense</em>. Pages that show an AdSense unit automatically widen their Content-Security-Policy to admit Google's ad origins. ` + adsGoogleStatus(googleOn, adsenseClient) + `</p>
  <div class="field"><label class="field-label" for="ad-adsense-client">Publisher id</label>
    <input id="ad-adsense-client" class="input font-mono" type="text" data-ads-key="` + settings.KeyAdsenseClient + `" value="` + html.EscapeString(adsenseClient) + `" placeholder="ca-pub-0000000000000000"></div>
  <button type="button" class="btn btn--primary btn--sm" id="ad-adsense-save">Save publisher id</button>
</div>

<div class="card">
  <div class="settings-block-title">Affiliate disclosure</div>
  <p class="text-sm muted mb-4">When the Affiliate module is on, this disclosure shows above every post.</p>
  <div class="field"><label class="field-label" for="ad-disclosure">Disclosure text</label>
    <textarea id="ad-disclosure" class="textarea" rows="2" data-ads-key="` + settings.KeyAffiliateDisclosure + `">` + html.EscapeString(disclosure) + `</textarea></div>
  <button type="button" class="btn btn--primary btn--sm" id="ad-disclosure-save">Save disclosure</button>
</div>

<div id="action-msg" role="status" aria-live="polite" class="action-msg"></div>
<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?m[1]:'';}
var msg=document.getElementById('action-msg');
function show(t,e){if(!msg)return;msg.textContent=t;msg.classList.toggle('is-error',!!e);msg.classList.add('visible');}
function jfetch(method,url,payload){var o={method:method,headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()}};if(payload)o.body=JSON.stringify(payload);return fetch(url,o).then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});});}
function jsave(key,val,btn){btn.disabled=true;show('Saving…',false);return fetch('/os/api/settings',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({key:key,value:val})}).then(function(r){btn.disabled=false;show(r.ok?'Saved':'Error',!r.ok);}).catch(function(e){btn.disabled=false;show('Error: '+e,true);});}
var createBtn=document.getElementById('ad-create-btn');
if(createBtn)createBtn.addEventListener('click',function(){
  var payload={name:val('ad-name'),placement:val('ad-placement'),kind:val('ad-kind'),image_url:val('ad-image'),link_url:val('ad-link'),alt_text:val('ad-alt'),html:val('ad-html'),enabled:true};
  if(!payload.name.trim()){show('Give the slot a name',true);return;}
  createBtn.disabled=true;show('Creating…',false);
  jfetch('POST','/os/api/ads',payload).then(function(res){createBtn.disabled=false;if(res.ok){location.reload();}else{show(res.d.detail||res.d.title||'Error',true);}}).catch(function(e){createBtn.disabled=false;show('Error: '+e,true);});
});
function val(id){var el=document.getElementById(id);return el?el.value:'';}
document.querySelectorAll('[data-ad-action]').forEach(function(b){
  b.addEventListener('click',function(){
    var act=b.getAttribute('data-ad-action');var id=b.getAttribute('data-id');
    if(act==='delete'){if(!confirm('Delete this ad slot?'))return;b.disabled=true;jfetch('DELETE','/os/api/ads/'+encodeURIComponent(id)).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;show(res.d.detail||'Error',true);}});}
    else if(act==='toggle'){b.disabled=true;jfetch('POST','/os/api/ads/'+encodeURIComponent(id)+'/toggle',{enabled:b.getAttribute('data-to')==='1'}).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;show(res.d.detail||'Error',true);}});}
  });
});
document.querySelectorAll('[data-adreview-action]').forEach(function(b){
  b.addEventListener('click',function(){
    var act=b.getAttribute('data-adreview-action');var id=b.getAttribute('data-id');
    if(act==='reject'&&!confirm('Reject this member ad? It will not be published.'))return;
    b.disabled=true;
    jfetch('POST','/os/api/ads/'+encodeURIComponent(id)+'/'+act).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;show(res.d.detail||(res.d.error&&res.d.error.message)||'Error',true);}});
  });
});
var priceBtn=document.getElementById('ad-price-save');
if(priceBtn)priceBtn.addEventListener('click',function(){var u=parseInt(val('ad-price'),10);if(isNaN(u)||u<0){show('Enter a valid price',true);return;}jsave('` + settings.KeyAdSlotPriceCents + `',String(u*100),priceBtn);});
var asBtn=document.getElementById('ad-adsense-save');
if(asBtn)asBtn.addEventListener('click',function(){jsave('` + settings.KeyAdsenseClient + `',val('ad-adsense-client'),asBtn);});
var dBtn=document.getElementById('ad-disclosure-save');
if(dBtn)dBtn.addEventListener('click',function(){jsave('` + settings.KeyAffiliateDisclosure + `',val('ad-disclosure'),dBtn);});
})();
</script>`

	writeOSHTML(w, adminOSLayout(nonce, "Advertising", "ads", cfg, htmpl.HTML(body)))
}

// memberAdBadge shows the count of ads awaiting review as an inline pill.
func memberAdBadge(n int) string {
	if n == 0 {
		return ""
	}
	return ` <span class="status-pill status-pill--draft">` + strconv.Itoa(n) + ` awaiting review</span>`
}

// memberAdReviewTable renders the moderation queue with approve/reject actions.
func memberAdReviewTable(pending []ads.Slot) string {
	if len(pending) == 0 {
		return `<div class="table-empty">No submissions awaiting review. Paid member ads appear here for approval.</div>`
	}
	rows := ""
	for i := range pending {
		s := pending[i]
		preview := html.EscapeString(s.ImageURL)
		if s.LinkURL != "" {
			preview += `<div class="row-meta">→ ` + html.EscapeString(s.LinkURL) + `</div>`
		}
		rows += `<tr>` +
			`<td class="muted text-sm">` + html.EscapeString(s.OwnerEmail) + `</td>` +
			`<td>` + html.EscapeString(s.Placement) + `</td>` +
			`<td class="row-title"><code>` + preview + `</code></td>` +
			`<td class="row-actions">` +
			`<button type="button" class="btn btn--primary btn--sm" data-adreview-action="approve" data-id="` + html.EscapeString(s.ID) + `">Approve</button> ` +
			`<button type="button" class="btn btn--ghost btn--sm" data-adreview-action="reject" data-id="` + html.EscapeString(s.ID) + `">Reject</button>` +
			`</td></tr>`
	}
	return `<div class="table-wrap"><table class="table">` +
		`<thead><tr><th>Member</th><th>Placement</th><th>Creative</th><th></th></tr></thead>` +
		`<tbody>` + rows + `</tbody></table></div>`
}

// handleOSAdReviewApprove publishes a paid, reviewed member ad.
func (a *App) handleOSAdReviewApprove(w http.ResponseWriter, r *http.Request) {
	if a.ads == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "ads-disabled", "Advertising not initialised", "")
		return
	}
	if err := a.ads.ApproveMemberAd(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "approve-failed", err.Error(), "")
		return
	}
	a.purgeAdCaches()
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "approved"})
}

// handleOSAdReviewReject declines a member ad.
func (a *App) handleOSAdReviewReject(w http.ResponseWriter, r *http.Request) {
	if a.ads == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "ads-disabled", "Advertising not initialised", "")
		return
	}
	if err := a.ads.RejectMemberAd(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "reject-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "rejected"})
}

func adsSlotsTable(slots []ads.Slot) string {
	if len(slots) == 0 {
		return `<div class="table-empty">No ad slots yet. Add one below.</div>`
	}
	rows := ""
	for i := range slots {
		s := slots[i]
		statusPill := `<span class="status-pill status-pill--live">● enabled</span>`
		toggleTo, toggleLabel := "0", "Disable"
		if !s.Enabled {
			statusPill = `<span class="status-pill status-pill--draft">● disabled</span>`
			toggleTo, toggleLabel = "1", "Enable"
		}
		rows += `<tr>
  <td class="row-title">` + html.EscapeString(s.Name) + `</td>
  <td>` + html.EscapeString(s.Placement) + `</td>
  <td>` + html.EscapeString(s.Kind) + `</td>
  <td>` + statusPill + `</td>
  <td class="row-actions">
    <button type="button" class="btn btn--ghost btn--sm" data-ad-action="toggle" data-to="` + toggleTo + `" data-id="` + html.EscapeString(s.ID) + `">` + toggleLabel + `</button>
    <button type="button" class="btn btn--ghost btn--sm" data-ad-action="delete" data-id="` + html.EscapeString(s.ID) + `">Delete</button>
  </td>
</tr>`
	}
	return `<div class="table-wrap"><table class="table">
  <thead><tr><th>Name</th><th>Placement</th><th>Kind</th><th>Status</th><th></th></tr></thead>
  <tbody>` + rows + `</tbody>
</table></div>`
}

func adsPlacementOptions() string {
	out := ""
	for _, p := range ads.Placements() {
		out += `<option value="` + html.EscapeString(p.ID) + `">` + html.EscapeString(p.Label) + `</option>`
	}
	return out
}

func adsGoogleStatus(moduleOn bool, client string) string {
	switch {
	case moduleOn && client != "":
		return `<strong style="color:var(--color-success,#22c55e)">AdSense is active.</strong>`
	case moduleOn:
		return `<strong>Module on — set a publisher id to start serving units.</strong>`
	default:
		return `<strong>Module is off.</strong>`
	}
}

// ── JSON CRUD handlers (session + CSRF, mounted under /os) ─────────────────────

func (a *App) handleOSAdsList(w http.ResponseWriter, r *http.Request) {
	if a.ads == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "ads-error", "ads not initialised", "")
		return
	}
	list, err := a.ads.List(r.Context())
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"slots": list})
}

func (a *App) handleOSAdCreate(w http.ResponseWriter, r *http.Request) {
	if a.ads == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "ads-error", "ads not initialised", "")
		return
	}
	var body struct {
		Name      string `json:"name"`
		Placement string `json:"placement"`
		Kind      string `json:"kind"`
		ImageURL  string `json:"image_url"`
		LinkURL   string `json:"link_url"`
		AltText   string `json:"alt_text"`
		HTML      string `json:"html"`
		Sort      int    `json:"sort"`
		Enabled   bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	slot, err := a.ads.Create(r.Context(), ads.SlotInput{
		Name: body.Name, Placement: body.Placement, Kind: body.Kind,
		ImageURL: body.ImageURL, LinkURL: body.LinkURL, AltText: body.AltText,
		HTML: body.HTML, Sort: body.Sort, Enabled: body.Enabled,
	})
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "ads-error", err.Error(), "")
		return
	}
	a.purgeAdCaches()
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"id": slot.ID})
}

func (a *App) handleOSAdToggle(w http.ResponseWriter, r *http.Request) {
	if a.ads == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "ads-error", "ads not initialised", "")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := a.ads.SetEnabled(r.Context(), chi.URLParam(r, "id"), body.Enabled); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "ads-error", err.Error(), "")
		return
	}
	a.purgeAdCaches()
	writeJSON(w, r, http.StatusOK, map[string]bool{"enabled": body.Enabled})
}

func (a *App) handleOSAdDelete(w http.ResponseWriter, r *http.Request) {
	if a.ads == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "ads-error", "ads not initialised", "")
		return
	}
	if err := a.ads.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "ads-error", err.Error(), "")
		return
	}
	a.purgeAdCaches()
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// purgeAdCaches drops cached rendered pages so an ad change shows immediately.
func (a *App) purgeAdCaches() { render.CachePurgeAll() }
