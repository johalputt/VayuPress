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
	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/settings"
)

// siteAdSwitches are a site's three ads switches, each "on" or "off".
var siteAdSwitches = []string{settings.KeyFeatureAds, settings.KeyFeatureGoogleAds, settings.KeyFeatureAffiliate}

// siteAdSwitchWords are a switch's name and what it does, in lang.
func siteAdSwitchWords(lang, key string) (label, hint string) {
	switch key {
	case settings.KeyFeatureAds:
		return i18n.T(lang, "Advertising"), i18n.T(lang, "This site shows its ad slots.")
	case settings.KeyFeatureGoogleAds:
		return i18n.T(lang, "Google AdSense"), i18n.T(lang, "AdSense slots serve, with this site's publisher ID.")
	case settings.KeyFeatureAffiliate:
		return i18n.T(lang, "Affiliate disclosure"), i18n.T(lang, "The disclosure shows above this site's posts.")
	}
	return key, ""
}

// siteAdTexts are a site's ads texts.
var siteAdTexts = []string{settings.KeyAdsenseClient, settings.KeyAffiliateDisclosure, settings.KeyAdsNever, settings.KeyAdsTxtLines}

// siteAdSettingsForm is a hosted site's ads switches and texts, saved by
// handleOSSiteAdSettingsSave.
func siteAdSettingsForm(lang string, adsOn, googleOn, affOn bool, client, disclosure, never, adsTxt string) string {
	on := map[string]bool{settings.KeyFeatureAds: adsOn, settings.KeyFeatureGoogleAds: googleOn, settings.KeyFeatureAffiliate: affOn}
	var b strings.Builder
	b.WriteString(`<div class="form-grid">`)
	for _, key := range siteAdSwitches {
		checked := ""
		if on[key] {
			checked = " checked"
		}
		label, hint := siteAdSwitchWords(lang, key)
		b.WriteString(`<label class="field--check"><input type="checkbox" data-site-ad-key="` + key + `"` + checked + `><span class="field-label">` +
			html.EscapeString(label) + `</span><span class="field-hint">` + html.EscapeString(hint) + `</span></label>`)
	}
	field := func(key, label, hint, value string, rows int) {
		ctl := `<input type="text" class="input" data-site-ad-key="` + key + `" value="` + html.EscapeString(value) + `" autocomplete="off">`
		if rows > 0 {
			ctl = `<textarea class="input" rows="` + strconv.Itoa(rows) + `" data-site-ad-key="` + key + `">` + html.EscapeString(value) + `</textarea>`
		}
		b.WriteString(`<label class="field"><span class="field-label">` + html.EscapeString(label) + `</span>` + ctl +
			`<span class="field-hint">` + html.EscapeString(hint) + `</span></label>`)
	}
	field(settings.KeyAdsenseClient, i18n.T(lang, "AdSense publisher ID"), i18n.T(lang, "This site's own, like {example}; blank serves no AdSense unit.", "example", "ca-pub-1234567890123456"), client, 0)
	field(settings.KeyAffiliateDisclosure, i18n.T(lang, "Affiliate disclosure"), i18n.T(lang, "Shown above this site's posts while its disclosure is on."), disclosure, 3)
	field(settings.KeyAdsNever, i18n.T(lang, "Never show ads on"), i18n.T(lang, "This site's pages and posts that show no ad. One address a line, \"/\" for its home page."), never, 3)
	field(settings.KeyAdsTxtLines, i18n.T(lang, "More ads.txt lines"), i18n.T(lang, "Other sellers this site authorises, one line each, after the line its AdSense publisher ID gives."), adsTxt, 3)
	b.WriteString(`</div><div class="vm-row"><button type="button" class="btn btn--primary btn--sm" data-site-ads-save>` + esc(i18n.T(lang, "Save")) + `</button></div>`)
	return b.String()
}

// siteAdValue reports whether v is a value key takes on a site's ads page.
func siteAdValue(key, v string) bool {
	for _, sw := range siteAdSwitches {
		if sw == key {
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
		writeAPIError(w, r, http.StatusNotFound, "unknown-domain", i18n.T(i18n.Lang(r.Context()), "No such site."), "")
		return
	}
	var body struct {
		Settings map[string]string `json:"settings"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", i18n.T(i18n.Lang(r.Context()), "The request could not be read."), "")
		return
	}
	kv := map[string]string{}
	for k, v := range body.Settings {
		v = strings.TrimSpace(v)
		if !siteAdValue(k, v) {
			writeAPIError(w, r, http.StatusBadRequest, "bad-value", i18n.T(i18n.Lang(r.Context()), "That is not a value {setting} takes; nothing was saved.", "setting", k), "")
			return
		}
		kv[k] = v
	}
	if len(kv) > 0 {
		if err := a.siteSettings.SetMany(r.Context(), siteSettingsScope(site), kv); err != nil {
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
		writeAPIError(w, r, http.StatusNotFound, "unknown-domain", i18n.T(i18n.Lang(r.Context()), "No such site."), "")
		return
	}
	// Once: a site that already has slots of its own keeps them as they are,
	// rather than gaining a second set beside them.
	if have, err := a.ads.List(r.Context(), site); err == nil && len(have) > 0 {
		writeAPIError(w, r, http.StatusConflict, "has-own-ads", i18n.T(i18n.Lang(r.Context()), "This site already has ad slots of its own; nothing was copied."), "")
		return
	}
	n, err := a.ads.CopyOwnSlots(r.Context(), "", site)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "copy-failed", err.Error(), "")
		return
	}
	kv := map[string]string{}
	for _, sw := range siteAdSwitches {
		kv[sw] = map[bool]string{true: "on", false: "off"}[a.siteSettings.FeatureEnabled(r.Context(), settings.ForPrimary(), sw)]
	}
	for _, k := range siteAdTexts {
		kv[k] = a.siteSettings.Get(r.Context(), settings.ForPrimary(), k)
	}
	if err := a.siteSettings.SetMany(r.Context(), siteSettingsScope(site), kv); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "copy-failed", err.Error(), "")
		return
	}
	dbpkg.AuditLog("ads.copy_to_site", dbpkg.AuditActor(r), site, strconv.Itoa(n)+" slot"+plural(n))
	a.purgeAdCaches()
	writeJSON(w, r, http.StatusOK, map[string]int{"slots": n})
}
