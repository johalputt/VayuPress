package render

import (
	"html/template"
	"net/url"
	"strings"
	"time"
)

// TagInfo is a single tag entry rendered on the public tag index page.
type TagInfo struct {
	Name  string
	Count int
}

// tagFuncs are the template helpers shared by the tag index and tag pages.
// tagURL path-escapes a tag so links survive spaces, slashes, and other
// characters that are legal in a tag but not in a raw URL path segment.
var tagFuncs = template.FuncMap{
	"humanDate": func(t time.Time) string { return t.Format("2 January 2006") },
	"shortDate": func(t time.Time) string { return t.UTC().Format("2006-01-02") },
	"tagURL":    func(s string) string { return url.PathEscape(s) },
	"plural": func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return many
	},
}

// tagIndexPage is the view model for the /tags topic-index page.
type tagIndexPage struct {
	Domain              string
	Version             string
	PicoCSSLink         template.HTML
	CustomCSSLink       template.HTML
	ArticleCSSLink      template.HTML
	HighContrastCSSLink template.HTML
	ThemeCSSLink        template.HTML
	HeadMeta            template.HTML
	ThemeToggleJSLink   template.HTML
	SiteName            string
	NavLinks            template.HTML
	Footer              template.HTML
	Tags                []TagInfo
	TotalPosts          int
}

// tagPage is the view model for a single /tags/{tag} listing page.
type tagPage struct {
	Domain              string
	Version             string
	PicoCSSLink         template.HTML
	CustomCSSLink       template.HTML
	ArticleCSSLink      template.HTML
	HighContrastCSSLink template.HTML
	ThemeCSSLink        template.HTML
	HeadMeta            template.HTML
	ThemeToggleJSLink   template.HTML
	PostCardMediaJSLink template.HTML
	SiteName            string
	NavLinks            template.HTML
	Footer              template.HTML
	Tag                 string
	Articles            []HomeArticle
	TotalCount          int
}

var tagIndexTmpl = template.Must(template.New("tagindex").Funcs(tagFuncs).Parse(`<!DOCTYPE html><html lang="en" data-theme="dark"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Topics — {{if .SiteName}}{{.SiteName}}{{else}}{{.Domain}}{{end}}</title>
<meta name="description" content="Browse every topic on {{if .SiteName}}{{.SiteName}}{{else}}{{.Domain}}{{end}} — {{len .Tags}} {{plural (len .Tags) "topic" "topics"}} across {{.TotalPosts}} {{plural .TotalPosts "post" "posts"}}.">
<meta name="generator" content="VayuPress {{.Version}}">
<link rel="canonical" href="https://{{.Domain}}/tags">
<meta property="og:type" content="website"><meta property="og:title" content="Topics — {{if .SiteName}}{{.SiteName}}{{else}}{{.Domain}}{{end}}">
<meta property="og:url" content="https://{{.Domain}}/tags">
{{.PicoCSSLink}}{{.CustomCSSLink}}{{.ArticleCSSLink}}{{.HighContrastCSSLink}}{{.ThemeCSSLink}}{{.HeadMeta}}{{.ThemeToggleJSLink}}
<link rel="manifest" href="/manifest.json">
<link rel="icon" type="image/png" href="/static/favicon-dark.png" media="(prefers-color-scheme: light)">
<link rel="icon" type="image/png" href="/static/favicon-light.png" media="(prefers-color-scheme: dark)">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
<script defer src="/static/vp-analytics.js"></script>
<script defer src="/static/js/portal.js"></script>
</head><body>
<a href="#main-content" class="skip-link">Skip to main content</a>
<div class="container">
<nav class="vayu-nav" aria-label="Primary">
  <a href="/" class="vayu-nav-brand"><img src="/static/favicon-light.png" alt="" width="24" height="24">{{if .SiteName}}{{.SiteName}}{{else}}VayuPress{{end}}</a>
  <div class="vayu-nav-links">
    {{.NavLinks}}
    <button type="button" id="vayu-theme-toggle" class="vayu-theme-toggle" aria-label="Toggle theme">☾</button>
  </div>
</nav>
<main id="main-content">
<section class="vayu-hero">
  <span class="vayu-hero-eyebrow">Topics</span>
  <h1>Browse by topic</h1>
  <p class="vayu-hero-tagline">{{len .Tags}} {{plural (len .Tags) "topic" "topics"}} across {{.TotalPosts}} published {{plural .TotalPosts "post" "posts"}}. Pick a thread and follow it.</p>
</section>
<div class="vayu-section-label">All topics</div>
{{if .Tags}}<div class="vayu-tag-cloud">
{{range .Tags}}<a class="vayu-tag vayu-tag--cloud" href="/tags/{{tagURL .Name}}">#{{.Name}}<span class="vayu-tag-count">{{.Count}}</span></a>
{{end}}</div>{{else}}<div class="vayu-empty">No topics yet. Tags will appear here as posts are published.</div>{{end}}
{{.Footer}}
</main>
</div></body></html>`))

