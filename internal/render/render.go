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

const articleCSSMin = `:root{--bg:#0B0F14;--surface:#111827;--border:#1F2937;--text:#E5E7EB;--muted:#9CA3AF;--accent:#3B82F6;--hi:#38BDF8;--max-w:720px;--font:'Inter',system-ui,sans-serif;--mono:'IBM Plex Mono',monospace;--radius:4px;--sp1:4px;--sp2:8px;--sp3:16px;--sp4:24px;--sp5:32px;--sp6:48px}
@media(prefers-color-scheme:light){:root{--bg:#fff;--surface:#F9FAFB;--border:#E5E7EB;--text:#111827;--muted:#6B7280}}
@media(prefers-reduced-motion:reduce){*,*::before,*::after{transition:none!important;animation:none!important}}
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
.skip-link{position:absolute;top:-40px;left:0;z-index:9999;background:var(--accent);color:#fff;padding:var(--sp2) var(--sp3);font:500 13px/1.4 var(--font);text-decoration:none;transition:top .2s}.skip-link:focus{top:0;outline:3px solid var(--hi);outline-offset:2px}
body{background:var(--bg);color:var(--text);font:400 18px/1.6 var(--font);padding:var(--sp5) var(--sp3)}
.container{max-width:var(--max-w);margin:0 auto}
header{border-bottom:1px solid var(--border);padding-bottom:var(--sp5);margin-bottom:var(--sp5)}
h1{font:700 2rem/1.2 var(--font);margin-bottom:var(--sp2);letter-spacing:-.5px}
.meta{color:var(--muted);font-size:13px;display:flex;flex-wrap:wrap;gap:var(--sp2)}
.tags a{display:inline-block;padding:2px var(--sp2);border:1px solid var(--border);border-radius:var(--radius);font-size:12px;color:var(--accent);text-decoration:none}
.tags a:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
.content{margin-top:var(--sp5)}.content h2,.content h3{font:600 1.25rem/1.3 var(--font);margin:var(--sp5) 0 var(--sp3)}
.content pre{background:var(--surface);border:1px solid var(--border);border-radius:var(--radius);padding:var(--sp3);overflow-x:auto;font:400 14px/1.5 var(--mono);margin:var(--sp3) 0}
.content code{background:var(--surface);padding:2px 6px;border-radius:var(--radius);font:400 14px var(--mono)}.content pre code{background:none;padding:0}
.content blockquote{border-left:4px solid var(--accent);padding-left:var(--sp3);color:var(--muted);margin:var(--sp3) 0}
footer{margin-top:var(--sp6);padding-top:var(--sp5);border-top:1px solid var(--border);font-size:13px;color:var(--muted)}
a:focus-visible{outline:2px solid var(--accent);outline-offset:2px;border-radius:2px}
@media(max-width:480px){body{padding:var(--sp3)}h1{font-size:1.5rem}}`

