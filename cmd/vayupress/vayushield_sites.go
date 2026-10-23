// SPDX-License-Identifier: Apache-2.0

package main

// vayushield_sites.go — a hosted site's own VayuShield settings: read from the
// site's scope, compiled into the shield, edited on the site's Security page.
//
// The rules of combination are the shield's (vayushield/site_policy.go):
// a threshold set here replaces the install's on this site; a list here is
// added to the install's, so the install's refusals hold on every site.

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
	"github.com/johalputt/vayupress/internal/vayushield"
	"github.com/johalputt/vayupress/internal/vayushield/oprules"
	"github.com/johalputt/vayupress/internal/vayushield/policy"
)

// shieldSiteField is one of a site's own shield settings.
type shieldSiteField struct {
	Key, Label, Hint string
	Threshold        bool           // a number in (0, 1]
	Rules            bool           // request rules (oprules); neither is a list
	Install          string         // the install's key it stands beside
	Choices          []headerChoice // a response header: one of these values, nothing else
}

// shieldSiteFields are the settings a site may set, in the order the page shows.
var shieldSiteFields = []shieldSiteField{
	{settings.KeyShieldSitePoW, "Silent proof-of-work at", "A score at or above this is asked for a proof of work.", true, false, settings.KeyShieldPoW, nil},
	{settings.KeyShieldSiteJS, "Visible check at", "A score at or above this is shown the check page.", true, false, settings.KeyShieldJS, nil},
	{settings.KeyShieldSiteBlock, "Refuse at", "A score at or above this is refused.", true, false, settings.KeyShieldBlock, nil},
	{settings.KeyShieldSiteDenyCIDRs, "Refuse these networks", "Addresses or CIDR ranges, one per line, refused on this site. Any entry in these lists can end: add until 2026-11-01, or a UTC time, until 2026-11-01T18:00.", false, false, settings.KeyShieldDenyCIDRs, nil},
	{settings.KeyShieldSiteAllowCIDRs, "Always serve these networks", "Never challenged on this site. The install's refusals still apply.", false, false, settings.KeyShieldAllowCIDRs, nil},
	{settings.KeyShieldSiteDenyCountries, "Refuse these countries", "Two-letter codes, one per line.", false, false, settings.KeyShieldDenyCountries, nil},
	{settings.KeyShieldSiteChallengeCountries, "Challenge these countries", "Served a proof of work rather than refused.", false, false, settings.KeyShieldChallengeCountries, nil},
	{settings.KeyShieldSiteAllowCountries, "Serve only these countries", "When set, every other country is refused on this site.", false, false, settings.KeyShieldAllowCountries, nil},
	{settings.KeyShieldSiteRules, "This site's request rules", "", false, true, settings.KeyShieldRules, nil},
	{Key: settings.KeySiteHSTS, Label: "HTTPS only (HSTS)", Hint: "How long browsers refuse plain http for this host. " + hstsConsequence, Choices: hstsChoices},
	{Key: settings.KeySiteReferrer, Label: "What other sites are told", Hint: "The Referer this site's pages send when a reader follows a link away.", Choices: referrerChoices},
	{Key: settings.KeySiteFrame, Label: "Framing", Hint: "Whether this site's pages may be shown inside a frame. Other sites may never frame them.", Choices: frameChoices},
	{Key: settings.KeySiteAllowCamera, Label: "Camera", Hint: "For a page of this site that asks for it.", Choices: allowChoices},
	{Key: settings.KeySiteAllowMic, Label: "Microphone", Hint: "For a page of this site that asks for it.", Choices: allowChoices},
	{Key: settings.KeySiteAllowGeo, Label: "Location", Hint: "For a page of this site that asks for it, such as a map.", Choices: allowChoices},
	{Key: settings.KeySiteAllowPayment, Label: "Payment requests", Hint: "For a page of this site that uses the browser's payment sheet.", Choices: allowChoices},
}

