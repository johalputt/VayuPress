// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_vayuveil.go — /os/vayuveil, the endpoint observation-control console
// (ADR-0150).
//
// The hardest thing about this page is not the layout. It is refusing to let it
// read as a shield.
//
// ADR-0150 is at P0: the Observation Contract registry and the host inventory
// are real, tested and useful, and they enforce nothing whatsoever. An operator
// who lands here and comes away believing their screen is protected has been
// misled by this page even if every individual word on it is true — so the
// phase boundary is the first thing on the page, the switch is labelled as a
// switch on reporting, and no tile can go green while nothing is enforcing.

import (
	"html"
	"net/http"
	"strconv"
	"strings"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/vayuveil"
	"github.com/johalputt/vayupress/internal/veilaudit"
)

// handleOSVayuVeil renders the observation-control console.
func (a *App) handleOSVayuVeil(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	csrfTokenFor(w, r)

	enabled := a.veilEnabled(r)
	var obs map[vayuveil.ChannelID]vayuveil.Observation
	if enabled {
		obs = vayuveil.Inventory(vayuveil.LiveHost())
	}
	checks := veilaudit.Run(veilaudit.Inputs{
		Enabled:      enabled,
		Channels:     vayuveil.Channels(),
		Observations: obs,
		// Empty on purpose, and it is the honest value: no ADR-0150 phase is
		// shipped, so no phase is enforcing. When P1 lands it goes in here and the
		// report changes because the world changed, not because the page was
		// re-worded.
		EnforcingPhases: map[vayuveil.Phase]bool{},
	})

	body := vayuVeilPage(enabled, vayuveil.Channels(), obs, checks)
	full := adminOSShellHead(nonce, "VayuVeil", "vayuveil", cfg) + body +
		adminOSShellFoot(nonce, vayuVeilScript, pageUsesAlpine(body))
	writeOSHTML(w, r, full)
}

func (a *App) veilEnabled(r *http.Request) bool {
	if a.siteSettings == nil {
		return false
	}
	return a.siteSettings.Get(r.Context(), settings.ForPrimary(), settings.KeyVeilEnabled) == "on"
}

// handleOSVayuVeilToggle activates or deactivates observation reporting.
func (a *App) handleOSVayuVeilToggle(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusInternalServerError, "no-settings", "settings store unavailable", "")
		return
	}
	state, ok := veilToggleState(r.URL.Query().Get("on"))
	if !ok {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request",
			"expected on=1 or on=0; nothing was changed", "")
		return
	}
	if err := a.siteSettings.SetMany(r.Context(), settings.ForPrimary(),
		map[string]string{settings.KeyVeilEnabled: state}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-failed", err.Error(), "")
		return
	}
	dbpkg.AuditLog("vayuveil.toggle", dbpkg.AuditActor(r), "veil.enabled", state)
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok", "state": state})
}

// statusChip renders a report row's verdict.
func veilStatusChip(s veilaudit.Status) string {
	switch s {
	case veilaudit.Pass:
		return `<span class="mon-chip mon-chip--on">enforcing</span>`
	case veilaudit.Fail:
		return `<span class="mon-chip mon-chip--off">open</span>`
	case veilaudit.Warn:
		return `<span class="mon-chip mon-chip--off">exposed</span>`
	case veilaudit.Unverified:
		return `<span class="mon-chip mon-chip--off">unverified</span>`
	default:
		return `<span class="mon-chip mon-chip--off">context</span>`
	}
}

