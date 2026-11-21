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
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/ads"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
)

// adConsoleSite is the site a console Ads request acts on: the hosted site in
// its address (/os/d/{id}/…), or "" for the install's own. Taken from the
// path, never the body, as every per-site page does.
func adConsoleSite(r *http.Request) string {
	if d, ok := osScopedDomain(r); ok {
		return d.ID
	}
	return ""
}

// handleOSAds renders the Advertising console for one site.
func (a *App) handleOSAds(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	ctx := r.Context()
	lang := i18n.Lang(ctx)
	title := i18n.T(lang, "Advertising")
	site := adConsoleSite(r)
	sc := siteSettingsScope(site)

	adsOn := a.adsEnabled(ctx, sc)
	googleOn := a.siteSettings != nil && a.siteSettings.FeatureEnabled(ctx, sc, settings.KeyFeatureGoogleAds)
	adsenseClient := a.adsenseClient(ctx, sc)
	disclosure, never, adsTxt := "", "", ""
	if a.siteSettings != nil {
		disclosure = a.siteSettings.Get(ctx, sc, settings.KeyAffiliateDisclosure)
		never = a.siteSettings.Get(ctx, sc, settings.KeyAdsNever)
		adsTxt = a.siteSettings.Get(ctx, sc, settings.KeyAdsTxtLines)
	}

	var slots []ads.Slot
	var pendingAds []ads.Slot
	counts := map[string]ads.Counts{}
	if a.ads != nil {
		slots, _ = a.ads.List(ctx, site)
		pendingAds, _ = a.ads.ListByStatus(ctx, site, ads.StatusPendingReview)
		if c, err := a.ads.Totals(ctx, site, 30, time.Now()); err == nil {
			counts = c
		}
	}
	// Off with nothing saved is a fresh install's Advertising: a setup page. Off
	// with slots or paid submissions waiting is a choice the operator made, and
	// the page keeps showing what is there.
	if site == "" && !adsOn && a.ads != nil && len(slots) == 0 && len(pendingAds) == 0 {
		writeOSHTML(w, r, adminOSLayout(nonce, title, "ads", cfg, htmpl.HTML(adsSetup(lang))))
		return
	}
	adPrice := a.adSlotPriceCents(ctx)

	addSlot := esc(i18n.T(lang, "Add an ad slot"))
	state := ui.State("ok", i18n.T(lang, "Showing ads"))
	lead := `<button type="button" class="btn btn--primary btn--sm" data-sheet="ad-new">` + addSlot + `</button>`
	if site != "" {
		// A hosted site's own Advertising: its switch is in its settings below,
		// and the install's ads can be copied onto it once.
		if !adsOn {
			state = ui.State("neutral", i18n.T(lang, "Advertising is off on this site"))
		}
		lead += ` <button type="button" class="btn btn--sm" data-ads-copy>` + esc(i18n.T(lang, "Copy my ads to this site")) + `</button>`
	} else if !adsOn {
		state = ui.State("neutral", i18n.T(lang, "Advertising is off"))
		lead = `<button type="button" class="btn btn--primary btn--sm" data-action="module-on" data-module="ads">` + esc(i18n.T(lang, "Turn on Advertising")) + `</button> ` +
			`<button type="button" class="btn btn--sm" data-sheet="ad-new">` + addSlot + `</button>`
	}
	if len(pendingAds) > 0 {
		state += ui.HTML(" ") + ui.State("warn", i18n.T(lang, "{n, plural, one {# member ad to review} other {# member ads to review}}", "n", len(pendingAds)))
	}
	field := func(id, key, kind, label, value string) ui.HTML {
		return settingControlIn(lang, settingField{ID: id, Key: key, Kind: kind, Label: label}, value)
	}
	adsense := i18n.T(lang, "Off: turn on Google AdSense in Tools and plugins first.")
	switch {
	case googleOn && adsenseClient != "":
		adsense = i18n.T(lang, "Serving AdSense units.")
	case googleOn:
		adsense = i18n.T(lang, "On, and waiting for a publisher ID.")
	}

	newSlot := adSlotForm(lang, "ad", ads.Slot{Kind: ads.KindImage}) +
		`<div class="mt-3"><button type="button" class="btn btn--primary btn--sm" id="ad-create-btn">` + esc(i18n.T(lang, "Add the slot")) + `</button></div>`
	// One sheet per slot, filled with what it holds now, as API keys' Manage
	// sheets are: an ad once placed could only be turned off or deleted, so a
	// changed link or a new image meant deleting the slot and making it again.
	closeLabel := i18n.T(lang, "Close")
	editSheets := ui.HTML("")
	for _, sl := range slots {
		pre := "ad-e-" + sl.ID
		editSheets += ui.SheetClosing(pre, i18n.T(lang, "Edit {slot}", "slot", sl.Name), closeLabel, ui.HTML(adSlotForm(lang, pre, sl)+
			`<div class="mt-3"><button type="button" class="btn btn--primary btn--sm" data-ad-save="`+html.EscapeString(sl.ID)+
			`" data-prefix="`+html.EscapeString(pre)+`" data-enabled="`+strconv.FormatBool(sl.Enabled)+`" data-sort="`+strconv.Itoa(sl.Sort)+`">`+esc(i18n.T(lang, "Save the slot"))+`</button></div>`))
	}

	priceName, clientName, disclosureName, neverName, adsTxtName := i18n.T(lang, "Price of a member ad"), i18n.T(lang, "AdSense publisher ID"),
		i18n.T(lang, "Affiliate disclosure"), i18n.T(lang, "Never show ads on"), i18n.T(lang, "More ads.txt lines")
	settingsSection := ui.Section(i18n.T(lang, "Pricing, networks and pages"), "", ui.Rows(
		ui.Row{Label: priceName, Hint: i18n.T(lang, "Charged once per submission, in minor units: 500 is {price}.", "price", priceLabel(a.payCurrency(ctx), 500)), ID: "ad-price",
			Control: field("ad-price", settings.KeyAdSlotPriceCents, "text", priceName, strconv.Itoa(adPrice))},
		ui.Row{Label: clientName, Hint: adsense + " " + i18n.T(lang, "Pages that show a unit let Google's ad origins through their CSP."), ID: "ad-adsense-client",
			Control: field("ad-adsense-client", settings.KeyAdsenseClient, "text", clientName, adsenseClient)},
		ui.Row{Label: disclosureName, Hint: i18n.T(lang, "Shown above every post while the Affiliate module is on."), ID: "ad-disclosure",
			Control: field("ad-disclosure", settings.KeyAffiliateDisclosure, "textarea", disclosureName, disclosure)},
		ui.Row{Label: neverName, Hint: i18n.T(lang, "These pages and posts show no ad, whatever their slots say. One address a line, \"/\" for the home page."), ID: "ad-never",
			Control: field("ad-never", settings.KeyAdsNever, "textarea", neverName, never)},
		ui.Row{Label: adsTxtName, Hint: i18n.T(lang, "Other sellers this site authorises, one line each; /ads.txt starts with the line your AdSense publisher ID gives."), ID: "ad-ads-txt",
			Control: field("ad-ads-txt", settings.KeyAdsTxtLines, "textarea", adsTxtName, adsTxt)},
	))
	if site != "" {
		settingsSection = ui.Section(i18n.T(lang, "This site's ads"), i18n.T(lang, "Its switches, its AdSense publisher ID and its pages"),
			ui.HTML(siteAdSettingsForm(lang, adsOn, googleOn, a.affiliateEnabled(ctx, sc), adsenseClient, disclosure, never, adsTxt)))
	}
	page := ui.SettingsPage(title, state, i18n.T(lang, "Ad slots and members' ads for this site alone, served from it with no ad network."),
		ui.HTML(`<div class="page-lead">`+lead+`</div>`),
		ui.Section(i18n.T(lang, "Member ads"), i18n.T(lang, "Paid submissions wait for your approval"), ui.HTML(memberAdReviewTable(lang, pendingAds))),
		ui.Section(i18n.T(lang, "Ad slots"), i18n.T(lang, "Each fills one placement"), ui.HTML(adsSlotsTable(lang, slots, counts))),
		settingsSection,
		ui.SheetClosing("ad-new", i18n.T(lang, "Add an ad slot"), closeLabel, ui.HTML(newSlot)),
		editSheets,
		ui.SaveBarSaying(i18n.T(lang, "Discard"), i18n.T(lang, "Save changes")),
	)
	// The page's calls go to its own site's address: an absolute /os/api/ads
	// on a hosted site's page edited the install's slots.
	apiBase := "/os/api/ads"
	if site != "" {
		apiBase = "/os/d/" + site + "/api/ads"
	}
	body := string(page) + `
<div id="action-msg" role="status" aria-live="polite" class="action-msg"></div>
<div id="ads-ctx" data-base="` + html.EscapeString(apiBase) + `" hidden></div>
<script nonce="` + nonce + `" src="/os/static/js/admin-os-ads.js?v=` + assetVer("js/admin-os-ads.js") + `"></script>`

	writeOSHTML(w, r, settingsLayout(nonce, title, "ads", cfg, htmpl.HTML(body)))
}

