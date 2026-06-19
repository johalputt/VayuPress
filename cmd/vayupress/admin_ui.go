package main

// admin_ui.go — the modern, CSP-compliant admin UI ("Admin v2"), mounted under
// the /admin/v2 route prefix. It is entirely self-contained and does NOT touch
// the existing /admin console, routes.go, main.go, or middleware.go. The parent
// wires it with a single call: a.registerAdminUIRoutes(r).
//
// CSP posture (from middleware.go, non-negotiable):
//   default-src 'self'; style-src 'self'; script-src 'self' 'nonce-<NONCE>'; ...
// Consequences honoured here:
//   - No inline <style> and no style="" attributes anywhere. All styling lives
//     in the precompiled /admin/v2/static/css/admin-v2.css.
//   - All scripts are same-origin external files; the single inline bootstrap
//     block carries the per-request nonce via render.CSPNonce(r).
//   - No external CDNs/origins are referenced.

import (
	"context"
	"html"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
)

// adminV2StaticDir resolves the on-disk static directory the same way main.go
// does, so the explicit asset routes serve the vendored CSS/JS. Operators who
// relocate STATIC_DIR get the right files without touching the existing
// /static allowlist in routes.go.
func adminV2StaticDir() string {
	return config.EnvOr("STATIC_DIR", "/var/www/vayupress/static")
}

// registerAdminUIRoutes mounts the modern admin UI under /admin/v2.
// All page routes require auth.RequireAPIKey. Pages render with a CSP nonce.
// The login page and the static asset routes are registered publicly (outside
// the auth group) so an unauthenticated operator can reach them.
func (a *App) registerAdminUIRoutes(r chi.Router) {
	// Public: vendored static assets (served same-origin → style-src/script-src 'self').
	r.Get("/admin/v2/static/css/admin-v2.css", serveAdminV2Asset("css/admin-v2.css", "text/css; charset=utf-8"))
	r.Get("/admin/v2/static/js/admin-v2.js", serveAdminV2Asset("js/admin-v2.js", "application/javascript; charset=utf-8"))
	// Public: brand fonts (font-src 'self'). Optional — operator drops woff2 files.
	r.Get("/admin/v2/static/fonts/{file}", func(w http.ResponseWriter, req *http.Request) {
		// Build the path from string literals matched in the switch, not from
		// the user-supplied URL parameter, to prevent any path-traversal.
		var canon string
		switch chi.URLParam(req, "file") {
		case "space-grotesk.woff2":
			canon = "space-grotesk.woff2"
		case "inter.woff2":
			canon = "inter.woff2"
		default:
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "font/woff2")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeFile(w, req, filepath.Join(adminV2StaticDir(), "fonts", canon))
	})

	// Public: login page.
	r.Get("/admin/v2/login", a.handleV2Login)

	// Protected pages.
	r.Group(func(pr chi.Router) {
		pr.Use(auth.RequireAPIKey)
		pr.Get("/admin/v2", a.handleV2Dashboard)
		pr.Get("/admin/v2/posts", a.handleV2Posts)
		pr.Get("/admin/v2/editor", a.handleV2Editor)
		pr.Get("/admin/v2/editor/{slug}", a.handleV2Editor)
		pr.Get("/admin/v2/settings", a.handleV2Settings)
	})
}

// serveAdminV2Asset serves a vendored asset from the resolved static dir with
// the correct Content-Type and an immutable cache header.
func serveAdminV2Asset(rel, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		http.ServeFile(w, req, filepath.Join(adminV2StaticDir(), filepath.FromSlash(rel)))
	}
}

// ── Shared layout ──────────────────────────────────────────────────────────