// vayuVeilPage builds the console body. Pure, so the page can be rendered and
// asserted on without a request, a settings store or a host to probe.
func vayuVeilPage(enabled bool, chans []vayuveil.Channel,
	obs map[vayuveil.ChannelID]vayuveil.Observation, checks []veilaudit.Check) string {
	esc := html.EscapeString
	var b strings.Builder

	b.WriteString(`<div class="page-header"><h1>VayuVeil</h1><div class="page-actions">` +
		`<a class="btn btn--ghost btn--sm" href="/os/adr">ADR-0150</a>` +
		`<span id="veil-status" class="text-sm muted" role="status" aria-live="polite"></span>` +
		`</div></div>`)
	b.WriteString(`<p class="page-sub">Which interfaces on this machine can observe a screen, a ` +
		`keyboard, a clipboard or the text inside a window — and what this install has decided about ` +
		`each of them. <b>Phase 0 registers those decisions and enforces none of them.</b></p>`)

	// ── Tiles ─────────────────────────────────────────────────────────────────
	pass, warn, fail, unver := veilaudit.Summary(checks)
	activeLabel, activeTone := "Inactive", "warn"
	if enabled {
		activeLabel, activeTone = "Reporting", ""
	}
	b.WriteString(`<div class="stat-grid">` +
		osStatTile("Observation control", activeLabel, activeTone) +
		osStatTile("Channels registered", strconv.Itoa(len(chans)), "") +
		osStatTile("Open on this host", strconv.Itoa(fail), tone(fail > 0)) +
		osStatTile("Enforcing", strconv.Itoa(pass), "warn") +
		`</div>`)

	// ── The switch, and the boundary it does NOT cross ────────────────────────
	b.WriteString(`<div class="section-head"><span class="section-head__title">Observation control</span>` +
		`<span class="section-head__hint">What this switch does, and what it cannot</span></div>`)
	b.WriteString(`<div class="mon-stack">`)

	btnLabel, btnAction, chip := "Activate", "1", `<span class="mon-chip mon-chip--off">inactive</span>`
	if enabled {
		btnLabel, btnAction, chip = "Deactivate", "0", `<span class="mon-chip mon-chip--on">reporting</span>`
	}
	b.WriteString(monAcc("🛡", "VayuVeil", "Inventory the observation channels on this host and report",
		chip, true,
		`<div class="card"><div class="settings-block-title">What this switch controls</div>`+
			`<p class="text-sm muted">Activating VayuVeil makes this install <b>look at itself</b>: it `+
			`probes which observation interfaces exist on this machine, which of them this process can `+
			`open, and reports both against the policy registered for each one.</p>`+
			`<p class="text-sm muted"><b>It does not protect anything, and turning it off does not `+
			`expose anything.</b> ADR-0150 Phase 0 registers every channel's policy, grant model, `+
			`indicator and audit level, and enforces none of it. Enforcement is Phase 1 and later, and `+
			`it lives in a compositor, a sandbox and a mandatory-access-control policy — not in this `+
			`binary. A switch here that read as a shield would be the exact claim this ADR was `+
			`written to prevent.</p>`+
			`<div class="vm-row"><button type="button" class="btn btn--primary btn--sm" `+
			`data-veil-toggle="`+btnAction+`">`+btnLabel+`</button></div></div>`))

	// ── The registry ──────────────────────────────────────────────────────────
	regChip := `<span class="mon-chip mon-chip--on">` + strconv.Itoa(len(chans)) + ` declared</span>`
	var reg strings.Builder
	reg.WriteString(`<div class="card"><p class="text-sm muted">Every interface through which ` +
		`observation is possible, with four obligations answered for each: what happens by default, ` +
		`how a person says yes, what they see while it is in use, and what is written down. A channel ` +
		`added without answering all four fails the build — which is the only honest meaning of ` +
		`&ldquo;no loophole&rdquo;: not that everything was thought of, but that anything missed ` +
		`cannot be introduced silently.</p>` +
		`<div class="table-wrap"><table class="table"><thead><tr><th>Channel</th><th>Default</th>` +
		`<th>Grant</th><th>Indicator</th><th>Audit</th><th>Enforced by</th></tr></thead><tbody>`)
	for _, c := range chans {
		reg.WriteString(`<tr><td>` + esc(c.Name) + `<div class="text-xs muted mono">` + esc(string(c.ID)) +
			`</div></td><td class="text-xs">` + esc(dispositionLabel(c.Default)) +
			`</td><td class="text-xs">` + esc(grantLabel(c.Grant)) +
			`</td><td class="text-xs">` + esc(indicatorLabel(c.Indicator)) +
			`</td><td class="text-xs">` + esc(auditLabel(c.Audit)) +
			`</td><td class="text-xs muted">` + esc(veilPhaseLabel(c.Phase)) + `</td></tr>`)
	}
	reg.WriteString(`</tbody></table></div></div>`)
	b.WriteString(monAcc("📋", "The Observation Contract", "Every channel, and the four questions it must answer",
		regChip, false, reg.String()))

	// ── The posture report ────────────────────────────────────────────────────
	postureChip := `<span class="mon-chip mon-chip--off">nothing enforcing</span>`
	if pass > 0 {
		postureChip = `<span class="mon-chip mon-chip--on">` + strconv.Itoa(pass) + ` enforcing</span>`
	}
	var post strings.Builder
	post.WriteString(`<div class="card"><p class="text-sm muted">Computed from what was <b>observed` +
		`</b>, never from what is configured. Green means <b>verified enforcing</b> — not ` +
		`&ldquo;switched on&rdquo; — so at Phase 0 nothing here is green, and that is the report ` +
		`working rather than failing. A channel that could not be checked reads <i>unverified</i>: ` +
		`absent evidence is never a pass.</p>`)
	for _, c := range checks {
		post.WriteString(`<p class="text-sm">` + veilStatusChip(c.Status) + ` <b>` + esc(c.Title) + `</b>`)
		if c.Permanent {
			post.WriteString(` <span class="mon-chip mon-chip--off">permanent</span>`)
		}
		post.WriteString(`<br><span class="text-sm muted">` + esc(c.Detail) + `</span></p>`)
	}
	post.WriteString(`</div>`)
	b.WriteString(monAcc("🔎", "Posture report", "What is actually true on this machine right now",
		postureChip, true, post.String()))

	// ── What this will never claim ────────────────────────────────────────────
	b.WriteString(monAcc("🚧", "What VayuVeil will never claim",
		"The boundary, stated so no future wording quietly moves it",
		`<span class="mon-chip mon-chip--off">by construction</span>`, false,
		`<div class="card"><p class="text-sm muted">Not &ldquo;screenshot-proof&rdquo;. Not protection `+
			`against an attacker with root, a kernel or driver-level attacker, DMA-capable hardware, `+
			`firmware, ME/PSP or SMM. Not protection against a camera pointed at the screen, an HDMI `+
			`splitter, a hostile monitor, or electromagnetic reconstruction. Not protection against a `+
			`person who grants capture to malware — policy did its job and the answer was still yes. `+
			`Not a defence against a compromised compositor, which is the trusted base for all of it.`+
			`</p><p class="text-sm muted">And a constitutional one: <b>no Recall-equivalent, ever.</b> `+
			`No periodic snapshotting, no local screen index, no timeline, not even opt-in. The moment `+
			`this product keeps a searchable history of your screen it <i>is</i> the threat, whatever `+
			`the encryption.</p><p class="text-sm muted">Each of these appears in the report above as a `+
			`permanent row that no setting clears. A report where everything eventually goes green `+
			`teaches you to stop reading it.</p></div>`))

	b.WriteString(`</div>`) // mon-stack
	_ = warn
	_ = unver
	_ = obs
	return b.String()
}

