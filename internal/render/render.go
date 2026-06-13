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
	cssHashes struct{ ArticleCSS, AdminCSS, HighContrastCSS string }
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

// ArticleCSSLink returns the versioned <link> for article.css.
func ArticleCSSLink() template.HTML { return CSSLink("article.css", cssHashes.ArticleCSS) }

// AdminCSSLink returns the versioned <link> for admin.css.
func AdminCSSLink() template.HTML { return CSSLink("admin.css", cssHashes.AdminCSS) }

// HighContrastCSSLink returns the versioned <link> for high-contrast.css.
func HighContrastCSSLink() template.HTML {
	return CSSLink("high-contrast.css", cssHashes.HighContrastCSS)
}

// ── Template ──────────────────────────────────────────────────────────────────

type articlePage struct {
	db.Article
	Domain              string
	Version             string
	Layout              ArticleLayoutType
	ArticleCSSLink      template.HTML
	HighContrastCSSLink template.HTML
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
}).Parse(`<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} — {{.Domain}}</title>
<meta name="description" content="{{.Content | trunc 160}}">
<meta name="generator" content="VayuPress {{.Version}}">
<link rel="canonical" href="https://{{.Domain}}/{{.Slug}}">
<meta property="og:type" content="article"><meta property="og:title" content="{{.Title}}">
<meta property="og:url" content="https://{{.Domain}}/{{.Slug}}">
<meta property="article:published_time" content="{{.CreatedAt | isoDate}}">
<meta property="article:modified_time" content="{{.UpdatedAt | isoDate}}">
<script type="application/ld+json">{"@context":"https://schema.org","@type":"BlogPosting","headline":"{{.Title | jsonAttr}}","datePublished":"{{.CreatedAt | isoDate}}","dateModified":"{{.UpdatedAt | isoDate}}","inLanguage":"en","author":{"@type":"Person","name":"Ankush Choudhary Johal","url":"https://{{.Domain}}/about"},"publisher":{"@type":"Organization","name":"VayuPress","url":"https://{{.Domain}}"}}</script>
{{.ArticleCSSLink}}{{.HighContrastCSSLink}}
</head><body>
<a href="#main-content" class="skip-link">Skip to main content</a>
<div class="container"><main id="main-content">
<article itemscope itemtype="https://schema.org/BlogPosting">
<header><h1 itemprop="headline">{{.Title}}</h1>
<div class="meta"><time itemprop="datePublished" datetime="{{.CreatedAt | shortDate}}">{{.CreatedAt | humanDate}}</time>
<span>· {{.Content | readTime}} min read</span>
{{if .Tags}}<nav class="tags" aria-label="Tags">{{range .Tags}}<a href="/tags/{{.}}" rel="tag">#{{.}}</a>{{end}}</nav>{{end}}</div>
</header><div class="content" itemprop="articleBody">{{.Content | safeHTML}}</div>
</article>
<footer><p>By <strong>Ankush Choudhary Johal</strong> · Powered by <a href="https://vayupress.com">VayuPress</a></p></footer>
</main></div></body></html>`))

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
	data := articlePage{
		Article:             a,
		Domain:              config.Cfg.Domain,
		Version:             Version,
		Layout:              layout,
		ArticleCSSLink:      ArticleCSSLink(),
		HighContrastCSSLink: HighContrastCSSLink(),
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

const articleCSSMin = `:root{--bg:#080b10;--surface:#0f1420;--surface2:#141c2e;--border:#1e2840;--border2:#263354;--text:#e2e8f0;--muted:#64748b;--accent:#6366f1;--accent2:#818cf8;--hi:#a5b4fc;--green:#22c55e;--max-w:740px;--font:'Inter',system-ui,sans-serif;--mono:'IBM Plex Mono','JetBrains Mono',monospace;--radius:6px;--radius2:10px;--sp1:4px;--sp2:8px;--sp3:16px;--sp4:24px;--sp5:36px;--sp6:56px}@media(prefers-color-scheme:light){:root{--bg:#fafafa;--surface:#fff;--surface2:#f1f5f9;--border:#e2e8f0;--border2:#cbd5e1;--text:#0f172a;--muted:#64748b;--accent:#4f46e5;--accent2:#6366f1;--hi:#4338ca}}@media(prefers-reduced-motion:reduce){*,*::before,*::after{transition:none!important;animation:none!important}}*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}html{scroll-behavior:smooth}.skip-link{position:absolute;top:-40px;left:0;z-index:9999;background:var(--accent);color:#fff;padding:var(--sp2) var(--sp3);font:500 13px/1.4 var(--font);text-decoration:none;transition:top .2s}.skip-link:focus{top:0;outline:3px solid var(--hi);outline-offset:2px}body{background:var(--bg);color:var(--text);font:400 18px/1.72 var(--font);padding:var(--sp5) var(--sp3)}body::before{content:'';position:fixed;top:0;left:0;right:0;height:1px;background:linear-gradient(90deg,transparent,var(--accent),var(--accent2),transparent);opacity:.6;z-index:100}.container{max-width:var(--max-w);margin:0 auto}header{padding-bottom:var(--sp5);margin-bottom:var(--sp5);position:relative}.site-nav{display:flex;align-items:center;justify-content:space-between;margin-bottom:var(--sp5);padding-bottom:var(--sp4);border-bottom:1px solid var(--border)}.site-nav-brand{display:flex;align-items:center;gap:var(--sp2);font:700 16px var(--font);color:var(--text);text-decoration:none}.site-nav-brand-icon{color:var(--accent);font-size:20px}.site-nav-links{display:flex;gap:var(--sp4)}.site-nav-links a{color:var(--muted);font-size:14px;text-decoration:none;transition:color .15s}.site-nav-links a:hover{color:var(--text)}.mode-indicator{font-size:11px;color:var(--green);font-family:var(--mono)}.mode-dot{display:inline-block;width:6px;height:6px;background:var(--green);border-radius:50%;margin-right:5px;animation:pulse 2s infinite}@keyframes pulse{0%,100%{opacity:1}50%{opacity:.4}}h1{font:700 2.2rem/1.18 var(--font);margin-bottom:var(--sp3);letter-spacing:-.6px;background:linear-gradient(135deg,var(--text) 60%,var(--hi));-webkit-background-clip:text;-webkit-text-fill-color:transparent;background-clip:text}.meta{color:var(--muted);font-size:13px;display:flex;flex-wrap:wrap;align-items:center;gap:var(--sp3);margin-bottom:var(--sp4)}.meta-separator{opacity:.3}.tags{display:flex;flex-wrap:wrap;gap:6px}.tags a{display:inline-block;padding:3px 10px;background:rgba(99,102,241,.1);border:1px solid rgba(99,102,241,.25);border-radius:20px;font-size:12px;color:var(--accent2);text-decoration:none;transition:all .15s}.tags a:hover{background:rgba(99,102,241,.2);border-color:var(--accent2)}.tags a:focus-visible{outline:2px solid var(--accent);outline-offset:2px}hr.content-divider{border:none;border-top:1px solid var(--border);margin:var(--sp4) 0;background:none}.content{margin-top:var(--sp5);line-height:1.8}.content>*+*{margin-top:var(--sp4)}.content h2{font:700 1.4rem/1.25 var(--font);margin:var(--sp6) 0 var(--sp3);color:var(--text);letter-spacing:-.3px}.content h3{font:600 1.15rem/1.3 var(--font);margin:var(--sp5) 0 var(--sp2);color:var(--text)}.content h2::before{content:'';display:block;width:32px;height:2px;background:linear-gradient(90deg,var(--accent),var(--accent2));border-radius:1px;margin-bottom:var(--sp2)}.content pre{background:var(--surface);border:1px solid var(--border2);border-radius:var(--radius2);padding:var(--sp3) var(--sp4);overflow-x:auto;font:400 14px/1.6 var(--mono);margin:var(--sp4) 0;position:relative}.content pre::before{content:attr(data-lang);position:absolute;top:10px;right:14px;font-size:11px;color:var(--muted);font-family:var(--mono)}.content code{background:rgba(99,102,241,.1);padding:2px 7px;border-radius:4px;font:400 14px var(--mono);color:var(--accent2)}.content pre code{background:none;padding:0;color:var(--text)}.content blockquote{border-left:3px solid var(--accent);padding:var(--sp3) var(--sp4);color:var(--muted);margin:var(--sp4) 0;background:var(--surface);border-radius:0 var(--radius) var(--radius) 0;font-style:italic}.content p{color:var(--text)}.content a{color:var(--accent2);text-decoration:none;border-bottom:1px solid rgba(99,102,241,.3);transition:border-color .15s}.content a:hover{border-color:var(--accent2)}.content ul,.content ol{padding-left:var(--sp4)}.content li{margin:var(--sp1) 0}.content img{max-width:100%;border-radius:var(--radius2);border:1px solid var(--border)}footer{margin-top:var(--sp6);padding:var(--sp5) 0 var(--sp4);border-top:1px solid var(--border);font-size:13px;color:var(--muted);display:flex;align-items:center;justify-content:space-between;gap:var(--sp3);flex-wrap:wrap}.footer-brand{display:flex;align-items:center;gap:6px;font-weight:500;color:var(--muted)}.footer-badge{font-size:10px;padding:2px 7px;background:rgba(34,197,94,.1);border:1px solid rgba(34,197,94,.2);border-radius:10px;color:var(--green)}.reading-progress{position:fixed;top:0;left:0;width:0;height:2px;background:linear-gradient(90deg,var(--accent),var(--accent2));z-index:200;transition:width .1s}a:focus-visible{outline:2px solid var(--accent);outline-offset:2px;border-radius:2px}@media(max-width:480px){body{padding:var(--sp3) var(--sp2)}h1{font-size:1.7rem;-webkit-text-fill-color:var(--text)}.site-nav-links{display:none}.meta{gap:var(--sp2)}}`

const adminCSSMin = `:root{--bg:#0B0F14;--surface:#111827;--surface2:#161f2e;--border:#1F2937;--border2:#2d3a4a;--text:#E5E7EB;--muted:#9CA3AF;--accent:#3B82F6;--hi:#38BDF8;--success:#10B981;--warn:#F59E0B;--error:#EF4444;--font:'Inter',system-ui,sans-serif;--mono:'IBM Plex Mono',monospace;--radius:6px;--sp1:4px;--sp2:8px;--sp3:16px;--sp4:24px;--sp5:32px;--sp6:48px;--surface-glass:rgba(17,24,39,.72);--glow-accent:rgba(59,130,246,.18);--gradient-surface:linear-gradient(135deg,#111827 0%,#161f2e 100%);--border-subtle:rgba(31,41,55,.6);--sidebar-w:200px}@media(prefers-reduced-motion:reduce){*,*::before,*::after{transition:none!important;animation:none!important}}*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}body{background:var(--bg);color:var(--text);font:400 14px/1.5 var(--font);min-height:100vh}.skip-link{position:absolute;top:-40px;left:0;z-index:9999;background:var(--accent);color:#fff;padding:var(--sp2) var(--sp3);font-weight:500;text-decoration:none;transition:top .15s}.skip-link:focus{top:0;outline:3px solid var(--hi);outline-offset:2px}.app-shell{display:grid;grid-template-rows:56px 1fr;grid-template-columns:var(--sidebar-w) 1fr;min-height:100vh}.topbar{grid-column:1/-1;display:flex;align-items:center;justify-content:space-between;padding:0 var(--sp4);height:56px;background:rgba(11,15,20,.92);backdrop-filter:blur(12px);-webkit-backdrop-filter:blur(12px);border-bottom:1px solid var(--border-subtle);border-left:3px solid transparent;background-clip:padding-box;position:sticky;top:0;z-index:100;box-shadow:0 1px 0 var(--border-subtle),0 4px 16px rgba(0,0,0,.4)}.topbar::before{content:'';position:absolute;left:0;top:0;bottom:0;width:3px;background:linear-gradient(180deg,var(--accent) 0%,var(--hi) 100%)}.topbar-brand{display:flex;align-items:center;gap:var(--sp2);font-weight:600;font-size:15px;color:var(--text);text-decoration:none;letter-spacing:-.01em}.topbar-domain{color:var(--muted);font-size:12px;font-weight:400;margin-left:4px}.topbar-actions{display:flex;align-items:center;gap:var(--sp2)}.kbd-hint{font:400 11px var(--mono);color:var(--muted);background:var(--surface2);border:1px solid var(--border2);border-radius:var(--radius);padding:2px 8px;cursor:pointer;transition:border-color .15s,color .15s,background .15s}.kbd-hint:hover,.kbd-hint:focus-visible{border-color:var(--accent);color:var(--text);background:rgba(59,130,246,.08);outline:2px solid var(--accent);outline-offset:2px}.sidebar{grid-row:2;grid-column:1;background:var(--surface);border-right:1px solid var(--border-subtle);display:flex;flex-direction:column;justify-content:space-between;padding:var(--sp3) 0;position:sticky;top:56px;height:calc(100vh - 56px);overflow-y:auto}.sidebar-nav{display:flex;flex-direction:column;gap:2px;padding:0 var(--sp2)}.sidebar-nav-item{display:flex;align-items:center;gap:var(--sp2);padding:8px 10px;border-radius:var(--radius);color:var(--muted);font-size:13px;font-weight:500;text-decoration:none;transition:background .12s,color .12s;white-space:nowrap}.sidebar-nav-item:hover{background:rgba(59,130,246,.08);color:var(--text)}.sidebar-nav-item.active{background:rgba(59,130,246,.12);color:var(--hi);border-left:2px solid var(--accent)}.sidebar-icon{font-size:14px;width:18px;text-align:center;flex-shrink:0}.sidebar-footer{padding:var(--sp3) var(--sp3);border-top:1px solid var(--border-subtle);display:flex;flex-direction:column;gap:4px}.sidebar-version{font:500 11px var(--mono);color:var(--muted)}.sidebar-constitution{font:400 10px var(--mono);color:var(--border2);letter-spacing:.02em}main{grid-row:2;grid-column:2;padding:var(--sp4);max-width:1100px;overflow-x:hidden}.section-title{font-size:10px;font-weight:600;letter-spacing:.08em;text-transform:uppercase;color:var(--muted);margin:var(--sp4) 0 var(--sp3);padding-bottom:var(--sp2);border-bottom:1px solid var(--border)}.section-header{display:flex;align-items:center;justify-content:space-between;margin:var(--sp4) 0 var(--sp3);padding-bottom:var(--sp2);border-bottom:1px solid var(--border)}.section-header-left{display:flex;align-items:center;gap:var(--sp2)}.section-header-icon{font-size:14px;color:var(--muted)}.section-header-title{font-size:10px;font-weight:600;letter-spacing:.08em;text-transform:uppercase;color:var(--muted)}.section-header-actions{display:flex;gap:var(--sp2)}.stat-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:var(--sp3);margin-bottom:var(--sp4)}.stat-card{background:var(--gradient-surface);border:1px solid var(--border);border-radius:var(--radius);padding:var(--sp3);transition:box-shadow .2s,border-color .2s;position:relative;overflow:hidden}.stat-card:hover{box-shadow:0 0 0 1px var(--accent),0 4px 20px var(--glow-accent);border-color:rgba(59,130,246,.35)}.stat-card.stat-primary::before{content:'';position:absolute;top:0;left:0;right:0;height:2px;background:linear-gradient(90deg,var(--accent),var(--hi))}.stat-val{font:700 2.5rem/1 var(--font);color:var(--accent);margin-bottom:4px;letter-spacing:-.02em}.stat-val.stat-ok{color:var(--success)}.stat-val.stat-warn{color:var(--warn)}.stat-val.stat-err{color:var(--error)}.stat-lbl{font-size:11px;color:var(--muted)}.stat-sub{font-size:11px;color:var(--muted);margin-top:6px}.stat-icon{position:absolute;top:var(--sp3);right:var(--sp3);font-size:18px;opacity:.25}.storage-bar{height:3px;background:var(--border2);border-radius:2px;margin-top:8px;overflow:hidden}.storage-fill{height:100%;border-radius:2px;background:linear-gradient(90deg,var(--accent),var(--hi))}.thresh-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:var(--sp2);margin-bottom:var(--sp4)}.thresh-item{display:flex;align-items:center;justify-content:space-between;background:var(--surface);border:1px solid var(--border);border-radius:var(--radius);padding:var(--sp2) var(--sp3);font-size:12px}.thresh-name{color:var(--muted)}.thresh-val{font:500 12px var(--mono);color:var(--text)}.thresh-ok{color:var(--success);font-weight:600}.thresh-fail{color:var(--error);font-weight:600}.action-row{display:flex;flex-wrap:wrap;gap:var(--sp2);margin-bottom:var(--sp4)}.btn{display:inline-flex;align-items:center;gap:6px;padding:7px 14px;background:var(--surface-glass);backdrop-filter:blur(4px);border:1px solid var(--border2);border-radius:var(--radius);color:var(--text);font:500 13px var(--font);cursor:pointer;text-decoration:none;transition:border-color .15s,background .15s,color .15s,box-shadow .15s}.btn:hover,.btn:focus-visible{border-color:var(--accent);background:rgba(59,130,246,.08);color:var(--hi);box-shadow:0 0 0 1px rgba(59,130,246,.2);outline:2px solid var(--accent);outline-offset:2px}.btn.btn-primary{background:linear-gradient(135deg,var(--accent) 0%,#2563eb 100%);border-color:var(--accent);color:#fff;box-shadow:0 2px 8px rgba(59,130,246,.3)}.btn.btn-primary:hover{background:linear-gradient(135deg,var(--hi) 0%,var(--accent) 100%);box-shadow:0 4px 16px rgba(59,130,246,.4)}.data-table{width:100%;border-collapse:collapse;font-size:13px}.data-table th{text-align:left;font-size:10px;font-weight:600;letter-spacing:.05em;text-transform:uppercase;color:var(--muted);padding:var(--sp2) var(--sp3);border-bottom:1px solid var(--border)}.data-table td{padding:var(--sp2) var(--sp3);border-bottom:1px solid var(--border);vertical-align:middle;max-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.data-table tr:hover td{background:var(--surface2)}.data-table td a{color:var(--accent);text-decoration:none}.action-msg{display:none;padding:var(--sp2) var(--sp3);background:var(--surface);border:1px solid var(--success);border-radius:var(--radius);font-size:13px;margin-bottom:var(--sp3)}.action-msg.visible{display:block}.links-row{display:flex;flex-wrap:wrap;gap:var(--sp3);margin-top:var(--sp3)}.links-row a{color:var(--accent);font-size:13px;text-decoration:none}.links-row a:hover{text-decoration:underline}.admin-footer{margin-top:var(--sp5);padding-top:var(--sp4);border-top:1px solid var(--border);font-size:11px;color:var(--muted)}.modal-backdrop{display:none;position:fixed;inset:0;z-index:1000;background:rgba(0,0,0,.75);backdrop-filter:blur(4px);align-items:center;justify-content:center}.modal-backdrop.open{display:flex}.modal{background:var(--surface);border:1px solid var(--border2);border-radius:var(--radius);padding:var(--sp4);min-width:320px;max-width:480px;width:90%;box-shadow:0 20px 60px rgba(0,0,0,.6)}.modal-title{display:flex;align-items:center;justify-content:space-between;font-weight:600;font-size:14px;margin-bottom:var(--sp3)}.modal-close{background:none;border:none;color:var(--muted);cursor:pointer;font-size:16px;padding:4px;border-radius:var(--radius);line-height:1}.modal-close:hover,.modal-close:focus-visible{color:var(--text);outline:2px solid var(--accent);outline-offset:2px}.shortcut-list{list-style:none;display:flex;flex-direction:column;gap:var(--sp2)}.shortcut-item{display:flex;align-items:center;justify-content:space-between;font-size:13px;padding:var(--sp2) 0;border-bottom:1px solid var(--border)}.shortcut-item:last-child{border-bottom:none}.shortcut-desc{color:var(--text)}kbd{display:inline-block;padding:2px 6px;background:var(--surface2);border:1px solid var(--border2);border-radius:3px;font:500 11px var(--mono);color:var(--text);min-width:22px;text-align:center}.mode-badge{display:inline-flex;align-items:center;gap:6px;padding:4px 10px;border-radius:100px;font:600 11px var(--font);letter-spacing:.04em;text-transform:uppercase}.mode-badge.mode-normal{background:rgba(16,185,129,.12);color:var(--success);border:1px solid rgba(16,185,129,.25)}.mode-badge.mode-degraded{background:rgba(245,158,11,.12);color:var(--warn);border:1px solid rgba(245,158,11,.25)}.mode-badge.mode-readonly,.mode-badge.mode-quarantined{background:rgba(239,68,68,.12);color:var(--error);border:1px solid rgba(239,68,68,.25)}.mode-badge.mode-recovery{background:rgba(56,189,248,.12);color:var(--hi);border:1px solid rgba(56,189,248,.25)}.mode-badge.mode-maintenance{background:rgba(168,85,247,.12);color:#c084fc;border:1px solid rgba(168,85,247,.25)}@keyframes badge-pulse{0%,100%{opacity:1}50%{opacity:.4}}.mode-badge .pulse-dot{display:inline-block;width:6px;height:6px;border-radius:50%;background:currentColor;animation:badge-pulse 2s ease-in-out infinite}.policy-row{display:flex;align-items:center;gap:var(--sp3);padding:var(--sp2) var(--sp3);border-radius:var(--radius);font-size:13px;transition:background .12s}.policy-row:hover{background:var(--surface2)}.policy-row-name{flex:1;color:var(--text);font-weight:500}.policy-row-category{font-size:11px;color:var(--muted);min-width:80px}.policy-row-severity{font-size:11px;min-width:60px;font-weight:600}.policy-row-result{font-size:12px;font:500 12px var(--mono);min-width:60px;text-align:right}.timeline-item{display:flex;gap:var(--sp3);padding:var(--sp2) 0;border-left:2px solid var(--border2);padding-left:var(--sp3);position:relative}.timeline-item::before{content:'';position:absolute;left:-5px;top:12px;width:8px;height:8px;border-radius:50%;background:var(--border2);border:2px solid var(--bg)}.timeline-item.tl-accent{border-left-color:var(--accent)}.timeline-item.tl-accent::before{background:var(--accent)}.timeline-time{font:400 11px var(--mono);color:var(--muted);white-space:nowrap;min-width:60px}.timeline-text{font-size:13px;color:var(--text)}.timeline-chain{font-size:11px;color:var(--muted);margin-top:2px}@keyframes health-pulse{0%,100%{box-shadow:0 0 0 0 currentColor}70%{box-shadow:0 0 0 4px transparent}}.health-dot{display:inline-block;width:10px;height:10px;border-radius:50%;flex-shrink:0}.health-dot.hd-ok{background:var(--success)}.health-dot.hd-warn{background:var(--warn);animation:health-pulse 2s ease-in-out infinite;color:var(--warn)}.health-dot.hd-err{background:var(--error);animation:health-pulse 1.5s ease-in-out infinite;color:var(--error)}.slo-bar{height:6px;border-radius:3px;background:var(--border2);overflow:hidden;margin-top:4px}.slo-fill{height:100%;border-radius:3px;transition:width .3s ease}.slo-fill[data-pct]{background:var(--success)}.slo-fill[style*="width:10"]{background:var(--success)}.slo-fill[style*="width:9"]{background:var(--warn)}.slo-fill[style*="width:8"]{background:var(--error)}.topology-node{display:inline-flex;flex-direction:column;align-items:center;padding:var(--sp2) var(--sp3);background:var(--surface);border:1px solid var(--border2);border-radius:var(--radius);font-size:12px;transition:box-shadow .2s,border-color .2s}.topology-node:hover,.topology-node.active{border-color:var(--accent);box-shadow:0 0 0 1px var(--accent),0 4px 20px var(--glow-accent)}.two-col{display:grid;grid-template-columns:1fr 1fr;gap:var(--sp3);margin-bottom:var(--sp4)}.panel-card{background:var(--surface);border:1px solid var(--border);border-radius:var(--radius);padding:var(--sp3)}.panel-card-title{font-size:10px;font-weight:600;letter-spacing:.08em;text-transform:uppercase;color:var(--muted);margin-bottom:var(--sp2)}.slo-row{display:flex;align-items:center;justify-content:space-between;padding:6px 0;border-bottom:1px solid var(--border-subtle);font-size:12px}.slo-row:last-child{border-bottom:none}.slo-name{color:var(--muted)}.slo-pct{font:500 12px var(--mono);color:var(--success)}.mode-banner{background:var(--gradient-surface);border:1px solid var(--border);border-radius:var(--radius);padding:var(--sp4);margin-bottom:var(--sp4);display:flex;align-items:center;justify-content:space-between;gap:var(--sp3)}.mode-banner-left{display:flex;align-items:center;gap:var(--sp3)}.mode-banner-icon{font-size:2rem;opacity:.7}.mode-banner-info{display:flex;flex-direction:column;gap:4px}.mode-banner-label{font-size:11px;font-weight:600;letter-spacing:.08em;text-transform:uppercase;color:var(--muted)}.mode-banner-value{font:700 1.25rem/1 var(--font);color:var(--text)}.mode-banner-desc{font-size:12px;color:var(--muted)}.mode-banner-action{color:var(--accent);font-size:13px;text-decoration:none}.mode-banner-action:hover{text-decoration:underline}.page-header{display:flex;align-items:flex-start;justify-content:space-between;margin-bottom:var(--sp4);gap:var(--sp3)}.page-header-text{}.page-header-title{font:700 1.25rem/1.2 var(--font);color:var(--text);margin-bottom:4px}.page-header-sub{font-size:12px;color:var(--muted)}a:focus-visible,button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}@media(max-width:768px){.app-shell{grid-template-columns:1fr;grid-template-rows:56px 1fr auto}.topbar{grid-column:1}.sidebar{grid-row:3;grid-column:1;position:static;height:auto;flex-direction:row;padding:var(--sp2) var(--sp3);border-right:none;border-top:1px solid var(--border-subtle);overflow-x:auto}.sidebar-nav{flex-direction:row;gap:4px;padding:0}.sidebar-footer{display:none}.sidebar-nav-item{padding:6px 10px;font-size:12px}main{grid-row:2;grid-column:1;padding:var(--sp3)}.two-col{grid-template-columns:1fr}.stat-grid{grid-template-columns:repeat(2,1fr)}}@media(max-width:600px){.topbar{padding:0 var(--sp3)}.stat-grid{grid-template-columns:repeat(2,1fr)}}`

const hcCSSMin = `@media(prefers-contrast:more){:root{--bg:#000;--surface:#0a0a0a;--border:#fff;--text:#fff;--muted:#ccc;--accent:#6699ff}.btn{border-width:2px!important}.stat-card{border-width:2px!important}.thresh-ok{color:#00ff88!important;font-weight:700!important}.thresh-fail{color:#ff4444!important;font-weight:700!important}}
@media(forced-colors:active){*:focus-visible{outline:3px solid Highlight!important;outline-offset:2px!important}.btn,button{forced-color-adjust:none;background:ButtonFace!important;color:ButtonText!important;border:2px solid ButtonBorder!important}.storage-fill{background:Highlight!important;forced-color-adjust:none}}`