// siteShieldSettings is one site's own settings as the shield takes them, and
// whether it sets anything at all.
func (a *App) siteShieldSettings(ctx context.Context, id string) (vayushield.SiteSettings, bool) {
	sc := settings.ForDomain(id)
	g := func(k string) string { return strings.TrimSpace(a.siteSettings.Get(ctx, sc, k)) }
	num := func(k string) float64 {
		f, err := strconv.ParseFloat(g(k), 64)
		if err != nil {
			return 0
		}
		return f
	}
	s := vayushield.SiteSettings{
		PoWThreshold:   num(settings.KeyShieldSitePoW),
		JSThreshold:    num(settings.KeyShieldSiteJS),
		BlockThreshold: num(settings.KeyShieldSiteBlock),
		Rules:          g(settings.KeyShieldSiteRules),
		Policy: policy.Config{
			AllowCIDRs:         policyLines(g(settings.KeyShieldSiteAllowCIDRs)),
			DenyCIDRs:          policyLines(g(settings.KeyShieldSiteDenyCIDRs)),
			AllowCountries:     policyLines(g(settings.KeyShieldSiteAllowCountries)),
			DenyCountries:      policyLines(g(settings.KeyShieldSiteDenyCountries)),
			ChallengeCountries: policyLines(g(settings.KeyShieldSiteChallengeCountries)),
		},
	}
	set := s.PoWThreshold > 0 || s.JSThreshold > 0 || s.BlockThreshold > 0 || s.Rules != "" ||
		len(s.Policy.AllowCIDRs)+len(s.Policy.DenyCIDRs)+len(s.Policy.AllowCountries)+len(s.Policy.DenyCountries)+len(s.Policy.ChallengeCountries) > 0
	return s, set
}

// applyShieldSiteSettings gives the shield every hosted site's own settings,
// and returns the entries it could not parse, per site.
func (a *App) applyShieldSiteSettings(ctx context.Context) map[string][]string {
	if a.vayuShield == nil || a.domains == nil || a.siteSettings == nil {
		return nil
	}
	list, err := a.domains.List(ctx)
	if err != nil {
		return nil
	}
	sites := map[string]vayushield.SiteSettings{}
	for _, d := range list {
		if d.IsPrimary {
			continue // the primary's settings are the install's
		}
		if s, ok := a.siteShieldSettings(ctx, d.ID); ok {
			sites[d.ID] = s
		}
	}
	return a.vayuShield.SetSiteSettings(sites)
}

// handleOSScopedShieldSave stores the URL's site's own shield settings and
// applies them at once. The site is the path's; a body naming another is
// refused. Only the keys of shieldSiteFields are written.
func (a *App) handleOSScopedShieldSave(w http.ResponseWriter, r *http.Request) {
	d, posted, ok := a.scopedSiteBody(w, r)
	if !ok {
		return
	}
	kv := map[string]string{}
	var changed []string
	for _, f := range shieldSiteFields {
		v, ok := posted[f.Key]
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if f.Choices != nil && !isChoice(f.Choices, v) {
			writeAPIError(w, r, http.StatusBadRequest, "invalid-choice", f.Label+" must be one of the listed choices", "")
			return
		}
		if f.Threshold && v != "" {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil || n <= 0 || n > 1 {
				writeAPIError(w, r, http.StatusBadRequest, "invalid-threshold",
					f.Label+" must be a number above 0 and at most 1, or empty to use the install's", "")
				return
			}
			v = strconv.FormatFloat(n, 'f', -1, 64)
		}
		kv[f.Key] = v
		changed = append(changed, f.Key)
	}
	if len(kv) > 0 {
		if err := a.siteSettings.SetMany(r.Context(), settings.ForDomain(d.ID), kv); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "save-failed", err.Error(), "")
			return
		}
		// Keys, never values: an address list is the operator's own business.
		dbpkg.AuditLog("vayushield.site_settings", dbpkg.AuditActor(r), d.Host, strings.Join(changed, ", "))
	}
	issues := a.applyShieldSiteSettings(r.Context())
	writeJSON(w, r, http.StatusOK, map[string]any{"status": "ok", "issues": issues[d.ID]})
}