// memberAdReviewTable renders the moderation queue with approve/reject actions.
func memberAdReviewTable(lang string, pending []ads.Slot) string {
	if len(pending) == 0 {
		return `<div class="table-empty">` + esc(i18n.T(lang, "No submissions awaiting review. Paid member ads appear here for approval.")) + `</div>`
	}
	approve, reject := esc(i18n.T(lang, "Approve")), esc(i18n.T(lang, "Reject"))
	rows := ""
	for i := range pending {
		s := pending[i]
		preview := html.EscapeString(s.ImageURL)
		if s.LinkURL != "" {
			preview += `<div class="row-meta">→ ` + html.EscapeString(s.LinkURL) + `</div>`
		}
		rows += `<tr>` +
			`<td class="muted text-sm" translate="no">` + html.EscapeString(s.OwnerEmail) + `</td>` +
			`<td>` + html.EscapeString(adPlacementLabel(lang, s.Placement)) + `</td>` +
			`<td class="row-title"><code>` + preview + `</code></td>` +
			`<td class="row-actions">` +
			`<button type="button" class="btn btn--primary btn--sm" data-adreview-action="approve" data-id="` + html.EscapeString(s.ID) + `">` + approve + `</button> ` +
			`<button type="button" class="btn btn--ghost btn--sm" data-adreview-action="reject" data-id="` + html.EscapeString(s.ID) + `">` + reject + `</button>` +
			`</td></tr>`
	}
	return `<div class="table-wrap"><table class="table">` +
		`<thead><tr><th>` + esc(i18n.T(lang, "Member")) + `</th><th>` + esc(i18n.T(lang, "Placement")) + `</th><th>` + esc(i18n.T(lang, "Creative")) + `</th><th></th></tr></thead>` +
		`<tbody>` + rows + `</tbody></table></div>`
}