const adminCSSMin = `:root{--bg:#0B0F14;--surface:#111827;--surface2:#161f2e;--border:#1F2937;--border2:#2d3a4a;--text:#E5E7EB;--muted:#9CA3AF;--accent:#3B82F6;--hi:#38BDF8;--success:#10B981;--warn:#F59E0B;--error:#EF4444;--font:'Inter',system-ui,sans-serif;--mono:'IBM Plex Mono',monospace;--radius:4px;--sp1:4px;--sp2:8px;--sp3:16px;--sp4:24px;--sp5:32px}
@media(prefers-reduced-motion:reduce){*,*::before,*::after{transition:none!important;animation:none!important}}
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
body{background:var(--bg);color:var(--text);font:400 14px/1.5 var(--font);min-height:100vh}
.skip-link{position:absolute;top:-40px;left:0;z-index:9999;background:var(--accent);color:#fff;padding:var(--sp2) var(--sp3);font-weight:500;text-decoration:none;transition:top .15s}.skip-link:focus{top:0;outline:3px solid var(--hi);outline-offset:2px}
.app-shell{display:grid;grid-template-rows:auto 1fr;min-height:100vh}
.topbar{display:flex;align-items:center;justify-content:space-between;padding:var(--sp3) var(--sp4);background:var(--surface);border-bottom:1px solid var(--border);position:sticky;top:0;z-index:100}
.topbar-brand{display:flex;align-items:center;gap:var(--sp2);font-weight:600;font-size:15px;color:var(--text);text-decoration:none}
.topbar-domain{color:var(--muted);font-size:12px;font-weight:400}
.topbar-actions{display:flex;align-items:center;gap:var(--sp2)}
.kbd-hint{font:400 11px var(--mono);color:var(--muted);background:var(--surface2);border:1px solid var(--border2);border-radius:var(--radius);padding:2px 6px;cursor:pointer;transition:border-color .15s,color .15s}
.kbd-hint:hover,.kbd-hint:focus-visible{border-color:var(--accent);color:var(--text);outline:2px solid var(--accent);outline-offset:2px}
main{padding:var(--sp4);max-width:1100px}
.section-title{font-size:10px;font-weight:600;letter-spacing:.08em;text-transform:uppercase;color:var(--muted);margin:var(--sp4) 0 var(--sp3);padding-bottom:var(--sp2);border-bottom:1px solid var(--border)}
.stat-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:var(--sp3);margin-bottom:var(--sp4)}
.stat-card{background:var(--surface);border:1px solid var(--border);border-radius:var(--radius);padding:var(--sp3)}
.stat-val{font:700 1.875rem/1 var(--font);color:var(--accent);margin-bottom:4px}
.stat-val.stat-ok{color:var(--success)}.stat-val.stat-warn{color:var(--warn)}.stat-val.stat-err{color:var(--error)}
.stat-lbl{font-size:11px;color:var(--muted)}.stat-sub{font-size:11px;color:var(--muted);margin-top:6px}
.storage-bar{height:3px;background:var(--border2);border-radius:2px;margin-top:8px;overflow:hidden}
.storage-fill{height:100%;border-radius:2px;background:var(--accent)}
.thresh-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:var(--sp2);margin-bottom:var(--sp4)}
.thresh-item{display:flex;align-items:center;justify-content:space-between;background:var(--surface);border:1px solid var(--border);border-radius:var(--radius);padding:var(--sp2) var(--sp3);font-size:12px}
.thresh-name{color:var(--muted)}.thresh-val{font:500 12px var(--mono);color:var(--text)}
.thresh-ok{color:var(--success);font-weight:600}.thresh-fail{color:var(--error);font-weight:600}
.action-row{display:flex;flex-wrap:wrap;gap:var(--sp2);margin-bottom:var(--sp4)}
.btn{display:inline-flex;align-items:center;gap:6px;padding:7px 14px;background:transparent;border:1px solid var(--border2);border-radius:var(--radius);color:var(--text);font:500 13px var(--font);cursor:pointer;text-decoration:none;transition:border-color .15s,background .15s,color .15s}
.btn:hover,.btn:focus-visible{border-color:var(--accent);background:rgba(59,130,246,.06);color:var(--hi);outline:2px solid var(--accent);outline-offset:2px}
.btn.btn-primary{background:var(--accent);border-color:var(--accent);color:#fff}
.data-table{width:100%;border-collapse:collapse;font-size:13px}
.data-table th{text-align:left;font-size:10px;font-weight:600;letter-spacing:.05em;text-transform:uppercase;color:var(--muted);padding:var(--sp2) var(--sp3);border-bottom:1px solid var(--border)}
.data-table td{padding:var(--sp2) var(--sp3);border-bottom:1px solid var(--border);vertical-align:middle;max-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.data-table tr:hover td{background:var(--surface2)}.data-table td a{color:var(--accent);text-decoration:none}
.action-msg{display:none;padding:var(--sp2) var(--sp3);background:var(--surface);border:1px solid var(--success);border-radius:var(--radius);font-size:13px;margin-bottom:var(--sp3)}
.action-msg.visible{display:block}.links-row{display:flex;flex-wrap:wrap;gap:var(--sp3);margin-top:var(--sp3)}
.links-row a{color:var(--accent);font-size:13px;text-decoration:none}.links-row a:hover{text-decoration:underline}
.admin-footer{margin-top:var(--sp5);padding-top:var(--sp4);border-top:1px solid var(--border);font-size:11px;color:var(--muted)}
.modal-backdrop{display:none;position:fixed;inset:0;z-index:1000;background:rgba(0,0,0,.7);align-items:center;justify-content:center}
.modal-backdrop.open{display:flex}.modal{background:var(--surface);border:1px solid var(--border2);border-radius:var(--radius);padding:var(--sp4);min-width:320px;max-width:480px;width:90%}
.modal-title{display:flex;align-items:center;justify-content:space-between;font-weight:600;font-size:14px;margin-bottom:var(--sp3)}
.modal-close{background:none;border:none;color:var(--muted);cursor:pointer;font-size:16px;padding:4px;border-radius:var(--radius);line-height:1}
.modal-close:hover,.modal-close:focus-visible{color:var(--text);outline:2px solid var(--accent);outline-offset:2px}
.shortcut-list{list-style:none;display:flex;flex-direction:column;gap:var(--sp2)}
.shortcut-item{display:flex;align-items:center;justify-content:space-between;font-size:13px;padding:var(--sp2) 0;border-bottom:1px solid var(--border)}
.shortcut-item:last-child{border-bottom:none}.shortcut-desc{color:var(--text)}
kbd{display:inline-block;padding:2px 6px;background:var(--surface2);border:1px solid var(--border2);border-radius:3px;font:500 11px var(--mono);color:var(--text);min-width:22px;text-align:center}
a:focus-visible,button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
@media(max-width:600px){.topbar{padding:var(--sp2) var(--sp3)}main{padding:var(--sp3)}.stat-grid{grid-template-columns:repeat(2,1fr)}}`

const hcCSSMin = `@media(prefers-contrast:more){:root{--bg:#000;--surface:#0a0a0a;--border:#fff;--text:#fff;--muted:#ccc;--accent:#6699ff}.btn{border-width:2px!important}.stat-card{border-width:2px!important}.thresh-ok{color:#00ff88!important;font-weight:700!important}.thresh-fail{color:#ff4444!important;font-weight:700!important}}
@media(forced-colors:active){*:focus-visible{outline:3px solid Highlight!important;outline-offset:2px!important}.btn,button{forced-color-adjust:none;background:ButtonFace!important;color:ButtonText!important;border:2px solid ButtonBorder!important}.storage-fill{background:Highlight!important;forced-color-adjust:none}}`