// scopedSiteBody reads a request that sets a hosted site's own settings: an
// administrator, the URL's hosted site, and a body naming that same site. It answers the refusal
// itself and reports false when there is one.
func (a *App) scopedSiteBody(w http.ResponseWriter, r *http.Request) (domain.Domain, map[string]string, bool) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return domain.Domain{}, nil, false
	}
	d, ok := osScopedDomain(r)
	if !ok || d.IsPrimary {
		writeAPIError(w, r, http.StatusNotFound, "unknown-domain", "no such hosted site", "")
		return domain.Domain{}, nil, false
	}
	var body struct {
		DomainID string            `json:"domain_id"`
		Settings map[string]string `json:"settings"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid JSON", "")
		return domain.Domain{}, nil, false
	}
	if !requireScopeMatchesPath(r, body.DomainID) {
		writeAPIError(w, r, http.StatusBadRequest, "scope-mismatch",
			"this request names a different site from the one in its address; nothing was saved", "")
		return domain.Domain{}, nil, false
	}
	return d, body.Settings, true
}

// scopedShieldForm is the site's own settings, each beside what the install
// uses, so it is never a guess whether a value is the site's or inherited.
func (a *App) scopedShieldForm(ctx context.Context, d domain.Domain) ui.HTML {
	if a.siteSettings == nil || d.IsPrimary {
		return ""
	}
	sc := settings.ForDomain(d.ID)
	var b strings.Builder
	b.WriteString(`<div id="scoped-shield-ctx" data-id="` + esc(d.ID) + `"></div>`)
	rows := make([]ui.Row, 0, len(shieldSiteFields))
	var rules string
	for _, f := range shieldSiteFields {
		own := a.siteSettings.Get(ctx, sc, f.Key)
		inst := strings.TrimSpace(a.siteSettings.Get(ctx, settings.ForPrimary(), f.Install))
		id := "ss-" + strings.ReplaceAll(f.Key, ".", "-")
		if f.Rules {
			rules = siteRulesField(a, d.ID, id, f, own)
			continue
		}
		var control string
		hint := f.Hint
		set := strings.TrimSpace(own) != ""
		if f.Choices != nil {
			var opts strings.Builder
			for _, c := range f.Choices {
				sel := ""
				if c.Value == strings.TrimSpace(own) {
					sel = " selected"
				}
				opts.WriteString(`<option value="` + esc(c.Value) + `"` + sel + `>` + esc(c.Label) + `</option>`)
			}
			control = `<select id="` + id + `" class="select" data-shield-key="` + esc(f.Key) + `">` + opts.String() + `</select>`
			if set {
				hint = "Set for this site. " + hint
			} else {
				hint = "Inherited: the built-in header. " + hint
			}
		} else if f.Threshold {
			control = `<input id="` + id + `" type="text" inputmode="decimal" class="input" data-shield-key="` + esc(f.Key) +
				`" value="` + esc(own) + `" placeholder="` + esc(inst) + `">`
			if set {
				hint = "Set for this site, in place of the install's " + inst + ". " + hint
			} else {
				hint = "Inherited: the install's " + inst + ". " + hint
			}
		} else {
			control = `<textarea id="` + id + `" class="input" rows="3" data-shield-key="` + esc(f.Key) + `">` + esc(own) + `</textarea>`
			n := len(policyLines(inst))
			switch {
			case set && n > 0:
				hint = "Set for this site, added to the install's " + countOf(n, "entry") + ". " + hint
			case set:
				hint = "Set for this site. " + hint
			case n > 0:
				hint = "Inherited: the install's " + countOf(n, "entry") + " apply here. " + hint
			default:
				hint = "Nothing set here or for the install. " + hint
			}
		}
		rows = append(rows, ui.Row{Label: f.Label, Hint: hint, ID: id, Control: ui.HTML(control)})
	}
	b.WriteString(string(ui.Rows(rows...)))
	// The site's own entries that have ended, read from its own lists: the
	// shield holds them merged with the install's.
	own, _ := a.siteShieldSettings(ctx, d.ID)
	own.Policy.Now = time.Now()
	ended, _ := policy.Compile(own.Policy)
	b.WriteString(expiredEntries(ended.Expired()))
	b.WriteString(rules)
	b.WriteString(`<div class="sa-list__sheet-actions"><button type="button" class="btn btn--primary btn--sm" data-shield-save>Save</button>` +
		`<button type="button" class="btn btn--ghost btn--sm" data-shield-try>Try on the last 7 days</button>` +
		`<span class="text-xs muted" id="scoped-shield-status" role="status" aria-live="polite"></span></div>` +
		`<p class="muted text-xs">Trying replays this site's recorded reader views against the rules and country lists as typed, after the install's, and saves nothing.</p>` +
		`<div id="scoped-shield-sim"></div>`)
	return ui.HTML(b.String())
}