// handleOSAdReviewApprove publishes a paid, reviewed member ad.
func (a *App) handleOSAdReviewApprove(w http.ResponseWriter, r *http.Request) {
	if a.ads == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "ads-disabled", i18n.T(i18n.Lang(r.Context()), "Advertising is off on this install."), "")
		return
	}
	if err := a.ads.ApproveMemberAd(r.Context(), adConsoleSite(r), chi.URLParam(r, "aid")); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "approve-failed", err.Error(), "")
		return
	}
	a.purgeAdCaches()
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "approved"})
}

// handleOSAdReviewReject declines a member ad.
func (a *App) handleOSAdReviewReject(w http.ResponseWriter, r *http.Request) {
	if a.ads == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "ads-disabled", i18n.T(i18n.Lang(r.Context()), "Advertising is off on this install."), "")
		return
	}
	if err := a.ads.RejectMemberAd(r.Context(), adConsoleSite(r), chi.URLParam(r, "aid")); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "reject-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "rejected"})
}

// adsSlotsTable lists slots with how often each was seen and clicked in
// the last 30 days (counts, by slot id).
func adsSlotsTable(lang string, slots []ads.Slot, counts map[string]ads.Counts) string {
	if len(slots) == 0 {
		return `<div class="table-empty">` + esc(i18n.T(lang, "No ad slots yet.")) + `</div>`
	}
	edit, del := esc(i18n.T(lang, "Edit")), esc(i18n.T(lang, "Delete"))
	rows := ""
	for i := range slots {
		s := slots[i]
		statusPill := string(ui.State("ok", i18n.T(lang, "On")))
		toggleTo, toggleLabel := "0", i18n.T(lang, "Turn off")
		if !s.Enabled {
			statusPill = string(ui.State("neutral", i18n.T(lang, "Off")))
			toggleTo, toggleLabel = "1", i18n.T(lang, "Turn on")
		}
		rows += `<tr>
  <td class="row-title" translate="no">` + html.EscapeString(s.Name) + `</td>
  <td>` + html.EscapeString(adPlacementLabel(lang, s.Placement)) + `</td>
  <td>` + html.EscapeString(adKindLabel(lang, s.Kind)) + `</td>
  <td>` + statusPill + `</td>
  <td class="mono">` + osGroupInt(int(counts[s.ID].Seen)) + `</td>
  <td class="mono">` + osGroupInt(int(counts[s.ID].Clicks)) + `</td>
  <td class="row-actions">
    <button type="button" class="btn btn--ghost btn--sm" data-sheet="ad-e-` + html.EscapeString(s.ID) + `">` + edit + `</button>
    <button type="button" class="btn btn--ghost btn--sm" data-ad-action="toggle" data-to="` + toggleTo + `" data-id="` + html.EscapeString(s.ID) + `">` + esc(toggleLabel) + `</button>
    <button type="button" class="btn btn--ghost btn--sm" data-ad-action="delete" data-id="` + html.EscapeString(s.ID) + `">` + del + `</button>
  </td>
</tr>`
	}
	return `<div class="table-wrap"><table class="table">
  <thead><tr><th>` + esc(i18n.T(lang, "Name")) + `</th><th>` + esc(i18n.T(lang, "Placement")) + `</th><th>` + esc(i18n.T(lang, "Kind")) + `</th><th>` + esc(i18n.T(lang, "Status")) +
		`</th><th>` + esc(i18n.T(lang, "Seen, 30 days")) + `</th><th>` + esc(i18n.T(lang, "Clicks")) + `</th><th></th></tr></thead>
  <tbody>` + rows + `</tbody>
</table></div>`
}

