// SPDX-License-Identifier: Apache-2.0

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
	"github.com/johalputt/vayupress/internal/search"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
)

// toolModule describes one entry in the Tools & Plugins registry.
type toolModule struct {
	ID       string // stable identifier, used by the toggle API
	Name     string // human label
	Desc     string // one-line description
	Category string // grouping header
	Icon     string // a name from the Still Air icon set (saIcons)

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
			ID: "comments", Name: "Comments", Category: "Engagement", Icon: "talk",
			Desc:    "Reader comments with moderation queue and approval emails.",
			FlagKey: settings.KeyFeatureComments,
			ready:   func(a *App) bool { return a.commentStore != nil },
		},
		{
			ID: "newsletter", Name: "Newsletter", Category: "Engagement", Icon: "mail",
			Desc:    "Double opt-in subscriptions and one-off broadcasts.",
			FlagKey: settings.KeyFeatureNewsletter,
			ready:   func(a *App) bool { return a.newsletterStore != nil },
		},
		{
			ID: "webmentions", Name: "Webmentions", Category: "Engagement", Icon: "link",
			Desc:    "W3C inbound webmention receiver with a moderation queue.",
			FlagKey: settings.KeyFeatureWebmentions,
			ready:   func(a *App) bool { return a.webmentionStore != nil },
		},
		{
			ID: "trending", Name: "Trending & pinned posts", Category: "Engagement", Icon: "timer",
			Desc:    "Show a trending-posts widget (most-viewed over the last 7/30 days, from the built-in analytics) plus your pinned posts on the homepage and under every post. Pin a post with “Feature this post” in the editor.",
			FlagKey: settings.KeyFeatureTrending,
			ready:   func(a *App) bool { return a.analytics != nil },
		},
		{
			ID: "collections", Name: "Collections", Category: "Content", Icon: "book",
			Desc:  "Group posts into ordered series and reading lists.",
			ready: func(a *App) bool { return a.collectionStore != nil },
		},
		{
			ID: "versions", Name: "Version history", Category: "Content", Icon: "timer",
			Desc:  "Automatic per-save snapshots with point-in-time restore.",
			ready: func(a *App) bool { return a.versionStore != nil },
		},
		{
			ID: "redirects", Name: "Redirects", Category: "Content", Icon: "forward",
			Desc:  "Operator-managed 301/302 rules served before routing.",
			ready: func(a *App) bool { return a.redirectMgr != nil },
		},
		{
			ID: "analytics", Name: "Privacy analytics", Category: "Insight", Icon: "trend",
			Desc:  "Cookieless, self-hosted pageview and referrer analytics.",
			ready: func(a *App) bool { return a.analytics != nil },
		},
		{
			ID: "search", Name: "Search", Category: "Insight", Icon: "search",
			Desc:    "VayuFind — the built-in, instant site search. A Ctrl/⌘-K search box opens a fast, typo-friendly overlay that filters a cached index entirely in the browser. Zero external services. Turn off to hide the search box and the /search page.",
			FlagKey: settings.KeyFeatureSearch,
			ready:   func(a *App) bool { return a.search != nil },
		},
		{
			ID: "members", Name: "Memberships", Category: "Insight", Icon: "audience",
			Desc:  "Free and paid reader accounts with paywalled content.",
			ready: func(a *App) bool { return a.members != nil },
		},
		{
			ID: "ai", Name: "AI assistant", Category: "Authoring", Icon: "bot",
			Desc:  "Local-only writing assistant (Ollama) — never leaves the box.",
			ready: func(a *App) bool { return a.aiAssist != nil },
		},
		{
			ID: "diagrams", Name: "Diagrams", Category: "Authoring", Icon: "grid",
			Desc:  "Pure-Go Mermaid→SVG rendering — no reader-side JavaScript.",
			ready: func(a *App) bool { return true },
		},
		{
			ID: "theme-studio", Name: "Theme Studio", Category: "Authoring", Icon: "palette",
			Desc:  "Live design-token editor compiled to strict-CSP CSS.",
			ready: func(a *App) bool { return a.siteSettings != nil },
		},
		{
			ID: "webhooks", Name: "Outbound webhooks", Category: "Integrations", Icon: "link",
			Desc:  "Signed event delivery to external endpoints with retries.",
			ready: func(a *App) bool { return a.webhooks != nil },
		},
		{
			ID: "payments", Name: "Payments & subscriptions", Category: "Monetization", Icon: "card",
			Desc:    "Accept paid memberships via a built-in direct gateway or any connected processor — with emailed receipts.",
			FlagKey: settings.KeyFeaturePayments,
			ready:   func(a *App) bool { return a.payments != nil && a.members != nil },
		},
		{
			ID: "ads", Name: "Advertising", Category: "Monetization", Icon: "megaphone",
			Desc:    "Show operator-managed ad slots on your posts — header, in-article, sidebar, or footer. Off until you enable it.",
			FlagKey: settings.KeyFeatureAds,
			ready:   func(a *App) bool { return a.ads != nil },
		},
		{
			ID: "googleads", Name: "Google AdSense", Category: "Monetization", Icon: "pulse",
			Desc:    "Optional: serve Google AdSense units in your ad slots. Requires a publisher id set in the Ads console.",
			FlagKey: settings.KeyFeatureGoogleAds,
			ready:   func(a *App) bool { return a.adsenseConfigured() },
		},
		{
			ID: "affiliate", Name: "Affiliate disclosure", Category: "Monetization", Icon: "tag",
			Desc:    "Show an FTC-style affiliate-links disclosure banner above your posts. Edit the text in the Ads console.",
			FlagKey: settings.KeyFeatureAffiliate,
			ready:   func(a *App) bool { return a.siteSettings != nil },
		},
		{
			ID: "sponsors", Name: "Sponsor banner", Category: "Monetization", Icon: "audience",
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
			st.Enabled = a.siteSettings.FeatureEnabled(ctx, settings.ForPrimary(), m.FlagKey)
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

	// One band per category, in registry order; a module is a row with its
	// state and, when it can be switched, its switch. Switches save as they
	// change (admin-os-tools.js), so the page has no SaveBar.
	var secs []ui.HTML
	var rows []ui.Row
	cat := ""
	flush := func() {
		if cat != "" {
			secs = append(secs, ui.Section(cat, "", ui.Rows(rows...)))
		}
		rows = nil
	}
	for _, s := range states {
		if s.Category != cat {
			flush()
			cat = s.Category
		}
		rows = append(rows, toolRow(s))
	}
	flush()
	secs = append(secs, ui.Section("Sandboxed plugins", "Out of process", ui.HTML(pluginRegistryHTML())))

	body := string(ui.SettingsPage("Tools and plugins", ui.State("ok", strconv.Itoa(active)+" of "+strconv.Itoa(total)+" on"),
		"Every module of this install, built in and switched here. Nothing is downloaded.", secs...)) +
		`<script nonce="` + nonce + `" src="/os/static/js/admin-os-tools.js?v=` + assetVer("js/admin-os-tools.js") + `"></script>`

	writeOSHTML(w, r, adminOSLayout(nonce, "Tools and plugins", "tools", cfg, htmpl.HTML(body)))
}

