// Package render handles article template rendering, cache management, CSS assets, and CSP nonces.
package render

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
	"github.com/microcosm-cc/bluemonday"
)

// ── Layout types ──────────────────────────────────────────────────────────────

// ArticleLayoutType selects the article template variant.
type ArticleLayoutType string

const (
	ArticleLayoutDefault ArticleLayoutType = "default"
	ArticleLayoutMinimal ArticleLayoutType = "minimal"
	ArticleLayoutWide    ArticleLayoutType = "wide"
)

// ── CSP nonce (ADR-0036) ──────────────────────────────────────────────────────

// ctxKeyCSPNonce is the context key for the per-request CSP nonce.
type ctxKeyCSPNonce struct{}

// CSPNonce returns the per-request CSP nonce stored in the request context.
func CSPNonce(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKeyCSPNonce{}).(string); ok {
		return v
	}
	return ""
}

// GenerateCSPNonce creates a random base64 nonce for a CSP script-src.
func GenerateCSPNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("ts%x", time.Now().UnixNano())
	}
	return base64.StdEncoding.EncodeToString(b)
}

// WithCSPNonce returns a new context with the nonce embedded.
func WithCSPNonce(ctx context.Context, nonce string) context.Context {
	return context.WithValue(ctx, ctxKeyCSPNonce{}, nonce)
}

// ── Package-level state ───────────────────────────────────────────────────────

var (
	policy    *bluemonday.Policy
	htmlTagRe = regexp.MustCompile(`<[^>]+>`)
	cssHashes struct{ ArticleCSS, AdminCSS, HighContrastCSS, CustomCSS string }
)

// Init initializes the HTML sanitizer, compiles the template, writes CSS assets, and warms the cache.
func Init(staticDir string) {
	policy = bluemonday.UGCPolicy()
	WriteCSSAssets(staticDir)
}

// ── CSS assets ────────────────────────────────────────────────────────────────

// WriteCSSAssets writes minified CSS files and computes their content hashes.
func WriteCSSAssets(staticDir string) {
	cssDir := filepath.Join(staticDir, "css")
	if err := os.MkdirAll(cssDir, 0755); err != nil {
		return
	}
	type asset struct {
		name, content string
		hash          *string
	}
	for _, a := range []asset{
		{"article.css", articleCSSMin, &cssHashes.ArticleCSS},
		{"admin.css", adminCSSMin, &cssHashes.AdminCSS},
		{"high-contrast.css", hcCSSMin, &cssHashes.HighContrastCSS},
		{"custom.css", customCSSMin, &cssHashes.CustomCSS},
	} {
		if err := os.WriteFile(filepath.Join(cssDir, a.name), []byte(a.content), 0644); err != nil {
			continue
		}
		sum := sha256.Sum256([]byte(a.content))
		*a.hash = hex.EncodeToString(sum[:])
	}
}

// CSSLink returns a versioned <link> tag for a CSS file.
func CSSLink(filename, hash string) template.HTML {
	ver := hash
	if len(ver) > 8 {
		ver = ver[:8]
	}
	return template.HTML(fmt.Sprintf(`<link rel="stylesheet" href="/static/css/%s?v=%s">`, filename, ver))
}

// picoVersion is the vendored Pico CSS release served from /static/css. It is a
// local copy (no third-party origin) so the strict CSP and sovereignty posture
// hold. Bump this when the vendored file in static/css/pico.min.css changes.
const picoVersion = "2.1.1"

// PicoCSSLink returns the <link> for the vendored Pico CSS base theme used by
// the public site. It is loaded before the VayuPress overrides so brand styles
// win the cascade.
func PicoCSSLink() template.HTML {
	return template.HTML(fmt.Sprintf(`<link rel="stylesheet" href="/static/css/pico.min.css?v=%s">`, picoVersion))
}

// ArticleCSSLink returns the versioned <link> for article.css.
func ArticleCSSLink() template.HTML { return CSSLink("article.css", cssHashes.ArticleCSS) }

// AdminCSSLink returns the versioned <link> for admin.css.
func AdminCSSLink() template.HTML { return CSSLink("admin.css", cssHashes.AdminCSS) }

// HighContrastCSSLink returns the versioned <link> for high-contrast.css.
func HighContrastCSSLink() template.HTML {
	return CSSLink("high-contrast.css", cssHashes.HighContrastCSS)
}

// CustomCSSLink returns the versioned <link> for the VayuPress brand overrides (custom.css).
func CustomCSSLink() template.HTML { return CSSLink("custom.css", cssHashes.CustomCSS) }

// ── Dynamic site settings ─────────────────────────────────────────────────────

// SiteSettings holds operator-configurable values that are injected into every
// public page render. The zero value is safe and falls back to Pico defaults.
type SiteSettings struct {
	Name         string // site brand name
	Tagline      string // hero headline
	Description  string // meta description
	Author       string // article author
	PrimaryLight string // --pico-primary for light mode (hex)
	PrimaryDark  string // --pico-primary for dark mode (hex)
	AccentLight  string // --vayu-accent for light mode (hex)
	AccentDark   string // --vayu-accent for dark mode (hex)
	CustomCSS    string // operator-supplied CSS, served via /theme.css

	// Declarative <head> capabilities (validated on write, escaped on render).
	// These replace raw head HTML so no arbitrary markup — meta-refresh
	// redirects, external beacons, <base> hijacks — can reach the page.
	Keywords     string // meta keywords
	ThemeColor   string // meta theme-color (hex)
	Robots       string // meta robots directive (allowlisted)
	VerifyGoogle string // google-site-verification token
	VerifyBing   string // msvalidate.01 token
}

var (
	activeSettingsMu sync.RWMutex
	activeSettings   SiteSettings
)

// SetActiveSettings replaces the global active site settings. Thread-safe.
func SetActiveSettings(s SiteSettings) {
	activeSettingsMu.Lock()
	activeSettings = s
	activeSettingsMu.Unlock()
}

// getActiveSettings returns a copy of the current active settings.
func getActiveSettings() SiteSettings {
	activeSettingsMu.RLock()
	s := activeSettings
	activeSettingsMu.RUnlock()
	return s
}

// ThemeCSS returns the operator-configurable CSS as a plain stylesheet body
// (NO <style> wrapper). It overrides Pico CSS variables with the operator palette
// and appends any operator-supplied custom CSS.
//
// This is served from the same origin at /theme.css rather than inlined, so it
// satisfies the strict `style-src 'self'` CSP (ADR-0036) — inline <style> blocks
// are blocked by policy. Callers reference it via ThemeCSSLink().
func ThemeCSS() string {
	s := getActiveSettings()
	var sb strings.Builder
	if s.PrimaryLight != "" || s.AccentLight != "" {
		sb.WriteString(":root,[data-theme=\"light\"]{")
		if s.PrimaryLight != "" {
			sb.WriteString("--pico-primary:" + s.PrimaryLight + ";--pico-a-color:" + s.PrimaryLight + ";")
		}
		if s.AccentLight != "" {
			sb.WriteString("--vayu-accent:" + s.AccentLight + ";")
		}
		sb.WriteString("}")
	}
	if s.PrimaryDark != "" || s.AccentDark != "" {
		sb.WriteString("[data-theme=\"dark\"]{")
		if s.PrimaryDark != "" {
			sb.WriteString("--pico-primary:" + s.PrimaryDark + ";--pico-a-color:" + s.PrimaryDark + ";")
		}
		if s.AccentDark != "" {
			sb.WriteString("--vayu-accent:" + s.AccentDark + ";")
		}
		sb.WriteString("}")
	}
	if s.CustomCSS != "" {
		sb.WriteString(s.CustomCSS)
	}
	return sb.String()
}

// ThemeCSSETag returns a stable content hash of the current dynamic theme CSS,
// suitable for an HTTP ETag so browsers revalidate when the palette changes.
func ThemeCSSETag() string {
	sum := sha256.Sum256([]byte(ThemeCSS()))
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// ThemeCSSLink returns the <link> for the dynamic per-site theme stylesheet.
// The URL is stable; the served file is sent with an ETag + no-cache so palette
// changes propagate even to disk-cached HTML pages on the next request.
func ThemeCSSLink() template.HTML {
	return template.HTML(`<link rel="stylesheet" href="/theme.css">`)
}

// headMetaHTML renders the declarative <head> capabilities to a safe, escaped
// allowlist of <meta> tags. Values are validated on write (hex/token/allowlist)
// and HTML-escaped here — defense in depth. No arbitrary operator markup ever
// reaches the document head, so meta-refresh redirects, external beacons, and
// <base> hijacks are structurally impossible.
func headMetaHTML(s SiteSettings) template.HTML {
	var sb strings.Builder
	esc := template.HTMLEscapeString
	writeMeta := func(name, content string) {
		if content == "" {
			return
		}
		sb.WriteString(`<meta name="` + name + `" content="` + esc(content) + `">`)
	}
	writeMeta("keywords", s.Keywords)
	writeMeta("theme-color", s.ThemeColor)
	writeMeta("robots", s.Robots)
	writeMeta("google-site-verification", s.VerifyGoogle)
	writeMeta("msvalidate.01", s.VerifyBing)
	return template.HTML(sb.String())
}

// ── Template ──────────────────────────────────────────────────────────────────

type articlePage struct {
	db.Article
	Domain              string
	Version             string
	Layout              ArticleLayoutType
	PicoCSSLink         template.HTML
	CustomCSSLink       template.HTML
	ArticleCSSLink      template.HTML
	HighContrastCSSLink template.HTML
	ThemeCSSLink        template.HTML
	HeadMeta            template.HTML
	SiteName            string
	Author              string
}

var articleTmpl = template.Must(template.New("article").Funcs(template.FuncMap{
	"trunc": func(s string, n int) string {
		s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, "")
		s = strings.TrimSpace(s)
		if len(s) > n {
			return s[:n] + "..."
		}
		return s
	},
	"safeHTML": func(s string) template.HTML { return template.HTML(s) },
	"jsonAttr": func(s string) string {
		s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, "")
		s = strings.TrimSpace(s)
		s = strings.ReplaceAll(s, `"`, `\"`)
		s = strings.ReplaceAll(s, "\n", " ")
		if len(s) > 300 {
			s = s[:300]
		}
		return s
	},
	"readTime": func(s string) int {
		text := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, "")
		words := len(strings.Fields(text))
		if words < 200 {
			return 1
		}
		return (words + 199) / 200
	},
	"isoDate":   func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
	"shortDate": func(t time.Time) string { return t.UTC().Format("2006-01-02") },
	"humanDate": func(t time.Time) string { return t.Format("2 January 2006") },
}).Parse(`<!DOCTYPE html><html lang="en" data-theme="dark"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} — {{.Domain}}</title>
<meta name="description" content="{{trunc .Content 160}}">
<meta name="generator" content="VayuPress {{.Version}}">
<link rel="canonical" href="https://{{.Domain}}/{{.Slug}}">
<meta property="og:type" content="article"><meta property="og:title" content="{{.Title}}">
<meta property="og:url" content="https://{{.Domain}}/{{.Slug}}">
<meta property="og:site_name" content="{{.Domain}}"><meta property="og:locale" content="en">
<meta property="og:description" content="{{trunc .Content 200}}">
<meta property="article:published_time" content="{{.CreatedAt | isoDate}}">
<meta property="article:modified_time" content="{{.UpdatedAt | isoDate}}">
{{range .Tags}}<meta property="article:tag" content="{{.}}">{{end}}
<meta name="twitter:card" content="summary"><meta name="twitter:title" content="{{.Title}}">
<meta name="twitter:description" content="{{trunc .Content 200}}">
<script type="application/ld+json">{"@context":"https://schema.org","@type":"BlogPosting","mainEntityOfPage":{"@type":"WebPage","@id":"https://{{.Domain}}/{{.Slug}}"},"headline":"{{.Title | jsonAttr}}","description":"{{.Content | jsonAttr}}","datePublished":"{{.CreatedAt | isoDate}}","dateModified":"{{.UpdatedAt | isoDate}}","inLanguage":"en","author":{"@type":"Person","name":"Ankush Choudhary Johal","url":"https://{{.Domain}}/about"},"publisher":{"@type":"Organization","name":"VayuPress","url":"https://{{.Domain}}"}}</script>
{{.PicoCSSLink}}{{.CustomCSSLink}}{{.ArticleCSSLink}}{{.HighContrastCSSLink}}{{.ThemeCSSLink}}{{.HeadMeta}}
<link rel="icon" type="image/png" href="/static/favicon-dark.png" media="(prefers-color-scheme: light)">
<link rel="icon" type="image/png" href="/static/favicon-light.png" media="(prefers-color-scheme: dark)">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head><body>
<a href="#main-content" class="skip-link">Skip to main content</a>
<div class="container">
<nav class="vayu-nav" aria-label="Primary">
  <a href="/" class="vayu-nav-brand"><img src="/static/favicon-light.png" alt="" width="24" height="24">{{if .SiteName}}{{.SiteName}}{{else}}VayuPress{{end}}</a>
  <div class="vayu-nav-links">
    <a href="/">Home</a>
    <a href="/feed.xml">Feed</a>
    <a href="/admin">Console</a>
  </div>
</nav>
<main id="main-content">
<article class="vayu-prose" itemscope itemtype="https://schema.org/BlogPosting">
<header class="vayu-article-header">
<h1 itemprop="headline">{{.Title}}</h1>
<div class="vayu-article-meta">
  <time itemprop="datePublished" datetime="{{.CreatedAt | shortDate}}">{{.CreatedAt | humanDate}}</time>
  <span>· {{.Content | readTime}} min read</span>
  {{if .Tags}}<span aria-label="Tags">{{range .Tags}}<a class="vayu-tag" href="/tags/{{.}}" rel="tag">#{{.}}</a> {{end}}</span>{{end}}
</div>
</header>
<div class="content" itemprop="articleBody">{{.Content | safeHTML}}</div>
</article>
<footer class="vayu-footer">
  <span>By <strong>{{if .Author}}{{.Author}}{{else}}Ankush Choudhary Johal{{end}}</strong> · Powered by <a href="https://vayupress.com">VayuPress</a></span>
  <span class="vayu-footer-badge">runtime · governed</span>
</footer>
</main></div></body></html>`))