var tagPageTmpl = template.Must(template.New("tagpage").Funcs(tagFuncs).Parse(`<!DOCTYPE html><html lang="en" data-theme="dark"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>#{{.Tag}} — {{if .SiteName}}{{.SiteName}}{{else}}{{.Domain}}{{end}}</title>
<meta name="description" content="{{.TotalCount}} {{plural .TotalCount "post" "posts"}} tagged “{{.Tag}}” on {{if .SiteName}}{{.SiteName}}{{else}}{{.Domain}}{{end}}.">
<meta name="generator" content="VayuPress {{.Version}}">
<link rel="canonical" href="https://{{.Domain}}/tags/{{tagURL .Tag}}">
<meta property="og:type" content="website"><meta property="og:title" content="#{{.Tag}} — {{if .SiteName}}{{.SiteName}}{{else}}{{.Domain}}{{end}}">
<meta property="og:url" content="https://{{.Domain}}/tags/{{tagURL .Tag}}">
{{.PicoCSSLink}}{{.CustomCSSLink}}{{.ArticleCSSLink}}{{.HighContrastCSSLink}}{{.ThemeCSSLink}}{{.HeadMeta}}{{.ThemeToggleJSLink}}
<link rel="manifest" href="/manifest.json">
<link rel="icon" type="image/png" href="/static/favicon-dark.png" media="(prefers-color-scheme: light)">
<link rel="icon" type="image/png" href="/static/favicon-light.png" media="(prefers-color-scheme: dark)">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
<script defer src="/static/vp-analytics.js"></script>
<script defer src="/static/js/portal.js"></script>
</head><body>
<a href="#main-content" class="skip-link">Skip to main content</a>
<div class="container">
<nav class="vayu-nav" aria-label="Primary">
  <a href="/" class="vayu-nav-brand"><img src="/static/favicon-light.png" alt="" width="24" height="24">{{if .SiteName}}{{.SiteName}}{{else}}VayuPress{{end}}</a>
  <div class="vayu-nav-links">
    {{.NavLinks}}
    <button type="button" id="vayu-theme-toggle" class="vayu-theme-toggle" aria-label="Toggle theme">☾</button>
  </div>
</nav>
<main id="main-content">
<section class="vayu-hero">
  <span class="vayu-hero-eyebrow">Topic</span>
  <h1>#{{.Tag}}</h1>
  <p class="vayu-hero-tagline">{{.TotalCount}} {{plural .TotalCount "post" "posts"}} tagged “{{.Tag}}”.</p>
</section>
<div class="vayu-section-label"><a class="vayu-tag-back" href="/tags">← All topics</a></div>
{{if .Articles}}<div class="vayu-post-list">
{{range .Articles}}<a class="vayu-post-card{{if .Image}} vayu-post-card--media{{end}}" href="/{{.Slug}}">
  {{if .Image}}<div class="vayu-post-thumb"><img src="{{.Image}}" alt="" loading="lazy" decoding="async"></div>{{end}}
  <div class="vayu-post-body">
    <div class="vayu-post-meta"><time datetime="{{.CreatedAt | shortDate}}">{{.CreatedAt | humanDate}}</time>{{if .Author}}<span class="vayu-post-dot" aria-hidden="true"></span><span class="vayu-post-author">{{.Author}}</span>{{end}}</div>
    <h2 class="vayu-post-title">{{.Title}}</h2>
    {{if .Excerpt}}<p class="vayu-post-excerpt">{{.Excerpt}}</p>{{end}}
  </div>
</a>{{end}}
</div>{{else}}<div class="vayu-empty">No posts tagged “{{.Tag}}” yet.</div>{{end}}
{{.Footer}}
</main>
</div>{{.PostCardMediaJSLink}}</body></html>`))

// RenderTagIndex renders the public topic-index page listing every tag with its
// post count. tags should already be sorted by the caller (count desc, name asc).
func RenderTagIndex(domain, version string, tags []TagInfo, totalPosts int) (string, error) {
	var buf strings.Builder
	s := getActiveSettings()
	err := tagIndexTmpl.Execute(&buf, tagIndexPage{
		Domain:              domain,
		Version:             version,
		PicoCSSLink:         PicoCSSLink(),
		CustomCSSLink:       CustomCSSLink(),
		ArticleCSSLink:      ArticleCSSLink(),
		HighContrastCSSLink: HighContrastCSSLink(),
		ThemeCSSLink:        ThemeCSSLink(),
		HeadMeta:            headMetaHTML(s),
		ThemeToggleJSLink:   ThemeToggleJSLink(),
		SiteName:            s.Name,
		NavLinks:            navLinksHTML(s.NavJSON),
		Footer:              footerHTML(s),
		Tags:                tags,
		TotalPosts:          totalPosts,
	})
	return buf.String(), err
}

// RenderTagPage renders a single tag's listing page from the published articles
// carrying that tag. totalCount is the number of matching posts.
func RenderTagPage(domain, version, tag string, articles []HomeArticle, totalCount int) (string, error) {
	var buf strings.Builder
	s := getActiveSettings()
	err := tagPageTmpl.Execute(&buf, tagPage{
		Domain:              domain,
		Version:             version,
		PicoCSSLink:         PicoCSSLink(),
		CustomCSSLink:       CustomCSSLink(),
		ArticleCSSLink:      ArticleCSSLink(),
		HighContrastCSSLink: HighContrastCSSLink(),
		ThemeCSSLink:        ThemeCSSLink(),
		HeadMeta:            headMetaHTML(s),
		ThemeToggleJSLink:   ThemeToggleJSLink(),
		PostCardMediaJSLink: PostCardMediaJSLink(),
		SiteName:            s.Name,
		NavLinks:            navLinksHTML(s.NavJSON),
		Footer:              footerHTML(s),
		Tag:                 tag,
		Articles:            articles,
		TotalCount:          totalCount,
	})
	return buf.String(), err
}

// TagPageCacheRel returns the cache-relative path for a tag page, or ok=false
// when the tag is not safe to use as a filesystem path component. This mirrors
// the tags/<tag>.html convention already used by CachePurge so per-tag pages are
// invalidated automatically when an article carrying that tag changes.
func TagPageCacheRel(tag string) (string, bool) {
	if tag == "" || unsafePathComponent(tag) {
		return "", false
	}
	return "tags/" + tag + ".html", true
}