// adminV2Layout renders the shared chrome (sidebar + topbar + main) around a
// page body. The nonce is injected on the single inline bootstrap <script>;
// admin-v2.js is loaded same-origin. No inline styles are emitted.
func adminV2Layout(nonce, title, sidebarActive, bodyHTML string) string {
	nav := func(href, label, key string) string {
		cls := "nav-link"
		if key == sidebarActive {
			cls += " active"
		}
		return `<a class="` + cls + `" href="` + href + `">` + html.EscapeString(label) + `</a>`
	}
	et := html.EscapeString(title)
	return `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + et + ` — VayuPress Admin</title>
<meta name="robots" content="noindex, nofollow">
<link rel="stylesheet" href="/admin/v2/static/css/admin-v2.css">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head><body>
<a href="#main-content" class="skip-link">Skip to main content</a>
<div class="shell">
<aside class="sidebar">
  <div class="sidebar-brand"><img src="/static/favicon-light.png" alt="" width="26" height="26">VayuPress</div>
  <nav class="sidebar-nav" aria-label="Primary">
    ` + nav("/admin/v2", "Dashboard", "dashboard") + `
    ` + nav("/admin/v2/posts", "Posts", "posts") + `
    ` + nav("/admin/v2/editor", "New Post", "editor") + `
    <div class="nav-section">System</div>
    ` + nav("/admin/v2/settings", "Settings", "settings") + `
    <a class="nav-link" href="/admin">Classic Console</a>
  </nav>
  <div class="sidebar-foot">Admin v2 · CSP-strict</div>
</aside>
<div class="main">
  <header class="topbar" role="banner">
    <button type="button" class="menu-toggle" data-action="toggle-sidebar" aria-label="Toggle menu">≡</button>
    <span class="topbar-title">` + et + `</span>
    <span class="topbar-spacer"></span>
    <a class="btn btn-primary btn-sm" href="/admin/v2/editor">New Post</a>
  </header>
  <main id="main-content" class="content">
` + bodyHTML + `
  </main>
</div>
</div>
<script nonce="` + nonce + `" src="/admin/v2/static/js/admin-v2.js"></script>
</body></html>`
}

// writeV2HTML applies the shared response headers (HTML content type, noindex,
// CSRF cookie for the double-submit autosave handshake) and writes the page.
func writeV2HTML(w http.ResponseWriter, body string) {
	if token := auth.GenerateCSRFToken(); token != "" {
		http.SetCookie(w, &http.Cookie{
			Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode,
			HttpOnly: false, Secure: csrfCookieSecure(), MaxAge: 3600,
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// ── Pages ──────────────────────────────────────────────────────────────────

func (a *App) handleV2Dashboard(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	snap := a.getAdminSnapshot()

	pct := int(snap.StoragePct)
	barCls, wCls := "progress-bar", storageWidthClass(pct)
	if pct >= 90 {
		barCls += " danger"
	} else if pct >= 75 {
		barCls += " warn"
	}

	recent := `<div class="table-empty">No articles yet.</div>`
	if len(snap.RecentArticles) > 0 {
		rows := ""
		for _, ra := range snap.RecentArticles {
			rows += `<tr><td><a href="/admin/v2/editor/` + html.EscapeString(ra.Slug) + `">` +
				html.EscapeString(ra.Title) + `</a></td><td class="muted">` +
				ra.CreatedAt.UTC().Format("2006-01-02 15:04") + `</td></tr>`
		}
		recent = `<table class="table"><thead><tr><th>Title</th><th>Created</th></tr></thead><tbody>` + rows + `</tbody></table>`
	}

	body := `<div class="page-header"><h1>Dashboard</h1></div>
<div class="grid grid-4">
  <div class="card stat"><span class="stat-value">` + strconv.Itoa(snap.TotalArticles) + `</span><span class="stat-label">Articles</span></div>
  <div class="card stat"><span class="stat-value stat-primary">` + strconv.Itoa(snap.PendingJobs) + `</span><span class="stat-label">Pending jobs</span></div>
  <div class="card stat"><span class="stat-value stat-accent">` + strconv.Itoa(snap.FailedJobs) + `</span><span class="stat-label">Failed jobs</span></div>
  <div class="card stat"><span class="stat-value">` + strconv.Itoa(snap.CompletedJobs) + `</span><span class="stat-label">Completed jobs</span></div>
</div>
<div class="card mt-2">
  <div class="card-title">Storage</div>
  <div class="progress"><div class="` + barCls + ` ` + wCls + `"></div></div>
  <div class="hint mt-1">` + strconv.Itoa(pct) + `% of quota used · cache hit ` +
		strconv.Itoa(int(snap.CacheHitRatio*100)) + `%</div>
</div>
<div class="card mt-2">
  <div class="card-title">Recent articles</div>
  ` + recent + `
</div>`
	writeV2HTML(w, adminV2Layout(nonce, "Dashboard", "dashboard", body))
}

func (a *App) handleV2Posts(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	res, err := a.articles.List(r.Context(), 1, 50, "")
	rows := ""
	if err == nil {
		for _, p := range res.Articles {
			tags := ""
			for _, t := range p.Tags {
				tags += `<span class="badge badge-primary tag-chip">#` + html.EscapeString(t) + `</span>`
			}
			rows += `<tr><td><a href="/admin/v2/editor/` + html.EscapeString(p.Slug) + `">` +
				html.EscapeString(p.Title) + `</a></td><td>` + tags + `</td><td class="muted">` +
				p.UpdatedAt.UTC().Format("2006-01-02") + `</td></tr>`
		}
	}
	table := `<div class="table-empty">No posts found.</div>`
	if rows != "" {
		table = `<table class="table"><thead><tr><th>Title</th><th>Tags</th><th>Updated</th></tr></thead><tbody>` + rows + `</tbody></table>`
	}
	body := `<div class="page-header"><h1>Posts</h1><a class="btn btn-primary" href="/admin/v2/editor">New Post</a></div>
<div class="card">` + table + `</div>`
	writeV2HTML(w, adminV2Layout(nonce, "Posts", "posts", body))
}