// HomeArticle is a single entry rendered on the public homepage index.
type HomeArticle struct {
	Title     string
	Slug      string
	Excerpt   string
	Tags      []string
	CreatedAt time.Time
}

type homePage struct {
	Domain              string
	Version             string
	PicoCSSLink         template.HTML
	CustomCSSLink       template.HTML
	ArticleCSSLink      template.HTML
	HighContrastCSSLink template.HTML
	ThemeCSSLink        template.HTML
	HeadMeta            template.HTML
	SiteName            string
	Tagline             string
	Description         string
	Articles            []HomeArticle
	TotalCount          int
}

var homeFuncs = template.FuncMap{
	"humanDate": func(t time.Time) string { return t.Format("2 January 2006") },
	"shortDate": func(t time.Time) string { return t.UTC().Format("2006-01-02") },
}

var homeTmpl = template.Must(template.New("home").Funcs(homeFuncs).Parse(`<!DOCTYPE html><html lang="en" data-theme="dark"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Domain}} — Sovereign Publishing Runtime</title>
<meta name="description" content="VayuPress — a governed, adaptive publishing runtime. Durable by design, observable end to end.">
<meta name="generator" content="VayuPress {{.Version}}">
<link rel="canonical" href="https://{{.Domain}}/">
<link rel="alternate" type="application/rss+xml" title="{{.Domain}} feed" href="/feed.xml">
<meta property="og:type" content="website"><meta property="og:title" content="{{.Domain}}">
<meta property="og:url" content="https://{{.Domain}}/">
{{.PicoCSSLink}}{{.CustomCSSLink}}{{.ArticleCSSLink}}{{.HighContrastCSSLink}}{{.ThemeCSSLink}}{{.HeadMeta}}
<link rel="icon" type="image/png" href="/static/favicon-dark.png" media="(prefers-color-scheme: light)">
<link rel="icon" type="image/png" href="/static/favicon-light.png" media="(prefers-color-scheme: dark)">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head><body>
<a href="#main-content" class="skip-link">Skip to main content</a>
<div class="container">
<nav class="vayu-nav" aria-label="Primary">
  <a href="/" class="vayu-nav-brand"><img src="/static/favicon-light.png" alt="" width="24" height="24">{{if .SiteName}}{{.SiteName}}{{else}}VayuPress{{end}}</a>
  <div class="vayu-nav-links">
    <a href="/">Home</a>
    <a href="/feed.xml">Feed</a>
    <a href="/admin">Console</a>
  </div>
  <span class="vayu-nav-status"><span class="vayu-mode-dot"></span>runtime · normal</span>
</nav>
<main id="main-content">
<section class="vayu-hero">
  <span class="vayu-hero-eyebrow">Sovereign Publishing Runtime</span>
  <h1>{{if .Tagline}}{{.Tagline}}{{else}}Publishing as an<br>adaptive runtime.{{end}}</h1>
  <p class="vayu-hero-tagline">{{if .Description}}{{.Description}}{{else}}Durable by design, observable end to end. Every write is queued, signed, and governed by a live operational state machine — not a CMS, a control plane.{{end}}</p>
  <div class="vayu-stats">
    <div><span class="vayu-stat-val">{{.TotalCount}}</span><span class="vayu-stat-label">Published</span></div>
    <div><span class="vayu-stat-val">Ed25519</span><span class="vayu-stat-label">Signed</span></div>
    <div><span class="vayu-stat-val">WAL</span><span class="vayu-stat-label">Durable</span></div>
    <div><span class="vayu-stat-val">v{{.Version}}</span><span class="vayu-stat-label">Runtime</span></div>
  </div>
</section>
<div class="vayu-section-label">Latest writing</div>
{{if .Articles}}<div class="vayu-post-list">
{{range .Articles}}<a class="vayu-post-card" href="/{{.Slug}}">
  <div class="vayu-post-meta"><time datetime="{{.CreatedAt | shortDate}}">{{.CreatedAt | humanDate}}</time>{{if .Tags}}<span>·</span><span>{{range $i, $t := .Tags}}{{if $i}} · {{end}}#{{$t}}{{end}}</span>{{end}}</div>
  <div class="vayu-post-title">{{.Title}}</div>
  {{if .Excerpt}}<div class="vayu-post-excerpt">{{.Excerpt}}</div>{{end}}
  <span class="vayu-post-arrow" aria-hidden="true">→</span>
</a>{{end}}
</div>{{else}}<div class="vayu-empty">No articles published yet. The runtime is live and waiting.</div>{{end}}
<footer class="vayu-footer">
  <div class="vayu-footer-brand"><img src="/static/favicon-light.png" alt="" width="20" height="20">VayuPress</div>
  <span class="vayu-footer-badge">runtime · governed</span>
</footer>
</main>
</div></body></html>`))

var notFoundTmpl = template.Must(template.New("404").Parse(`<!DOCTYPE html><html lang="en" data-theme="dark"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>404 — {{.Domain}}</title><meta name="robots" content="noindex">
<meta name="generator" content="VayuPress {{.Version}}">
{{.PicoCSSLink}}{{.CustomCSSLink}}{{.ArticleCSSLink}}{{.HighContrastCSSLink}}{{.ThemeCSSLink}}{{.HeadMeta}}
<link rel="icon" type="image/png" href="/static/favicon-dark.png" media="(prefers-color-scheme: light)">
<link rel="icon" type="image/png" href="/static/favicon-light.png" media="(prefers-color-scheme: dark)">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head><body>
<div class="container">
<nav class="vayu-nav" aria-label="Primary">
  <a href="/" class="vayu-nav-brand"><img src="/static/favicon-light.png" alt="" width="24" height="24">{{if .SiteName}}{{.SiteName}}{{else}}VayuPress{{end}}</a>
  <div class="vayu-nav-links"><a href="/">Home</a><a href="/feed.xml">Feed</a><a href="/admin">Console</a></div>
</nav>
<main id="main-content"><div class="vayu-err">
  <div class="vayu-err-code">404</div>
  <div class="vayu-err-msg">This route resolves to nothing.</div>
  <div class="vayu-err-sub">The requested resource is not in the published set.</div>
  <a href="/">← Return to index</a>
</div></main>
</div></body></html>`))

// RenderHome renders the public homepage index from recent articles.
func RenderHome(domain, version string, articles []HomeArticle, totalCount int) (string, error) {
	var buf strings.Builder
	s := getActiveSettings()
	err := homeTmpl.Execute(&buf, homePage{
		Domain:              domain,
		Version:             version,
		PicoCSSLink:         PicoCSSLink(),
		CustomCSSLink:       CustomCSSLink(),
		ArticleCSSLink:      ArticleCSSLink(),
		HighContrastCSSLink: HighContrastCSSLink(),
		ThemeCSSLink:        ThemeCSSLink(),
		HeadMeta:            headMetaHTML(s),
		SiteName:            s.Name,
		Tagline:             s.Tagline,
		Description:         s.Description,
		Articles:            articles,
		TotalCount:          totalCount,
	})
	return buf.String(), err
}

// Render404 renders the branded not-found page.
func Render404(domain, version string) string {
	var buf strings.Builder
	s := getActiveSettings()
	_ = notFoundTmpl.Execute(&buf, homePage{
		Domain:              domain,
		Version:             version,
		PicoCSSLink:         PicoCSSLink(),
		CustomCSSLink:       CustomCSSLink(),
		ArticleCSSLink:      ArticleCSSLink(),
		HighContrastCSSLink: HighContrastCSSLink(),
		ThemeCSSLink:        ThemeCSSLink(),
		HeadMeta:            headMetaHTML(s),
		SiteName:            s.Name,
	})
	return buf.String()
}

// Version is set by main after boot to embed in rendered pages.
var Version string

// RenderArticle renders an article with the default layout.
func RenderArticle(a db.Article) (string, error) {
	return RenderArticleWithLayout(a, ArticleLayoutDefault)
}

// RenderArticleWithLayout sanitizes content, executes the template, and records render latency.
func RenderArticleWithLayout(a db.Article, layout ArticleLayoutType) (string, error) {
	a.Content = policy.Sanitize(a.Content)
	start := time.Now()
	var buf strings.Builder
	s := getActiveSettings()
	data := articlePage{
		Article:             a,
		Domain:              config.Cfg.Domain,
		Version:             Version,
		Layout:              layout,
		PicoCSSLink:         PicoCSSLink(),
		CustomCSSLink:       CustomCSSLink(),
		ArticleCSSLink:      ArticleCSSLink(),
		HighContrastCSSLink: HighContrastCSSLink(),
		ThemeCSSLink:        ThemeCSSLink(),
		HeadMeta:            headMetaHTML(s),
		SiteName:            s.Name,
		Author:              s.Author,
	}
	if err := articleTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template: %w", err)
	}
	metrics.RenderLatency.Record(time.Since(start))
	return buf.String(), nil
}

