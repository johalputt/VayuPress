package main

// admin_os_tools.go — VayuPress VayuOS "Tools & Plugins" panel.
//
// This is the first stone of the VayuOS vision: a single surface that lists
// every platform module, shows its live runtime status, and lets the operator
// enable or disable the toggleable ones with one click. There is no third-party
// download or remote registry — every module ships inside the single binary,
// so "install" is not a network action: a module is either built in (always
// present) or operator-toggleable via a persisted feature flag.
//
// CSP posture is identical to the rest of VayuOS: no inline styles, the only
// inline <script> carries the per-request nonce, all user-facing strings are
// escaped before HTML emit, and toggles are CSRF-protected writes.

import (
	"context"
	"encoding/json"
	"html"
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/plugins"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// toolModule describes one entry in the Tools & Plugins registry.
type toolModule struct {
	ID       string // stable identifier, used by the toggle API
	Name     string // human label
	Desc     string // one-line description
	Category string // grouping header
	Icon     string // emoji glyph (rendered as text, never as markup)

	// FlagKey is the settings key that toggles this module. Empty means the
	// module is built in and always on (no operator switch).
	FlagKey string

	// ready reports whether the backing store/subsystem is wired at runtime.
	// A module can be enabled by flag yet not ready (e.g. SMTP unconfigured).
	ready func(a *App) bool
}

// toolRegistry is the canonical, ordered list of platform modules. Ordering is
// deliberate: toggleable content features first, then always-on infrastructure.
func (a *App) toolRegistry() []toolModule {
	return []toolModule{
		{
			ID: "comments", Name: "Comments", Category: "Engagement", Icon: "💬",
			Desc:    "Reader comments with moderation queue and approval emails.",
			FlagKey: settings.KeyFeatureComments,
			ready:   func(a *App) bool { return a.commentStore != nil },
		},
		{
			ID: "newsletter", Name: "Newsletter", Category: "Engagement", Icon: "✉️",
			Desc:    "Double opt-in subscriptions and one-off broadcasts.",
			FlagKey: settings.KeyFeatureNewsletter,
			ready:   func(a *App) bool { return a.newsletterStore != nil },
		},
		{
			ID: "webmentions", Name: "Webmentions", Category: "Engagement", Icon: "🔗",
			Desc:    "W3C inbound webmention receiver with a moderation queue.",
			FlagKey: settings.KeyFeatureWebmentions,
			ready:   func(a *App) bool { return a.webmentionStore != nil },
		},
		{
			ID: "collections", Name: "Collections", Category: "Content", Icon: "📚",
			Desc:  "Group posts into ordered series and reading lists.",
			ready: func(a *App) bool { return a.collectionStore != nil },
		},
		{
			ID: "versions", Name: "Version history", Category: "Content", Icon: "🕘",
			Desc:  "Automatic per-save snapshots with point-in-time restore.",
			ready: func(a *App) bool { return a.versionStore != nil },
		},
		{
			ID: "redirects", Name: "Redirects", Category: "Content", Icon: "↪️",
			Desc:  "Operator-managed 301/302 rules served before routing.",
			ready: func(a *App) bool { return a.redirectMgr != nil },
		},
		{
			ID: "analytics", Name: "Privacy analytics", Category: "Insight", Icon: "📈",
			Desc:  "Cookieless, self-hosted pageview and referrer analytics.",
			ready: func(a *App) bool { return a.analytics != nil },
		},
		{
			ID: "members", Name: "Memberships", Category: "Insight", Icon: "👥",
			Desc:  "Free and paid reader accounts with paywalled content.",
			ready: func(a *App) bool { return a.members != nil },
		},
		{
			ID: "ai", Name: "AI assistant", Category: "Authoring", Icon: "🤖",
			Desc:  "Local-only writing assistant (Ollama) — never leaves the box.",
			ready: func(a *App) bool { return a.aiAssist != nil },
		},
		{
			ID: "diagrams", Name: "Diagrams", Category: "Authoring", Icon: "📐",
			Desc:  "Pure-Go Mermaid→SVG rendering — no reader-side JavaScript.",
			ready: func(a *App) bool { return true },
		},
		{
			ID: "theme-studio", Name: "Theme Studio", Category: "Authoring", Icon: "🎨",
			Desc:  "Live design-token editor compiled to strict-CSP CSS.",
			ready: func(a *App) bool { return a.siteSettings != nil },
		},
		{
			ID: "webhooks", Name: "Outbound webhooks", Category: "Integrations", Icon: "🪝",
			Desc:  "Signed event delivery to external endpoints with retries.",
			ready: func(a *App) bool { return a.webhooks != nil },
		},
		{
			ID: "payments", Name: "Payments & subscriptions", Category: "Monetization", Icon: "💳",
			Desc:    "Accept paid memberships via a built-in direct gateway or any connected processor — with emailed receipts.",
			FlagKey: settings.KeyFeaturePayments,
			ready:   func(a *App) bool { return a.payments != nil && a.members != nil },
		},
		{
			ID: "ads", Name: "Advertising", Category: "Monetization", Icon: "📣",
			Desc:    "Show operator-managed ad slots on your posts — header, in-article, sidebar, or footer. Off until you enable it.",
			FlagKey: settings.KeyFeatureAds,
			ready:   func(a *App) bool { return a.ads != nil },
		},
		{
			ID: "googleads", Name: "Google AdSense", Category: "Monetization", Icon: "🟢",
			Desc:    "Optional: serve Google AdSense units in your ad slots. Requires a publisher id set in the Ads console.",
			FlagKey: settings.KeyFeatureGoogleAds,
			ready:   func(a *App) bool { return a.adsenseConfigured() },
		},
		{
			ID: "affiliate", Name: "Affiliate disclosure", Category: "Monetization", Icon: "🔖",
			Desc:    "Show an FTC-style affiliate-links disclosure banner above your posts. Edit the text in the Ads console.",
			FlagKey: settings.KeyFeatureAffiliate,
			ready:   func(a *App) bool { return a.siteSettings != nil },
		},
		{
			ID: "sponsors", Name: "Sponsor banner", Category: "Monetization", Icon: "🤝",
			Desc:    "Promote a sponsor using a dedicated header ad slot. Pairs with the Advertising module's sponsor placement.",
			FlagKey: settings.KeyFeatureSponsors,
			ready:   func(a *App) bool { return a.ads != nil },
		},
	}
}

// toolState is the runtime view of a module returned to the UI.
type toolState struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Desc       string `json:"desc"`
	Category   string `json:"category"`
	Icon       string `json:"icon"`
	Toggleable bool   `json:"toggleable"`
	Enabled    bool   `json:"enabled"`
	Ready      bool   `json:"ready"`
}