func adsPlacementOptions(lang, selected string) string {
	out := ""
	for _, p := range ads.Placements() {
		sel := ""
		if p.ID == selected {
			sel = " selected"
		}
		out += `<option value="` + html.EscapeString(p.ID) + `"` + sel + `>` + html.EscapeString(adPlacementLabel(lang, p.ID)) + `</option>`
	}
	return out
}

// adPlacementLabel is a placement's name in lang. The ads package keeps the
// English names for its own uses; a placement it adds later shows by its id
// here until it is named.
func adPlacementLabel(lang, id string) string {
	switch id {
	case ads.PlacementHeader:
		return i18n.T(lang, "Header (top of page)")
	case ads.PlacementAbovePost:
		return i18n.T(lang, "Above the post")
	case ads.PlacementBelowPost:
		return i18n.T(lang, "Below the post")
	case ads.PlacementSidebar:
		return i18n.T(lang, "Sidebar")
	case ads.PlacementFooter:
		return i18n.T(lang, "Footer")
	}
	return id
}

// adKindLabel is what fills a slot, said in lang.
func adKindLabel(lang, kind string) string {
	switch kind {
	case ads.KindImage:
		return i18n.T(lang, "An image and a link")
	case ads.KindHTML:
		return i18n.T(lang, "An HTML creative, sanitised")
	case ads.KindAdSense:
		return i18n.T(lang, "A Google AdSense unit")
	}
	return kind
}

// adShowLabel is where a slot shows, said in lang.
func adShowLabel(lang, id string) string {
	switch id {
	case ads.ShowEverywhere:
		return i18n.T(lang, "Every page with this placement")
	case ads.ShowPosts:
		return i18n.T(lang, "Posts only")
	case ads.ShowPages:
		return i18n.T(lang, "Pages only")
	case ads.ShowChosen:
		return i18n.T(lang, "Only the pages and posts listed")
	}
	return id
}

