// SPDX-License-Identifier: Apache-2.0

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
	"github.com/johalputt/vayupress/internal/ui"
)

// handleOSAds renders the Advertising console.
func (a *App) handleOSAds(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	ctx := r.Context()

	adsOn := a.adsEnabled(ctx)
	googleOn := a.siteSettings != nil && a.siteSettings.FeatureEnabled(ctx, settings.ForPrimary(), settings.KeyFeatureGoogleAds)
	adsenseClient := a.adsenseClient(ctx)
	disclosure := ""
	if a.siteSettings != nil {
		disclosure = a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyAffiliateDisclosure)
	}

	var slots []ads.Slot
	var pendingAds []ads.Slot
	if a.ads != nil {
		slots, _ = a.ads.List(ctx)
		pendingAds, _ = a.ads.ListByStatus(ctx, ads.StatusPendingReview)
	}
	// Off with nothing saved is a fresh install's Advertising: a setup page. Off
	// with slots or paid submissions waiting is a choice the operator made, and
	// the page keeps showing what is there.
	if !adsOn && a.ads != nil && len(slots) == 0 && len(pendingAds) == 0 {
		writeOSHTML(w, r, adminOSLayout(nonce, "Advertising", "ads", cfg, htmpl.HTML(adsSetup())))
		return
	}
	adPrice := a.adSlotPriceCents(ctx)

	state := ui.State("ok", "Showing ads")
	lead := `<button type="button" class="btn btn--primary btn--sm" data-sheet="ad-new">Add an ad slot</button>`
	if !adsOn {
		state = ui.State("neutral", "Advertising is off")
		lead = `<button type="button" class="btn btn--primary btn--sm" data-action="module-on" data-module="ads">Turn on Advertising</button> ` +
			`<button type="button" class="btn btn--sm" data-sheet="ad-new">Add an ad slot</button>`
	}
	if len(pendingAds) > 0 {
		state += ui.HTML(" ") + ui.State("warn", strconv.Itoa(len(pendingAds))+" member ad"+plural(len(pendingAds))+" to review")
	}
	field := func(id, key, kind, label, value string) ui.HTML {
		return settingControl(settingField{ID: id, Key: key, Kind: kind, Label: label}, value)
	}
	adsense := "Off: turn on Google AdSense in Tools and plugins first."
	switch {
	case googleOn && adsenseClient != "":
		adsense = "Serving AdSense units."
	case googleOn:
		adsense = "On, and waiting for a publisher ID."
	}

	newSlot := `<div class="field"><label class="field-label" for="ad-name">Name</label><input id="ad-name" class="input" type="text" placeholder="Below-post banner"></div>
<div class="field"><label class="field-label" for="ad-placement">Placement</label><select id="ad-placement" class="select">` + adsPlacementOptions() + `</select></div>
<div class="field"><label class="field-label" for="ad-kind">What fills it</label><select id="ad-kind" class="select"><option value="image">An image and a link</option><option value="html">An HTML creative, sanitised</option><option value="adsense">A Google AdSense unit</option></select></div>
<div class="field"><label class="field-label" for="ad-image">Image URL</label><input id="ad-image" class="input" type="text" placeholder="/media/banner.png or https://…"><span class="field-hint">For an image and a link.</span></div>
<div class="field"><label class="field-label" for="ad-link">Where it links</label><input id="ad-link" class="input" type="text" placeholder="https://sponsor.example"><span class="field-hint">For an image and a link.</span></div>
<div class="field"><label class="field-label" for="ad-alt">Label</label><input id="ad-alt" class="input" type="text" placeholder="Sponsored by …"></div>
<div class="field"><label class="field-label" for="ad-html">HTML, or the AdSense unit ID</label><textarea id="ad-html" class="textarea font-mono" rows="3"></textarea></div>
<div class="mt-3"><button type="button" class="btn btn--primary btn--sm" id="ad-create-btn">Add the slot</button></div>`

	page := ui.SettingsPage("Advertising", state, "Your own ad slots and your members' ads, served from this site with no ad network.",
		ui.HTML(`<div class="page-lead">`+lead+`</div>`),
		ui.Section("Member ads", "Paid submissions wait for your approval", ui.HTML(memberAdReviewTable(pendingAds))),
		ui.Section("Ad slots", "Each fills one placement", ui.HTML(adsSlotsTable(slots))),
		ui.Section("Pricing and networks", "", ui.Rows(
			ui.Row{Label: "Price of a member ad", Hint: "Charged once per submission, in minor units: 500 is " + priceLabel(a.payCurrency(ctx), 500) + ".", ID: "ad-price",
				Control: field("ad-price", settings.KeyAdSlotPriceCents, "text", "Price of a member ad", strconv.Itoa(adPrice))},
			ui.Row{Label: "AdSense publisher ID", Hint: adsense + " Pages that show a unit let Google's ad origins through their CSP.", ID: "ad-adsense-client",
				Control: field("ad-adsense-client", settings.KeyAdsenseClient, "text", "AdSense publisher ID", adsenseClient)},
			ui.Row{Label: "Affiliate disclosure", Hint: "Shown above every post while the Affiliate module is on.", ID: "ad-disclosure",
				Control: field("ad-disclosure", settings.KeyAffiliateDisclosure, "textarea", "Affiliate disclosure", disclosure)},
		)),
		ui.Sheet("ad-new", "Add an ad slot", ui.HTML(newSlot)),
		ui.SaveBar(),
	)
	body := string(page) + `
<div id="action-msg" role="status" aria-live="polite" class="action-msg"></div>
<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?m[1]:'';}
var msg=document.getElementById('action-msg');
function show(t,e){if(!msg)return;msg.textContent=t;msg.classList.toggle('is-error',!!e);msg.classList.add('visible');}
function jfetch(method,url,payload){var o={method:method,headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()}};if(payload)o.body=JSON.stringify(payload);return fetch(url,o).then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});});}
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
    if(act==='delete'){vpConfirm({title:'Delete this ad slot?',confirm:'Delete'},function(){b.disabled=true;jfetch('DELETE','/os/api/ads/'+encodeURIComponent(id)).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;show(res.d.detail||'Error',true);}});});}
    else if(act==='toggle'){b.disabled=true;jfetch('POST','/os/api/ads/'+encodeURIComponent(id)+'/toggle',{enabled:b.getAttribute('data-to')==='1'}).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;show(res.d.detail||'Error',true);}});}
  });
});
document.querySelectorAll('[data-adreview-action]').forEach(function(b){
  b.addEventListener('click',function(){
    var act=b.getAttribute('data-adreview-action');var id=b.getAttribute('data-id');
    var go=function(){
      b.disabled=true;
      jfetch('POST','/os/api/ads/'+encodeURIComponent(id)+'/'+act).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;show(res.d.detail||(res.d.error&&res.d.error.message)||'Error',true);}});
    };
    if(act==='reject'){vpConfirm({title:'Reject this member ad?',message:'It will not be published.',confirm:'Reject'},go);}else{go();}
  });
});
})();
</script>`

	writeOSHTML(w, r, settingsLayout(nonce, "Advertising", "ads", cfg, htmpl.HTML(body)))
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
		return `<div class="table-empty">No ad slots yet.</div>`
	}
	rows := ""
	for i := range slots {
		s := slots[i]
		statusPill := string(ui.State("ok", "On"))
		toggleTo, toggleLabel := "0", "Turn off"
		if !s.Enabled {
			statusPill = string(ui.State("neutral", "Off"))
			toggleTo, toggleLabel = "1", "Turn on"
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

// adsSetup is Advertising until it is on.
func adsSetup() string {
	return string(ui.Setup(ui.SetupPage{
		Icon:  "megaphone",
		Title: "Advertising is off",
		What:  "Your own ad slots, served from this site with no ad network, and ads your members can buy for you to approve.",
		Steps: []ui.SetupStep{
			{Title: "Advertising on", Detail: "Nothing shows on the site until a slot has something to fill it."},
			{Title: "An ad slot", Detail: "A placement (header, in-article, sidebar or footer) and what fills it: your own image and link, an HTML creative, or a Google AdSense unit."},
		},
		Action: `<button type="button" class="btn btn--primary" data-action="module-on" data-module="ads">Turn on Advertising</button>`,
	}))
}
