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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/mode"
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
	// Vendored DOMPurify (Cure53, Apache-2.0/MPL-2.0) — self-hosted, served
	// same-origin so script-src 'self' covers it with no nonce. Used by the
	// editor preview as the HTML sanitiser (no external CDN, ADR-0065).
	r.Get("/admin/v2/static/js/purify.min.js", serveAdminV2Asset("js/purify.min.js", "application/javascript; charset=utf-8"))
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

	// Public: login page + credential submission (password sign-in, Tier 1).
	r.Get("/admin/v2/login", a.handleV2Login)
	// Login/logout are credential-bearing form posts; they cannot send the
	// double-submit CSRF header, so they rely on the SameSite=Lax session cookie
	// rather than CSRFTokenMiddleware.
	r.Post("/admin/v2/login", a.handleV2LoginSubmit)
	r.Post("/admin/v2/logout", a.handleV2Logout)

	// Admin v2 is soft-deprecated (ADR-0069 Stage 2). By default its pages
	// redirect to the v3 equivalents; the ADMIN_LEGACY escape hatch keeps them
	// live (with a deprecation banner) for one more release.
	if !adminLegacyEnabled() {
		redirect := legacyRedirect()
		r.Get("/admin/v2", redirect)
		r.Get("/admin/v2/posts", redirect)
		r.Get("/admin/v2/editor", redirect)
		r.Get("/admin/v2/editor/{slug}", redirect)
		r.Get("/admin/v2/seo", redirect)
		r.Get("/admin/v2/settings", redirect)
		return
	}

	// Protected pages — accept an API key OR a login session (Tier 1).
	r.Group(func(pr chi.Router) {
		pr.Use(a.requireSessionOrAPIKey)
		pr.Get("/admin/v2", a.handleV2Dashboard)
		pr.Get("/admin/v2/posts", a.handleV2Posts)
		pr.Get("/admin/v2/editor", a.handleV2Editor)
		pr.Get("/admin/v2/editor/{slug}", a.handleV2Editor)
		pr.Get("/admin/v2/seo", a.handleV2SEO)
		pr.Get("/admin/v2/settings", a.handleV2Settings)
		// SEO artefact regeneration — CSRF-protected governed write.
		pr.With(auth.CSRFTokenMiddleware).Post("/admin/v2/api/seo/regenerate", a.handleSEORegenerate)
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
    ` + nav("/admin/v2/seo", "SEO", "seo") + `
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
    <a class="btn btn-ghost btn-sm" href="/admin/v3" title="Try the next-generation admin">✨ Admin v3</a>
    <a class="btn btn-primary btn-sm" href="/admin/v2/editor">New Post</a>
    <form method="POST" action="/admin/v2/logout" class="topbar-logout">
      <button type="submit" class="btn btn-ghost btn-sm">Sign out</button>
    </form>
  </header>
  <main id="main-content" class="content">
` + legacyDeprecationBanner(nonce) + bodyHTML + `
  </main>
</div>
</div>
<script src="/admin/v2/static/js/purify.min.js"></script>
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

	body := `<div class="page-header"><h1>Dashboard</h1>
  <div class="btn-row">
    <a class="btn btn-ghost btn-sm" href="/admin/v2/seo">SEO</a>
    <a class="btn btn-primary" href="/admin/v2/editor">New Post</a>
  </div>
</div>
<div class="grid grid-4">
  <div class="card stat"><span class="stat-value">` + strconv.Itoa(snap.TotalArticles) + `</span><span class="stat-label">Articles</span></div>
  <div class="card stat"><span class="stat-value stat-primary">` + strconv.Itoa(snap.PendingJobs) + `</span><span class="stat-label">Pending jobs</span></div>
  <div class="card stat"><span class="stat-value stat-accent">` + strconv.Itoa(snap.FailedJobs) + `</span><span class="stat-label">Failed jobs</span></div>
  <div class="card stat"><span class="stat-value">` + strconv.Itoa(snap.CompletedJobs) + `</span><span class="stat-label">Completed jobs</span></div>
</div>
<div class="grid grid-2 mt-2">
  <div class="card">
    <div class="card-title">Storage</div>
    <div class="progress"><div class="` + barCls + ` ` + wCls + `"></div></div>
    <div class="hint mt-1">` + strconv.Itoa(pct) + `% of quota used · cache hit ` +
		strconv.Itoa(int(snap.CacheHitRatio*100)) + `%</div>
  </div>
  <div class="card">
    <div class="card-title">Quick actions</div>
    <div class="quick-actions">
      <a class="quick-action" href="/admin/v2/editor"><span class="qa-icon">✍️</span><span>Write a post</span></a>
      <a class="quick-action" href="/admin/v2/posts"><span class="qa-icon">📄</span><span>Manage posts</span></a>
      <a class="quick-action" href="/admin/v2/seo"><span class="qa-icon">🔍</span><span>SEO dashboard</span></a>
      <a class="quick-action" href="/admin/v2/settings"><span class="qa-icon">⚙️</span><span>Settings &amp; updates</span></a>
    </div>
  </div>
</div>
<div class="card mt-2">
  <div class="card-title">Recent articles</div>
  ` + recent + `
</div>`
	writeV2HTML(w, adminV2Layout(nonce, "Dashboard", "dashboard", body))
}

func (a *App) handleV2Posts(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	res, err := a.articles.List(r.Context(), 1, 200, "")
	rows := ""
	count := 0
	if err == nil {
		for _, p := range res.Articles {
			count++
			tags := ""
			searchTags := ""
			for _, t := range p.Tags {
				tags += `<span class="badge badge-primary tag-chip">#` + html.EscapeString(t) + `</span>`
				searchTags += " " + t
			}
			// data-search carries lowercased title+tags for instant client-side filtering.
			rows += `<tr data-post-row data-search="` + html.EscapeString(strings.ToLower(p.Title+searchTags)) + `">` +
				`<td><a href="/admin/v2/editor/` + html.EscapeString(p.Slug) + `">` +
				html.EscapeString(p.Title) + `</a><div class="row-sub muted">/` + html.EscapeString(p.Slug) + `</div></td>` +
				`<td>` + tags + `</td>` +
				`<td class="muted">` + p.UpdatedAt.UTC().Format("2006-01-02") + `</td>` +
				`<td class="row-actions"><a class="btn btn-ghost btn-sm" href="/admin/v2/editor/` + html.EscapeString(p.Slug) + `">Edit</a>` +
				`<a class="btn btn-ghost btn-sm" href="/` + html.EscapeString(p.Slug) + `" target="_blank" rel="noopener">View ↗</a></td>` +
				`</tr>`
		}
	}

	var body string
	if count == 0 {
		// Delightful empty state instead of a bare "no rows" line.
		body = `<div class="page-header"><h1>Posts</h1></div>
<div class="card empty-state">
  <div class="empty-icon">✍️</div>
  <div class="empty-title">No posts yet</div>
  <div class="empty-sub">Your published articles will appear here. Write your first one — it takes a minute.</div>
  <a class="btn btn-primary mt-2" href="/admin/v2/editor">Write your first post</a>
</div>`
	} else {
		body = `<div class="page-header"><h1>Posts <span class="count-pill">` + strconv.Itoa(count) + `</span></h1>
  <a class="btn btn-primary" href="/admin/v2/editor">New Post</a>
</div>
<div class="card">
  <div class="toolbar-row">
    <input class="input search-input" type="search" data-posts-search placeholder="Search posts by title or tag…" aria-label="Search posts">
  </div>
  <table class="table"><thead><tr><th>Title</th><th>Tags</th><th>Updated</th><th></th></tr></thead>
    <tbody>` + rows + `</tbody>
  </table>
  <div class="table-empty" data-search-empty hidden>No posts match your search.</div>
</div>`
	}
	writeV2HTML(w, adminV2Layout(nonce, "Posts", "posts", body))
}

func (a *App) handleV2Editor(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	slug := chi.URLParam(r, "slug")
	title, content := "", ""
	format, source := "markdown", ""
	heading := "New Post"
	if slug != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		art, err := a.articles.Get(ctx, slug)
		if err == nil {
			title, content = art.Title, art.Content
			heading = "Edit Post"
		}
		// Prefer the editable source side-car (multi-format round-trip). If a
		// post was authored in the v2 editor we get back the original Markdown
		// or HTML; otherwise we fall back to the stored (HTML) content below.
		var f, s string
		if err := dbpkg.DB.QueryRowContext(r.Context(),
			`SELECT format, source FROM article_sources WHERE slug=?`, slug).Scan(&f, &s); err == nil && s != "" {
			format, source = f, s
		} else {
			// Legacy / non-v2 post: edit the stored HTML directly.
			format, source = "html", content
		}
	}
	body := editorBodyHTML(slug, heading, title, format, source)
	writeV2HTML(w, adminV2Layout(nonce, heading, "editor", body))
}

