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
	"os"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/i18n"
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
// Names, descriptions and categories are in lang; an ID and a flag are not.
func (a *App) toolRegistry(lang string) []toolModule {
	return []toolModule{
		{
			ID: "comments", Name: i18n.T(lang, "Comments"), Category: i18n.T(lang, "Engagement"), Icon: "talk",
			Desc:    i18n.T(lang, "Reader comments with moderation queue and approval emails."),
			FlagKey: settings.KeyFeatureComments,
			ready:   func(a *App) bool { return a.commentStore != nil },
		},
		{
			ID: "newsletter", Name: i18n.T(lang, "Newsletter"), Category: i18n.T(lang, "Engagement"), Icon: "mail",
			Desc:    i18n.T(lang, "Double opt-in subscriptions and one-off broadcasts."),
			FlagKey: settings.KeyFeatureNewsletter,
			ready:   func(a *App) bool { return a.newsletterStore != nil },
		},
		{
			ID: "webmentions", Name: i18n.T(lang, "Webmentions"), Category: i18n.T(lang, "Engagement"), Icon: "link",
			Desc:    i18n.T(lang, "W3C inbound webmention receiver with a moderation queue."),
			FlagKey: settings.KeyFeatureWebmentions,
			ready:   func(a *App) bool { return a.webmentionStore != nil },
		},
		{
			ID: "trending", Name: i18n.T(lang, "Trending & pinned posts"), Category: i18n.T(lang, "Engagement"), Icon: "timer",
			Desc:    i18n.T(lang, "Show a trending-posts widget (most-viewed over the last 7/30 days, from the built-in analytics) plus your pinned posts on the homepage and under every post. Pin a post with “Feature this post” in the editor."),
			FlagKey: settings.KeyFeatureTrending,
			ready:   func(a *App) bool { return a.analytics != nil },
		},
		{
			ID: "post-images", Name: i18n.T(lang, "Card images from Pixabay"), Category: i18n.T(lang, "Content"), Icon: "image",
			Desc:    a.postImagesSummary(context.Background(), lang),
			FlagKey: settings.KeyFeaturePostImages,
			ready:   func(a *App) bool { return a.pixabayKey(context.Background()) != "" },
		},
		{
			ID: "collections", Name: i18n.T(lang, "Collections"), Category: i18n.T(lang, "Content"), Icon: "book",
			Desc:  i18n.T(lang, "Group posts into ordered series and reading lists."),
			ready: func(a *App) bool { return a.collectionStore != nil },
		},
		{
			ID: "versions", Name: i18n.T(lang, "Version history"), Category: i18n.T(lang, "Content"), Icon: "timer",
			Desc:  i18n.T(lang, "Automatic per-save snapshots with point-in-time restore."),
			ready: func(a *App) bool { return a.versionStore != nil },
		},
		{
			ID: "redirects", Name: i18n.T(lang, "Redirects"), Category: i18n.T(lang, "Content"), Icon: "forward",
			Desc:  i18n.T(lang, "Operator-managed 301/302 rules served before routing."),
			ready: func(a *App) bool { return a.redirectMgr != nil },
		},
		{
			ID: "analytics", Name: i18n.T(lang, "Privacy analytics"), Category: i18n.T(lang, "Insight"), Icon: "trend",
			Desc:  i18n.T(lang, "Cookieless, self-hosted pageview and referrer analytics."),
			ready: func(a *App) bool { return a.analytics != nil },
		},
		{
			ID: "search", Name: i18n.T(lang, "Search"), Category: i18n.T(lang, "Insight"), Icon: "search",
			Desc:    i18n.T(lang, "VayuFind — the built-in, instant site search. A Ctrl/⌘-K search box opens a fast, typo-friendly overlay that filters a cached index entirely in the browser. Zero external services. Turn off to hide the search box and the /search page."),
			FlagKey: settings.KeyFeatureSearch,
			ready:   func(a *App) bool { return a.search != nil },
		},
		{
			ID: "members", Name: i18n.T(lang, "Memberships"), Category: i18n.T(lang, "Insight"), Icon: "audience",
			Desc:  i18n.T(lang, "Free and paid reader accounts with paywalled content."),
			ready: func(a *App) bool { return a.members != nil },
		},
		{
			ID: "ai", Name: i18n.T(lang, "AI assistant"), Category: i18n.T(lang, "Authoring"), Icon: "bot",
			Desc:  i18n.T(lang, "Local-only writing assistant (Ollama) — never leaves the box."),
			ready: func(a *App) bool { return a.aiAssist != nil },
		},
		{
			ID: "diagrams", Name: i18n.T(lang, "Diagrams"), Category: i18n.T(lang, "Authoring"), Icon: "grid",
			Desc:  i18n.T(lang, "Pure-Go Mermaid→SVG rendering — no reader-side JavaScript."),
			ready: func(a *App) bool { return true },
		},
		{
			ID: "theme-studio", Name: i18n.T(lang, "Theme Studio"), Category: i18n.T(lang, "Authoring"), Icon: "palette",
			Desc:  i18n.T(lang, "Live design-token editor compiled to strict-CSP CSS."),
			ready: func(a *App) bool { return a.siteSettings != nil },
		},
		{
			ID: "webhooks", Name: i18n.T(lang, "Outbound webhooks"), Category: i18n.T(lang, "Integrations"), Icon: "link",
			Desc:  i18n.T(lang, "Signed event delivery to external endpoints with retries."),
			ready: func(a *App) bool { return a.webhooks != nil },
		},
		{
			ID: "payments", Name: i18n.T(lang, "Payments & subscriptions"), Category: i18n.T(lang, "Monetization"), Icon: "card",
			Desc:    i18n.T(lang, "Accept paid memberships via a built-in direct gateway or any connected processor — with emailed receipts."),
			FlagKey: settings.KeyFeaturePayments,
			ready:   func(a *App) bool { return a.payments != nil && a.members != nil },
		},
		{
			ID: "ads", Name: i18n.T(lang, "Advertising"), Category: i18n.T(lang, "Monetization"), Icon: "megaphone",
			Desc:    i18n.T(lang, "Show operator-managed ad slots on your posts — header, in-article, sidebar, or footer. Off until you enable it."),
			FlagKey: settings.KeyFeatureAds,
			ready:   func(a *App) bool { return a.ads != nil },
		},
		{
			ID: "googleads", Name: i18n.T(lang, "Google AdSense"), Category: i18n.T(lang, "Monetization"), Icon: "pulse",
			Desc:    i18n.T(lang, "Optional: serve Google AdSense units in your ad slots. Requires a publisher ID set on Advertising."),
			FlagKey: settings.KeyFeatureGoogleAds,
			ready:   func(a *App) bool { return a.adsenseConfigured() },
		},
		{
			ID: "affiliate", Name: i18n.T(lang, "Affiliate disclosure"), Category: i18n.T(lang, "Monetization"), Icon: "tag",
			Desc:    i18n.T(lang, "Show an FTC-style affiliate-links disclosure banner above your posts. Edit the text on Advertising."),
			FlagKey: settings.KeyFeatureAffiliate,
			ready:   func(a *App) bool { return a.siteSettings != nil },
		},
		{
			ID: "sponsors", Name: i18n.T(lang, "Sponsor banner"), Category: i18n.T(lang, "Monetization"), Icon: "audience",
			Desc:    i18n.T(lang, "Promote a sponsor using a dedicated header ad slot. Pairs with the Advertising module's sponsor placement."),
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
	for _, m := range a.toolRegistry(i18n.Lang(ctx)) {
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
	lang := i18n.Lang(r.Context())
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
		rows = append(rows, toolRow(lang, s))
	}
	flush()
	secs = append(secs, ui.Section(i18n.T(lang, "Sandboxed plugins"), i18n.T(lang, "Out of process"), ui.HTML(pluginRegistryHTML(lang))))

	title := i18n.T(lang, "Tools and plugins")
	body := string(ui.SettingsPage(title, ui.State("ok", i18n.T(lang, "{active} of {total} on", "active", active, "total", total)),
		i18n.T(lang, "Every module of this install, built in and switched here. Nothing is downloaded."), secs...)) +
		`<script nonce="` + nonce + `" src="/os/static/js/admin-os-tools.js?v=` + assetVer("js/admin-os-tools.js") + `"></script>`

	writeOSHTML(w, r, adminOSLayout(nonce, title, "tools", cfg, htmpl.HTML(body)))
}

// pluginsDir is where installed plugins live, one directory each.
func pluginsDir() string { return config.EnvOr("PLUGINS_DIR", "/var/lib/vayupress/plugins") }

// startPlugins starts the hook workers and loads the plugins installed on
// this machine, each checked before it starts (internal/plugins/loader.go),
// when the install runs plugins at all (VAYU_PLUGINS_ENABLED=true).
func (a *App) startPlugins() {
	if os.Getenv("VAYU_PLUGINS_ENABLED") != "true" {
		return
	}
	a.pluginManager.Start(plugins.DefaultPoolSize, plugins.DefaultQueueDepth)
	plugins.LoadDir(a.pluginRegistry, pluginsDir(), Version)
}

// pluginRegistryHTML renders the installed plugins: each one loaded, with its
// runtime health (process state, invocations, crashes, quarantine), and each
// one refused, with the reason. Plugins are off unless the install was started
// with VAYU_PLUGINS_ENABLED=true, and the page says so.
func pluginRegistryHTML(lang string) string {
	code := func(s string) string { return "<code>" + html.EscapeString(s) + "</code>" }
	if os.Getenv("VAYU_PLUGINS_ENABLED") != "true" {
		return `<p class="muted text-sm">` + escFill(i18n.T(lang, "Plugins are off on this install. They run when the server is started with {setting}; each one is a directory in {dir} holding its {manifest} and binary ({docs}). A plugin runs as its own process: its own namespaces, cgroup limits, a syscall filter, no capabilities, and a path and network allowlist.",
			"setting", htmlMark(0), "dir", htmlMark(1), "manifest", htmlMark(2), "docs", htmlMark(3)), code("VAYU_PLUGINS_ENABLED=true"), code(pluginsDir()), code("plugin.json"), code("docs/plugins/README.md")) + `</p>`
	}
	stats := plugins.SubprocessStats()
	var refused []plugins.LoadResult
	for _, r := range plugins.LoadResults() {
		if r.Err != nil {
			refused = append(refused, r)
		}
	}
	if len(stats) == 0 && len(refused) == 0 {
		return `<p class="muted text-sm">` + escFill(i18n.T(lang, "None installed in {dir}. A plugin is a directory there holding its {manifest} and binary, loaded when the server starts ({docs}).",
			"dir", htmlMark(0), "manifest", htmlMark(1), "docs", htmlMark(2)), code(pluginsDir()), code("plugin.json"), code("docs/plugins/README.md")) + `</p>`
	}
	var rows strings.Builder
	for _, s := range stats {
		state := ui.State("neutral", i18n.T(lang, "Stopped"))
		switch {
		case s.Quarantined:
			state = ui.State("danger", i18n.T(lang, "Quarantined"))
		case s.Running:
			state = ui.State("ok", i18n.T(lang, "Running"))
		}
		pid := "—"
		if s.PID > 0 {
			pid = strconv.Itoa(s.PID)
		}
		rows.WriteString(`<tr><td class="row-title" translate="no">` + html.EscapeString(s.Name) + `</td><td class="muted text-sm">` + pid +
			`</td><td class="muted text-sm">` + strconv.FormatInt(s.Invocations, 10) + `</td><td class="muted text-sm">` + strconv.Itoa(s.Crashes) +
			`</td><td>` + string(state) + `</td></tr>`)
	}
	for _, r := range refused {
		rows.WriteString(`<tr><td class="row-title"><span translate="no">` + html.EscapeString(r.Name) + `</span><div class="muted text-sm" translate="no">` + html.EscapeString(r.Err.Error()) +
			`</div></td><td class="muted text-sm">—</td><td class="muted text-sm">—</td><td class="muted text-sm">—</td><td>` + string(ui.State("warn", i18n.T(lang, "Not loaded"))) + `</td></tr>`)
	}
	th := func(s string) string { return `<th>` + html.EscapeString(s) + `</th>` }
	return `<div class="table-wrap"><table class="table"><thead><tr>` + th(i18n.T(lang, "Plugin")) + th(i18n.T(lang, "PID")) + th(i18n.T(lang, "Invocations")) + th(i18n.T(lang, "Crashes")) + th(i18n.T(lang, "State")) + `</tr></thead><tbody>` +
		rows.String() + `</tbody></table></div>`
}

// toolRow renders one module: what it does on the left; its state and, for a
// module that can be switched, its switch on the right. data-tool-card and
// data-tool-status are where admin-os-tools.js writes a change.
func toolRow(lang string, s toolState) ui.Row {
	state := ui.State("ok", i18n.T(lang, "On"))
	switch {
	case s.Toggleable && !s.Enabled:
		state = ui.State("neutral", i18n.T(lang, "Off"))
	case !s.Ready:
		state = ui.State("warn", i18n.T(lang, "Not running"))
	case !s.Toggleable:
		state = ui.State("ok", i18n.T(lang, "Built in"))
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
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", i18n.T(i18n.Lang(r.Context()), "The request could not be read."), "")
		return
	}
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "settings-error", i18n.T(i18n.Lang(r.Context()), "The settings cannot be read on this install."), "")
		return
	}

	// Resolve the module to its flag key via the registry.
	var flag string
	for _, m := range a.toolRegistry(i18n.Lang(r.Context())) {
		if m.ID == body.ID {
			flag = m.FlagKey
			break
		}
	}
	if flag == "" || !settings.FeatureKeys[flag] {
		writeAPIError(w, r, http.StatusBadRequest, "not-toggleable", i18n.T(i18n.Lang(r.Context()), "That module is built in or unknown, and has no switch."), "")
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