func (a *App) toolStates(ctx context.Context) []toolState {
	out := make([]toolState, 0, 12)
	for _, m := range a.toolRegistry() {
		st := toolState{
			ID: m.ID, Name: m.Name, Desc: m.Desc,
			Category: m.Category, Icon: m.Icon,
			Toggleable: m.FlagKey != "",
			Enabled:    true,
			Ready:      m.ready(a),
		}
		if m.FlagKey != "" && a.siteSettings != nil {
			st.Enabled = a.siteSettings.FeatureEnabled(ctx, m.FlagKey)
		}
		out = append(out, st)
	}
	return out
}

// ── Page ─────────────────────────────────────────────────────────────────────

func (a *App) handleOSTools(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	states := a.toolStates(r.Context())

	// Count enabled toggleable + total ready for the summary line.
	active, total := 0, len(states)
	for _, s := range states {
		if s.Ready && (!s.Toggleable || s.Enabled) {
			active++
		}
	}

	// Render grouped cards in registry order, emitting a category header the
	// first time each category is seen.
	var cards strings.Builder
	seen := map[string]bool{}
	for _, s := range states {
		if !seen[s.Category] {
			seen[s.Category] = true
			cards.WriteString(`<div class="tools-cat">` + html.EscapeString(s.Category) + `</div>`)
		}
		cards.WriteString(toolCardHTML(s))
	}

	body := `<div class="page-header">
  <h1>Tools &amp; Plugins <span class="count-pill">` + strconv.Itoa(active) + `/` + strconv.Itoa(total) + `</span></h1>
  <div class="page-actions">
    <span class="text-sm muted">Sovereign modules — all built in, zero downloads.</span>
  </div>
</div>
<div class="tools-grid" data-tools-grid>` + cards.String() + `</div>` +
		pluginRegistryHTML() + `
<script nonce="` + nonce + `" src="/os/static/js/admin-os-tools.js"></script>`

	writeOSHTML(w, adminOSLayout(nonce, "Tools & Plugins", "tools", cfg, htmpl.HTML(body)))
}

