// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_site_ads.go — a hosted site's own Advertising settings, and the
// one-time copy of the install's ads onto it.
//
// The install's Ads page saves its settings through the Settings save bar,
// which writes the install's scope; a hosted site's page writes its own
// scope here, through an allowlist of the six ads keys, with the scope taken
// from the path (adConsoleSite), never the body.

import (
	"encoding/json"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/ads"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
)

// siteAdSwitches are a site's three ads switches, each "on" or "off".
var siteAdSwitches = []struct{ Key, Label, Hint string }{
	{settings.KeyFeatureAds, "Advertising", "This site shows its ad slots."},
	{settings.KeyFeatureGoogleAds, "Google AdSense", "AdSense slots serve, with this site's publisher ID."},
	{settings.KeyFeatureAffiliate, "Affiliate disclosure", "The disclosure shows above this site's posts."},
}

// siteAdTexts are a site's ads texts.
var siteAdTexts = []string{settings.KeyAdsenseClient, settings.KeyAffiliateDisclosure, settings.KeyAdsNever}

// siteAdSettingsForm is a hosted site's ads switches and texts, saved by
// handleOSSiteAdSettingsSave.
func siteAdSettingsForm(adsOn, googleOn, affOn bool, client, disclosure, never string) string {
	on := map[string]bool{settings.KeyFeatureAds: adsOn, settings.KeyFeatureGoogleAds: googleOn, settings.KeyFeatureAffiliate: affOn}
	var b strings.Builder
	b.WriteString(`<div class="form-grid">`)
	for _, sw := range siteAdSwitches {
		checked := ""
		if on[sw.Key] {
			checked = " checked"
		}
		b.WriteString(`<label class="field--check"><input type="checkbox" data-site-ad-key="` + sw.Key + `"` + checked + `><span class="field-label">` +
			html.EscapeString(sw.Label) + `</span><span class="field-hint">` + html.EscapeString(sw.Hint) + `</span></label>`)
	}
	field := func(key, label, hint, value string, rows int) {
		ctl := `<input type="text" class="input" data-site-ad-key="` + key + `" value="` + html.EscapeString(value) + `" autocomplete="off">`
		if rows > 0 {
			ctl = `<textarea class="input" rows="` + strconv.Itoa(rows) + `" data-site-ad-key="` + key + `">` + html.EscapeString(value) + `</textarea>`
		}
		b.WriteString(`<label class="field"><span class="field-label">` + html.EscapeString(label) + `</span>` + ctl +
			`<span class="field-hint">` + html.EscapeString(hint) + `</span></label>`)
	}
	field(settings.KeyAdsenseClient, "AdSense publisher ID", "This site's own, like ca-pub-1234567890123456; blank serves no AdSense unit.", client, 0)
	field(settings.KeyAffiliateDisclosure, "Affiliate disclosure", "Shown above this site's posts while its disclosure is on.", disclosure, 3)
	field(settings.KeyAdsNever, "Never show ads on", "This site's pages and posts that show no ad. One address a line, \"/\" for its home page.", never, 3)
	b.WriteString(`</div><div class="vm-row"><button type="button" class="btn btn--primary btn--sm" data-site-ads-save>Save</button></div>`)
	return b.String()
}

// siteAdValue reports whether v is a value key takes on a site's ads page.
func siteAdValue(key, v string) bool {
	for _, sw := range siteAdSwitches {
		if sw.Key == key {
			return v == "on" || v == "off"
		}
	}
	if key == settings.KeyAdsenseClient {
		return v == "" || ads.ValidPublisherID(v)
	}
	for _, k := range siteAdTexts {
		if k == key {
			return true
		}
	}
	return false
}

// handleOSSiteAdSettingsSave writes a hosted site's ads settings to its scope.
func (a *App) handleOSSiteAdSettingsSave(w http.ResponseWriter, r *http.Request) {
	site := adConsoleSite(r)
	if site == "" || a.siteSettings == nil {
		writeAPIError(w, r, http.StatusNotFound, "unknown-domain", "no such site", "")
		return
	}
	var body struct {
		Settings map[string]string `json:"settings"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid JSON", "")
		return
	}
	kv := map[string]string{}
	for k, v := range body.Settings {
		v = strings.TrimSpace(v)
		if !siteAdValue(k, v) {
			writeAPIError(w, r, http.StatusBadRequest, "bad-value", "That is not a value "+k+" takes; nothing was saved.", "")
			return
		}
		kv[k] = v
	}
	if len(kv) > 0 {
		if err := a.siteSettings.SetMany(r.Context(), adScope(site), kv); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "save-failed", err.Error(), "")
			return
		}
	}
	a.purgeAdCaches()
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handleOSSiteAdsCopy copies the install's own slots and ads settings onto a
// hosted site, once: a copy, not a link.
func (a *App) handleOSSiteAdsCopy(w http.ResponseWriter, r *http.Request) {
	site := adConsoleSite(r)
	if site == "" || a.ads == nil || a.siteSettings == nil {
		writeAPIError(w, r, http.StatusNotFound, "unknown-domain", "no such site", "")
		return
	}
	// Once: a site that already has slots of its own keeps them as they are,
	// rather than gaining a second set beside them.
	if have, err := a.ads.List(r.Context(), site); err == nil && len(have) > 0 {
		writeAPIError(w, r, http.StatusConflict, "has-own-ads", "This site already has ad slots of its own; nothing was copied.", "")
		return
	}
	n, err := a.ads.CopyOwnSlots(r.Context(), "", site)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "copy-failed", err.Error(), "")
		return
	}
	kv := map[string]string{}
	for _, sw := range siteAdSwitches {
		kv[sw.Key] = map[bool]string{true: "on", false: "off"}[a.siteSettings.FeatureEnabled(r.Context(), settings.ForPrimary(), sw.Key)]
	}
	for _, k := range siteAdTexts {
		kv[k] = a.siteSettings.Get(r.Context(), settings.ForPrimary(), k)
	}
	if err := a.siteSettings.SetMany(r.Context(), adScope(site), kv); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "copy-failed", err.Error(), "")
		return
	}
	dbpkg.AuditLog("ads.copy_to_site", dbpkg.AuditActor(r), site, strconv.Itoa(n)+" slot"+plural(n))
	a.purgeAdCaches()
	writeJSON(w, r, http.StatusOK, map[string]int{"slots": n})
}