// adSlotForm is the slot's fields, ids prefixed with prefix, filled from sl:
// the same form adds a slot (prefix "ad", an empty slot) and edits one.
func adSlotForm(lang, prefix string, sl ads.Slot) string {
	id := func(f string) string { return html.EscapeString(prefix + "-" + f) }
	kinds := ""
	for _, k := range []string{ads.KindImage, ads.KindHTML, ads.KindAdSense} {
		sel := ""
		if k == sl.Kind {
			sel = " selected"
		}
		kinds += `<option value="` + k + `"` + sel + `>` + esc(adKindLabel(lang, k)) + `</option>`
	}
	label := func(f, text string) string {
		return `<label class="field-label" for="` + id(f) + `">` + esc(text) + `</label>`
	}
	forImage := `<span class="field-hint">` + esc(i18n.T(lang, "For an image and a link.")) + `</span>`
	return `<div class="field">` + label("name", i18n.T(lang, "Name")) + `<input id="` + id("name") + `" class="input" type="text" placeholder="` + esc(i18n.T(lang, "Below-post banner")) + `" value="` + html.EscapeString(sl.Name) + `"></div>
<div class="field">` + label("placement", i18n.T(lang, "Placement")) + `<select id="` + id("placement") + `" class="select">` + adsPlacementOptions(lang, sl.Placement) + `</select></div>
<div class="field">` + label("show-on", i18n.T(lang, "Shows on")) + `<select id="` + id("show-on") + `" class="select">` + adsShowOptions(lang, sl.ShowOn) + `</select></div>
<div class="field">` + label("show-paths", i18n.T(lang, "The pages and posts it shows on")) + `<textarea id="` + id("show-paths") + `" class="textarea font-mono" rows="3" placeholder="/about&#10;/a-post">` + html.EscapeString(strings.Join(sl.ShowPaths, "\n")) + `</textarea><span class="field-hint">` +
		esc(i18n.T(lang, "For \"{choice}\": one address a line, \"/\" for the home page.", "choice", adShowLabel(lang, ads.ShowChosen))) + `</span></div>
<div class="field">` + label("kind", i18n.T(lang, "What fills it")) + `<select id="` + id("kind") + `" class="select">` + kinds + `</select></div>
<div class="field">` + label("image", i18n.T(lang, "Image URL")) + `<input id="` + id("image") + `" class="input" type="text" placeholder="` + esc(i18n.T(lang, "{file} or {address}", "file", "/media/banner.png", "address", "https://…")) + `" value="` + html.EscapeString(sl.ImageURL) + `">` + forImage + `</div>
<div class="field">` + label("link", i18n.T(lang, "Where it links")) + `<input id="` + id("link") + `" class="input" type="text" placeholder="https://sponsor.example" value="` + html.EscapeString(sl.LinkURL) + `">` + forImage + `</div>
<div class="field">` + label("alt", i18n.T(lang, "Label")) + `<input id="` + id("alt") + `" class="input" type="text" placeholder="` + esc(i18n.T(lang, "Sponsored by …")) + `" value="` + html.EscapeString(sl.AltText) + `"></div>
<div class="field">` + label("html", i18n.T(lang, "HTML, or the AdSense unit ID")) + `<textarea id="` + id("html") + `" class="textarea font-mono" rows="3">` + html.EscapeString(sl.HTML) + `</textarea></div>`
}

// adsShowOptions is the slot form's choice of pages, with selected chosen.
func adsShowOptions(lang, selected string) string {
	out := ""
	for _, o := range ads.ShowOptions() {
		sel := ""
		if o.ID == selected {
			sel = " selected"
		}
		out += `<option value="` + html.EscapeString(o.ID) + `"` + sel + `>` + html.EscapeString(adShowLabel(lang, o.ID)) + `</option>`
	}
	return out
}