// DetectLayout selects the layout for an article based on admin query param or content tags.
func DetectLayout(a db.Article, r *http.Request, isAdmin bool) ArticleLayoutType {
	if isAdmin {
		switch ArticleLayoutType(r.URL.Query().Get("layout")) {
		case ArticleLayoutMinimal:
			return ArticleLayoutMinimal
		case ArticleLayoutWide:
			return ArticleLayoutWide
		}
	}
	for _, tag := range a.Tags {
		switch tag {
		case "layout:minimal":
			return ArticleLayoutMinimal
		case "layout:wide":
			return ArticleLayoutWide
		}
	}
	return ArticleLayoutDefault
}

// ── Cache helpers ─────────────────────────────────────────────────────────────

// CacheWrite writes content to a path under the configured cache directory.
func CacheWrite(relPath, content string) error {
	full := filepath.Join(config.Cfg.CacheDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	oldSize := int64(0)
	if fi, err := os.Stat(full); err == nil {
		oldSize = fi.Size()
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		return err
	}
	db.UpdateStorageDelta(int64(len(content)) - oldSize)
	return nil
}

// CachePurgeAll removes every rendered HTML fragment (home, all posts, all tag
// pages) so they regenerate with current site settings. Used when a global
// change — e.g. a theme/identity update — affects the markup of every page.
func CachePurgeAll() {
	os.Remove(filepath.Join(config.Cfg.CacheDir, "home", "index.html"))
	for _, sub := range []string{"posts", "tags"} {
		dir := filepath.Join(config.Cfg.CacheDir, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
				continue
			}
			if fi, ferr := e.Info(); ferr == nil {
				db.UpdateStorageDelta(-fi.Size())
			}
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// CachePurge removes the cached file for an article and its associated tag pages.
func CachePurge(slug string, tags []string, generateSitemap, generateRSS, generateRobots func()) {
	postFile := filepath.Join(config.Cfg.CacheDir, "posts", slug+".html")
	if fi, err := os.Stat(postFile); err == nil {
		db.UpdateStorageDelta(-fi.Size())
	}
	os.Remove(postFile)
	os.Remove(filepath.Join(config.Cfg.CacheDir, "home", "index.html"))
	for _, t := range tags {
		if t != "" {
			tagFile := filepath.Join(config.Cfg.CacheDir, "tags", t+".html")
			if fi, err := os.Stat(tagFile); err == nil {
				db.UpdateStorageDelta(-fi.Size())
			}
			os.Remove(tagFile)
		}
	}
	if generateSitemap != nil {
		go generateSitemap()
	}
	if generateRSS != nil {
		go generateRSS()
	}
	if generateRobots != nil {
		go generateRobots()
	}
}

// WarmCache pre-renders the 1000 most recently updated articles that are not already cached.
func WarmCache(splitTags func(string) []string) {
	rows, err := db.DB.Query(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles ORDER BY updated_at DESC LIMIT 1000`)
	if err != nil {
		return
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var a db.Article
		var tagsStr string
		rows.Scan(&a.ID, &a.Title, &a.Slug, &a.Content, &tagsStr, &a.CreatedAt, &a.UpdatedAt)
		a.Tags = splitTags(tagsStr)
		dest := filepath.Join(config.Cfg.CacheDir, "posts", a.Slug+".html")
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		html, err := RenderArticle(a)
		if err != nil {
			continue
		}
		CacheWrite(filepath.Join("posts", a.Slug+".html"), html)
		count++
	}
	logging.LogInfo("cache-warm", fmt.Sprintf("pre-rendered %d articles", count))
}

// StripHTML removes all HTML tags from s and returns plain text.
func StripHTML(s string) string {
	return htmlTagRe.ReplaceAllString(s, "")
}

// SanitizeHTML runs the bluemonday UGC policy over s.
func SanitizeHTML(s string) string {
	if policy == nil {
		return s
	}
	return policy.Sanitize(s)
}

// ── Minified CSS constants ────────────────────────────────────────────────────

const articleCSSMin = `:root{--bg:#080b10;--surface:#0f1420;--surface2:#141c2e;--border:#1e2840;--border2:#263354;--text:#e2e8f0;--muted:#64748b;--accent:#6366f1;--accent2:#818cf8;--hi:#a5b4fc;--green:#22c55e;--max-w:740px;--font:'Inter',system-ui,sans-serif;--mono:'IBM Plex Mono','JetBrains Mono',monospace;--radius:6px;--radius2:10px;--sp1:4px;--sp2:8px;--sp3:16px;--sp4:24px;--sp5:36px;--sp6:56px}@media(prefers-color-scheme:light){:root{--bg:#fafafa;--surface:#fff;--surface2:#f1f5f9;--border:#e2e8f0;--border2:#cbd5e1;--text:#0f172a;--muted:#64748b;--accent:#4f46e5;--accent2:#6366f1;--hi:#4338ca}}@media(prefers-reduced-motion:reduce){*,*::before,*::after{transition:none!important;animation:none!important}}*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}html{scroll-behavior:smooth}.skip-link{position:absolute;top:-40px;left:0;z-index:9999;background:var(--accent);color:#fff;padding:var(--sp2) var(--sp3);font:500 13px/1.4 var(--font);text-decoration:none;transition:top .2s}.skip-link:focus{top:0;outline:3px solid var(--hi);outline-offset:2px}body{background:var(--bg);color:var(--text);font:400 18px/1.72 var(--font);padding:var(--sp5) var(--sp3)}body::before{content:'';position:fixed;top:0;left:0;right:0;height:1px;background:linear-gradient(90deg,transparent,var(--accent),var(--accent2),transparent);opacity:.6;z-index:100}.container{max-width:var(--max-w);margin:0 auto}header{padding-bottom:var(--sp5);margin-bottom:var(--sp5);position:relative}.site-nav{display:flex;align-items:center;justify-content:space-between;margin-bottom:var(--sp5);padding-bottom:var(--sp4);border-bottom:1px solid var(--border)}.site-nav-brand{display:flex;align-items:center;gap:var(--sp2);font:700 16px var(--font);color:var(--text);text-decoration:none}.site-nav-brand-icon{color:var(--accent);font-size:20px}.site-nav-links{display:flex;gap:var(--sp4)}.site-nav-links a{color:var(--muted);font-size:14px;text-decoration:none;transition:color .15s}.site-nav-links a:hover{color:var(--text)}.mode-indicator{font-size:11px;color:var(--green);font-family:var(--mono)}.mode-dot{display:inline-block;width:6px;height:6px;background:var(--green);border-radius:50%;margin-right:5px;animation:pulse 2s infinite}@keyframes pulse{0%,100%{opacity:1}50%{opacity:.4}}h1{font:700 2.2rem/1.18 var(--font);margin-bottom:var(--sp3);letter-spacing:-.6px;background:linear-gradient(135deg,var(--text) 60%,var(--hi));-webkit-background-clip:text;-webkit-text-fill-color:transparent;background-clip:text}.meta{color:var(--muted);font-size:13px;display:flex;flex-wrap:wrap;align-items:center;gap:var(--sp3);margin-bottom:var(--sp4)}.meta-separator{opacity:.3}.tags{display:flex;flex-wrap:wrap;gap:6px}.tags a{display:inline-block;padding:3px 10px;background:rgba(99,102,241,.1);border:1px solid rgba(99,102,241,.25);border-radius:20px;font-size:12px;color:var(--accent2);text-decoration:none;transition:all .15s}.tags a:hover{background:rgba(99,102,241,.2);border-color:var(--accent2)}.tags a:focus-visible{outline:2px solid var(--accent);outline-offset:2px}hr.content-divider{border:none;border-top:1px solid var(--border);margin:var(--sp4) 0;background:none}.content{margin-top:var(--sp5);line-height:1.8}.content>*+*{margin-top:var(--sp4)}.content h2{font:700 1.4rem/1.25 var(--font);margin:var(--sp6) 0 var(--sp3);color:var(--text);letter-spacing:-.3px}.content h3{font:600 1.15rem/1.3 var(--font);margin:var(--sp5) 0 var(--sp2);color:var(--text)}.content h2::before{content:'';display:block;width:32px;height:2px;background:linear-gradient(90deg,var(--accent),var(--accent2));border-radius:1px;margin-bottom:var(--sp2)}.content pre{background:var(--surface);border:1px solid var(--border2);border-radius:var(--radius2);padding:var(--sp3) var(--sp4);overflow-x:auto;font:400 14px/1.6 var(--mono);margin:var(--sp4) 0;position:relative}.content pre::before{content:attr(data-lang);position:absolute;top:10px;right:14px;font-size:11px;color:var(--muted);font-family:var(--mono)}.content code{background:rgba(99,102,241,.1);padding:2px 7px;border-radius:4px;font:400 14px var(--mono);color:var(--accent2)}.content pre code{background:none;padding:0;color:var(--text)}.content blockquote{border-left:3px solid var(--accent);padding:var(--sp3) var(--sp4);color:var(--muted);margin:var(--sp4) 0;background:var(--surface);border-radius:0 var(--radius) var(--radius) 0;font-style:italic}.content p{color:var(--text)}.content a{color:var(--accent2);text-decoration:none;border-bottom:1px solid rgba(99,102,241,.3);transition:border-color .15s}.content a:hover{border-color:var(--accent2)}.content ul,.content ol{padding-left:var(--sp4)}.content li{margin:var(--sp1) 0}.content img{max-width:100%;border-radius:var(--radius2);border:1px solid var(--border)}footer{margin-top:var(--sp6);padding:var(--sp5) 0 var(--sp4);border-top:1px solid var(--border);font-size:13px;color:var(--muted);display:flex;align-items:center;justify-content:space-between;gap:var(--sp3);flex-wrap:wrap}.footer-brand{display:flex;align-items:center;gap:6px;font-weight:500;color:var(--muted)}.footer-badge{font-size:10px;padding:2px 7px;background:rgba(34,197,94,.1);border:1px solid rgba(34,197,94,.2);border-radius:10px;color:var(--green)}.reading-progress{position:fixed;top:0;left:0;width:0;height:2px;background:linear-gradient(90deg,var(--accent),var(--accent2));z-index:200;transition:width .1s}a:focus-visible{outline:2px solid var(--accent);outline-offset:2px;border-radius:2px}@media(max-width:480px){body{padding:var(--sp3) var(--sp2)}h1{font-size:1.7rem;-webkit-text-fill-color:var(--text)}.site-nav-links{display:none}.meta{gap:var(--sp2)}}.home-hero{padding:var(--sp4) 0 var(--sp5);border-bottom:1px solid var(--border);margin-bottom:var(--sp5);position:relative}.home-eyebrow{display:inline-flex;align-items:center;gap:8px;font:600 12px var(--mono);letter-spacing:.12em;text-transform:uppercase;color:var(--accent2);margin-bottom:var(--sp3)}.home-eyebrow::before{content:'';width:7px;height:7px;border-radius:50%;background:var(--green);box-shadow:0 0 12px var(--green);animation:pulse 2.4s infinite}.home-title{font:800 clamp(2.3rem,6vw,3.7rem)/1.05 var(--font);letter-spacing:-1.5px;margin-bottom:var(--sp3);background:linear-gradient(135deg,var(--text) 45%,var(--hi));-webkit-background-clip:text;-webkit-text-fill-color:transparent;background-clip:text}.home-tagline{font:400 1.12rem/1.6 var(--font);color:var(--muted);max-width:600px;margin-bottom:var(--sp4)}.home-stats{display:flex;gap:var(--sp5);flex-wrap:wrap}.home-stat{display:flex;flex-direction:column;gap:3px}.home-stat-val{font:700 1.35rem/1 var(--font);color:var(--text);letter-spacing:-.4px}.home-stat-label{font:400 10px var(--mono);color:var(--muted);text-transform:uppercase;letter-spacing:.1em}.section-label{font:600 11px var(--mono);letter-spacing:.14em;text-transform:uppercase;color:var(--muted);margin-bottom:var(--sp3);display:flex;align-items:center;gap:var(--sp3)}.section-label::after{content:'';flex:1;height:1px;background:var(--border)}.post-list{display:flex;flex-direction:column;gap:3px}.post-card{display:block;padding:var(--sp3);border:1px solid transparent;border-radius:var(--radius2);text-decoration:none;transition:border-color .15s,background .15s,transform .15s;position:relative}.post-card:hover{border-color:var(--border2);background:var(--surface);transform:translateX(3px)}.post-card-meta{display:flex;align-items:center;gap:var(--sp2);font:400 12px var(--mono);color:var(--muted);margin-bottom:6px}.post-card-dot{width:3px;height:3px;border-radius:50%;background:var(--muted)}.post-card-title{font:700 1.28rem/1.3 var(--font);color:var(--text);letter-spacing:-.4px;margin-bottom:6px;transition:color .15s}.post-card:hover .post-card-title{color:var(--hi)}.post-card-excerpt{font:400 14px/1.6 var(--font);color:var(--muted);margin-bottom:9px;max-width:680px}.post-card-arrow{position:absolute;right:var(--sp3);top:var(--sp3);color:var(--border2);font-size:18px;opacity:0;transition:opacity .15s,color .15s,transform .15s}.post-card:hover .post-card-arrow{opacity:1;color:var(--accent2);transform:translateX(3px)}.home-empty{padding:var(--sp6) 0;text-align:center;color:var(--muted);font-family:var(--mono);font-size:13px}.err-page{min-height:58vh;display:flex;flex-direction:column;align-items:center;justify-content:center;text-align:center;gap:10px}.err-code{font:800 6rem/1 var(--mono);letter-spacing:-3px;background:linear-gradient(135deg,var(--accent),var(--hi));-webkit-background-clip:text;-webkit-text-fill-color:transparent;background-clip:text}.err-msg{font:700 1.25rem var(--font);color:var(--text)}.err-sub{font:400 13px var(--mono);color:var(--muted)}.err-link{margin-top:var(--sp3);display:inline-flex;align-items:center;gap:6px;padding:9px 20px;border:1px solid var(--border2);border-radius:var(--radius);color:var(--accent2);text-decoration:none;font:500 14px var(--font);transition:border-color .15s,background .15s}.err-link:hover{border-color:var(--accent);background:rgba(99,102,241,.08)}`

const adminCSSMin = `:root{--bg:#020408;--bg2:#060a10;--surface:#0a1018;--surface2:#0e1520;--surface3:#121c2a;--border:#162030;--border2:#1e2d42;--text:#dde6f0;--text2:#8a9bb5;--muted:#374a62;--accent:#6366f1;--accent2:#818cf8;--hi:#a5b4fc;--gold:#f59e0b;--green:#10b981;--cyan:#06b6d4;--error:#ef4444;--purple:#8b5cf6;--red:#f87171;--font:'Inter',system-ui,sans-serif;--mono:'IBM Plex Mono','Fira Code',monospace;--radius:3px;--radius2:6px;--sidebar-w:210px;--topbar-h:48px;--glow-accent:rgba(99,102,241,.15);--glow-green:rgba(16,185,129,.12);--glow-gold:rgba(245,158,11,.12);--gradient-card:linear-gradient(135deg,#0a1018 0%,#0e1520 100%)}@media(prefers-reduced-motion:reduce){*,*::before,*::after{transition:none!important;animation:none!important}}*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}body{background:var(--bg);color:var(--text);font:400 13px/1.5 var(--font);min-height:100vh;-webkit-font-smoothing:antialiased}.skip-link{position:absolute;top:-40px;left:0;z-index:9999;background:var(--accent);color:#fff;padding:8px 16px;font-weight:500;text-decoration:none;transition:top .15s}.skip-link:focus{top:0;outline:3px solid var(--hi);outline-offset:2px}.app-shell{display:grid;grid-template-rows:var(--topbar-h) 1fr;grid-template-columns:var(--sidebar-w) 1fr;min-height:100vh}.topbar{grid-column:1/-1;display:flex;align-items:center;justify-content:space-between;padding:0 14px 0 0;height:var(--topbar-h);background:rgba(2,4,8,.98);backdrop-filter:blur(20px) saturate(180%);-webkit-backdrop-filter:blur(20px) saturate(180%);border-bottom:1px solid var(--border);position:sticky;top:0;z-index:100;box-shadow:0 1px 0 var(--border),0 4px 32px rgba(0,0,0,.8)}.topbar-logo{display:flex;align-items:center;height:100%;padding:0 14px;border-right:1px solid var(--border);gap:9px;text-decoration:none;flex-shrink:0}.omega-mark{font:900 16px/1 var(--mono);color:var(--accent);letter-spacing:-.02em;text-shadow:0 0 20px rgba(99,102,241,.6)}.brand-mark{display:block;flex:0 0 auto}.topbar-wordmark{font:600 12px/1 var(--font);color:var(--text);letter-spacing:-.02em}.topbar-sep{color:var(--border2);margin:0 2px}.topbar-domain{font:400 10px var(--mono);color:var(--muted)}.topbar-center{display:flex;align-items:center;gap:12px;flex:1;padding:0 16px}.live-chip{display:inline-flex;align-items:center;gap:4px;padding:2px 7px;background:rgba(16,185,129,.08);border:1px solid rgba(16,185,129,.2);border-radius:100px;font:700 9px/1 var(--mono);letter-spacing:.08em;color:var(--green)}.live-dot{width:4px;height:4px;border-radius:50%;background:var(--green);animation:live-beat 2s ease-in-out infinite}@keyframes live-beat{0%,100%{transform:scale(1);opacity:1}50%{transform:scale(1.8);opacity:.4}}.topbar-constitution{font:400 10px var(--mono);color:var(--muted)}.topbar-right{display:flex;align-items:center;gap:7px}.snapshot-age{font:400 10px var(--mono);color:var(--muted)}.mode-badge{display:inline-flex;align-items:center;gap:4px;padding:2px 9px;border-radius:100px;font:700 9px/1 var(--mono);letter-spacing:.07em;text-transform:uppercase}.mode-badge.mode-normal{background:rgba(16,185,129,.1);color:var(--green);border:1px solid rgba(16,185,129,.3)}.mode-badge.mode-degraded{background:rgba(245,158,11,.1);color:var(--gold);border:1px solid rgba(245,158,11,.3)}.mode-badge.mode-readonly,.mode-badge.mode-quarantined{background:rgba(239,68,68,.1);color:var(--error);border:1px solid rgba(239,68,68,.3)}.mode-badge.mode-recovery{background:rgba(6,182,212,.1);color:var(--cyan);border:1px solid rgba(6,182,212,.3)}.mode-badge.mode-maintenance{background:rgba(139,92,246,.1);color:var(--purple);border:1px solid rgba(139,92,246,.3)}.pulse-dot{display:inline-block;width:4px;height:4px;border-radius:50%;background:currentColor;animation:live-beat 2.5s ease-in-out infinite}.kbd-hint{font:400 10px var(--mono);color:var(--muted);background:var(--surface2);border:1px solid var(--border2);border-radius:var(--radius);padding:2px 7px;cursor:pointer;transition:border-color .12s,color .12s}.kbd-hint:hover,.kbd-hint:focus-visible{border-color:var(--accent);color:var(--text);outline:2px solid var(--accent);outline-offset:2px}.sidebar{grid-row:2;grid-column:1;background:var(--bg2);border-right:1px solid var(--border);display:flex;flex-direction:column;position:sticky;top:var(--topbar-h);height:calc(100vh - var(--topbar-h));overflow-y:auto;scrollbar-width:none}.sidebar::-webkit-scrollbar{display:none}.sidebar-section{padding:12px 7px 3px}.sidebar-section-label{font:600 9px/1 var(--mono);letter-spacing:.1em;text-transform:uppercase;color:var(--muted);padding:0 7px 5px;display:block;opacity:.6}.sidebar-item{display:flex;align-items:center;justify-content:space-between;padding:5px 7px;border-radius:var(--radius);color:var(--text2);font:500 11px/1.2 var(--font);text-decoration:none;transition:background .1s,color .1s;white-space:nowrap;margin-bottom:1px;position:relative}.sidebar-item:hover{background:rgba(255,255,255,.04);color:var(--text)}.sidebar-item.active{background:rgba(99,102,241,.1);color:var(--hi)}.sidebar-item.active::before{content:'';position:absolute;left:0;top:3px;bottom:3px;width:2px;background:var(--accent);border-radius:0 1px 1px 0}.sidebar-item-left{display:flex;align-items:center;gap:7px}.sidebar-icon{font-size:10px;width:13px;text-align:center;opacity:.7}.sidebar-badge{font:600 9px var(--mono);padding:1px 5px;border-radius:100px;background:rgba(99,102,241,.15);color:var(--accent2)}.sidebar-status{display:inline-block;width:4px;height:4px;border-radius:50%;background:var(--border2)}.sidebar-status.s-ok{background:var(--green)}.sidebar-status.s-warn{background:var(--gold);animation:live-beat 3s ease-in-out infinite}.sidebar-status.s-err{background:var(--error);animation:live-beat 1.5s ease-in-out infinite}.activity-dot{width:4px;height:4px;border-radius:50%;background:var(--green);animation:live-beat 3s infinite;flex-shrink:0}.sidebar-footer{margin-top:auto;padding:9px 11px;border-top:1px solid var(--border)}.sidebar-version{font:600 9px var(--mono);color:var(--muted);display:block}.sidebar-constitution{font:400 9px var(--mono);color:var(--border2);display:block;margin-top:2px}main{grid-row:2;grid-column:2;padding:16px 20px;overflow-x:hidden}.page-header{display:flex;align-items:flex-start;justify-content:space-between;margin-bottom:12px;gap:10px}.page-title{font:700 1rem/1.2 var(--font);color:var(--text);letter-spacing:-.02em;margin-bottom:2px}.page-sub{font:400 10px var(--mono);color:var(--muted)}.section-title{font:600 9px/1 var(--mono);letter-spacing:.1em;text-transform:uppercase;color:var(--muted);padding-bottom:5px;border-bottom:1px solid var(--border);margin:16px 0 9px}.mode-banner{display:flex;align-items:center;gap:10px;padding:9px 13px;border-radius:var(--radius2);border:1px solid var(--border);background:var(--gradient-card);margin-bottom:12px;position:relative;overflow:hidden}.mode-banner::before{content:'';position:absolute;left:0;top:0;bottom:0;width:3px}.mode-banner.mode-normal::before{background:var(--green)}.mode-banner.mode-degraded::before{background:var(--gold)}.mode-banner.mode-readonly::before,.mode-banner.mode-quarantined::before{background:var(--error)}.mode-banner.mode-recovery::before{background:var(--cyan)}.mode-banner.mode-maintenance::before{background:var(--purple)}.mode-banner-pulse{position:relative;width:22px;height:22px;flex-shrink:0}.mode-banner-pulse::before,.mode-banner-pulse::after{content:'';position:absolute;inset:0;border-radius:50%;background:var(--green);opacity:0;animation:pulse-ring 3s ease-out infinite}.mode-banner-pulse::after{animation-delay:1.5s}.mode-banner-pulse-dot{position:absolute;inset:7px;border-radius:50%;background:var(--green)}@keyframes pulse-ring{0%{transform:scale(.3);opacity:.7}100%{transform:scale(1.9);opacity:0}}.mode-banner-info{flex:1;display:flex;align-items:center;gap:12px;flex-wrap:wrap}.mode-banner-state{font:800 13px/1 var(--mono);color:var(--green);letter-spacing:.05em}.mode-banner-desc{font:400 10px var(--mono);color:var(--muted)}.mode-banner-meta{font:400 9px var(--mono);color:var(--border2)}.mode-banner-action{font:500 10px var(--font);color:var(--accent2);text-decoration:none;white-space:nowrap}.mode-banner-action:hover{text-decoration:underline}.metric-grid{display:grid;grid-template-columns:repeat(6,1fr);gap:7px;margin-bottom:12px}.metric-card{background:var(--gradient-card);border:1px solid var(--border);border-radius:var(--radius2);padding:10px 12px 8px;position:relative;overflow:hidden;transition:border-color .2s,box-shadow .2s;animation:fade-in-up .3s ease both}@keyframes fade-in-up{from{opacity:0;transform:translateY(6px)}to{opacity:1;transform:translateY(0)}}.metric-card::before{content:'';position:absolute;top:0;left:0;right:0;height:1px;background:linear-gradient(90deg,transparent,rgba(99,102,241,.25),transparent)}.metric-card:hover{border-color:rgba(99,102,241,.3);box-shadow:0 0 0 1px rgba(99,102,241,.06),0 6px 24px rgba(0,0,0,.5),inset 0 1px 0 rgba(255,255,255,.03)}.metric-card.card-primary{border-color:rgba(99,102,241,.25)}.metric-card.card-primary::before{background:linear-gradient(90deg,var(--accent),var(--hi));height:2px}.metric-label{font:600 9px/1 var(--mono);letter-spacing:.08em;text-transform:uppercase;color:var(--muted);margin-bottom:4px}.metric-val{font:900 3rem/1 var(--font);letter-spacing:-.05em;color:var(--text);margin-bottom:2px;white-space:nowrap}.metric-val.v-accent{color:var(--accent2)}.metric-val.v-ok{color:var(--green)}.metric-val.v-warn{color:var(--gold)}.metric-val.v-err{color:var(--error)}.metric-sub{font:400 9px var(--mono);color:var(--muted)}.metric-trend{display:flex;align-items:center;gap:4px;font:500 9px var(--mono);margin-top:3px}.trend-up{color:var(--green)}.trend-flat{color:var(--muted)}.sparkline{display:block;width:100%;height:20px;margin-top:5px;overflow:visible}.storage-bar{height:2px;background:var(--border2);border-radius:1px;margin-top:5px;overflow:hidden}.storage-fill{height:100%;border-radius:1px;background:linear-gradient(90deg,var(--accent),var(--hi))}.depth-card{background:var(--gradient-card);border:1px solid var(--border);border-radius:var(--radius2);box-shadow:inset 0 1px 0 rgba(255,255,255,.02),0 4px 24px rgba(0,0,0,.4);overflow:hidden}.depth-card-header{display:flex;align-items:center;justify-content:space-between;padding:7px 13px;border-bottom:1px solid var(--border);background:rgba(255,255,255,.01)}.depth-card-label{font:600 9px/1 var(--mono);letter-spacing:.1em;text-transform:uppercase;color:var(--muted)}.depth-card-body{padding:9px 13px}.two-col{display:grid;grid-template-columns:1fr 1fr;gap:8px;margin-bottom:12px}.three-col{display:grid;grid-template-columns:1fr 1fr 1fr;gap:7px;margin-bottom:12px}.panel-card{background:var(--gradient-card);border:1px solid var(--border);border-radius:var(--radius2);padding:9px 13px;box-shadow:inset 0 1px 0 rgba(255,255,255,.02)}.panel-card-title{font:600 9px/1 var(--mono);letter-spacing:.1em;text-transform:uppercase;color:var(--muted);margin-bottom:7px;padding-bottom:5px;border-bottom:1px solid var(--border)}.kernel-row{display:flex;align-items:center;justify-content:space-between;padding:3px 0;border-bottom:1px solid rgba(22,32,48,.8)}.kernel-row:last-child{border-bottom:none}.kernel-key{font:500 10px var(--mono);color:var(--muted)}.kernel-val{font:600 11px var(--mono);color:var(--text)}.kernel-val.kv-ok{color:var(--green)}.kernel-val.kv-warn{color:var(--gold)}.kernel-val.kv-err{color:var(--error)}.slo-row{padding:4px 0;border-bottom:1px solid rgba(22,32,48,.7)}.slo-row:last-child{border-bottom:none}.slo-row-top{display:flex;align-items:center;justify-content:space-between;margin-bottom:3px}.slo-name{font:400 10px var(--mono);color:var(--text2)}.slo-pct{font:700 10px var(--mono);color:var(--green)}.slo-bar{height:2px;background:var(--border2);border-radius:1px;overflow:hidden}.slo-fill{height:100%;border-radius:1px;background:var(--green);transition:width .6s cubic-bezier(.4,0,.2,1);animation:slo-breathe 4s ease-in-out infinite}@keyframes slo-breathe{0%,100%{opacity:1}50%{opacity:.75}}.slo-fill.f-warn{background:var(--gold)}.slo-fill.f-err{background:var(--error)}.trace-waterfall{font:400 10px var(--mono);display:flex;flex-direction:column;gap:3px}.trace-span{display:flex;align-items:center;gap:7px}.trace-span-label{width:130px;color:var(--text2);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;flex-shrink:0}.trace-span-bar-wrap{flex:1;position:relative;height:14px;background:rgba(22,32,48,.8);border-radius:2px;overflow:hidden}.trace-span-bar{position:absolute;top:2px;bottom:2px;border-radius:1px;min-width:3px}.trace-span-dur{font:600 9px var(--mono);color:var(--muted);width:40px;text-align:right;flex-shrink:0}.event-stream{font:400 10px var(--mono);display:flex;flex-direction:column;gap:2px}.event-row{display:flex;align-items:baseline;gap:8px;padding:3px 0;border-bottom:1px solid rgba(22,32,48,.5)}.event-row:last-child{border-bottom:none}.event-time{font:400 10px var(--mono);color:var(--muted);white-space:nowrap;flex-shrink:0}.event-type{font:600 10px var(--mono);color:var(--accent2);flex-shrink:0}.event-payload{font:400 10px var(--mono);color:var(--text2);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.event-cursor{display:inline-block;width:5px;height:10px;background:var(--accent);border-radius:1px;animation:blink-cursor .9s step-end infinite;vertical-align:middle;margin-left:2px}@keyframes blink-cursor{0%,100%{opacity:1}50%{opacity:0}}.wal-bar{height:3px;background:var(--border2);border-radius:2px;overflow:hidden;position:relative;margin-top:4px}.wal-fill{height:100%;border-radius:2px;background:linear-gradient(90deg,var(--accent),var(--hi));animation:wal-pulse 3s ease-in-out infinite}@keyframes wal-pulse{0%,100%{opacity:.7;transform:scaleX(.95)}50%{opacity:1;transform:scaleX(1)}}.topo-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:6px}.topo-node{display:flex;align-items:center;gap:7px;padding:7px 9px;background:var(--surface2);border:1px solid var(--border);border-radius:var(--radius);transition:border-color .15s,box-shadow .15s}.topo-node.topo-ok{border-color:rgba(16,185,129,.2)}.topo-node.topo-ok:hover{box-shadow:0 0 0 1px rgba(16,185,129,.2),0 4px 16px var(--glow-green);border-color:rgba(16,185,129,.35)}.topo-node.topo-warn{border-color:rgba(245,158,11,.2)}.topo-node.topo-warn:hover{box-shadow:0 0 0 1px rgba(245,158,11,.2),0 4px 16px var(--glow-gold);border-color:rgba(245,158,11,.35)}.topo-dot{width:6px;height:6px;border-radius:50%;flex-shrink:0}.topo-dot.d-ok{background:var(--green);animation:live-beat 4s ease-in-out infinite}.topo-dot.d-warn{background:var(--gold);animation:live-beat 2s ease-in-out infinite}.topo-info{display:flex;flex-direction:column;gap:2px}.topo-name{font:600 10px var(--font);color:var(--text)}.topo-status{font:400 9px var(--mono);color:var(--muted)}.thresh-grid{display:grid;grid-template-columns:repeat(4,1fr);gap:7px;margin-bottom:12px}.thresh-item{padding:7px 11px;background:var(--gradient-card);border:1px solid var(--border);border-radius:var(--radius2);display:flex;flex-direction:column;gap:3px}.thresh-name{font:600 9px var(--mono);letter-spacing:.06em;text-transform:uppercase;color:var(--muted)}.thresh-val{font:800 1.35rem/1 var(--font);color:var(--text);letter-spacing:-.03em}.thresh-ok{color:var(--green);font:700 10px var(--mono)}.thresh-fail{color:var(--error);font:700 10px var(--mono)}.thresh-limit{font:400 9px var(--mono);color:var(--muted)}.action-row{display:flex;flex-wrap:wrap;gap:5px;margin-bottom:12px}.btn{display:inline-flex;align-items:center;gap:5px;padding:5px 11px;background:rgba(255,255,255,.04);border:1px solid var(--border2);border-radius:var(--radius);color:var(--text2);font:500 11px var(--font);cursor:pointer;text-decoration:none;transition:border-color .12s,background .12s,color .12s}.btn:hover,.btn:focus-visible{border-color:var(--accent);background:rgba(99,102,241,.08);color:var(--hi);outline:2px solid var(--accent);outline-offset:2px}.btn.btn-primary{background:linear-gradient(135deg,var(--accent) 0%,#4f46e5 100%);border-color:var(--accent);color:#fff;box-shadow:0 2px 8px rgba(99,102,241,.3)}.btn.btn-primary:hover{box-shadow:0 4px 16px rgba(99,102,241,.4)}.data-table{width:100%;border-collapse:collapse;font-size:11px}.data-table th{text-align:left;font:600 9px var(--mono);letter-spacing:.08em;text-transform:uppercase;color:var(--muted);padding:5px 9px;border-bottom:1px solid var(--border)}.data-table td{padding:4px 9px;border-bottom:1px solid rgba(22,32,48,.5);vertical-align:middle;max-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.data-table tr:last-child td{border-bottom:none}.data-table tr:hover td{background:rgba(255,255,255,.02)}.data-table td a{color:var(--accent2);text-decoration:none}.data-table td a:hover{text-decoration:underline}.action-msg{display:none;padding:5px 9px;background:rgba(16,185,129,.08);border:1px solid rgba(16,185,129,.2);border-radius:var(--radius);font:400 11px var(--font);margin-bottom:9px}.action-msg.visible{display:block}.links-row{display:flex;flex-wrap:wrap;gap:10px;margin-top:7px}.links-row a{color:var(--accent2);font-size:11px;text-decoration:none}.links-row a:hover{text-decoration:underline}.admin-footer{margin-top:24px;padding-top:12px;border-top:1px solid var(--border);font:400 9px var(--mono);color:var(--muted);display:flex;align-items:center;justify-content:space-between}.modal-backdrop{display:none;position:fixed;inset:0;z-index:1000;background:rgba(0,0,0,.88);backdrop-filter:blur(6px);align-items:center;justify-content:center}.modal-backdrop.open{display:flex}.modal{background:var(--surface);border:1px solid var(--border2);border-radius:var(--radius2);padding:18px;min-width:280px;max-width:400px;width:90%;box-shadow:0 24px 64px rgba(0,0,0,.9)}.modal-title{display:flex;align-items:center;justify-content:space-between;font:600 12px var(--font);margin-bottom:12px}.modal-close{background:none;border:none;color:var(--muted);cursor:pointer;font-size:14px;padding:3px;border-radius:var(--radius);line-height:1}.modal-close:hover,.modal-close:focus-visible{color:var(--text);outline:2px solid var(--accent);outline-offset:2px}.shortcut-list{list-style:none;display:flex;flex-direction:column;gap:5px}.shortcut-item{display:flex;align-items:center;justify-content:space-between;font-size:11px;padding:4px 0;border-bottom:1px solid var(--border)}.shortcut-item:last-child{border-bottom:none}.shortcut-desc{color:var(--text)}kbd{display:inline-block;padding:2px 5px;background:var(--surface2);border:1px solid var(--border2);border-radius:3px;font:500 9px var(--mono);color:var(--text);min-width:18px;text-align:center}.health-dot{display:inline-block;width:7px;height:7px;border-radius:50%;flex-shrink:0}.health-dot.hd-ok{background:var(--green)}.health-dot.hd-warn{background:var(--gold);animation:live-beat 2s ease-in-out infinite}.health-dot.hd-err{background:var(--error);animation:live-beat 1.5s ease-in-out infinite}a:focus-visible,button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}@media(max-width:1200px){.metric-grid{grid-template-columns:repeat(3,1fr)}}@media(max-width:768px){.app-shell{grid-template-columns:1fr;grid-template-rows:var(--topbar-h) 1fr auto}.topbar{grid-column:1}.topbar-center{display:none}.sidebar{grid-row:3;grid-column:1;position:static;height:auto;flex-direction:row;overflow-x:auto;border-right:none;border-top:1px solid var(--border);padding:0}.sidebar-section{display:flex;padding:5px 3px}.sidebar-section-label{display:none}.sidebar-footer{display:none}.sidebar-item{padding:4px 7px;font-size:10px}main{grid-row:2;grid-column:1;padding:10px}.metric-grid{grid-template-columns:repeat(2,1fr)}.two-col,.three-col{grid-template-columns:1fr}.thresh-grid{grid-template-columns:repeat(2,1fr)}.topo-grid{grid-template-columns:repeat(2,1fr)}}@media(max-width:480px){.metric-grid{grid-template-columns:repeat(2,1fr)}}.kernel-panel,.trace-panel,.event-stream-panel,.fault-panel{background:var(--gradient-card);border:1px solid var(--border);border-radius:var(--radius2);padding:9px 13px;box-shadow:inset 0 1px 0 rgba(255,255,255,.02)}.kernel-panel-title,.trace-panel-title,.event-stream-title,.fault-panel-title{font:600 9px/1 var(--mono);letter-spacing:.1em;text-transform:uppercase;color:var(--muted);margin-bottom:7px;padding-bottom:5px;border-bottom:1px solid var(--border);display:flex;align-items:center;justify-content:space-between;gap:8px}.trace-panel{margin-bottom:12px}.trace-id{font:400 9px var(--mono);color:var(--accent2);text-transform:none;letter-spacing:0}.trace-span-depth{font:400 10px var(--mono);color:var(--muted);width:36px;white-space:pre;flex-shrink:0}.trace-span-bar.bar-root{background:linear-gradient(90deg,var(--accent),var(--accent2))}.trace-span-bar.bar-db{background:var(--cyan)}.trace-span-bar.bar-io{background:var(--green)}.trace-span-bar.bar-sign{background:var(--purple)}.trace-span-bar.bar-out{background:var(--gold)}.stream-live{display:inline-flex;align-items:center;gap:4px;font:700 8px var(--mono);letter-spacing:.08em;color:var(--green);text-transform:none}.stream-live-dot{width:4px;height:4px;border-radius:50%;background:var(--green);animation:live-beat 2s ease-in-out infinite}.event-log{display:flex;flex-direction:column;gap:1px;font:400 10px var(--mono)}.event-line{display:grid;grid-template-columns:64px 96px 1fr;align-items:baseline;gap:8px;padding:3px 0;border-bottom:1px solid rgba(22,32,48,.5)}.event-line:last-child{border-bottom:none}.el-ts{font:400 10px var(--mono);color:var(--muted)}.el-type{font:600 9px var(--mono);padding:1px 5px;border-radius:2px;text-align:center;white-space:nowrap}.el-type.et-write{background:rgba(99,102,241,.12);color:var(--accent2)}.el-type.et-read{background:rgba(6,182,212,.1);color:var(--cyan)}.el-type.et-sign{background:rgba(139,92,246,.12);color:var(--purple)}.el-type.et-health{background:rgba(16,185,129,.1);color:var(--green)}.el-type.et-mode{background:rgba(245,158,11,.1);color:var(--gold)}.el-msg{font:400 10px var(--mono);color:var(--text2);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.fault-row{display:flex;align-items:center;gap:10px;padding:4px 0;border-bottom:1px solid rgba(22,32,48,.6)}.fault-row:last-child{border-bottom:none}.fault-name{font:400 10px var(--mono);color:var(--text2);flex:1}.fault-trigger{font:700 12px var(--mono);color:var(--muted);width:24px;text-align:center}.fault-armed{font:400 9px var(--mono);color:var(--muted)}.timeline-panel{background:var(--gradient-card);border:1px solid var(--border);border-radius:var(--radius2);padding:11px 15px 13px;margin-bottom:12px;position:relative;overflow:hidden}.timeline-panel::before{content:'';position:absolute;top:0;left:0;right:0;height:2px;background:linear-gradient(90deg,transparent,var(--accent2),transparent)}.timeline-head{display:flex;align-items:center;justify-content:space-between;margin-bottom:11px;padding-bottom:6px;border-bottom:1px solid var(--border);gap:10px;flex-wrap:wrap}.timeline-head-title{font:700 11px/1 var(--font);color:var(--text);letter-spacing:-.01em;display:flex;align-items:center;gap:8px}.timeline-head-sub{font:400 9px var(--mono);color:var(--muted)}.tl-badge{display:inline-flex;align-items:center;gap:4px;padding:2px 7px;border-radius:100px;font:700 8px var(--mono);letter-spacing:.08em;background:rgba(99,102,241,.1);color:var(--accent2);border:1px solid rgba(99,102,241,.25)}.tl-badge-dot{width:4px;height:4px;border-radius:50%;background:var(--accent2);animation:live-beat 2.2s ease-in-out infinite}.timeline{position:relative;display:flex;flex-direction:column}.timeline::before{content:'';position:absolute;left:96px;top:7px;bottom:9px;width:1px;background:linear-gradient(180deg,transparent,var(--border2) 6%,var(--border2) 94%,transparent)}.tl-entry{position:relative;display:grid;grid-template-columns:86px 1fr;gap:20px;padding:4px 0;align-items:start}.tl-time{display:flex;flex-direction:column;align-items:flex-end;gap:1px;padding-top:1px}.tl-clock{font:600 10px var(--mono);color:var(--text2)}.tl-rel{font:400 8px var(--mono);color:var(--muted)}.tl-node{position:absolute;left:91px;top:5px;width:11px;height:11px;border-radius:50%;border:2px solid var(--bg);z-index:2;background:var(--muted)}.tl-node.tl-ok{background:var(--green);box-shadow:0 0 0 3px rgba(16,185,129,.12)}.tl-node.tl-info{background:var(--cyan);box-shadow:0 0 0 3px rgba(6,182,212,.1)}.tl-node.tl-accent{background:var(--accent2);box-shadow:0 0 0 3px rgba(99,102,241,.12)}.tl-node.tl-warn{background:var(--gold);box-shadow:0 0 0 3px rgba(245,158,11,.14);animation:live-beat 2.4s ease-in-out infinite}.tl-node.tl-err{background:var(--error);box-shadow:0 0 0 3px rgba(239,68,68,.16);animation:live-beat 1.6s ease-in-out infinite}.tl-body{display:flex;flex-direction:column;gap:2px}.tl-msg{font:500 11px/1.4 var(--font);color:var(--text)}.tl-cat{font:700 8px var(--mono);letter-spacing:.06em;text-transform:uppercase;padding:1px 5px;border-radius:2px;margin-right:6px;vertical-align:middle}.tl-cat-sys{background:rgba(99,102,241,.12);color:var(--accent2)}.tl-cat-db{background:rgba(6,182,212,.1);color:var(--cyan)}.tl-cat-gov{background:rgba(245,158,11,.1);color:var(--gold)}.tl-cat-queue{background:rgba(16,185,129,.1);color:var(--green)}.tl-cat-mode{background:rgba(139,92,246,.12);color:var(--purple)}.tl-cat-fault{background:rgba(239,68,68,.12);color:var(--red)}.tl-cat-ok{background:rgba(16,185,129,.1);color:var(--green)}.tl-causal{font:400 9px/1.4 var(--mono);color:var(--muted);display:flex;align-items:flex-start;gap:5px;margin-top:1px}.tl-causal::before{content:'';width:8px;height:8px;border-left:1px solid var(--border2);border-bottom:1px solid var(--border2);border-radius:0 0 0 2px;flex-shrink:0;margin-top:-1px}.tl-entry.tl-last .tl-msg{color:var(--hi)}@keyframes tl-enter{0%{opacity:0;transform:translateX(-8px)}60%{opacity:1}100%{opacity:1;transform:none}}.tl-entry.tl-enter{animation:tl-enter .5s cubic-bezier(.2,.7,.2,1) both}.tl-entry.tl-enter .tl-node{animation:tl-node-flash .7s ease both}@keyframes tl-node-flash{0%{box-shadow:0 0 0 0 rgba(99,102,241,.5)}100%{box-shadow:0 0 0 7px rgba(99,102,241,0)}}.tl-causal::before{animation:tl-elbow 2.4s ease-in-out infinite}@keyframes tl-elbow{0%,100%{border-color:var(--border2)}50%{border-color:var(--accent2)}}.tl-causal::after{content:'';position:absolute;left:3px;top:50%;width:4px;height:4px;border-radius:50%;background:var(--accent2);opacity:0;animation:tl-spark 2.4s ease-in-out infinite}.tl-causal{position:relative;padding-left:0}@keyframes tl-spark{0%{opacity:0;transform:translate(0,-7px)}40%{opacity:.9}70%{opacity:.9;transform:translate(0,1px)}100%{opacity:0;transform:translate(6px,1px)}}.tl-stream-flag{display:inline-flex;align-items:center;gap:4px;font:700 8px var(--mono);letter-spacing:.06em;color:var(--green);margin-left:8px}.tl-stream-flag.paused{color:var(--muted)}.esc-arrow{animation:esc-pulse 1.7s ease-in-out infinite}.esc-chain .esc-arrow:nth-of-type(1){animation-delay:0s}.esc-chain .esc-arrow:nth-of-type(2){animation-delay:.2s}.esc-chain .esc-arrow:nth-of-type(3){animation-delay:.4s}.esc-chain .esc-arrow:nth-of-type(4){animation-delay:.6s}@keyframes esc-pulse{0%,100%{opacity:.35;transform:translateX(-1px)}50%{opacity:1;transform:translateX(2px)}}@media(max-width:768px){.timeline::before{left:78px}.tl-entry{grid-template-columns:70px 1fr;gap:16px}.tl-node{left:73px}}.console-note{font:400 11px/1.55 var(--mono);color:var(--text2);background:rgba(99,102,241,.04);border:1px solid var(--border);border-left:2px solid var(--accent);border-radius:var(--radius);padding:9px 13px;margin-bottom:14px}.mode-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:8px;margin-bottom:6px}.mode-tile{background:var(--gradient-card);border:1px solid var(--border);border-radius:var(--radius2);padding:12px 14px;position:relative;overflow:hidden;display:flex;flex-direction:column;gap:7px;min-height:124px}.mode-tile::before{content:'';position:absolute;left:0;top:0;bottom:0;width:3px;background:var(--border2)}.mode-tile.m-normal::before{background:var(--green)}.mode-tile.m-degraded::before{background:var(--gold)}.mode-tile.m-readonly::before,.mode-tile.m-quarantined::before{background:var(--error)}.mode-tile.m-recovery::before{background:var(--cyan)}.mode-tile.m-maintenance::before{background:var(--purple)}.mode-tile.current{border-color:rgba(99,102,241,.4);box-shadow:0 0 0 1px rgba(99,102,241,.15),0 6px 24px rgba(0,0,0,.5)}.mode-tile-top{display:flex;align-items:center;justify-content:space-between;gap:8px}.mode-tile-name{font:800 13px var(--mono);letter-spacing:.04em;color:var(--text)}.mode-tile-badge{font:700 8px var(--mono);letter-spacing:.06em;padding:2px 7px;border-radius:100px;text-transform:uppercase;white-space:nowrap}.mode-tile-badge.cur{background:rgba(99,102,241,.15);color:var(--accent2)}.mode-tile-badge.reach{background:rgba(16,185,129,.1);color:var(--green)}.mode-tile-badge.block{background:rgba(55,74,98,.25);color:var(--muted)}.mode-tile-desc{font:400 10px/1.5 var(--mono);color:var(--muted);flex:1}.mode-tile-btn{align-self:flex-start;font:600 10px var(--mono);padding:5px 11px;border-radius:var(--radius);border:1px solid var(--border2);background:rgba(255,255,255,.03);color:var(--text2);cursor:pointer;transition:border-color .12s,color .12s,background .12s}.mode-tile-btn:hover{border-color:var(--accent);color:var(--hi);background:rgba(99,102,241,.1)}.mode-tile-btn:disabled{opacity:.4;cursor:not-allowed}.fe-table{width:100%;border-collapse:collapse;margin-bottom:6px}.fe-table th{text-align:left;font:600 9px var(--mono);letter-spacing:.08em;text-transform:uppercase;color:var(--muted);padding:6px 10px;border-bottom:1px solid var(--border)}.fe-table td{padding:9px 10px;border-bottom:1px solid rgba(22,32,48,.6);font:400 11px var(--mono);color:var(--text2);vertical-align:middle}.fe-table tr:hover td{background:rgba(255,255,255,.02)}.fe-name{color:var(--text);font-weight:600}.fe-count{font:800 15px var(--mono);color:var(--muted)}.fe-count.hot{color:var(--gold)}.fe-count.crit{color:var(--error)}.fe-target{font:600 10px var(--mono);padding:2px 8px;border-radius:100px}.fe-target.t-readonly,.fe-target.t-quarantined{background:rgba(239,68,68,.1);color:var(--error)}.fe-target.t-degraded{background:rgba(245,158,11,.1);color:var(--gold)}.fe-target.t-recovery{background:rgba(6,182,212,.1);color:var(--cyan)}.fe-sim-btn{font:600 10px var(--mono);padding:5px 12px;border-radius:var(--radius);border:1px solid var(--border2);background:rgba(255,255,255,.03);color:var(--text2);cursor:pointer;transition:border-color .12s,color .12s,background .12s}.fe-sim-btn:hover{border-color:var(--error);color:var(--red);background:rgba(239,68,68,.08)}.esc-chain{display:flex;align-items:center;gap:9px;flex-wrap:wrap}.esc-step{font:600 10px var(--mono);padding:6px 11px;border-radius:var(--radius);background:var(--surface2);border:1px solid var(--border);color:var(--text2)}.esc-arrow{color:var(--muted);font-size:13px}.q-strip{display:grid;grid-template-columns:repeat(6,1fr);gap:7px;margin-bottom:12px}.q-stat{background:var(--gradient-card);border:1px solid var(--border);border-radius:var(--radius2);padding:10px 13px;position:relative;overflow:hidden}.q-stat::before{content:'';position:absolute;top:0;left:0;right:0;height:1px;background:linear-gradient(90deg,transparent,rgba(99,102,241,.2),transparent)}.q-stat-val{font:800 1.6rem/1 var(--font);letter-spacing:-.03em;color:var(--text)}.q-stat-label{font:600 9px var(--mono);letter-spacing:.06em;text-transform:uppercase;color:var(--muted);margin-top:5px}.topo-wrap{background:var(--gradient-card);border:1px solid var(--border);border-radius:var(--radius2);padding:10px;margin-bottom:10px;box-shadow:inset 0 1px 0 rgba(255,255,255,.02)}.topo-svg{width:100%;height:auto;display:block;background:radial-gradient(ellipse at 50% -10%,rgba(99,102,241,.05),transparent 55%)}.topo-band{font:700 9px var(--mono);letter-spacing:.1em;fill:var(--muted);opacity:.55;text-transform:uppercase}.topo-edge{stroke:#22324a;stroke-width:1.4;fill:none}.topo-edge.flow{stroke:#2f405e;stroke-dasharray:5 5;animation:topo-flow 1.1s linear infinite}@keyframes topo-flow{to{stroke-dashoffset:-10}}.topo-edge.ctrl{stroke:#8b5cf6;stroke-opacity:.45;stroke-width:1.3;stroke-dasharray:2 4}.topo-rect{fill:#0b1119;stroke-width:1.5}.topo-node:hover .topo-rect{fill:#0e1626}.topo-label{font:600 13px var(--mono);fill:var(--text)}.topo-sub{font:400 10px var(--mono);fill:var(--muted)}.topo-legend{display:flex;gap:18px;flex-wrap:wrap;padding:2px 4px 4px}.tl-leg{display:inline-flex;align-items:center;gap:6px;font:400 10px var(--mono);color:var(--muted)}.tl-leg-dot{width:7px;height:7px;border-radius:50%}.tl-leg-line{width:18px;height:0;border-top:1.5px solid #2f405e}.tl-leg-line.ctrl{border-top:1.5px dashed #8b5cf6}.policy-strip{display:flex;gap:10px;margin-bottom:18px}.policy-stat{background:var(--surface);border:1px solid var(--border);border-radius:var(--radius2);padding:10px 16px 8px;min-width:90px}.policy-stat-val{font:700 26px/1 var(--mono);color:var(--text)}.policy-stat-val.ps-pass{color:var(--green)}.policy-stat-val.ps-warn{color:var(--gold)}.policy-stat-val.ps-fail{color:var(--error)}.policy-stat-label{font:600 9px var(--mono);letter-spacing:.06em;text-transform:uppercase;color:var(--muted);margin-top:5px}.policy-history{width:100%;border-collapse:collapse;font-size:12px}.policy-history th{font:600 9px var(--mono);letter-spacing:.06em;text-transform:uppercase;color:var(--muted);text-align:left;padding:4px 8px 8px;border-bottom:1px solid var(--border)}.policy-history td{padding:7px 8px;border-bottom:1px solid #0e1827;vertical-align:middle}.policy-history tr:last-child td{border-bottom:none}.pol-badge{display:inline-block;padding:2px 8px;border-radius:10px;font:700 10px var(--mono)}.pol-pass{background:#0d2a1c;color:var(--green)}.pol-warn{background:#2a1f06;color:var(--gold)}.pol-fail{background:#2a0d0d;color:var(--error)}.pol-cat{font:500 10px var(--mono);color:var(--muted);text-transform:uppercase;letter-spacing:.05em}.pol-name{font:500 12px var(--mono);color:var(--text)}.pol-detail{font:400 11px var(--mono);color:var(--text2);max-width:340px}.pol-runid{font:400 10px var(--mono);color:var(--border2)}.pol-ts{font:400 11px var(--mono);color:var(--muted)}.policy-trend{display:flex;gap:3px;align-items:flex-end;height:36px;margin-top:4px}.trend-bar{width:12px;border-radius:2px 2px 0 0;min-height:3px}.trend-bar.tb-pass{background:var(--green)}.trend-bar.tb-warn{background:var(--gold)}.trend-bar.tb-fail{background:var(--error)}.theme-tabs{display:flex;gap:2px;margin-bottom:18px;border-bottom:1px solid var(--border)}.theme-tab{padding:7px 14px;font:600 11px var(--mono);color:var(--text2);cursor:pointer;border:none;background:none;border-bottom:2px solid transparent;margin-bottom:-1px;transition:color .12s,border-color .12s}.theme-tab.active{color:var(--hi);border-bottom-color:var(--accent)}.theme-panel{display:none}.theme-panel.active{display:block}.field-row{display:grid;grid-template-columns:160px 1fr;align-items:start;gap:10px;margin-bottom:12px}.field-label{font:600 10px var(--mono);letter-spacing:.06em;text-transform:uppercase;color:var(--muted);padding-top:8px}.field-input{background:var(--surface2);border:1px solid var(--border2);border-radius:var(--radius);color:var(--text);font:400 12px var(--mono);padding:6px 9px;width:100%;transition:border-color .12s;box-sizing:border-box}.field-input:focus{border-color:var(--accent);outline:none}textarea.field-input{resize:vertical;min-height:120px;font-size:11px;line-height:1.55}.field-hex{max-width:120px}.color-pair{display:flex;align-items:center;gap:8px}.color-swatch{width:32px;height:32px;border-radius:var(--radius);border:1px solid var(--border2);cursor:pointer;padding:0}.field-hint{font:400 9px var(--mono);color:var(--muted);margin-top:4px}.theme-note{margin-bottom:14px}.theme-actions{margin-top:20px;display:flex;align-items:center;gap:14px}.theme-save{display:inline-flex;align-items:center;gap:6px;padding:7px 18px;background:linear-gradient(135deg,var(--accent) 0%,#4f46e5 100%);border:none;border-radius:var(--radius);color:#fff;font:600 11px var(--font);cursor:pointer;transition:opacity .15s}.theme-save:hover{opacity:.88}.theme-save:disabled{opacity:.5;cursor:not-allowed}.save-status{font:400 10px var(--mono);color:var(--muted)}.err-banner{padding:8px 12px;background:rgba(239,68,68,.1);border:1px solid rgba(239,68,68,.3);border-radius:var(--radius);color:var(--error);font:400 11px var(--mono);margin-bottom:14px}.ok-banner{display:none;padding:8px 12px;background:rgba(16,185,129,.08);border:1px solid rgba(16,185,129,.25);border-radius:var(--radius);color:var(--green);font:400 11px var(--mono);margin-bottom:14px}.warn-box{padding:8px 12px;background:rgba(245,158,11,.07);border:1px solid rgba(245,158,11,.2);border-radius:var(--radius);color:var(--gold);font:400 10px var(--mono);margin-bottom:12px}`

const hcCSSMin = `@media(prefers-contrast:more){:root{--bg:#000;--surface:#0a0a0a;--border:#fff;--text:#fff;--muted:#ccc;--accent:#6699ff}.btn{border-width:2px!important}.stat-card{border-width:2px!important}.thresh-ok{color:#00ff88!important;font-weight:700!important}.thresh-fail{color:#ff4444!important;font-weight:700!important}}
@media(forced-colors:active){*:focus-visible{outline:3px solid Highlight!important;outline-offset:2px!important}.btn,button{forced-color-adjust:none;background:ButtonFace!important;color:ButtonText!important;border:2px solid ButtonBorder!important}.storage-fill{background:Highlight!important;forced-color-adjust:none}}`
const customCSSMin = `/*
 * VayuPress brand theme — Pico v2 CSS variable overrides.
 * Load order: pico.min.css → custom.css → article.css
 * Teal primary (#0d9488), saffron accent (#f59e0b), Geist/system stack.
 */

/* ── Light mode ─────────────────────────────────────────────────────────── */
:root,
[data-theme="light"] {
  --pico-font-family-sans-serif: "Geist", "Inter", system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  --pico-font-family-serif: Georgia, "Times New Roman", serif;
  --pico-font-family: var(--pico-font-family-sans-serif);
  --pico-line-height: 1.75;
  --pico-font-size: 1.0625rem;

  /* Brand colours */
  --pico-primary: #0d9488;
  --pico-primary-hover: #0f766e;
  --pico-primary-focus: rgba(13, 148, 136, 0.25);
  --pico-primary-inverse: #fff;
  --vayu-accent: #f59e0b;
  --vayu-accent-hover: #d97706;

  /* Surface */
  --pico-background-color: #f8fafc;
  --pico-card-background-color: #fff;
  --pico-card-border-color: #e2e8f0;

  /* Text */
  --pico-color: #0f172a;
  --pico-h1-color: #0f172a;
  --pico-h2-color: #1e293b;
  --pico-h3-color: #334155;
  --pico-muted-color: #64748b;
  --pico-muted-border-color: #cbd5e1;

  /* Links */
  --pico-a-color: var(--pico-primary);
  --pico-a-hover-color: var(--pico-primary-hover);

  /* Code */
  --pico-code-background-color: #f1f5f9;
  --pico-code-color: #0e7490;
  --pico-ins-color: #065f46;
  --pico-del-color: #9f1239;

  /* Border radius */
  --pico-border-radius: 0.5rem;
  --pico-card-sectioning-background-color: #f8fafc;

  /* Prose width */
  --vayu-prose-width: 68ch;
  --vayu-wide-width: 90ch;
}

/* ── Dark mode ──────────────────────────────────────────────────────────── */
[data-theme="dark"] {
  --pico-primary: #2dd4bf;
  --pico-primary-hover: #5eead4;
  --pico-primary-focus: rgba(45, 212, 191, 0.20);
  --pico-primary-inverse: #0f172a;
  --vayu-accent: #fbbf24;
  --vayu-accent-hover: #f59e0b;

  /* Surface */
  --pico-background-color: #0a0f1a;
  --pico-card-background-color: #111827;
  --pico-card-border-color: #1e293b;
  --pico-card-sectioning-background-color: #0f172a;

  /* Text */
  --pico-color: #e2e8f0;
  --pico-h1-color: #f1f5f9;
  --pico-h2-color: #e2e8f0;
  --pico-h3-color: #cbd5e1;
  --pico-muted-color: #94a3b8;
  --pico-muted-border-color: #1e293b;

  /* Links */
  --pico-a-color: var(--pico-primary);
  --pico-a-hover-color: var(--pico-primary-hover);

  /* Code */
  --pico-code-background-color: #0f172a;
  --pico-code-color: #67e8f9;
}

/* ── Global typography ──────────────────────────────────────────────────── */
body {
  font-feature-settings: "kern" 1, "liga" 1, "calt" 1;
  -webkit-font-smoothing: antialiased;
  text-rendering: optimizeLegibility;
}

h1, h2, h3, h4, h5, h6 {
  font-weight: 700;
  letter-spacing: -0.02em;
  line-height: 1.25;
}

/* ── Prose layout (article body) ────────────────────────────────────────── */
.vayu-prose {
  max-width: var(--vayu-prose-width);
  margin-inline: auto;
  line-height: var(--pico-line-height);
}

.vayu-prose p {
  margin-block: 1.25em;
}

.vayu-prose figure {
  margin-inline: 0;
  text-align: center;
}

.vayu-prose figure img,
.vayu-prose img {
  max-width: 100%;
  height: auto;
  border-radius: var(--pico-border-radius);
  display: block;
  margin-inline: auto;
}

.vayu-prose figcaption {
  font-size: 0.875em;
  color: var(--pico-muted-color);
  margin-top: 0.5em;
  font-style: italic;
}

.vayu-prose blockquote {
  border-left: 3px solid var(--vayu-accent);
  margin-inline: 0;
  padding-inline-start: 1.25em;
  color: var(--pico-muted-color);
}

.vayu-prose pre {
  overflow-x: auto;
  border-radius: var(--pico-border-radius);
}

/* ── Site navigation ────────────────────────────────────────────────────── */
.vayu-nav {
  display: flex;
  align-items: center;
  gap: 1.5rem;
  padding-block: 1rem;
  border-bottom: 1px solid var(--pico-muted-border-color);
  margin-bottom: 2.5rem;
  flex-wrap: wrap;
}

.vayu-nav-brand {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-weight: 700;
  font-size: 1.125rem;
  color: var(--pico-color);
  text-decoration: none;
  letter-spacing: -0.02em;
}

.vayu-nav-brand:hover { color: var(--pico-primary); text-decoration: none; }

.vayu-nav-brand img { border-radius: 4px; }

.vayu-nav-links {
  display: flex;
  gap: 1.25rem;
  margin-left: auto;
  align-items: center;
}

.vayu-nav-links a {
  font-size: 0.9375rem;
  color: var(--pico-muted-color);
  text-decoration: none;
  transition: color 0.15s ease;
}

.vayu-nav-links a:hover { color: var(--pico-primary); }

.vayu-nav-status { font-size: 0.75rem; color: var(--pico-muted-color); }

.vayu-mode-dot {
  display: inline-block;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--pico-primary);
  margin-right: 0.35rem;
  vertical-align: middle;
  animation: vayu-pulse 2.5s ease-in-out infinite;
}

@keyframes vayu-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.35; }
}

/* ── Site footer ────────────────────────────────────────────────────────── */
.vayu-footer {
  border-top: 1px solid var(--pico-muted-border-color);
  margin-top: 4rem;
  padding-block: 2rem;
  display: flex;
  align-items: center;
  gap: 1rem;
  flex-wrap: wrap;
  font-size: 0.875rem;
  color: var(--pico-muted-color);
}

.vayu-footer a { color: var(--pico-muted-color); }
.vayu-footer a:hover { color: var(--pico-primary); }

.vayu-footer-brand { display: flex; align-items: center; gap: 0.5rem; }

.vayu-footer-badge {
  margin-left: auto;
  font-size: 0.75rem;
  letter-spacing: 0.05em;
  text-transform: uppercase;
  color: var(--pico-primary);
  opacity: 0.75;
}

/* ── Article header ─────────────────────────────────────────────────────── */
.vayu-article-header { margin-bottom: 2rem; }

.vayu-article-header h1 {
  font-size: clamp(1.75rem, 4vw, 2.75rem);
  margin-bottom: 0.75rem;
}

.vayu-article-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem 1rem;
  font-size: 0.9rem;
  color: var(--pico-muted-color);
}

.vayu-tag {
  display: inline-block;
  font-size: 0.78rem;
  padding: 0.15em 0.55em;
  border-radius: 99px;
  background: var(--pico-primary-focus);
  color: var(--pico-primary);
  text-decoration: none;
  font-weight: 500;
  transition: background 0.15s;
}

.vayu-tag:hover {
  background: var(--pico-primary);
  color: var(--pico-primary-inverse);
  text-decoration: none;
}

/* ── Skip link ──────────────────────────────────────────────────────────── */
.skip-link {
  position: absolute;
  top: -999px;
  left: 0;
  background: var(--pico-primary);
  color: var(--pico-primary-inverse);
  padding: 0.5rem 1rem;
  z-index: 9999;
  border-radius: 0 0 var(--pico-border-radius) 0;
}
.skip-link:focus { top: 0; }

/* ── Hero section (homepage) ────────────────────────────────────────────── */
.vayu-hero {
  padding-block: 3rem 2.5rem;
  border-bottom: 1px solid var(--pico-muted-border-color);
  margin-bottom: 2.5rem;
}

.vayu-hero-eyebrow {
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: var(--pico-primary);
  margin-bottom: 1rem;
  display: block;
}

.vayu-hero h1 {
  font-size: clamp(2rem, 5vw, 3.5rem);
  max-width: 16ch;
  margin-bottom: 1rem;
}

.vayu-hero-tagline {
  max-width: 55ch;
  color: var(--pico-muted-color);
  font-size: 1.0625rem;
  line-height: 1.7;
  margin-bottom: 2rem;
}

.vayu-stats {
  display: flex;
  flex-wrap: wrap;
  gap: 1.5rem 2.5rem;
  margin-top: 1.5rem;
}

.vayu-stat-val {
  display: block;
  font-size: 1.5rem;
  font-weight: 700;
  color: var(--pico-h1-color);
  font-variant-numeric: tabular-nums;
}

.vayu-stat-label {
  font-size: 0.75rem;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--pico-muted-color);
}

/* ── Post list ──────────────────────────────────────────────────────────── */
.vayu-section-label {
  font-size: 0.72rem;
  font-weight: 700;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: var(--pico-muted-color);
  margin-bottom: 1.25rem;
}

.vayu-post-list { display: flex; flex-direction: column; gap: 0; }

.vayu-post-card {
  display: block;
  padding: 1.25rem 0;
  border-bottom: 1px solid var(--pico-muted-border-color);
  text-decoration: none;
  color: var(--pico-color);
  position: relative;
  transition: padding-left 0.15s ease;
}

.vayu-post-card:hover { padding-left: 0.5rem; }
.vayu-post-card:hover .vayu-post-title { color: var(--pico-primary); }

.vayu-post-meta {
  font-size: 0.82rem;
  color: var(--pico-muted-color);
  margin-bottom: 0.35rem;
  display: flex;
  flex-wrap: wrap;
  gap: 0.4rem;
  align-items: center;
}

.vayu-post-title {
  font-size: 1.125rem;
  font-weight: 600;
  letter-spacing: -0.01em;
  transition: color 0.15s;
  margin-bottom: 0.25rem;
}

.vayu-post-excerpt {
  font-size: 0.9rem;
  color: var(--pico-muted-color);
  line-height: 1.55;
}

.vayu-post-arrow {
  position: absolute;
  right: 0.25rem;
  top: 50%;
  transform: translateY(-50%);
  color: var(--pico-muted-color);
  opacity: 0;
  transition: opacity 0.15s, right 0.15s;
}

.vayu-post-card:hover .vayu-post-arrow { opacity: 1; right: 0; }

/* ── Empty state ────────────────────────────────────────────────────────── */
.vayu-empty {
  padding: 3rem 0;
  text-align: center;
  color: var(--pico-muted-color);
  font-size: 0.9375rem;
}

/* ── Error pages ────────────────────────────────────────────────────────── */
.vayu-err { padding: 5rem 0; text-align: center; }
.vayu-err-code {
  font-size: clamp(4rem, 12vw, 8rem);
  font-weight: 900;
  line-height: 1;
  color: var(--pico-primary);
  opacity: 0.15;
  margin-bottom: 0.5rem;
}
.vayu-err-msg {
  font-size: 1.25rem;
  font-weight: 600;
  margin-bottom: 0.5rem;
}
.vayu-err-sub { color: var(--pico-muted-color); margin-bottom: 1.5rem; }

/* ── Responsive ─────────────────────────────────────────────────────────── */
@media (max-width: 600px) {
  .vayu-nav-links { gap: 0.75rem; }
  .vayu-stats { gap: 1rem 1.75rem; }
  .vayu-post-arrow { display: none; }
}
`