func (a *App) handleV2Editor(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	slug := chi.URLParam(r, "slug")
	title, content := "", ""
	heading := "New Post"
	if slug != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		art, err := a.articles.Get(ctx, slug)
		if err == nil {
			title, content = art.Title, art.Content
			heading = "Edit Post"
		}
	}
	body := editorBodyHTML(slug, heading, title, content)
	writeV2HTML(w, adminV2Layout(nonce, heading, "editor", body))
}

// editorBodyHTML builds the post-editor body: meta column + toolbar + split
// view (textarea/preview) + slash palette + status bar + SEO preview. All
// interactivity is wired by admin-v2.js via data-* attributes (CSP-safe).
func editorBodyHTML(slug, heading, title, content string) string {
	et := html.EscapeString(title)
	ec := html.EscapeString(content)
	es := html.EscapeString(slug)
	return `<div class="page-header"><h1>` + html.EscapeString(heading) + `</h1>
  <div class="btn-row">
    <button type="button" class="btn btn-ghost btn-sm" data-action="toggle-preview" title="Toggle preview (Ctrl/⌘+P)">Preview</button>
    <button type="button" class="btn btn-ghost btn-sm" data-action="toggle-distraction" title="Focus mode (Ctrl/⌘+.)">Focus</button>
    <div class="dropdown">
      <button type="button" class="btn btn-ghost btn-sm" data-dropdown-toggle="version-menu" data-load-versions="version-menu">History ▾</button>
      <div class="dropdown-menu" id="version-menu"><div class="version-item muted">Open to load…</div></div>
    </div>
    <button type="button" class="btn btn-accent btn-sm" data-action="save-now" title="Save (Ctrl/⌘+S)">Save</button>
  </div>
</div>
<div class="editor-grid">
  <div class="editor-meta">
    <div class="card">
      <div class="field"><label for="ed-title">Title</label>
        <input id="ed-title" class="input" data-field="title" value="` + et + `" placeholder="Post title"></div>
      <div class="field"><label for="ed-slug">Slug</label>
        <input id="ed-slug" class="input" data-field="slug" value="` + es + `" placeholder="auto-from-title"></div>
      <div class="card seo-card">
        <div class="card-title">SEO preview</div>
        <div class="seo-preview">
          <div class="seo-preview-title" data-seo-title>Untitled</div>
          <div class="seo-preview-url" data-seo-url>/your-slug</div>
          <div class="seo-preview-desc" data-seo-desc>No description yet.</div>
        </div>
        <div class="seo-meter"><div class="seo-meter-bar" data-seo-meter></div></div>
        <div class="hint" data-seo-hint>Add a title and 50+ words for a healthy score.</div>
      </div>
      <p class="hint">Autosave PUTs to <code>/api/v1/articles/{slug}</code> as you type. Drag an image onto the editor to upload.</p>
      <!-- AUTH HANDSHAKE: leave blank if the API key is supplied via cookie/proxy.
           Operators wire a real key here only if RequireAPIKey needs the header. -->
      <input type="hidden" id="vp-api-key" value="">
    </div>
  </div>
  <div class="editor-pane">
    <div class="editor-toolbar">
      <button type="button" class="tool-btn" data-wrap="**|**" title="Bold (Ctrl/⌘+B)"><b>B</b></button>
      <button type="button" class="tool-btn" data-wrap="*|*" title="Italic (Ctrl/⌘+I)"><i>I</i></button>
      <button type="button" class="tool-btn" data-prefix="## " title="Heading">H</button>
      <button type="button" class="tool-btn" data-wrap="[|](https://)" title="Link (Ctrl/⌘+K)">↗</button>
      <button type="button" class="tool-btn" data-wrap="` + "`" + `|` + "`" + `" title="Inline code">&lt;/&gt;</button>
      <button type="button" class="tool-btn" data-prefix="> " title="Quote">&ldquo;</button>
      <button type="button" class="tool-btn" data-prefix="- " title="List">&bull;</button>
      <button type="button" class="tool-btn" data-action="insert-image" title="Upload image">🖼</button>
      <span class="tool-spacer"></span>
      <button type="button" class="tool-btn" data-action="toggle-distraction" title="Focus mode">⤢</button>
      <span class="badge save-status" data-save-status>Idle</span>
    </div>
    <div class="split">
      <div class="split-pane">
        <textarea class="editor-area" data-editor data-slug="` + es + `" spellcheck="true" placeholder="Write in Markdown… type / for commands, drag an image to upload">` + ec + `</textarea>
        <div class="slash-palette" data-slash-palette role="listbox" aria-label="Insert block"></div>
        <div class="drop-overlay" data-drop-overlay><div class="drop-overlay-inner">Drop image to upload</div></div>
      </div>
      <div class="split-pane preview-pane"><div class="preview" data-preview></div></div>
    </div>
    <div class="editor-statusbar">
      <span data-wordcount>0 words</span><span class="sep">·</span>
      <span data-charcount>0 chars</span><span class="sep">·</span>
      <span data-readtime>~1 min read</span>
      <span class="tool-spacer"></span>
      <span class="kbd-hint-text">/ commands · Ctrl/⌘+S save · Ctrl/⌘+. focus</span>
    </div>
  </div>
</div>
<input type="file" accept="image/png,image/jpeg,image/gif,image/webp" data-image-input hidden>`
}