// pluginRegistryHTML renders the live sandboxed-plugin registry: every
// subprocess plugin registered via plugins.RegisterSubprocess, with its runtime
// health (process state, invocation count, crash/quarantine status). Empty when
// no out-of-process plugins are installed, with a pointer to the interface spec.
func pluginRegistryHTML() string {
	stats := plugins.SubprocessStats()
	if len(stats) == 0 {
		return `<p class="muted text-sm">None installed. A sandboxed plugin runs as its own process (seccomp, namespaces, a capability allowlist) and speaks the JSON protocol in <code>docs/plugins/SPEC.md</code>.</p>`
	}
	var rows strings.Builder
	for _, s := range stats {
		state := ui.State("neutral", "Stopped")
		switch {
		case s.Quarantined:
			state = ui.State("danger", "Quarantined")
		case s.Running:
			state = ui.State("ok", "Running")
		}
		pid := "—"
		if s.PID > 0 {
			pid = strconv.Itoa(s.PID)
		}
		rows.WriteString(`<tr><td class="row-title">` + html.EscapeString(s.Name) + `</td><td class="muted text-sm">` + pid +
			`</td><td class="muted text-sm">` + strconv.FormatInt(s.Invocations, 10) + `</td><td class="muted text-sm">` + strconv.Itoa(s.Crashes) +
			`</td><td>` + string(state) + `</td></tr>`)
	}
	return `<div class="table-wrap"><table class="table"><thead><tr><th>Plugin</th><th>PID</th><th>Invocations</th><th>Crashes</th><th>State</th></tr></thead><tbody>` +
		rows.String() + `</tbody></table></div>`
}

// toolRow renders one module: what it does on the left; its state and, for a
// module that can be switched, its switch on the right. data-tool-card and
// data-tool-status are where admin-os-tools.js writes a change.
func toolRow(s toolState) ui.Row {
	state := ui.State("ok", "On")
	switch {
	case s.Toggleable && !s.Enabled:
		state = ui.State("neutral", "Off")
	case !s.Ready:
		state = ui.State("warn", "Not running")
	case !s.Toggleable:
		state = ui.State("ok", "Built in")
	}
	control := `<span class="tool-ctl" data-tool-card="` + html.EscapeString(s.ID) + `"><span data-tool-status>` + string(state) + `</span>`
	if s.Toggleable {
		checked := ""
		if s.Enabled {
			checked = " checked"
		}
		control += `<input type="checkbox" class="toggle" role="switch" aria-label="` + html.EscapeString(s.Name) + `" data-tool-toggle="` + html.EscapeString(s.ID) + `"` + checked + `>`
	}
	return ui.Row{Icon: s.Icon, Label: s.Name, Hint: s.Desc, Control: ui.HTML(control + `</span>`)}
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
	if err := a.siteSettings.SetMany(r.Context(), settings.ForPrimary(), map[string]string{flag: val}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "settings-error", err.Error(), "")
		return
	}
	// Apply runtime side-effects for flags that gate a live subsystem, so the
	// change takes effect immediately without a restart. Search: flip the
	// built-in engine on/off and show/hide the public search box & modal.
	if flag == settings.KeyFeatureSearch {
		search.SetEnabled(body.Enabled)
		render.SetSearchEnabled(body.Enabled)
	}
	// Audit the operator action — toggling a public-facing feature is
	// security-relevant, so leave a trail in the structured log.
	logging.LogInfo("tools", "feature "+body.ID+" set to "+val)
	// Toggling a public surface (ads, comments, payments…) can change the markup
	// of every cached page, so drop the rendered cache to apply it immediately.
	render.CachePurgeAll()
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"id": body.ID, "enabled": body.Enabled})
}