// handleOSAdUpdate saves an edited slot. A member's paid ad keeps its review
// status: Update writes the creative, never the status.
func (a *App) handleOSAdUpdate(w http.ResponseWriter, r *http.Request) {
	if a.ads == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "ads-error", i18n.T(i18n.Lang(r.Context()), "Advertising is off on this install."), "")
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
		ShowOn    string `json:"show_on"`
		ShowPaths string `json:"show_paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", i18n.T(i18n.Lang(r.Context()), "The request could not be read."), "")
		return
	}
	id := chi.URLParam(r, "aid")
	if err := a.ads.Update(r.Context(), adConsoleSite(r), id, ads.SlotInput{
		Name: body.Name, Placement: body.Placement, Kind: body.Kind,
		ImageURL: body.ImageURL, LinkURL: body.LinkURL, AltText: body.AltText,
		HTML: body.HTML, Sort: body.Sort, Enabled: body.Enabled,
		ShowOn: body.ShowOn, ShowPaths: body.ShowPaths,
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
		writeAPIError(w, r, http.StatusServiceUnavailable, "ads-error", i18n.T(i18n.Lang(r.Context()), "Advertising is off on this install."), "")
		return
	}
	list, err := a.ads.List(r.Context(), adConsoleSite(r))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"slots": list})
}

func (a *App) handleOSAdCreate(w http.ResponseWriter, r *http.Request) {
	if a.ads == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "ads-error", i18n.T(i18n.Lang(r.Context()), "Advertising is off on this install."), "")
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
		ShowOn    string `json:"show_on"`
		ShowPaths string `json:"show_paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", i18n.T(i18n.Lang(r.Context()), "The request could not be read."), "")
		return
	}
	slot, err := a.ads.Create(r.Context(), adConsoleSite(r), ads.SlotInput{
		Name: body.Name, Placement: body.Placement, Kind: body.Kind,
		ImageURL: body.ImageURL, LinkURL: body.LinkURL, AltText: body.AltText,
		HTML: body.HTML, Sort: body.Sort, Enabled: body.Enabled,
		ShowOn: body.ShowOn, ShowPaths: body.ShowPaths,
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
		writeAPIError(w, r, http.StatusServiceUnavailable, "ads-error", i18n.T(i18n.Lang(r.Context()), "Advertising is off on this install."), "")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := a.ads.SetEnabled(r.Context(), adConsoleSite(r), chi.URLParam(r, "aid"), body.Enabled); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "ads-error", err.Error(), "")
		return
	}
	a.purgeAdCaches()
	writeJSON(w, r, http.StatusOK, map[string]bool{"enabled": body.Enabled})
}

func (a *App) handleOSAdDelete(w http.ResponseWriter, r *http.Request) {
	if a.ads == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "ads-error", i18n.T(i18n.Lang(r.Context()), "Advertising is off on this install."), "")
		return
	}
	if err := a.ads.Delete(r.Context(), adConsoleSite(r), chi.URLParam(r, "aid")); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "ads-error", err.Error(), "")
		return
	}
	a.purgeAdCaches()
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// purgeAdCaches drops cached rendered pages so an ad change shows immediately.
func (a *App) purgeAdCaches() { render.CachePurgeAll() }

// adsSetup is Advertising until it is on.
func adsSetup(lang string) string {
	return string(ui.Setup(ui.SetupPage{
		Icon:  "megaphone",
		Title: i18n.T(lang, "Advertising is off"),
		What:  i18n.T(lang, "Your own ad slots, served from this site with no ad network, and ads your members can buy for you to approve."),
		Steps: []ui.SetupStep{
			{Title: i18n.T(lang, "Advertising on"), Detail: i18n.T(lang, "Nothing shows on the site until a slot has something to fill it.")},
			{Title: i18n.T(lang, "An ad slot"), Detail: i18n.T(lang, "A placement (header, in-article, sidebar or footer) and what fills it: your own image and link, an HTML creative, or a Google AdSense unit.")},
		},
		Action: ui.HTML(`<button type="button" class="btn btn--primary" data-action="module-on" data-module="ads">` + esc(i18n.T(lang, "Turn on Advertising")) + `</button>`),
	}))
}
