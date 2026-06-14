package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strings"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// hexColorRe matches #rgb and #rrggbb CSS hex colours (case-insensitive).
var hexColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// verifyTokenRe matches search-engine verification tokens: letters, digits and
// the punctuation those providers use. Anything else is rejected so the value
// can only ever render inside a meta content="" attribute.
var verifyTokenRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// robotsChoices lists the <meta name="robots"> options in display order. The
// label is shown in the <select>; the value is what gets persisted and must be
// a member of settings.RobotsOptions.
var robotsChoices = []struct{ value, label string }{
	{"", "Default (no directive — fully indexable)"},
	{"index,follow", "index, follow"},
	{"index,nofollow", "index, nofollow"},
	{"noindex,follow", "noindex, follow"},
	{"noindex,nofollow", "noindex, nofollow"},
}

// robotsOptionsHTML renders the <option> elements for the robots <select>,
// marking current as selected.
func robotsOptionsHTML(current string) string {
	var sb strings.Builder
	for _, c := range robotsChoices {
		sel := ""
		if c.value == current {
			sel = " selected"
		}
		sb.WriteString(`<option value="` + template.HTMLEscapeString(c.value) + `"` + sel + `>` + template.HTMLEscapeString(c.label) + `</option>`)
	}
	return sb.String()
}

// handleThemeCSS serves the dynamic per-site theme stylesheet at /theme.css.
// Served from the same origin (text/css) so it satisfies the strict
// `style-src 'self'` CSP. An ETag over the CSS content lets browsers revalidate
// cheaply; no-cache forces revalidation so palette changes propagate even to
// disk-cached HTML pages on the next request.
func (a *App) handleThemeCSS(w http.ResponseWriter, r *http.Request) {
	etag := render.ThemeCSSETag()
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("ETag", etag)
	// Palette changes are infrequent; a short max-age lets browsers serve from
	// cache without a round-trip, while the ETag still yields cheap 304s and
	// caps propagation lag (≤60 s) after a save. CachePurgeAll() already
	// regenerates the HTML pages on save.
	w.Header().Set("Cache-Control", "public, max-age=60")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	fmt.Fprint(w, render.ThemeCSS())
}

// handleThemeToggleJS serves the public sun/moon theme switcher script.
// Same-origin static asset → satisfies `script-src 'self'` without a nonce.
func (a *App) handleThemeToggleJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	fmt.Fprint(w, render.ThemeToggleJS)
}

// handleThemeGet renders the admin theme-editor page.
func (a *App) handleThemeGet(w http.ResponseWriter, r *http.Request) {
	vals, err := a.siteSettings.GetAll(r.Context())
	if err != nil {
		http.Error(w, "failed to load settings", 500)
		return
	}
	modeStr := string(mode.Global.Current())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, themeEditorPage(vals, modeStr, render.CSPNonce(r), ""))
}

