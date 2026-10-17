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
	dbpkg "github.com/johalputt/vayupress/internal/db"
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

	newSlot := adSlotForm("ad", ads.Slot{Kind: ads.KindImage}) +
		`<div class="mt-3"><button type="button" class="btn btn--primary btn--sm" id="ad-create-btn">Add the slot</button></div>`
	// One sheet per slot, filled with what it holds now, as API keys' Manage
	// sheets are: an ad once placed could only be turned off or deleted, so a
	// changed link or a new image meant deleting the slot and making it again.
	editSheets := ui.HTML("")
	for _, sl := range slots {
		pre := "ad-e-" + sl.ID
		editSheets += ui.Sheet(pre, "Edit "+sl.Name, ui.HTML(adSlotForm(pre, sl)+
			`<div class="mt-3"><button type="button" class="btn btn--primary btn--sm" data-ad-save="`+html.EscapeString(sl.ID)+
			`" data-prefix="`+html.EscapeString(pre)+`" data-enabled="`+strconv.FormatBool(sl.Enabled)+`" data-sort="`+strconv.Itoa(sl.Sort)+`">Save the slot</button></div>`))
	}

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
		editSheets,
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
document.querySelectorAll('[data-ad-save]').forEach(function(b){
  b.addEventListener('click',function(){
    var p=b.getAttribute('data-prefix');
    var payload={name:val(p+'-name'),placement:val(p+'-placement'),kind:val(p+'-kind'),image_url:val(p+'-image'),link_url:val(p+'-link'),alt_text:val(p+'-alt'),html:val(p+'-html'),enabled:b.getAttribute('data-enabled')==='true',sort:parseInt(b.getAttribute('data-sort'),10)||0};
    if(!payload.name.trim()){show('Give the slot a name',true);return;}
    b.disabled=true;show('Saving…',false);
    jfetch('PUT','/os/api/ads/'+encodeURIComponent(b.getAttribute('data-ad-save')),payload).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;show(res.d.detail||(res.d.error&&res.d.error.message)||'Error',true);}}).catch(function(e){b.disabled=false;show('Error: '+e,true);});
  });
});
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
    <button type="button" class="btn btn--ghost btn--sm" data-sheet="ad-e-` + html.EscapeString(s.ID) + `">Edit</button>
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

func adsPlacementOptions(selected string) string {
	out := ""
	for _, p := range ads.Placements() {
		sel := ""
		if p.ID == selected {
			sel = " selected"
		}
		out += `<option value="` + html.EscapeString(p.ID) + `"` + sel + `>` + html.EscapeString(p.Label) + `</option>`
	}
	return out
}

// adSlotForm is the slot's fields, ids prefixed with prefix, filled from sl:
// the same form adds a slot (prefix "ad", an empty slot) and edits one.
func adSlotForm(prefix string, sl ads.Slot) string {
	id := func(f string) string { return html.EscapeString(prefix + "-" + f) }
	kinds := ""
	for _, k := range []struct{ id, label string }{{ads.KindImage, "An image and a link"}, {ads.KindHTML, "An HTML creative, sanitised"}, {ads.KindAdSense, "A Google AdSense unit"}} {
		sel := ""
		if k.id == sl.Kind {
			sel = " selected"
		}
		kinds += `<option value="` + k.id + `"` + sel + `>` + k.label + `</option>`
	}
	return `<div class="field"><label class="field-label" for="` + id("name") + `">Name</label><input id="` + id("name") + `" class="input" type="text" placeholder="Below-post banner" value="` + html.EscapeString(sl.Name) + `"></div>
<div class="field"><label class="field-label" for="` + id("placement") + `">Placement</label><select id="` + id("placement") + `" class="select">` + adsPlacementOptions(sl.Placement) + `</select></div>
<div class="field"><label class="field-label" for="` + id("kind") + `">What fills it</label><select id="` + id("kind") + `" class="select">` + kinds + `</select></div>
<div class="field"><label class="field-label" for="` + id("image") + `">Image URL</label><input id="` + id("image") + `" class="input" type="text" placeholder="/media/banner.png or https://…" value="` + html.EscapeString(sl.ImageURL) + `"><span class="field-hint">For an image and a link.</span></div>
<div class="field"><label class="field-label" for="` + id("link") + `">Where it links</label><input id="` + id("link") + `" class="input" type="text" placeholder="https://sponsor.example" value="` + html.EscapeString(sl.LinkURL) + `"><span class="field-hint">For an image and a link.</span></div>
<div class="field"><label class="field-label" for="` + id("alt") + `">Label</label><input id="` + id("alt") + `" class="input" type="text" placeholder="Sponsored by …" value="` + html.EscapeString(sl.AltText) + `"></div>
<div class="field"><label class="field-label" for="` + id("html") + `">HTML, or the AdSense unit ID</label><textarea id="` + id("html") + `" class="textarea font-mono" rows="3">` + html.EscapeString(sl.HTML) + `</textarea></div>`
}

// handleOSAdUpdate saves an edited slot. A member's paid ad keeps its review
// status: Update writes the creative, never the status.
func (a *App) handleOSAdUpdate(w http.ResponseWriter, r *http.Request) {
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
	id := chi.URLParam(r, "id")
	if err := a.ads.Update(r.Context(), id, ads.SlotInput{
		Name: body.Name, Placement: body.Placement, Kind: body.Kind,
		ImageURL: body.ImageURL, LinkURL: body.LinkURL, AltText: body.AltText,
		HTML: body.HTML, Sort: body.Sort, Enabled: body.Enabled,
	}); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "ads-error", err.Error(), "")
		return
	}
	dbpkg.AuditLog("ads.update", dbpkg.AuditActor(r), id, body.Placement)
	a.purgeAdCaches()
	writeJSON(w, r, http.StatusOK, map[string]string{"id": id})
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