func tone(bad bool) string {
	if bad {
		return "warn"
	}
	return ""
}

func dispositionLabel(d vayuveil.Disposition) string {
	switch d {
	case vayuveil.DispositionAbsent:
		return "absent (not built in)"
	case vayuveil.DispositionDeny:
		return "deny"
	case vayuveil.DispositionGrant:
		return "grant only"
	}
	return "—"
}

func grantLabel(g vayuveil.GrantModel) string {
	switch g {
	case vayuveil.GrantNone:
		return "none"
	case vayuveil.GrantPerUse:
		return "per use"
	case vayuveil.GrantScopedTimed:
		return "scoped, time-boxed"
	case vayuveil.GrantPinned:
		return "pinned, listed"
	}
	return "—"
}

func indicatorLabel(i vayuveil.IndicatorKind) string {
	switch i {
	case vayuveil.IndicatorNone:
		return "none"
	case vayuveil.IndicatorCompositor:
		return "compositor-drawn"
	case vayuveil.IndicatorPanel:
		return "panel only"
	}
	return "—"
}

func auditLabel(a vayuveil.AuditLevel) string {
	switch a {
	case vayuveil.AuditAll:
		return "every attempt"
	case vayuveil.AuditGrantsOnly:
		return "grants only"
	case vayuveil.AuditNone:
		return "none"
	}
	return "—"
}

func veilPhaseLabel(p vayuveil.Phase) string {
	switch p {
	case vayuveil.PhaseP1Screen:
		return "P1 — compositor"
	case vayuveil.PhaseP2Input:
		return "P2 — input & clipboard"
	case vayuveil.PhaseP3Accessibility:
		return "P3 — accessibility"
	case vayuveil.PhaseP4Sandbox:
		return "P4 — sandbox & MAC"
	case vayuveil.PhaseP5Hygiene:
		return "P5 — egress & memory"
	}
	return "—"
}

const vayuVeilScript = `
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
var st=document.getElementById('veil-status');
var btn=document.querySelector('[data-veil-toggle]');
if(btn)btn.addEventListener('click',function(){
  var on=btn.getAttribute('data-veil-toggle');
  btn.disabled=true; if(st)st.textContent='Saving…';
  fetch('/os/api/vayuveil/toggle?on='+encodeURIComponent(on),{method:'POST',
    headers:{'X-CSRF-Token':csrf()}})
    .then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};});})
    .then(function(res){btn.disabled=false;
      if(!res.ok){if(st)st.textContent=(res.j&&res.j.message)||'Could not save';return;}
      location.reload();})
    .catch(function(e){btn.disabled=false; if(st)st.textContent='Error: '+e;});
});
})();`

// veilToggleState maps the query value to a stored state, and REFUSES anything
// it does not recognise rather than falling through to "off".
//
// The first version defaulted: anything that was not "1" deactivated. That turns
// a typo, a stale client or a proxy that mangled the query into a silent
// deactivation reported as success — a control doing the opposite of what was
// asked, and saying it worked. An unrecognised instruction is not an instruction.
func veilToggleState(v string) (string, bool) {
	switch strings.TrimSpace(v) {
	case "1":
		return "on", true
	case "0":
		return "off", true
	}
	return "", false
}