func (a *App) handleV2Settings(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	body := `<div class="page-header"><h1>Settings</h1></div>
<div class="card">
  <div class="card-title">Updates</div>
  <div class="update-panel">
    <p class="update-status" data-update-status>Click check to query for a newer VayuPress release.</p>
    <div class="btn-row">
      <button type="button" class="btn btn-primary" data-action="check-updates">Check for updates</button>
    </div>
    <p class="hint">Queries <code>/admin/api/updates/check</code> (built separately).</p>
  </div>
</div>
<div class="card mt-2">
  <div class="card-title">About</div>
  <p class="muted">VayuPress Admin v2 — strict-CSP, vendored UI. Served under <code>/admin/v2</code>.</p>
</div>`
	writeV2HTML(w, adminV2Layout(nonce, "Settings", "settings", body))
}

func (a *App) handleV2Login(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	body := `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in — VayuPress Admin</title><meta name="robots" content="noindex, nofollow">
<link rel="stylesheet" href="/admin/v2/static/css/admin-v2.css">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head><body>
<div class="login-wrap"><div class="card login-card">
  <div class="login-brand">VayuPress</div>
  <form method="GET" action="/admin/v2">
    <div class="field"><label for="lg-key">API key</label>
      <input id="lg-key" class="input" type="password" autocomplete="off" placeholder="Provided via header/cookie at the proxy"></div>
    <p class="hint">Access is gated by <code>auth.RequireAPIKey</code>. Supply your key through the configured header or cookie, then continue.</p>
    <div class="btn-row mt-2"><a class="btn btn-primary" href="/admin/v2">Enter console</a></div>
  </form>
</div></div>
<script nonce="` + nonce + `" src="/admin/v2/static/js/admin-v2.js"></script>
</body></html>`
	_, _ = w.Write([]byte(body))
}

// storageWidthClass maps a 0–100 percentage to a precompiled width utility
// class, avoiding any inline style attribute (style-src 'self').
func storageWidthClass(pct int) string {
	buckets := []int{0, 10, 20, 25, 30, 40, 50, 60, 70, 75, 80, 90, 100}
	chosen := 0
	for _, b := range buckets {
		if pct >= b {
			chosen = b
		}
	}
	return "w-" + strconv.Itoa(chosen)
}