// handleThemeSave processes the JSON POST from the theme editor.
// The browser sends application/json via fetch with the X-CSRF-Token header.
func (a *App) handleThemeSave(w http.ResponseWriter, r *http.Request) {
	cur := mode.Global.Current()
	if cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		json.NewEncoder(w).Encode(map[string]string{"error": "settings cannot be saved in " + string(cur) + " mode"}) //nolint:errcheck
		return
	}

	fail := func(code int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
	}

	var body struct {
		SiteName        string `json:"site.name"`
		SiteTagline     string `json:"site.tagline"`
		SiteDescription string `json:"site.description"`
		SiteAuthor      string `json:"site.author"`
		PrimaryLight    string `json:"theme.primary_light"`
		PrimaryDark     string `json:"theme.primary_dark"`
		AccentLight     string `json:"theme.accent_light"`
		AccentDark      string `json:"theme.accent_dark"`
		CustomCSS       string `json:"theme.custom_css"`
		Keywords        string `json:"head.keywords"`
		ThemeColor      string `json:"head.theme_color"`
		Robots          string `json:"head.robots"`
		VerifyGoogle    string `json:"head.verify_google"`
		VerifyBing      string `json:"head.verify_bing"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(400, "invalid JSON: "+err.Error())
		return
	}

	customCSS := strings.TrimSpace(body.CustomCSS)
	if len(customCSS) > 16*1024 {
		fail(400, "Custom CSS exceeds the 16 KB limit")
		return
	}

	// Validate colour fields are #rgb / #rrggbb so they can't break the served
	// stylesheet or smuggle extra CSS declarations into the variable block.
	for label, val := range map[string]string{
		"Primary (light)": body.PrimaryLight,
		"Primary (dark)":  body.PrimaryDark,
		"Accent (light)":  body.AccentLight,
		"Accent (dark)":   body.AccentDark,
		"Theme colour":    body.ThemeColor,
	} {
		if v := strings.TrimSpace(val); v != "" && !hexColorRe.MatchString(v) {
			fail(400, label+" must be a hex colour like #0d9488")
			return
		}
	}

	// Declarative <head> capabilities: each is allowlisted/tokenised so only a
	// safe, escaped <meta> subset can ever reach the document head.
	keywords := strings.TrimSpace(body.Keywords)
	if len(keywords) > 256 {
		fail(400, "Keywords exceed the 256-character limit")
		return
	}
	robots := strings.TrimSpace(body.Robots)
	if !settings.RobotsOptions[robots] {
		fail(400, "Robots directive is not an allowed value")
		return
	}
	verifyGoogle := strings.TrimSpace(body.VerifyGoogle)
	verifyBing := strings.TrimSpace(body.VerifyBing)
	for label, tok := range map[string]string{
		"Google verification": verifyGoogle,
		"Bing verification":   verifyBing,
	} {
		if tok != "" && !verifyTokenRe.MatchString(tok) {
			fail(400, label+" token may contain only letters, digits, '-', '_', and '.'")
			return
		}
	}

	kv := map[string]string{
		settings.KeySiteName:          strings.TrimSpace(body.SiteName),
		settings.KeySiteTagline:       strings.TrimSpace(body.SiteTagline),
		settings.KeySiteDescription:   strings.TrimSpace(body.SiteDescription),
		settings.KeySiteAuthor:        strings.TrimSpace(body.SiteAuthor),
		settings.KeyThemePrimaryLight: strings.TrimSpace(body.PrimaryLight),
		settings.KeyThemePrimaryDark:  strings.TrimSpace(body.PrimaryDark),
		settings.KeyThemeAccentLight:  strings.TrimSpace(body.AccentLight),
		settings.KeyThemeAccentDark:   strings.TrimSpace(body.AccentDark),
		settings.KeyThemeCustomCSS:    customCSS,
		settings.KeyHeadKeywords:      keywords,
		settings.KeyHeadThemeColor:    strings.TrimSpace(body.ThemeColor),
		settings.KeyHeadRobots:        robots,
		settings.KeyHeadVerifyGoogle:  verifyGoogle,
		settings.KeyHeadVerifyBing:    verifyBing,
	}

	if err := a.siteSettings.SetMany(r.Context(), kv); err != nil {
		fail(500, "save failed: "+err.Error())
		return
	}

	// Push updated values into the render pipeline immediately.
	if newVals, err := a.siteSettings.GetAll(r.Context()); err == nil {
		render.SetActiveSettings(render.SiteSettings{
			Name:         newVals[settings.KeySiteName],
			Tagline:      newVals[settings.KeySiteTagline],
			Description:  newVals[settings.KeySiteDescription],
			Author:       newVals[settings.KeySiteAuthor],
			PrimaryLight: newVals[settings.KeyThemePrimaryLight],
			PrimaryDark:  newVals[settings.KeyThemePrimaryDark],
			AccentLight:  newVals[settings.KeyThemeAccentLight],
			AccentDark:   newVals[settings.KeyThemeAccentDark],
			CustomCSS:    newVals[settings.KeyThemeCustomCSS],
			Keywords:     newVals[settings.KeyHeadKeywords],
			ThemeColor:   newVals[settings.KeyHeadThemeColor],
			Robots:       newVals[settings.KeyHeadRobots],
			VerifyGoogle: newVals[settings.KeyHeadVerifyGoogle],
			VerifyBing:   newVals[settings.KeyHeadVerifyBing],
		})
	}

	// Identity (name/tagline/description) and custom <head> are baked into the
	// cached HTML, so purge all rendered fragments and regenerate the feeds.
	// The palette propagates separately via /theme.css revalidation.
	render.CachePurgeAll()
	go generateSitemap()
	go generateRSS()
	go generateRobots()

	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "theme", Severity: "info",
		Msg: "site settings updated", RequestID: getRequestID(r),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
}

// themeEditorPage returns the full HTML for the theme editor admin page.
func themeEditorPage(vals map[string]string, modeStr, nonce, errMsg string) string {
	safeErr := template.HTMLEscapeString(errMsg)

	v := func(key string) string {
		if val, ok := vals[key]; ok {
			return template.HTMLEscapeString(val)
		}
		if def, ok := settings.Defaults[key]; ok {
			return template.HTMLEscapeString(def)
		}
		return ""
	}
	raw := func(key string) string {
		if val, ok := vals[key]; ok {
			return val
		}
		if def, ok := settings.Defaults[key]; ok {
			return def
		}
		return ""
	}
	// vOr returns the escaped value, falling back to fallback when unset (used to
	// give the colour swatch a sensible starting hue when no value is stored).
	vOr := func(key, fallback string) string {
		if got := raw(key); got != "" {
			return template.HTMLEscapeString(got)
		}
		return template.HTMLEscapeString(fallback)
	}

	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Theme · VayuPress Console</title>
<link rel="stylesheet" href="/static/css/admin.css">
</head><body>
<div class="app-shell">
<header class="topbar">
  <a href="/admin" class="topbar-logo">
    <span class="omega-mark">Ω</span>
    <span class="topbar-wordmark">VayuPress<span class="topbar-sep">/</span><span class="topbar-domain">Theme</span></span>
  </a>
  <div class="topbar-center"><span class="live-chip"><span class="live-dot"></span>LIVE</span></div>
  <div class="topbar-right">
    <span class="mode-badge mode-`)
	sb.WriteString(template.HTMLEscapeString(modeStr))
	sb.WriteString(`"><span class="pulse-dot"></span>`)
	sb.WriteString(template.HTMLEscapeString(modeStr))
	sb.WriteString(`</span>
  </div>
</header>
<nav class="sidebar" aria-label="Admin navigation">
  <div class="sidebar-section">
    <span class="sidebar-section-label">Core</span>
    <a href="/admin" class="sidebar-item"><div class="sidebar-item-left"><span class="sidebar-icon">◈</span>Overview</div></a>
    <a href="/api/v1/articles" class="sidebar-item"><div class="sidebar-item-left"><span class="sidebar-icon">◻</span>Articles</div></a>
    <a href="/admin/replay" class="sidebar-item"><div class="sidebar-item-left"><span class="sidebar-icon">⟲</span>Replay</div></a>
  </div>
  <div class="sidebar-section">
    <span class="sidebar-section-label">Observe</span>
    <a href="/admin/topology" class="sidebar-item"><div class="sidebar-item-left"><span class="sidebar-icon">❖</span>Topology</div></a>
    <a href="/health/dependencies" class="sidebar-item"><div class="sidebar-item-left"><span class="sidebar-icon">♥</span>Health</div></a>
  </div>
  <div class="sidebar-section">
    <span class="sidebar-section-label">Govern</span>
    <a href="/admin/modes" class="sidebar-item"><div class="sidebar-item-left"><span class="sidebar-icon">⬡</span>System Modes</div></a>
    <a href="/admin/policy" class="sidebar-item"><div class="sidebar-item-left"><span class="sidebar-icon">✦</span>Policy</div></a>
    <a href="/admin/adr" class="sidebar-item"><div class="sidebar-item-left"><span class="sidebar-icon">≡</span>ADRs</div></a>
  </div>
  <div class="sidebar-section">
    <span class="sidebar-section-label">Site</span>
    <a href="/admin/theme" class="sidebar-item active"><div class="sidebar-item-left"><span class="sidebar-icon">◑</span>Theme</div></a>
  </div>
  <div class="sidebar-section">
    <span class="sidebar-section-label">System</span>
    <a href="/metrics" class="sidebar-item" target="_blank" rel="noopener"><div class="sidebar-item-left"><span class="sidebar-icon">∼</span>Metrics</div></a>
  </div>
  <div class="sidebar-footer">
    <span class="sidebar-version">v` + template.HTMLEscapeString(render.Version) + `</span>
    <span class="sidebar-constitution">Ω1–Ω9 compliant</span>
  </div>
</nav>
<main id="main-content">
<div class="page-header">
  <div>
    <div class="page-title">Theme &amp; Site Settings</div>
    <div class="page-sub">Customise identity, palette, and injected code · changes live immediately · governed write</div>
  </div>
  <a class="btn" href="/" target="_blank" rel="noopener">View Public Site ↗</a>
</div>
`)

	if safeErr != "" {
		sb.WriteString(`<div class="err-banner">` + safeErr + `</div>`)
	}
	sb.WriteString(`<div id="ok-banner" class="ok-banner">✓ Settings saved — public pages updated.</div>`)

	sb.WriteString(`
<div class="theme-tabs">
  <button type="button" class="theme-tab active" data-tab="identity">Identity</button>
  <button type="button" class="theme-tab" data-tab="palette">Palette</button>
  <button type="button" class="theme-tab" data-tab="code">Custom CSS</button>
  <button type="button" class="theme-tab" data-tab="head">Head &amp; SEO</button>
</div>

<!-- Identity -->
<div id="tab-identity" class="theme-panel active">
  <div class="field-row">
    <span class="field-label">Site Name</span>
    <div>
      <input type="text" id="site.name" class="field-input" value="` + v(settings.KeySiteName) + `" maxlength="80" placeholder="VayuPress">
      <div class="field-hint">Shown in the nav brand and page titles.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Tagline</span>
    <div>
      <input type="text" id="site.tagline" class="field-input" value="` + v(settings.KeySiteTagline) + `" maxlength="160" placeholder="Publishing as an adaptive runtime.">
      <div class="field-hint">Hero headline on the homepage.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Description</span>
    <div>
      <input type="text" id="site.description" class="field-input" value="` + v(settings.KeySiteDescription) + `" maxlength="300" placeholder="Durable by design, observable end to end.">
      <div class="field-hint">Used as the meta description on all public pages.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Author</span>
    <div>
      <input type="text" id="site.author" class="field-input" value="` + v(settings.KeySiteAuthor) + `" maxlength="120" placeholder="Ankush Choudhary Johal">
      <div class="field-hint">Author name in article footers and JSON-LD schema.</div>
    </div>
  </div>
</div>

<!-- Palette -->
<div id="tab-palette" class="theme-panel">
  <p class="field-hint theme-note">These override Pico CSS variables on every public page render. Use valid hex colours (e.g. #0d9488).</p>
  <div class="section-title">Light Mode</div>
  <div class="field-row">
    <span class="field-label">Primary</span>
    <div>
      <div class="color-pair">
        <input type="color" class="color-swatch" id="swatch-pl" data-sync="theme.primary_light" value="` + v(settings.KeyThemePrimaryLight) + `">
        <input type="text" id="theme.primary_light" class="field-input field-hex" data-sync="swatch-pl"
               value="` + v(settings.KeyThemePrimaryLight) + `" placeholder="#0d9488" maxlength="7">
      </div>
      <div class="field-hint">Link colour, button fill, tag highlights.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Accent</span>
    <div>
      <div class="color-pair">
        <input type="color" class="color-swatch" id="swatch-al" data-sync="theme.accent_light" value="` + v(settings.KeyThemeAccentLight) + `">
        <input type="text" id="theme.accent_light" class="field-input field-hex" data-sync="swatch-al"
               value="` + v(settings.KeyThemeAccentLight) + `" placeholder="#f59e0b" maxlength="7">
      </div>
      <div class="field-hint">Blockquote border, mode-dot pulse, stat highlights.</div>
    </div>
  </div>
  <div class="section-title">Dark Mode</div>
  <div class="field-row">
    <span class="field-label">Primary</span>
    <div>
      <div class="color-pair">
        <input type="color" class="color-swatch" id="swatch-pd" data-sync="theme.primary_dark" value="` + v(settings.KeyThemePrimaryDark) + `">
        <input type="text" id="theme.primary_dark" class="field-input field-hex" data-sync="swatch-pd"
               value="` + v(settings.KeyThemePrimaryDark) + `" placeholder="#2dd4bf" maxlength="7">
      </div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Accent</span>
    <div>
      <div class="color-pair">
        <input type="color" class="color-swatch" id="swatch-ad" data-sync="theme.accent_dark" value="` + v(settings.KeyThemeAccentDark) + `">
        <input type="text" id="theme.accent_dark" class="field-input field-hex" data-sync="swatch-ad"
               value="` + v(settings.KeyThemeAccentDark) + `" placeholder="#fbbf24" maxlength="7">
      </div>
    </div>
  </div>
</div>

<!-- Custom Code -->
<div id="tab-code" class="theme-panel">
  <div class="warn-box">Custom CSS is served same-origin via <code>/theme.css</code> (CSP-safe) and cannot reach external origins or execute scripts. Max 16 KB.</div>
  <div class="field-row">
    <span class="field-label">Custom CSS</span>
    <div>
      <textarea id="theme.custom_css" class="field-input" rows="10" placeholder="/* e.g. body { font-family: Georgia, serif; } */" maxlength="16384">` + template.HTMLEscapeString(raw(settings.KeyThemeCustomCSS)) + `</textarea>
      <div class="field-hint">Appended after pico.min.css + custom.css in the served stylesheet.</div>
    </div>
  </div>
</div>

<!-- Head & SEO (declarative — no raw HTML) -->
<div id="tab-head" class="theme-panel">
  <div class="warn-box">Raw &lt;head&gt; HTML is intentionally not accepted — it would allow meta-refresh redirects, external beacons, and &lt;base&gt; hijacks the CSP cannot fully block. These fields render to a validated, escaped &lt;meta&gt; allowlist only.</div>
  <div class="field-row">
    <span class="field-label">Keywords</span>
    <div>
      <input type="text" id="head.keywords" class="field-input" value="` + v(settings.KeyHeadKeywords) + `" maxlength="256" placeholder="publishing, governance, sovereignty">
      <div class="field-hint">Comma-separated. Rendered as &lt;meta name="keywords"&gt;.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Theme colour</span>
    <div>
      <div class="color-pair">
        <input type="color" class="color-swatch" id="swatch-tc" data-sync="head.theme_color" value="` + vOr(settings.KeyHeadThemeColor, "#0d9488") + `">
        <input type="text" id="head.theme_color" class="field-input field-hex" data-sync="swatch-tc" value="` + v(settings.KeyHeadThemeColor) + `" placeholder="#0d9488" maxlength="7">
      </div>
      <div class="field-hint">Browser UI tint. Rendered as &lt;meta name="theme-color"&gt;.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Robots</span>
    <div>
      <select id="head.robots" class="field-input">` + robotsOptionsHTML(raw(settings.KeyHeadRobots)) + `</select>
      <div class="field-hint">Crawler directive. Leave default unless intentionally restricting indexing.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Google verify</span>
    <div>
      <input type="text" id="head.verify_google" class="field-input" value="` + v(settings.KeyHeadVerifyGoogle) + `" maxlength="128" placeholder="google-site-verification token">
      <div class="field-hint">Token only (letters/digits/-._). Rendered as the verification &lt;meta&gt;.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Bing verify</span>
    <div>
      <input type="text" id="head.verify_bing" class="field-input" value="` + v(settings.KeyHeadVerifyBing) + `" maxlength="128" placeholder="msvalidate.01 token">
      <div class="field-hint">Token only. Rendered as &lt;meta name="msvalidate.01"&gt;.</div>
    </div>
  </div>
</div>

<div class="theme-actions">
  <button id="save-btn" class="theme-save">◑ Save Settings</button>
  <span id="save-status" class="save-status"></span>
</div>

<script nonce="` + template.HTMLEscapeString(nonce) + `">
(function(){
  function getVal(id){ var el=document.getElementById(id); return el?el.value:''; }
  function csrf(){
    var m=document.cookie.split('; ').find(function(r){return r.startsWith('vp_csrf=');});
    return m?m.split('=')[1]:'';
  }
  // Tab switching (no inline handlers — CSP-clean).
  document.querySelectorAll('.theme-tab').forEach(function(btn){
    btn.addEventListener('click', function(){
      var name=btn.getAttribute('data-tab');
      document.querySelectorAll('.theme-panel').forEach(function(p){p.classList.remove('active');});
      document.querySelectorAll('.theme-tab').forEach(function(b){b.classList.remove('active');});
      var panel=document.getElementById('tab-'+name);
      if(panel) panel.classList.add('active');
      btn.classList.add('active');
    });
  });
  // Two-way colour <-> hex sync via data-sync attributes.
  document.querySelectorAll('[data-sync]').forEach(function(el){
    el.addEventListener('input', function(){
      var target=document.getElementById(el.getAttribute('data-sync'));
      if(target) target.value=el.value;
    });
  });
  // Save.
  var btn=document.getElementById('save-btn');
  var status=document.getElementById('save-status');
  btn.addEventListener('click', function(){
    btn.disabled=true;
    status.style.color='var(--muted)';
    status.textContent='Saving…';
    var payload={
      'site.name':getVal('site.name'),
      'site.tagline':getVal('site.tagline'),
      'site.description':getVal('site.description'),
      'site.author':getVal('site.author'),
      'theme.primary_light':getVal('theme.primary_light'),
      'theme.primary_dark':getVal('theme.primary_dark'),
      'theme.accent_light':getVal('theme.accent_light'),
      'theme.accent_dark':getVal('theme.accent_dark'),
      'theme.custom_css':getVal('theme.custom_css'),
      'head.keywords':getVal('head.keywords'),
      'head.theme_color':getVal('head.theme_color'),
      'head.robots':getVal('head.robots'),
      'head.verify_google':getVal('head.verify_google'),
      'head.verify_bing':getVal('head.verify_bing')
    };
    fetch('/admin/theme',{
      method:'POST',
      headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},
      body:JSON.stringify(payload)
    }).then(function(r){return r.json();}).then(function(data){
      btn.disabled=false;
      if(data.error){
        status.style.color='var(--error)';
        status.textContent='✗ '+data.error;
      } else {
        status.style.color='var(--green)';
        status.textContent='✓ Saved — public pages updated';
        var ok=document.getElementById('ok-banner');
        ok.style.display='block';
        setTimeout(function(){ok.style.display='none';},4000);
      }
    }).catch(function(e){
      btn.disabled=false;
      status.style.color='var(--error)';
      status.textContent='✗ Network error: '+e.message;
    });
  });
})();
</script>
</main>
</div>
</body></html>
`)
	return sb.String()
}
