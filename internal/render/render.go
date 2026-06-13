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

const adminCSSMin = `:root{--bg:#04060b;--bg2:#080c14;--surface:#0d1117;--surface2:#121a26;--surface3:#161f2e;--border:#1a2236;--border2:#21304a;--text:#e2e8f0;--text2:#94a3b8;--muted:#3d4f6a;--accent:#6366f1;--accent2:#818cf8;--hi:#a5b4fc;--gold:#f59e0b;--green:#10b981;--cyan:#06b6d4;--error:#ef4444;--purple:#8b5cf6;--font:'Inter',system-ui,-apple-system,sans-serif;--mono:'IBM Plex Mono','JetBrains Mono','Fira Code',monospace;--radius:4px;--radius2:8px;--sidebar-w:220px;--topbar-h:52px;--sp1:4px;--sp2:8px;--sp3:12px;--sp4:20px;--sp5:32px;--glow-accent:rgba(99,102,241,.15);--glow-green:rgba(16,185,129,.12);--glow-gold:rgba(245,158,11,.12);--gradient-card:linear-gradient(135deg,#0d1117 0%,#0f1520 100%)}@media(prefers-reduced-motion:reduce){*,*::before,*::after{transition:none!important;animation:none!important}}*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}body{background:var(--bg);color:var(--text);font:400 13px/1.5 var(--font);min-height:100vh;-webkit-font-smoothing:antialiased}.skip-link{position:absolute;top:-40px;left:0;z-index:9999;background:var(--accent);color:#fff;padding:8px 16px;font-weight:500;text-decoration:none;transition:top .15s}.skip-link:focus{top:0;outline:3px solid var(--hi);outline-offset:2px}.app-shell{display:grid;grid-template-rows:var(--topbar-h) 1fr;grid-template-columns:var(--sidebar-w) 1fr;min-height:100vh}.topbar{grid-column:1/-1;display:flex;align-items:center;justify-content:space-between;padding:0 16px 0 0;height:var(--topbar-h);background:rgba(4,6,11,.97);backdrop-filter:blur(20px) saturate(180%);-webkit-backdrop-filter:blur(20px) saturate(180%);border-bottom:1px solid var(--border);position:sticky;top:0;z-index:100;box-shadow:0 1px 0 var(--border),0 4px 32px rgba(0,0,0,.7)}.topbar-logo{display:flex;align-items:center;height:100%;padding:0 16px;border-right:1px solid var(--border);gap:10px;text-decoration:none;flex-shrink:0}.omega-mark{font:900 17px/1 var(--mono);color:var(--accent);letter-spacing:-.02em;text-shadow:0 0 20px rgba(99,102,241,.5)}.topbar-wordmark{font:600 13px/1 var(--font);color:var(--text);letter-spacing:-.02em}.topbar-sep{color:var(--border2);margin:0 2px}.topbar-domain{font:400 11px var(--mono);color:var(--muted)}.topbar-center{display:flex;align-items:center;gap:14px;flex:1;padding:0 20px}.live-chip{display:inline-flex;align-items:center;gap:5px;padding:3px 8px;background:rgba(16,185,129,.08);border:1px solid rgba(16,185,129,.2);border-radius:100px;font:700 9px/1 var(--mono);letter-spacing:.08em;color:var(--green)}.live-dot{width:5px;height:5px;border-radius:50%;background:var(--green);animation:live-beat 2s ease-in-out infinite}@keyframes live-beat{0%,100%{transform:scale(1);opacity:1}50%{transform:scale(1.7);opacity:.5}}.topbar-constitution{font:400 10px var(--mono);color:var(--muted)}.topbar-right{display:flex;align-items:center;gap:8px}.snapshot-age{font:400 10px var(--mono);color:var(--muted)}.mode-badge{display:inline-flex;align-items:center;gap:5px;padding:3px 10px;border-radius:100px;font:700 9px/1 var(--mono);letter-spacing:.07em;text-transform:uppercase}.mode-badge.mode-normal{background:rgba(16,185,129,.1);color:var(--green);border:1px solid rgba(16,185,129,.3)}.mode-badge.mode-degraded{background:rgba(245,158,11,.1);color:var(--gold);border:1px solid rgba(245,158,11,.3)}.mode-badge.mode-readonly,.mode-badge.mode-quarantined{background:rgba(239,68,68,.1);color:var(--error);border:1px solid rgba(239,68,68,.3)}.mode-badge.mode-recovery{background:rgba(6,182,212,.1);color:var(--cyan);border:1px solid rgba(6,182,212,.3)}.mode-badge.mode-maintenance{background:rgba(139,92,246,.1);color:var(--purple);border:1px solid rgba(139,92,246,.3)}.pulse-dot{display:inline-block;width:5px;height:5px;border-radius:50%;background:currentColor;animation:live-beat 2.5s ease-in-out infinite}.kbd-hint{font:400 10px var(--mono);color:var(--muted);background:var(--surface2);border:1px solid var(--border2);border-radius:var(--radius);padding:3px 8px;cursor:pointer;transition:border-color .12s,color .12s}.kbd-hint:hover,.kbd-hint:focus-visible{border-color:var(--accent);color:var(--text);outline:2px solid var(--accent);outline-offset:2px}.sidebar{grid-row:2;grid-column:1;background:var(--bg2);border-right:1px solid var(--border);display:flex;flex-direction:column;position:sticky;top:var(--topbar-h);height:calc(100vh - var(--topbar-h));overflow-y:auto;scrollbar-width:none}.sidebar::-webkit-scrollbar{display:none}.sidebar-section{padding:14px 8px 4px}.sidebar-section-label{font:600 9px/1 var(--mono);letter-spacing:.1em;text-transform:uppercase;color:var(--muted);padding:0 8px 6px;display:block;opacity:.7}.sidebar-item{display:flex;align-items:center;justify-content:space-between;padding:6px 8px;border-radius:var(--radius);color:var(--text2);font:500 12px/1.2 var(--font);text-decoration:none;transition:background .1s,color .1s;white-space:nowrap;margin-bottom:1px;position:relative}.sidebar-item:hover{background:rgba(255,255,255,.04);color:var(--text)}.sidebar-item.active{background:rgba(99,102,241,.1);color:var(--hi)}.sidebar-item.active::before{content:'';position:absolute;left:0;top:4px;bottom:4px;width:2px;background:var(--accent);border-radius:0 1px 1px 0}.sidebar-item-left{display:flex;align-items:center;gap:8px}.sidebar-icon{font-size:11px;width:14px;text-align:center;opacity:.7}.sidebar-badge{font:600 9px var(--mono);padding:1px 5px;border-radius:100px;background:rgba(99,102,241,.15);color:var(--accent2)}.sidebar-status{display:inline-block;width:5px;height:5px;border-radius:50%;background:var(--border2)}.sidebar-status.s-ok{background:var(--green)}.sidebar-status.s-warn{background:var(--gold);animation:live-beat 3s ease-in-out infinite}.sidebar-status.s-err{background:var(--error);animation:live-beat 1.5s ease-in-out infinite}.sidebar-footer{margin-top:auto;padding:10px 12px;border-top:1px solid var(--border)}.sidebar-version{font:600 10px var(--mono);color:var(--muted);display:block}.sidebar-constitution{font:400 10px var(--mono);color:var(--border2);display:block;margin-top:2px}main{grid-row:2;grid-column:2;padding:18px 22px;overflow-x:hidden}.page-header{display:flex;align-items:flex-start;justify-content:space-between;margin-bottom:14px;gap:12px}.page-title{font:700 1.05rem/1.2 var(--font);color:var(--text);letter-spacing:-.02em;margin-bottom:2px}.page-sub{font:400 10px var(--mono);color:var(--muted)}.section-title{font:600 9px/1 var(--mono);letter-spacing:.1em;text-transform:uppercase;color:var(--muted);padding-bottom:6px;border-bottom:1px solid var(--border);margin:18px 0 10px}.mode-banner{display:flex;align-items:center;gap:12px;padding:10px 14px;border-radius:var(--radius2);border:1px solid var(--border);background:var(--gradient-card);margin-bottom:14px;position:relative;overflow:hidden}.mode-banner::before{content:'';position:absolute;left:0;top:0;bottom:0;width:3px}.mode-banner.mode-normal::before{background:var(--green)}.mode-banner.mode-degraded::before{background:var(--gold)}.mode-banner.mode-readonly::before,.mode-banner.mode-quarantined::before{background:var(--error)}.mode-banner.mode-recovery::before{background:var(--cyan)}.mode-banner.mode-maintenance::before{background:var(--purple)}.mode-banner-pulse{position:relative;width:26px;height:26px;flex-shrink:0}.mode-banner-pulse::before,.mode-banner-pulse::after{content:'';position:absolute;inset:0;border-radius:50%;background:var(--green);opacity:0;animation:pulse-ring 3s ease-out infinite}.mode-banner-pulse::after{animation-delay:1.5s}.mode-banner-pulse-dot{position:absolute;inset:8px;border-radius:50%;background:var(--green)}@keyframes pulse-ring{0%{transform:scale(.3);opacity:.7}100%{transform:scale(1.8);opacity:0}}.mode-banner-info{flex:1;display:flex;align-items:center;gap:12px}.mode-banner-state{font:700 12px/1 var(--mono);color:var(--green);letter-spacing:.05em}.mode-banner-desc{font:400 10px var(--mono);color:var(--muted)}.mode-banner-action{font:500 11px var(--font);color:var(--accent2);text-decoration:none;white-space:nowrap}.mode-banner-action:hover{text-decoration:underline}.metric-grid{display:grid;grid-template-columns:repeat(6,1fr);gap:8px;margin-bottom:14px}.metric-card{background:var(--gradient-card);border:1px solid var(--border);border-radius:var(--radius2);padding:12px 14px 10px;position:relative;overflow:hidden;transition:border-color .2s,box-shadow .2s}.metric-card::before{content:'';position:absolute;top:0;left:0;right:0;height:1px;background:linear-gradient(90deg,transparent,rgba(99,102,241,.3),transparent)}.metric-card:hover{border-color:rgba(99,102,241,.35);box-shadow:0 0 0 1px rgba(99,102,241,.08),0 8px 32px rgba(0,0,0,.5),inset 0 1px 0 rgba(255,255,255,.04)}.metric-card.card-primary{border-color:rgba(99,102,241,.3)}.metric-card.card-primary::before{background:linear-gradient(90deg,var(--accent),var(--hi));height:2px}.metric-label{font:600 9px/1 var(--mono);letter-spacing:.08em;text-transform:uppercase;color:var(--muted);margin-bottom:5px}.metric-val{font:900 2.8rem/1 var(--font);letter-spacing:-.05em;color:var(--text);margin-bottom:3px;white-space:nowrap}.metric-val.v-accent{color:var(--accent2)}.metric-val.v-ok{color:var(--green)}.metric-val.v-warn{color:var(--gold)}.metric-val.v-err{color:var(--error)}.metric-sub{font:400 10px var(--mono);color:var(--muted)}.metric-trend{display:flex;align-items:center;gap:4px;font:500 10px var(--mono);margin-top:4px}.trend-up{color:var(--green)}.trend-flat{color:var(--muted)}.sparkline{display:block;width:100%;height:22px;margin-top:6px;overflow:visible}.storage-bar{height:2px;background:var(--border2);border-radius:1px;margin-top:6px;overflow:hidden}.storage-fill{height:100%;border-radius:1px;background:linear-gradient(90deg,var(--accent),var(--hi))}.depth-card{background:var(--gradient-card);border:1px solid var(--border);border-radius:var(--radius2);box-shadow:inset 0 1px 0 rgba(255,255,255,.03),0 4px 24px rgba(0,0,0,.4);overflow:hidden}.depth-card-header{display:flex;align-items:center;justify-content:space-between;padding:8px 14px;border-bottom:1px solid var(--border);background:rgba(255,255,255,.01)}.depth-card-label{font:600 9px/1 var(--mono);letter-spacing:.1em;text-transform:uppercase;color:var(--muted)}.depth-card-body{padding:10px 14px}.two-col{display:grid;grid-template-columns:1fr 1fr;gap:10px;margin-bottom:14px}.three-col{display:grid;grid-template-columns:1fr 1fr 1fr;gap:8px;margin-bottom:14px}.panel-card{background:var(--gradient-card);border:1px solid var(--border);border-radius:var(--radius2);padding:10px 14px;box-shadow:inset 0 1px 0 rgba(255,255,255,.03)}.panel-card-title{font:600 9px/1 var(--mono);letter-spacing:.1em;text-transform:uppercase;color:var(--muted);margin-bottom:8px}.policy-row{display:flex;align-items:center;gap:8px;padding:4px 6px;border-radius:var(--radius);font-size:12px;transition:background .1s}.policy-row:hover{background:rgba(255,255,255,.03)}.policy-check{font-size:11px;width:14px;flex-shrink:0}.policy-row-name{flex:1;font:500 11px var(--mono);color:var(--text)}.policy-row-category{font:400 10px var(--mono);color:var(--muted);min-width:76px}.policy-row-severity{font:600 10px var(--mono);min-width:52px}.policy-row-result{font:700 11px var(--mono);min-width:38px;text-align:right}.slo-row{padding:4px 0;border-bottom:1px solid rgba(26,34,54,.7)}.slo-row:last-child{border-bottom:none}.slo-row-top{display:flex;align-items:center;justify-content:space-between;margin-bottom:3px}.slo-name{font:400 10px var(--mono);color:var(--text2)}.slo-pct{font:700 10px var(--mono);color:var(--green)}.slo-bar{height:2px;background:var(--border2);border-radius:1px;overflow:hidden}.slo-fill{height:100%;border-radius:1px;background:var(--green);transition:width .6s cubic-bezier(.4,0,.2,1)}.slo-fill.f-warn{background:var(--gold)}.slo-fill.f-err{background:var(--error)}.event-row{display:flex;align-items:baseline;gap:10px;padding:3px 0;border-bottom:1px solid rgba(26,34,54,.5);font-size:11px}.event-row:last-child{border-bottom:none}.event-time{font:400 10px var(--mono);color:var(--muted);white-space:nowrap;flex-shrink:0}.event-type{font:600 10px var(--mono);color:var(--accent2);flex-shrink:0}.event-payload{font:400 10px var(--mono);color:var(--text2);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.event-cursor{display:inline-block;width:6px;height:11px;background:var(--accent);border-radius:1px;animation:blink-cursor .9s step-end infinite;vertical-align:middle;margin-left:2px}@keyframes blink-cursor{0%,100%{opacity:1}50%{opacity:0}}.topo-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:6px}.topo-node{display:flex;align-items:center;gap:8px;padding:8px 10px;background:var(--surface2);border:1px solid var(--border);border-radius:var(--radius);transition:border-color .15s,box-shadow .15s}.topo-node.topo-ok{border-color:rgba(16,185,129,.2)}.topo-node.topo-ok:hover{box-shadow:0 0 0 1px rgba(16,185,129,.25),0 4px 16px var(--glow-green);border-color:rgba(16,185,129,.35)}.topo-node.topo-warn{border-color:rgba(245,158,11,.2)}.topo-node.topo-warn:hover{box-shadow:0 0 0 1px rgba(245,158,11,.25),0 4px 16px var(--glow-gold);border-color:rgba(245,158,11,.35)}.topo-dot{width:7px;height:7px;border-radius:50%;flex-shrink:0}.topo-dot.d-ok{background:var(--green);animation:live-beat 4s ease-in-out infinite}.topo-dot.d-warn{background:var(--gold);animation:live-beat 2s ease-in-out infinite}.topo-info{display:flex;flex-direction:column;gap:2px}.topo-name{font:600 11px var(--font);color:var(--text)}.topo-status{font:400 10px var(--mono);color:var(--muted)}.thresh-grid{display:grid;grid-template-columns:repeat(4,1fr);gap:8px;margin-bottom:14px}.thresh-item{padding:8px 12px;background:var(--gradient-card);border:1px solid var(--border);border-radius:var(--radius2);display:flex;flex-direction:column;gap:3px}.thresh-name{font:600 9px var(--mono);letter-spacing:.06em;text-transform:uppercase;color:var(--muted)}.thresh-val{font:800 1.4rem/1 var(--font);color:var(--text);letter-spacing:-.03em}.thresh-ok{color:var(--green);font:700 10px var(--mono)}.thresh-fail{color:var(--error);font:700 10px var(--mono)}.thresh-limit{font:400 9px var(--mono);color:var(--muted)}.action-row{display:flex;flex-wrap:wrap;gap:6px;margin-bottom:14px}.btn{display:inline-flex;align-items:center;gap:5px;padding:5px 12px;background:rgba(255,255,255,.04);border:1px solid var(--border2);border-radius:var(--radius);color:var(--text2);font:500 12px var(--font);cursor:pointer;text-decoration:none;transition:border-color .12s,background .12s,color .12s}.btn:hover,.btn:focus-visible{border-color:var(--accent);background:rgba(99,102,241,.08);color:var(--hi);outline:2px solid var(--accent);outline-offset:2px}.btn.btn-primary{background:linear-gradient(135deg,var(--accent) 0%,#4f46e5 100%);border-color:var(--accent);color:#fff;box-shadow:0 2px 8px rgba(99,102,241,.3)}.btn.btn-primary:hover{box-shadow:0 4px 16px rgba(99,102,241,.4)}.data-table{width:100%;border-collapse:collapse;font-size:12px}.data-table th{text-align:left;font:600 9px var(--mono);letter-spacing:.08em;text-transform:uppercase;color:var(--muted);padding:6px 10px;border-bottom:1px solid var(--border)}.data-table td{padding:5px 10px;border-bottom:1px solid rgba(26,34,54,.5);vertical-align:middle;max-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.data-table tr:last-child td{border-bottom:none}.data-table tr:hover td{background:rgba(255,255,255,.02)}.data-table td a{color:var(--accent2);text-decoration:none}.data-table td a:hover{text-decoration:underline}.action-msg{display:none;padding:6px 10px;background:rgba(16,185,129,.08);border:1px solid rgba(16,185,129,.2);border-radius:var(--radius);font:400 12px var(--font);margin-bottom:10px}.action-msg.visible{display:block}.links-row{display:flex;flex-wrap:wrap;gap:12px;margin-top:8px}.links-row a{color:var(--accent2);font-size:12px;text-decoration:none}.links-row a:hover{text-decoration:underline}.admin-footer{margin-top:28px;padding-top:14px;border-top:1px solid var(--border);font:400 10px var(--mono);color:var(--muted);display:flex;align-items:center;justify-content:space-between}.modal-backdrop{display:none;position:fixed;inset:0;z-index:1000;background:rgba(0,0,0,.85);backdrop-filter:blur(6px);align-items:center;justify-content:center}.modal-backdrop.open{display:flex}.modal{background:var(--surface);border:1px solid var(--border2);border-radius:var(--radius2);padding:20px;min-width:300px;max-width:420px;width:90%;box-shadow:0 24px 64px rgba(0,0,0,.8)}.modal-title{display:flex;align-items:center;justify-content:space-between;font:600 13px var(--font);margin-bottom:14px}.modal-close{background:none;border:none;color:var(--muted);cursor:pointer;font-size:16px;padding:4px;border-radius:var(--radius);line-height:1}.modal-close:hover,.modal-close:focus-visible{color:var(--text);outline:2px solid var(--accent);outline-offset:2px}.shortcut-list{list-style:none;display:flex;flex-direction:column;gap:6px}.shortcut-item{display:flex;align-items:center;justify-content:space-between;font-size:12px;padding:5px 0;border-bottom:1px solid var(--border)}.shortcut-item:last-child{border-bottom:none}.shortcut-desc{color:var(--text)}kbd{display:inline-block;padding:2px 6px;background:var(--surface2);border:1px solid var(--border2);border-radius:3px;font:500 10px var(--mono);color:var(--text);min-width:20px;text-align:center}.stat-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(130px,1fr));gap:8px;margin-bottom:14px}.stat-card{background:var(--gradient-card);border:1px solid var(--border);border-radius:var(--radius2);padding:12px 14px;position:relative;overflow:hidden;transition:box-shadow .2s,border-color .2s}.stat-card:hover{box-shadow:0 0 0 1px rgba(99,102,241,.2),0 8px 32px rgba(0,0,0,.5);border-color:rgba(99,102,241,.3)}.stat-card.stat-primary::before{content:'';position:absolute;top:0;left:0;right:0;height:2px;background:linear-gradient(90deg,var(--accent),var(--hi))}.stat-val{font:900 2.8rem/1 var(--font);color:var(--text);margin-bottom:4px;letter-spacing:-.05em}.stat-val.stat-ok{color:var(--green)}.stat-val.stat-warn{color:var(--gold)}.stat-val.stat-err{color:var(--error)}.stat-lbl{font:600 9px var(--mono);letter-spacing:.08em;text-transform:uppercase;color:var(--muted)}.stat-sub{font:400 10px var(--mono);color:var(--muted);margin-top:4px}.stat-icon{position:absolute;top:10px;right:12px;font-size:14px;opacity:.12}.section-header{display:flex;align-items:center;justify-content:space-between;margin:18px 0 10px;padding-bottom:6px;border-bottom:1px solid var(--border)}.section-header-title{font:600 9px/1 var(--mono);letter-spacing:.1em;text-transform:uppercase;color:var(--muted)}.section-header-actions{display:flex;gap:8px}.health-dot{display:inline-block;width:8px;height:8px;border-radius:50%;flex-shrink:0}.health-dot.hd-ok{background:var(--green)}.health-dot.hd-warn{background:var(--gold);animation:live-beat 2s ease-in-out infinite}.health-dot.hd-err{background:var(--error);animation:live-beat 1.5s ease-in-out infinite}a:focus-visible,button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}@media(max-width:1200px){.metric-grid{grid-template-columns:repeat(3,1fr)}}@media(max-width:768px){.app-shell{grid-template-columns:1fr;grid-template-rows:var(--topbar-h) 1fr auto}.topbar{grid-column:1}.topbar-center{display:none}.sidebar{grid-row:3;grid-column:1;position:static;height:auto;flex-direction:row;overflow-x:auto;border-right:none;border-top:1px solid var(--border);padding:0}.sidebar-section{display:flex;padding:6px 4px}.sidebar-section-label{display:none}.sidebar-footer{display:none}.sidebar-item{padding:5px 8px;font-size:11px}main{grid-row:2;grid-column:1;padding:12px}.metric-grid{grid-template-columns:repeat(2,1fr)}.two-col,.three-col{grid-template-columns:1fr}.thresh-grid{grid-template-columns:repeat(2,1fr)}.topo-grid{grid-template-columns:repeat(2,1fr)}}@media(max-width:480px){.metric-grid{grid-template-columns:repeat(2,1fr)}}`

const hcCSSMin = `@media(prefers-contrast:more){:root{--bg:#000;--surface:#0a0a0a;--border:#fff;--text:#fff;--muted:#ccc;--accent:#6699ff}.btn{border-width:2px!important}.stat-card{border-width:2px!important}.thresh-ok{color:#00ff88!important;font-weight:700!important}.thresh-fail{color:#ff4444!important;font-weight:700!important}}
@media(forced-colors:active){*:focus-visible{outline:3px solid Highlight!important;outline-offset:2px!important}.btn,button{forced-color-adjust:none;background:ButtonFace!important;color:ButtonText!important;border:2px solid ButtonBorder!important}.storage-fill{background:Highlight!important;forced-color-adjust:none}}`
