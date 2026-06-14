package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// handleThemeGet renders the admin theme-editor page.
func (a *App) handleThemeGet(w http.ResponseWriter, r *http.Request) {
	vals, err := a.siteSettings.GetAll(r.Context())
	if err != nil {
		http.Error(w, "failed to load settings", 500)
		return
	}
	modeStr := string(mode.Global.Current())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, themeEditorPage(vals, modeStr, ""))
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
		CustomHead      string `json:"theme.custom_head"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON: " + err.Error()}) //nolint:errcheck
		return
	}

	customHead := strings.TrimSpace(body.CustomHead)
	if strings.Contains(strings.ToLower(customHead), "<script") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Custom Head HTML must not contain <script> tags"}) //nolint:errcheck
		return
	}
	customCSS := strings.TrimSpace(body.CustomCSS)
	if len(customCSS) > 16*1024 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Custom CSS exceeds the 16 KB limit"}) //nolint:errcheck
		return
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
		settings.KeyThemeCustomHead:   customHead,
	}

	if err := a.siteSettings.SetMany(r.Context(), kv); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": "save failed: " + err.Error()}) //nolint:errcheck
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
			CustomHead:   newVals[settings.KeyThemeCustomHead],
		})
	}

	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "theme", Severity: "info",
		Msg: "site settings updated", RequestID: getRequestID(r),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
}

// themeEditorPage returns the full HTML for the theme editor admin page.
func themeEditorPage(vals map[string]string, modeStr, errMsg string) string {
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

	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Theme · VayuPress Console</title>
<link rel="stylesheet" href="/static/css/admin.css">
<style>
.theme-tabs{display:flex;gap:2px;margin-bottom:18px;border-bottom:1px solid var(--border)}
.theme-tab{padding:7px 14px;font:600 11px var(--mono);color:var(--text2);cursor:pointer;border:none;background:none;border-bottom:2px solid transparent;margin-bottom:-1px;transition:color .12s,border-color .12s}
.theme-tab.active{color:var(--hi);border-bottom-color:var(--accent)}
.theme-panel{display:none}.theme-panel.active{display:block}
.field-row{display:grid;grid-template-columns:160px 1fr;align-items:start;gap:10px;margin-bottom:12px}
.field-label{font:600 10px var(--mono);letter-spacing:.06em;text-transform:uppercase;color:var(--muted);padding-top:8px}
.field-input{background:var(--surface2);border:1px solid var(--border2);border-radius:var(--radius);color:var(--text);font:400 12px var(--mono);padding:6px 9px;width:100%;transition:border-color .12s;box-sizing:border-box}
.field-input:focus{border-color:var(--accent);outline:none}
textarea.field-input{resize:vertical;min-height:120px;font-size:11px;line-height:1.55}
.color-pair{display:flex;align-items:center;gap:8px}
.color-swatch{width:32px;height:32px;border-radius:var(--radius);border:1px solid var(--border2);cursor:pointer;padding:0}
.field-hint{font:400 9px var(--mono);color:var(--muted);margin-top:4px}
.theme-save{display:inline-flex;align-items:center;gap:6px;padding:7px 18px;background:linear-gradient(135deg,var(--accent) 0%,#4f46e5 100%);border:none;border-radius:var(--radius);color:#fff;font:600 11px var(--font);cursor:pointer;transition:opacity .15s}
.theme-save:hover{opacity:.88}
.theme-save:disabled{opacity:.5;cursor:not-allowed}
.err-banner{padding:8px 12px;background:rgba(239,68,68,.1);border:1px solid rgba(239,68,68,.3);border-radius:var(--radius);color:var(--error);font:400 11px var(--mono);margin-bottom:14px}
.ok-banner{display:none;padding:8px 12px;background:rgba(16,185,129,.08);border:1px solid rgba(16,185,129,.25);border-radius:var(--radius);color:var(--green);font:400 11px var(--mono);margin-bottom:14px}
.warn-box{padding:8px 12px;background:rgba(245,158,11,.07);border:1px solid rgba(245,158,11,.2);border-radius:var(--radius);color:var(--gold);font:400 10px var(--mono);margin-bottom:12px}
</style>
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
</div>
`)

	if safeErr != "" {
		sb.WriteString(`<div class="err-banner">` + safeErr + `</div>`)
	}
	sb.WriteString(`<div id="ok-banner" class="ok-banner">✓ Settings saved — public pages updated.</div>`)

	sb.WriteString(`
<div class="theme-tabs">
  <button type="button" class="theme-tab active" onclick="showTab('identity',this)">Identity</button>
  <button type="button" class="theme-tab" onclick="showTab('palette',this)">Palette</button>
  <button type="button" class="theme-tab" onclick="showTab('code',this)">Custom Code</button>
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
  <p class="field-hint" style="margin-bottom:14px">These override Pico CSS variables on every public page render. Use valid hex colours (e.g. #0d9488).</p>
  <div class="section-title">Light Mode</div>
  <div class="field-row">
    <span class="field-label">Primary</span>
    <div>
      <div class="color-pair">
        <input type="color" class="color-swatch" id="swatch-pl" value="` + v(settings.KeyThemePrimaryLight) + `"
               oninput="document.getElementById('theme.primary_light').value=this.value">
        <input type="text" id="theme.primary_light" class="field-input" style="max-width:120px"
               value="` + v(settings.KeyThemePrimaryLight) + `" placeholder="#0d9488" maxlength="7"
               oninput="document.getElementById('swatch-pl').value=this.value">
      </div>
      <div class="field-hint">Link colour, button fill, tag highlights.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Accent</span>
    <div>
      <div class="color-pair">
        <input type="color" class="color-swatch" id="swatch-al" value="` + v(settings.KeyThemeAccentLight) + `"
               oninput="document.getElementById('theme.accent_light').value=this.value">
        <input type="text" id="theme.accent_light" class="field-input" style="max-width:120px"
               value="` + v(settings.KeyThemeAccentLight) + `" placeholder="#f59e0b" maxlength="7"
               oninput="document.getElementById('swatch-al').value=this.value">
      </div>
      <div class="field-hint">Blockquote border, mode-dot pulse, stat highlights.</div>
    </div>
  </div>
  <div class="section-title">Dark Mode</div>
  <div class="field-row">
    <span class="field-label">Primary</span>
    <div>
      <div class="color-pair">
        <input type="color" class="color-swatch" id="swatch-pd" value="` + v(settings.KeyThemePrimaryDark) + `"
               oninput="document.getElementById('theme.primary_dark').value=this.value">
        <input type="text" id="theme.primary_dark" class="field-input" style="max-width:120px"
               value="` + v(settings.KeyThemePrimaryDark) + `" placeholder="#2dd4bf" maxlength="7"
               oninput="document.getElementById('swatch-pd').value=this.value">
      </div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Accent</span>
    <div>
      <div class="color-pair">
        <input type="color" class="color-swatch" id="swatch-ad" value="` + v(settings.KeyThemeAccentDark) + `"
               oninput="document.getElementById('theme.accent_dark').value=this.value">
        <input type="text" id="theme.accent_dark" class="field-input" style="max-width:120px"
               value="` + v(settings.KeyThemeAccentDark) + `" placeholder="#fbbf24" maxlength="7"
               oninput="document.getElementById('swatch-ad').value=this.value">
      </div>
    </div>
  </div>
</div>

<!-- Custom Code -->
<div id="tab-code" class="theme-panel">
  <div class="warn-box">⚠ Code injected here appears verbatim on every public page. Never include &lt;script&gt; tags — they are blocked server-side.</div>
  <div class="field-row">
    <span class="field-label">Custom CSS</span>
    <div>
      <textarea id="theme.custom_css" class="field-input" rows="10" placeholder="/* e.g. body { font-family: Georgia, serif; } */" maxlength="16384">` + template.HTMLEscapeString(raw(settings.KeyThemeCustomCSS)) + `</textarea>
      <div class="field-hint">Injected inside a &lt;style&gt; tag after pico.min.css + custom.css. Max 16 KB.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Custom &lt;head&gt;</span>
    <div>
      <textarea id="theme.custom_head" class="field-input" rows="6" placeholder="&lt;!-- e.g. analytics snippet, preconnect --&gt;">` + template.HTMLEscapeString(raw(settings.KeyThemeCustomHead)) + `</textarea>
      <div class="field-hint">Injected verbatim inside &lt;head&gt; on public pages. No &lt;script&gt; tags allowed.</div>
    </div>
  </div>
</div>

<div style="margin-top:20px;display:flex;align-items:center;gap:14px">
  <button id="save-btn" class="theme-save" onclick="saveSettings()">◑ Save Settings</button>
  <span id="save-status" style="font:400 10px var(--mono);color:var(--muted)"></span>
</div>

<script>
function showTab(name, btn) {
  document.querySelectorAll('.theme-panel').forEach(function(p){p.classList.remove('active');});
  document.querySelectorAll('.theme-tab').forEach(function(b){b.classList.remove('active');});
  document.getElementById('tab-'+name).classList.add('active');
  btn.classList.add('active');
}
function getVal(id) {
  var el = document.getElementById(id);
  return el ? el.value : '';
}
function csrf() {
  var m = document.cookie.split('; ').find(function(r){return r.startsWith('vp_csrf=');});
  return m ? m.split('=')[1] : '';
}
function saveSettings() {
  var btn = document.getElementById('save-btn');
  var status = document.getElementById('save-status');
  btn.disabled = true;
  status.textContent = 'Saving…';
  var payload = {
    'site.name': getVal('site.name'),
    'site.tagline': getVal('site.tagline'),
    'site.description': getVal('site.description'),
    'site.author': getVal('site.author'),
    'theme.primary_light': getVal('theme.primary_light'),
    'theme.primary_dark': getVal('theme.primary_dark'),
    'theme.accent_light': getVal('theme.accent_light'),
    'theme.accent_dark': getVal('theme.accent_dark'),
    'theme.custom_css': getVal('theme.custom_css'),
    'theme.custom_head': getVal('theme.custom_head')
  };
  fetch('/admin/theme', {
    method: 'POST',
    headers: {'Content-Type': 'application/json', 'X-CSRF-Token': csrf()},
    body: JSON.stringify(payload)
  }).then(function(r){return r.json();}).then(function(data){
    btn.disabled = false;
    if (data.error) {
      status.style.color = 'var(--error)';
      status.textContent = '✗ ' + data.error;
    } else {
      status.style.color = 'var(--green)';
      status.textContent = '✓ Saved — public pages updated';
      document.getElementById('ok-banner').style.display = 'block';
      setTimeout(function(){document.getElementById('ok-banner').style.display='none';}, 4000);
    }
  }).catch(function(e){
    btn.disabled = false;
    status.style.color = 'var(--error)';
    status.textContent = '✗ Network error: ' + e.message;
  });
}
</script>
</main>
</div>
</body></html>
`)
	return sb.String()
}