// siteRulesField is the site's request rules at full width (a rule is a line
// of text, too long for a row's control column), with what each has matched.
func siteRulesField(a *App, site, id string, f shieldSiteField, own string) string {
	note := ` This site's rules run after the install's, so a site can add a rule and never override one.`
	var hits string
	if a.vayuShield != nil {
		if inst, _ := a.vayuShield.OperatorRules(); len(inst) > 0 {
			note = ` This site's rules run after the install's ` + countOf(len(inst), "rule") + `, so a site can add a rule and never override one.`
		}
		// The issues are the stored text's, read again here: the shield keeps
		// only the rules it could compile.
		_, bad := oprules.Parse(own)
		hits = operatorRuleHits(a.vayuShield.SiteOperatorRules(site), bad, time.Now())
	}
	return `<div class="vs-field vs-field--area"><label for="` + id + `">` + esc(f.Label) + `</label>` +
		`<textarea id="` + id + `" class="input" rows="4" spellcheck="false" data-shield-key="` + esc(f.Key) +
		`" placeholder="block path=/private">` + esc(own) + `</textarea>` +
		`<p class="muted text-xs">` + operatorRulesHint + note + `</p>` + hits + `</div>`
}

// scopedShieldScript saves the form; the server answers with the entries it
// could not parse, which the status line names.
func scopedShieldScript(nonce string) string {
	return `<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
var node=document.getElementById('scoped-shield-ctx');
var ID=node?node.getAttribute('data-id'):'';
var btn=document.querySelector('[data-shield-save]');
var st=document.getElementById('scoped-shield-status');
if(!ID||!btn)return;
function draft(){var p={};document.querySelectorAll('[data-shield-key]').forEach(function(el){p[el.getAttribute('data-shield-key')]=el.value;});return p;}
var tryBtn=document.querySelector('[data-shield-try]');
var sim=document.getElementById('scoped-shield-sim');
if(tryBtn&&sim)tryBtn.addEventListener('click',function(){
  tryBtn.disabled=true; sim.textContent='Trying…';
  fetch('/os/d/'+encodeURIComponent(ID)+'/api/shield/simulate',{method:'POST',
    headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},
    body:JSON.stringify({domain_id:ID,settings:draft()})})
    .then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};});})
    .then(function(res){tryBtn.disabled=false;
      // The report is the server's own markup, every value in it escaped there.
      if(res.ok&&res.j&&res.j.report){sim.innerHTML=res.j.report;}else{sim.textContent=(res.j&&res.j.message)||'Could not try it';}})
    .catch(function(e){tryBtn.disabled=false; sim.textContent='Error: '+e;});
});
btn.addEventListener('click',function(){
  var payload=draft();
  btn.disabled=true; if(st)st.textContent='Saving…';
  fetch('/os/d/'+encodeURIComponent(ID)+'/api/shield',{method:'POST',
    headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},
    body:JSON.stringify({domain_id:ID,settings:payload})})
    .then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};});})
    .then(function(res){btn.disabled=false; if(!st)return;
      if(!res.ok){st.textContent=(res.j&&res.j.message)||'Could not save';return;}
      var bad=(res.j&&res.j.issues)||[];
      st.textContent=bad.length?('Saved. Not understood, so not applied: '+bad.join(', ')):'Saved and applied';})
    .catch(function(e){btn.disabled=false; if(st)st.textContent='Error: '+e;});
});
})();
</script>`
}