// editorBodyHTML builds the post-editor body: meta column + toolbar + split
// view (textarea/preview) + slash palette + status bar + SEO preview. All
// interactivity is wired by admin-v2.js via data-* attributes (CSP-safe).
// The textarea holds the editable *source*; data-format selects how it is
// interpreted (markdown or html) for preview and on save.
func editorBodyHTML(slug, heading, title, format, source string) string {
	et := html.EscapeString(title)
	ec := html.EscapeString(source)
	es := html.EscapeString(slug)
	ef := html.EscapeString(format)
	mdSel, htmlSel := "", ""
	if format == "html" {
		htmlSel = " active"
	} else {
		mdSel = " active"
	}
	viewBtn := ""
	if slug != "" {
		viewBtn = `<a class="btn btn-ghost btn-sm" href="/` + es + `" target="_blank" rel="noopener" title="Open the live page">View ↗</a>`
	}
	return `<div class="page-header"><h1>` + html.EscapeString(heading) + `</h1>
  <div class="btn-row">
    <div class="seg" role="group" aria-label="Editor format">
      <button type="button" class="seg-btn` + mdSel + `" data-format-btn="markdown" title="Write in Markdown">Markdown</button>
      <button type="button" class="seg-btn` + htmlSel + `" data-format-btn="html" title="Write raw HTML">HTML</button>
    </div>
    <button type="button" class="btn btn-ghost btn-sm" data-action="toggle-preview" title="Toggle preview (Ctrl/⌘+P)">Preview</button>
    <button type="button" class="btn btn-ghost btn-sm" data-action="toggle-distraction" title="Focus mode (Ctrl/⌘+.)">Focus</button>
    ` + viewBtn + `
    <div class="dropdown">
      <button type="button" class="btn btn-ghost btn-sm" data-dropdown-toggle="version-menu" data-load-versions="version-menu">History ▾</button>
      <div class="dropdown-menu" id="version-menu"><div class="version-item muted">Open to load…</div></div>
    </div>
    <button type="button" class="btn btn-accent btn-sm" data-action="save-now" title="Save (Ctrl/⌘+S)">Save</button>
  </div>
</div>
<input type="hidden" data-format-state value="` + ef + `">
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
	cur := html.EscapeString(Version)
	body := `<div class="page-header"><h1>Settings</h1></div>

<div class="card update-card">
  <div class="update-head">
    <div>
      <div class="card-title">Software updates</div>
      <div class="version-line">Running <span class="badge badge-primary">v` + cur + `</span></div>
    </div>
    <button type="button" class="btn btn-primary" data-action="check-updates">Check for updates</button>
  </div>

  <div class="update-result" data-update-result hidden>
    <div class="update-banner" data-update-banner></div>
    <div class="changelog" data-update-changelog></div>

    <!-- Guided, signature-verified apply (ADR-0064): applying swaps the binary
         and is therefore CLI-only and Ed25519-verified. The UI surfaces the
         exact command and a dry-run, never executing a privileged web action. -->
    <div class="apply-guide" data-apply-guide hidden>
      <div class="card-title">Apply this update</div>
      <p class="hint">For safety, applying is signature-verified and runs from the CLI (no web RCE). VayuPress backs up first, verifies the Ed25519 signature, then swaps the binary atomically — your old binary is kept as <code>.bak</code> for rollback.</p>
      <ol class="apply-steps">
        <li><span class="step-n">1</span> Dry-run to preview: <code class="cmd" data-copy="vayupress update apply --dry-run">vayupress update apply --dry-run</code></li>
        <li><span class="step-n">2</span> Apply &amp; restart: <code class="cmd" data-copy="vayupress update apply">vayupress update apply</code></li>
        <li><span class="step-n">3</span> Roll back if needed: <code class="cmd" data-copy="vayupress update rollback">vayupress update rollback</code></li>
      </ol>
      <p class="hint">Requires <code>VAYU_SELFUPDATE_ENABLED=true</code> and a pinned <code>VAYU_RELEASE_PUBKEY</code>. See <code>docs/UPGRADING.md</code>.</p>
    </div>
  </div>

  <p class="hint">Checks <code>/admin/api/updates/check</code> against the official release feed. No telemetry is sent — the check is a single outbound request you initiate.</p>
</div>

<div class="card mt-2">
  <div class="card-title">Update history</div>
  <div data-update-history><p class="muted">Loading recent update activity…</p></div>
</div>

<div class="card mt-2">
  <div class="card-title">About</div>
  <p class="muted">VayuPress Admin v2 — strict-CSP (no <code>eval</code>, no inline styles, per-request nonce), fully vendored UI, zero telemetry. Served under <code>/admin/v2</code> (ADR-0065).</p>
</div>`
	writeV2HTML(w, adminV2Layout(nonce, "Settings", "settings", body))
}

// handleV2SEO renders the native SEO dashboard: artefact status (sitemap, feed,
// robots), per-article SEO readiness, and a one-click regenerate. All numbers
// are computed from the live DB and on-disk cache — no third-party services.
func (a *App) handleV2SEO(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)

	// Artefact freshness — stat the generated cache files.
	artefact := func(name string) (bool, string) {
		fi, err := os.Stat(filepath.Join(config.Cfg.CacheDir, name))
		if err != nil {
			return false, "missing"
		}
		return true, fi.ModTime().UTC().Format("2006-01-02 15:04") + " UTC"
	}
	smOK, smWhen := artefact("sitemap.xml")
	feedOK, feedWhen := artefact("feed.xml")
	robotsOK, robotsWhen := artefact("robots.txt")

	// Per-article readiness — titles present, content depth, thin-content count.
	// Computed in SQL so we read content length without loading every body.
	// Thin ≈ <1500 chars (~300 words). A missing/blank title is counted once.
	total, thin, noTitle := 0, 0, 0
	if rows, err := dbpkg.DB.QueryContext(r.Context(), `SELECT COALESCE(TRIM(title),''), LENGTH(COALESCE(content,'')) FROM articles`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var title string
			var clen int
			if rows.Scan(&title, &clen) != nil {
				continue
			}
			total++
			if title == "" {
				noTitle++
			} else if clen < 1500 {
				thin++
			}
		}
	}
	healthy := total - thin - noTitle
	if healthy < 0 {
		healthy = 0
	}

	statusBadge := func(ok bool, when string) string {
		if ok {
			return `<span class="badge badge-ok">✓ generated</span><span class="muted seo-when"> ` + html.EscapeString(when) + `</span>`
		}
		return `<span class="badge badge-warn">not generated</span>`
	}

	body := `<div class="page-header"><h1>SEO</h1>
  <button type="button" class="btn btn-primary" data-action="seo-regenerate">Regenerate artefacts</button>
</div>
<div class="seo-status" data-seo-status hidden></div>

<div class="grid grid-3">
  <div class="card stat"><span class="stat-value stat-primary">` + strconv.Itoa(healthy) + `</span><span class="stat-label">SEO-healthy posts</span></div>
  <div class="card stat"><span class="stat-value stat-accent">` + strconv.Itoa(thin) + `</span><span class="stat-label">Thin (&lt;300 words)</span></div>
  <div class="card stat"><span class="stat-value">` + strconv.Itoa(noTitle) + `</span><span class="stat-label">Missing title</span></div>
</div>

<div class="card mt-2">
  <div class="card-title">Generated artefacts</div>
  <table class="table">
    <thead><tr><th>Artefact</th><th>Status</th><th>Path</th></tr></thead>
    <tbody>
      <tr><td>Sitemap</td><td>` + statusBadge(smOK, smWhen) + `</td><td><code>/sitemap.xml</code></td></tr>
      <tr><td>RSS feed</td><td>` + statusBadge(feedOK, feedWhen) + `</td><td><code>/feed.xml</code></td></tr>
      <tr><td>robots.txt</td><td>` + statusBadge(robotsOK, robotsWhen) + `</td><td><code>/robots.txt</code></td></tr>
    </tbody>
  </table>
  <p class="hint">Regenerating rebuilds the sitemap (up to 50k URLs), the RSS feed (latest 50), and robots.txt from the live database, then writes them to the cache served at the public paths above.</p>
</div>

<div class="card mt-2">
  <div class="card-title">On-page SEO (automatic)</div>
  <p class="muted">Every article page ships these with no configuration:</p>
  <ul class="seo-checklist">
    <li>✓ Semantic <code>&lt;title&gt;</code> and meta description from the post</li>
    <li>✓ OpenGraph + Twitter Card tags for rich link previews</li>
    <li>✓ JSON-LD <code>Article</code> structured data (author, dates, headline)</li>
    <li>✓ Canonical URL + reading-time and tag metadata</li>
    <li>✓ Zero third-party scripts — fast Core Web Vitals by construction</li>
  </ul>
</div>`
	writeV2HTML(w, adminV2Layout(nonce, "SEO", "seo", body))
}

// handleSEORegenerate rebuilds the SEO artefacts on demand. It is a
// CSRF-protected, mode-gated governed write (it only writes to the cache dir).
func (a *App) handleSEORegenerate(w http.ResponseWriter, r *http.Request) {
	cur := mode.Global.Current()
	if cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		writeJSON(w, r, http.StatusServiceUnavailable, map[string]string{"error": "cannot regenerate in " + string(cur) + " mode"})
		return
	}
	generateSitemap()
	generateRSS()
	generateRobots()
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"status":      "ok",
		"regenerated": []string{"sitemap.xml", "feed.xml", "robots.txt"},
	})
}

func (a *App) handleV2Login(w http.ResponseWriter, r *http.Request) {
	// If already authenticated, skip the form.
	if auth.HasValidAPIKey(r) {
		http.Redirect(w, r, "/admin/v2", http.StatusSeeOther)
		return
	}
	if a.sessions != nil {
		if token := auth.SessionTokenFromRequest(r); token != "" {
			if _, err := a.sessions.Validate(r.Context(), token); err == nil {
				http.Redirect(w, r, "/admin/v2", http.StatusSeeOther)
				return
			}
		}
	}
	a.renderLoginPage(w, r, "")
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