// pluginRegistryHTML renders the live sandboxed-plugin registry: every
// subprocess plugin registered via plugins.RegisterSubprocess, with its runtime
// health (process state, invocation count, crash/quarantine status). Empty when
// no out-of-process plugins are installed, with a pointer to the interface spec.
func pluginRegistryHTML() string {
	stats := plugins.SubprocessStats()

	header := `<div class="tools-cat">Sandboxed plugins (out-of-process)</div>`
	if len(stats) == 0 {
		return header + `<div class="card">
  <div class="tool-card__desc muted">No out-of-process plugins are installed. Sandboxed plugins run as isolated subprocesses (seccomp, namespaces, capability allowlists) and speak the line-oriented JSON IPC protocol — see <code>docs/plugins/SPEC.md</code>.</div>
</div>`
	}

	var rows strings.Builder
	for _, s := range stats {
		status, cls := "Stopped", "tool-status--idle"
		switch {
		case s.Quarantined:
			status, cls = "Quarantined", "tool-status--off"
		case s.Running:
			status, cls = "Running", "tool-status--on"
		}
		pid := "—"
		if s.PID > 0 {
			pid = strconv.Itoa(s.PID)
		}
		rows.WriteString(`<tr>
  <td class="row-title">` + html.EscapeString(s.Name) + `</td>
  <td class="muted text-sm">` + pid + `</td>
  <td class="muted text-sm">` + strconv.FormatInt(s.Invocations, 10) + `</td>
  <td class="muted text-sm">` + strconv.Itoa(s.Crashes) + `</td>
  <td><span class="tool-status ` + cls + `">` + status + `</span></td>
</tr>`)
	}

	return header + `<div class="card">
  <div class="table-wrap"><table class="table">
    <thead><tr><th>Plugin</th><th>PID</th><th>Invocations</th><th>Crashes</th><th>Status</th></tr></thead>
    <tbody>` + rows.String() + `</tbody>
  </table></div>
  <div class="text-xs muted mt-3">Each plugin is a sandboxed subprocess (capability allowlists, seccomp, namespaces). Interface contract: <code>docs/plugins/SPEC.md</code>.</div>
</div>`
}

// toolCardHTML renders a single module card. Toggleable modules get a switch;
// built-in modules get a static "Built-in" badge. Status reflects readiness.
func toolCardHTML(s toolState) string {
	var status, statusCls string
	switch {
	case s.Toggleable && !s.Enabled:
		status, statusCls = "Disabled", "tool-status--off"
	case !s.Ready:
		status, statusCls = "Inactive", "tool-status--idle"
	default:
		status, statusCls = "Active", "tool-status--on"
	}

	var control string
	if s.Toggleable {
		checked := ""
		if s.Enabled {
			checked = " checked"
		}
		// The switch posts through admin-os-tools.js (CSRF-guarded fetch).
		control = `<label class="switch" title="Enable or disable this module">
      <input type="checkbox" class="switch-input" data-tool-toggle="` + html.EscapeString(s.ID) + `"` + checked + `>
      <span class="switch-track" aria-hidden="true"></span>
    </label>`
	} else {
		control = `<span class="chip">Built-in</span>`
	}

	return `<div class="tool-card" data-tool-card="` + html.EscapeString(s.ID) + `">
  <div class="tool-card__head">
    <span class="tool-card__icon" aria-hidden="true">` + html.EscapeString(s.Icon) + `</span>
    <div class="tool-card__title">` + html.EscapeString(s.Name) + `</div>
    ` + control + `
  </div>
  <div class="tool-card__desc">` + html.EscapeString(s.Desc) + `</div>
  <div class="tool-card__foot">
    <span class="tool-status ` + statusCls + `" data-tool-status>` + status + `</span>
  </div>
</div>`
}

// ── APIs ─────────────────────────────────────────────────────────────────────

// handleOSToolsList returns the registry as JSON (read-only, no CSRF).
func (a *App) handleOSToolsList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"tools": a.toolStates(r.Context())})
}

// handleOSToolToggle flips a single toggleable module on or off. Only flags in
// settings.FeatureKeys are accepted; anything else is rejected so a built-in
// module can never be switched off.
func (a *App) handleOSToolToggle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "settings-error", "settings not initialised", "")
		return
	}

	// Resolve the module to its flag key via the registry.
	var flag string
	for _, m := range a.toolRegistry() {
		if m.ID == body.ID {
			flag = m.FlagKey
			break
		}
	}
	if flag == "" || !settings.FeatureKeys[flag] {
		writeAPIError(w, r, http.StatusBadRequest, "not-toggleable", "Unknown or built-in module", "")
		return
	}

	val := "off"
	if body.Enabled {
		val = "on"
	}
	if err := a.siteSettings.SetMany(r.Context(), map[string]string{flag: val}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "settings-error", err.Error(), "")
		return
	}
	// Audit the operator action — toggling a public-facing feature is
	// security-relevant, so leave a trail in the structured log.
	logging.LogInfo("tools", "feature "+body.ID+" set to "+val)
	// Toggling a public surface (ads, comments, payments…) can change the markup
	// of every cached page, so drop the rendered cache to apply it immediately.
	render.CachePurgeAll()
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"id": body.ID, "enabled": body.Enabled})
}
