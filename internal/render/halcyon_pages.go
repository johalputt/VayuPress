// SPDX-License-Identifier: Apache-2.0

package render

import (
	"fmt"
	"html/template"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/seo"
	"github.com/johalputt/vayupress/internal/theme"
)

// halcyon_pages.go — Halcyon's templates and the functions that fill them.
//
// Ad anchors. The ad injectors (cmd/vayupress injectArticleAds and
// injectHomeAds) splice slots into the rendered page at fixed strings, and
// these templates keep every one of them where the design wants the slot:
// `<main id="main-content">` (header slot, the top of the page's column),
// `<div class="content" itemprop="articleBody">` (above the post), `</article>`
// (below the post, before the end matter), `</main></div>` and `</main>`
// (footer slot, after the content and before the site footer), and
// `<div class="h-rail-ads" data-ads="sidebar"></div>` (the sidebar slot, which
// only Halcyon has a place for: the river's side column and the article rail).
// halcyon_test.go asserts each one is present.

// halcyonIcons are the interface glyphs, drawn on a 20-unit grid with a
// hairline stroke. They are decoration beside a word or inside a labelled
// control, so they carry no text of their own.
var halcyonIcons = map[string]string{
	"search": `<circle cx="9" cy="9" r="5.6"/><path d="m13.2 13.2 3.6 3.6"/>`,
	"sun":    `<circle cx="10" cy="10" r="3.4"/><path d="M10 2.6v1.6M10 15.8v1.6M2.6 10h1.6M15.8 10h1.6M4.8 4.8l1.1 1.1M14.1 14.1l1.1 1.1M4.8 15.2l1.1-1.1M14.1 5.9l1.1-1.1"/>`,
	"moon":   `<path d="M15.6 12.4A6.4 6.4 0 0 1 7.6 4.4a6.4 6.4 0 1 0 8 8z"/>`,
	"auto":   `<circle cx="10" cy="10" r="6.6"/><path d="M10 3.4a6.6 6.6 0 0 1 0 13.2z" fill="currentColor"/>`,
	"arrow":  `<path d="M4 10h11.5M11 5.5l4.5 4.5-4.5 4.5"/>`,
	"share":  `<path d="M10 3v9.4M6.6 6.2 10 2.8l3.4 3.4"/><path d="M6.4 8.6H5.6A1.6 1.6 0 0 0 4 10.2v5.2A1.6 1.6 0 0 0 5.6 17h8.8a1.6 1.6 0 0 0 1.6-1.6v-5.2a1.6 1.6 0 0 0-1.6-1.6h-.8"/>`,
	"link":   `<path d="M8.6 11.4a3.2 3.2 0 0 0 4.5 0l2.6-2.6a3.2 3.2 0 0 0-4.5-4.5l-.9.9"/><path d="M11.4 8.6a3.2 3.2 0 0 0-4.5 0l-2.6 2.6a3.2 3.2 0 0 0 4.5 4.5l.9-.9"/>`,
	"menu":   `<path d="M3.6 7.4h12.8M3.6 12.6h12.8"/>`,
	"list":   `<path d="M7.4 5.6h9M7.4 10h9M7.4 14.4h9"/><circle cx="4.2" cy="5.6" r=".6" fill="currentColor"/><circle cx="4.2" cy="10" r=".6" fill="currentColor"/><circle cx="4.2" cy="14.4" r=".6" fill="currentColor"/>`,
	"x":      `<path d="m5.4 5.4 9.2 9.2M14.6 5.4l-9.2 9.2"/>`,
	"back":   `<path d="M12.4 4.4 6.8 10l5.6 5.6"/>`,
}

var halcyonFuncs = template.FuncMap{
	"icon": func(name string) template.HTML {
		return template.HTML(`<svg class="h-i" viewBox="0 0 20 20" aria-hidden="true" focusable="false">` + halcyonIcons[name] + `</svg>`) //nolint:gosec // G203: constant markup
	},
	"blogBase":  BlogBase,
	"portalJS":  PortalJSLink,
	"pwaHead":   PWAHeadTags,
	"pwaJS":     PWARegisterJSLink,
	"humanDate": func(t time.Time) string { return config.FormatSite(t, "2 January 2006") },
	"shortDate": func(t time.Time) string { return config.FormatSite(t, "2006-01-02") },
	"isoDate":   func(t time.Time) string { return config.InSite(t).Format(time.RFC3339) },
	"tagURL":    func(s string) string { return "/tags/" + url.PathEscape(s) },
	"topicName": func(s string) string { return strings.ReplaceAll(s, "-", " ") },
	"caps":      func(on bool, s string) template.HTML { return capsText(on)(s) },
	"reason":    sharesReason,
	"num":       groupThousands,
	"plural": func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return many
	},
	"jsonAttr":     jsonAttr,
	"inc":          func(i int) int { return i + 1 },
	"firstTopicOf": firstTopic,
}

// groupThousands writes a count the way a reader reads it: 42,349.
func groupThousands(n int) string {
	s := strconv.Itoa(n)
	if n < 0 || len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

const halcyonShared = `
{{define "start"}}<!DOCTYPE html>
<html lang="en" class="h" data-face="{{.Cfg.Face}}" data-size="{{.Cfg.TextSize}}" data-site-face="{{.Cfg.Face}}" data-site-size="{{.Cfg.TextSize}}" data-appearance="{{.Cfg.Appearance}}"{{if eq .Cfg.Appearance "light"}} data-theme="light"{{else if eq .Cfg.Appearance "dark"}} data-theme="dark"{{end}}{{if .Cfg.Selection}} data-selection{{end}}><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.PageTitle}}</title>
{{if .Description}}<meta name="description" content="{{.Description}}">{{end}}
{{if .Robots}}<meta name="robots" content="{{.Robots}}">{{end}}
<meta name="generator" content="VayuPress {{.Version}}">
{{if .Canonical}}<link rel="canonical" href="{{.Canonical}}">{{end}}{{if .PrevURL}}
<link rel="prev" href="{{.PrevURL}}">{{end}}{{if .NextURL}}
<link rel="next" href="{{.NextURL}}">{{end}}
{{.ArticleHead}}
{{.FontPreload}}<link rel="stylesheet" href="{{.CSSHref}}"><link rel="stylesheet" href="/theme.css">
<script>{{.PrefsJS}}</script>
{{.HeadMeta}}{{pwaHead}}{{pwaJS}}{{.JSONLD}}
<link rel="alternate" type="application/rss+xml" title="{{.SiteName}}" href="/feed.xml">
<link rel="icon" type="image/png" href="/static/favicon-dark.png" media="(prefers-color-scheme: light)">
<link rel="icon" type="image/png" href="/static/favicon-light.png" media="(prefers-color-scheme: dark)">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
{{if .Analytics}}<script defer src="/static/vp-analytics.js"></script>{{end}}
{{portalJS}}
<script defer src="{{.JSHref}}"></script>
</head><body class="h-{{.Kind}}">
<a href="#main-content" class="h-skip">Skip to the content</a>
{{template "bar" .}}{{end}}

{{define "bar"}}<header class="h-bar" data-h-bar><div class="h-wrap h-bar-in">
{{if eq .Kind "article"}}<a class="h-ib h-only-phone" href="{{blogBase}}" aria-label="Back to {{.SiteName}}">{{icon "back"}}</a>{{end}}
<a class="h-name" href="{{blogBase}}">{{.SiteName}}</a>
{{if .Nav}}<nav class="h-links" aria-label="Primary">{{range .Nav}}<a href="{{.Href}}"{{if .External}} rel="noopener noreferrer"{{end}}>{{.Label}}</a>{{end}}</nav>{{end}}
<div class="h-tools">
{{if .ShowSearch}}<form class="h-search" method="get" action="/search" role="search" data-vayu-search><button class="h-ib" type="submit" aria-label="Search">{{icon "search"}}</button></form>{{end}}
<button class="h-ib h-only-wide h-js-only" type="button" data-h-cycle aria-label="Appearance: follows the device">{{icon "auto"}}</button>
{{if .ShowMembership}}<a class="h-signin h-only-wide" href="/members">Sign in</a><a class="h-btn h-btn-line h-only-wide" href="/signup">Subscribe</a>{{end}}
<button class="h-ib h-only-phone h-js-only" type="button" data-h-open="h-menu" aria-label="Menu" aria-expanded="false" aria-controls="h-menu">{{icon "menu"}}</button>
</div></div>{{if eq .Kind "article"}}<div class="h-line" data-h-line></div>{{end}}</header>{{end}}

{{define "schemes"}}<div class="h-seg" role="group" aria-label="Appearance" data-h-schemes><button type="button" data-h-scheme="auto">{{icon "auto"}}Auto</button><button type="button" data-h-scheme="light">{{icon "sun"}}Light</button><button type="button" data-h-scheme="dark">{{icon "moon"}}Dark</button></div>{{end}}

{{define "end"}}<footer class="h-foot"><div class="h-wrap">
<div class="h-foot-row">
<div class="h-foot-about"><a class="h-name" href="{{blogBase}}">{{.SiteName}}</a>{{if .About}}<p>{{.About}}</p>{{end}}{{if .Social}}<ul class="h-social">{{range .Social}}<li><a href="{{.Href}}"{{if .External}} rel="noopener noreferrer"{{end}}>{{.Label}}</a></li>{{end}}</ul>{{end}}</div>
{{range .FooterCols}}<div class="h-foot-col">{{if .Title}}<h2 class="h-label">{{.Title}}</h2>{{end}}<ul>{{range .Links}}<li><a href="{{.Href}}"{{if .External}} rel="noopener noreferrer"{{end}}>{{.Label}}</a></li>{{end}}</ul></div>{{end}}
</div>
<div class="h-foot-base"><span>{{.Copyright}}</span>{{if .Legal}}<nav class="h-legal" aria-label="Legal">{{range .Legal}}<a href="{{.Href}}"{{if .External}} rel="noopener noreferrer"{{end}}>{{.Label}}</a>{{end}}</nav>{{end}}<span class="h-pub">Published with <a href="https://vayupress.com" rel="noopener noreferrer">VayuPress</a></span><span class="h-js-only">{{template "schemes" .}}</span></div>
</div></footer>
<div class="h-scrim" data-h-scrim hidden></div>
<div class="h-bsheet" id="h-menu" role="dialog" aria-modal="true" aria-label="Menu" hidden><div class="h-grab" aria-hidden="true"></div>
<a class="h-it" href="{{blogBase}}">Home</a>{{range .Nav}}<a class="h-it" href="{{.Href}}"{{if .External}} rel="noopener noreferrer"{{end}}>{{.Label}}</a>{{end}}{{if .ShowMembership}}<a class="h-it" href="/members">Sign in</a>{{end}}
<div class="h-div" aria-hidden="true"></div><p class="h-label">Appearance</p>{{template "schemes" .}}
{{if .ShowMembership}}<a class="h-btn h-btn-ink h-btn-wide" href="/signup">Subscribe</a>{{end}}
</div>
{{.Scripts}}</body></html>{{end}}

{{define "meta"}}<div class="h-meta">{{with firstTopicOf .Tags}}<a class="h-topic" href="{{tagURL .}}">{{topicName .}}</a><span class="h-sep" aria-hidden="true">·</span>{{end}}<time datetime="{{shortDate .CreatedAt}}">{{humanDate .CreatedAt}}</time></div>{{end}}

{{define "pager"}}{{if gt .TotalPages 1}}<nav class="h-pager" aria-label="Pages">{{if .PrevURL}}<a rel="prev" href="{{.PrevURL}}">Newer</a>{{end}}<span>Page {{.Page}} of {{num .TotalPages}}</span>{{if .NextURL}}<a rel="next" href="{{.NextURL}}">Older writing {{icon "arrow"}}</a>{{end}}</nav>{{end}}{{end}}

{{define "months"}}{{$caps := .Cfg.SmallCaps}}{{range .Months}}<section class="h-month"><h2>{{.Label}}</h2><ol>{{range .Rows}}<li><a class="h-row" href="/{{.Slug}}"><span class="h-d">{{.Day}}</span><b>{{.Title}}</b><span class="h-t">{{topicName .Topic}}</span>{{if .WithExc}}<span class="h-x">{{caps $caps .Excerpt}}</span>{{end}}</a></li>{{end}}</ol></section>{{end}}{{end}}

{{define "trend"}}{{if or .Cfg.MostRead .Cfg.Pinned}}<section class="h-trend" aria-labelledby="h-trend-h" data-mostread="{{if .Cfg.MostRead}}on{{else}}off{{end}}" data-pinned="{{if .Cfg.Pinned}}on{{else}}off{{end}}"><h2 class="h-sec" id="h-trend-h">{{if .Cfg.MostRead}}Most read{{else}}Pinned{{end}}</h2><div data-vayu-trending hidden></div></section>{{end}}{{end}}

{{define "topicrow"}}{{if .Topics}}<nav class="h-topic-nav" aria-label="Topics">{{$cur := .Topic}}{{range .Topics}}<a href="{{tagURL .Name}}"{{if eq .Name $cur}} aria-current="page"{{end}}>{{topicName .Name}}<span class="h-n">{{num .Count}}</span></a>{{end}}<a href="/tags">All topics</a></nav>{{end}}{{end}}

{{define "subscribe"}}{{if and .Cfg.Subscribe .ShowMembership}}<section class="h-letter" aria-label="Subscribe"><p>One email when there is something new worth reading.</p><form method="POST" action="/members/login" class="h-letter-form"><label class="h-vh" for="h-letter-email">Email address</label><input class="h-field" id="h-letter-email" type="email" name="email" required autocomplete="email" placeholder="you@example.com"><button class="h-btn h-btn-ink" type="submit">Subscribe</button></form></section>{{end}}{{end}}
`

const halcyonHomeTmpl = `{{template "start" .}}<div class="h-page"><main id="main-content" class="h-wrap">
{{$caps := .Cfg.SmallCaps}}
{{if eq .Composition "empty"}}<p class="h-empty">Nothing is published here yet.</p>
{{else if eq .Composition "front"}}
{{if .Lead}}<section class="h-front">
<article class="h-lead">{{template "meta" .Lead}}<h2><a href="/{{.Lead.Slug}}">{{.Lead.Title}}</a></h2>{{if .Lead.Excerpt}}<p class="h-excerpt">{{caps $caps .Lead.Excerpt}}</p>{{end}}{{if .Lead.Author}}<p class="h-meta h-by">By {{.Lead.Author}}</p>{{end}}</article>
{{if .Also}}<aside class="h-also" aria-labelledby="h-also-h"><h2 class="h-label" id="h-also-h">Also new</h2><ol>{{range .Also}}<li>{{template "meta" .}}<a class="h-also-t" href="/{{.Slug}}">{{.Title}}</a></li>{{end}}</ol><div class="h-rail-ads" data-ads="sidebar"></div></aside>{{end}}
</section>{{end}}
{{if .Desks}}<div class="h-desks">{{range $i, $d := .Desks}}<section class="h-desk" aria-labelledby="h-desk-{{$i}}"><h2 id="h-desk-{{$i}}"><span class="h-num" aria-hidden="true">{{inc $i}}</span><a href="{{tagURL $d.Name}}">{{topicName $d.Name}}</a><span class="h-n">{{num $d.Count}}</span></h2><ol>{{range $d.Posts}}<li><a href="/{{.Slug}}">{{.Title}}</a><time class="h-meta" datetime="{{shortDate .CreatedAt}}">{{humanDate .CreatedAt}}</time></li>{{end}}</ol></section>{{end}}</div>{{end}}
{{template "trend" .}}
{{if .Months}}<section class="h-more" aria-labelledby="h-more-h"><h2 class="h-sec" id="h-more-h">More new writing</h2>{{template "months" .}}</section>{{end}}
{{template "subscribe" .}}
{{else if eq .Composition "river"}}
{{if and (eq .Page 1) .About}}<section class="h-intro"><p>{{.About}}</p></section>{{end}}
<div class="h-river"><div class="h-entries">{{range .River}}<article class="h-entry">{{template "meta" .}}<h2><a href="/{{.Slug}}">{{.Title}}</a></h2>{{if .Excerpt}}<p class="h-excerpt">{{caps $caps .Excerpt}}</p>{{end}}</article>{{end}}{{template "pager" .}}</div>
<aside class="h-side">{{template "trend" .}}{{if and .Cfg.Topics .Topics}}<section aria-labelledby="h-side-topics"><h2 class="h-label" id="h-side-topics">Topics</h2><div class="h-topics">{{range .Topics}}<a href="{{tagURL .Name}}">{{topicName .Name}}<span class="h-n">{{num .Count}}</span></a>{{end}}</div></section>{{end}}{{template "subscribe" .}}<div class="h-rail-ads" data-ads="sidebar"></div></aside></div>
{{else}}
<header class="h-ix-head"><div>{{if .Eyebrow}}<p class="h-eyebrow">{{if .EyebrowHref}}<a href="{{.EyebrowHref}}">{{.Eyebrow}}</a>{{else}}{{.Eyebrow}}{{end}}{{if .Topic}} / <a href="{{tagURL .Topic}}">{{topicName .Topic}}</a>{{end}}</p>{{end}}<h1>{{.Heading}}</h1>{{if .Total}}<p class="h-count">{{num .Total}} {{plural .Total "article" "articles"}}, newest first.</p>{{end}}</div>
{{if .ShowSearch}}<form class="h-find" method="get" action="/search" role="search" data-vayu-search><label class="h-vh" for="h-find-q">Search the archive</label>{{icon "search"}}<input id="h-find-q" type="search" name="q" placeholder="Search the archive"><kbd class="h-js-only" aria-hidden="true">/</kbd></form>{{end}}</header>
{{if .Cfg.Topics}}{{template "topicrow" .}}{{end}}
{{if .Months}}{{template "months" .}}{{else}}<p class="h-empty">Nothing is published here yet.</p>{{end}}
{{template "pager" .}}
{{end}}
</main></div>{{template "end" .}}`

const halcyonArticleTmpl = `{{template "start" .}}<div class="h-page"><main id="main-content">
<article class="h-post h-post--{{.Cfg.Article}}{{if .IsPage}} h-post--page{{end}}" itemscope itemtype="https://schema.org/BlogPosting">
{{if and (eq .Cfg.Article "margin") (not .IsPage)}}<dl class="h-facts">{{if .Topic}}<div><dt>Section</dt><dd><a href="{{tagURL .Topic}}">{{topicName .Topic}}</a></dd></div>{{end}}<div><dt>Published</dt><dd><time datetime="{{shortDate .CreatedAt}}">{{humanDate .CreatedAt}}</time></dd></div><div><dt>Reading time</dt><dd>{{.Minutes}} {{plural .Minutes "minute" "minutes"}}</dd></div>{{if .Author}}<div><dt>Written by</dt><dd>{{if .AuthorSlug}}<a href="/author/{{.AuthorSlug}}" rel="author">{{.Author}}</a>{{else}}{{.Author}}{{end}}</dd></div>{{end}}</dl>{{end}}
<header class="h-post-head">
{{if and (not .IsPage) (ne .Cfg.Article "margin")}}<p class="h-kicker">{{if .Topic}}<a href="{{tagURL .Topic}}">{{topicName .Topic}}</a> · {{end}}<time itemprop="datePublished" datetime="{{shortDate .CreatedAt}}">{{humanDate .CreatedAt}}</time> · {{.Minutes}} min read</p>{{end}}
<h1 itemprop="headline">{{.Title}}</h1>
{{if .Standfirst}}<div class="h-standfirst">{{.Standfirst}}</div>{{end}}
{{if not .IsPage}}<div class="h-byline">{{if and .Author (ne .Cfg.Article "margin")}}<p class="h-who" itemprop="author" itemscope itemtype="https://schema.org/Person">By {{if .AuthorSlug}}<a href="/author/{{.AuthorSlug}}" rel="author"><b itemprop="name">{{.Author}}</b></a>{{else}}<b itemprop="name">{{.Author}}</b>{{end}}</p>{{end}}<div class="h-tools h-js-only"><button class="h-ib h-aa" type="button" data-h-open="h-reader" aria-expanded="false" aria-controls="h-reader" aria-label="Reading settings">Aa</button><button class="h-ib" type="button" data-h-share aria-label="Share">{{icon "share"}}</button><button class="h-ib" type="button" data-h-copylink aria-label="Copy link">{{icon "link"}}</button></div></div>{{end}}
</header>
{{if .FeatureImage}}<figure class="h-cover"><img src="{{.FeatureImage}}" alt="" loading="eager" decoding="async" referrerpolicy="no-referrer"></figure>{{end}}
<aside class="h-rail">{{if .Outline}}<nav class="h-outline" aria-labelledby="h-outline-h"><h2 class="h-label" id="h-outline-h">On this page</h2><ol>{{range .Outline}}<li><a href="#{{.ID}}">{{.Text}}</a></li>{{end}}</ol><p class="h-left h-js-only" data-h-left></p></nav>{{end}}<div class="h-rail-ads" data-ads="sidebar"></div></aside>
<div class="h-body"><div class="content" itemprop="articleBody">{{.Body}}</div>{{if .ContactForm}}<section id="vayu-contact" class="vayu-contact" aria-label="Contact form"></section>{{end}}{{if not .IsPage}}<p class="h-fin" aria-hidden="true">···</p>{{end}}</div>
</article>
{{if not .IsPage}}<div class="h-end">
{{if .Tags}}<p class="h-filed">Filed under {{range $i, $t := .Tags}}{{if $i}}{{if eq (inc $i) (len $.Tags)}} and {{else}}, {{end}}{{end}}<a href="{{tagURL $t}}" rel="tag">{{topicName $t}}</a>{{end}}.</p>{{end}}
{{if and .Author .AuthorBio}}<section class="h-author" aria-label="About the author"><div><p class="h-author-name">{{if .AuthorSlug}}<a href="/author/{{.AuthorSlug}}" rel="author">{{.Author}}</a>{{else}}{{.Author}}{{end}}</p><p>{{.AuthorBio}}</p></div>{{if .AuthorSlug}}<a class="h-follow" href="/author/{{.AuthorSlug}}">More from {{.Author}}</a>{{end}}</section>{{end}}
{{if .Related}}<section class="h-next" aria-labelledby="h-next-h"><h2 class="h-label" id="h-next-h">Related reading</h2><ol class="h-rel">{{range .Related}}<li><div><a href="/{{.Slug}}">{{.Title}}</a><p class="h-why">{{reason .Shared}}</p></div><time class="h-meta" datetime="{{shortDate .CreatedAt}}">{{humanDate .CreatedAt}}</time></li>{{end}}</ol></section>{{end}}
{{if .CommentsOn}}<section class="h-talk" aria-labelledby="h-talk-h"><div class="h-talk-head"><h2 id="h-talk-h">Comments</h2><p>Comments are read before they appear.</p></div><div id="vayu-comments" class="vayu-comments" data-slug="{{.Slug}}"></div></section>{{end}}
{{template "trend" .}}
</div>
<div class="h-pop h-reader" id="h-reader" role="dialog" aria-label="Reading settings" hidden>
<div><div class="h-lb"><span class="h-label">Text size</span><output data-h-size-out>{{.Cfg.TextSize}} px</output></div><div class="h-size"><span class="h-a1" aria-hidden="true">A</span><input type="range" min="17" max="23" step="1" value="{{.Cfg.TextSize}}" data-h-size aria-label="Text size"><span class="h-a2" aria-hidden="true">A</span></div></div>
<div><p class="h-label">Typeface</p><div class="h-seg" role="group" aria-label="Typeface"><button type="button" class="h-serif" data-h-face="serif">Serif</button><button type="button" data-h-face="sans">Sans</button></div></div>
<div><p class="h-label">Line width</p><div class="h-seg" role="group" aria-label="Line width"><button type="button" data-h-width="narrow">Narrow</button><button type="button" data-h-width="standard">Standard</button><button type="button" data-h-width="wide">Wide</button></div></div>
<div><p class="h-label">Appearance</p>{{template "schemes" .}}</div>
<div class="h-reset"><span>Saved on this device only.</span><button type="button" data-h-reset>Use the site’s settings</button></div>
</div>
{{if .Cfg.Dock}}<div class="h-dock h-js-only" data-h-dock>{{if .Outline}}<button class="h-ib" type="button" data-h-open="h-sections" aria-expanded="false" aria-controls="h-sections" aria-label="Sections">{{icon "list"}}</button>{{end}}<span class="h-left" data-h-left></span><button class="h-ib h-aa" type="button" data-h-open="h-reader" aria-expanded="false" aria-controls="h-reader" aria-label="Reading settings">Aa</button><button class="h-ib" type="button" data-h-share aria-label="Share">{{icon "share"}}</button></div>
{{if .Outline}}<div class="h-bsheet" id="h-sections" role="dialog" aria-modal="true" aria-label="Sections" hidden><div class="h-grab" aria-hidden="true"></div><p class="h-label">On this page</p>{{range .Outline}}<a class="h-it" href="#{{.ID}}">{{.Text}}</a>{{end}}</div>{{end}}{{end}}
{{end}}
</main></div>{{template "end" .}}`

const halcyonSearchTmpl = `{{template "start" .}}<div class="h-page"><main id="main-content" class="h-wrap h-narrow">
<header class="h-ix-head h-ix-head--search"><div><h1>Search</h1>{{if .Query}}<p class="h-count">{{if .Hits}}{{len .Hits}} {{plural (len .Hits) "result" "results"}} for “{{.Query}}”{{else}}Nothing matches “{{.Query}}”.{{end}}</p>{{end}}</div></header>
<form class="h-find h-find--page" method="get" action="/search" role="search"><label class="h-vh" for="h-q">Search the site</label>{{icon "search"}}<input id="h-q" type="search" name="q" value="{{.Query}}" placeholder="Search the site" autofocus><button class="h-btn h-btn-ink" type="submit">Search</button></form>
{{if .Query}}{{if .Hits}}<ol class="h-results">{{range .Hits}}<li><a href="/{{.Slug}}">{{.Title}}</a><p class="h-meta">{{with firstTopicOf .Tags}}<span class="h-topic">{{topicName .}}</span><span class="h-sep" aria-hidden="true">·</span>{{end}}<time datetime="{{shortDate .CreatedAt}}">{{humanDate .CreatedAt}}</time></p></li>{{end}}</ol>
{{else}}<div class="h-empty"><p>Check the spelling, or try a broader word. <a href="/tags">The topics</a> are a good place to start.</p></div>{{end}}{{end}}
</main></div>{{template "end" .}}`

const halcyonTopicsTmpl = `{{template "start" .}}<div class="h-page"><main id="main-content" class="h-wrap">
<header class="h-ix-head"><div><p class="h-eyebrow">The archive</p><h1>Topics</h1><p class="h-count">{{num (len .Tags)}} {{plural (len .Tags) "topic" "topics"}} across {{num .Total}} {{plural .Total "article" "articles"}}.</p></div></header>
{{if .Tags}}<ol class="h-topic-list">{{range .Tags}}<li><a href="{{tagURL .Name}}">{{topicName .Name}}</a><span class="h-n">{{num .Count}}</span></li>{{end}}</ol>{{else}}<p class="h-empty">Topics appear here as posts are published.</p>{{end}}
</main></div>{{template "end" .}}`

const halcyonNotFoundTmpl = `{{template "start" .}}<div class="h-page"><main id="main-content" class="h-wrap h-narrow h-lost">
<p class="h-eyebrow">Not found</p><h1>There is nothing at this address.</h1>
<p>The page may have moved, or the link may be mistyped.</p>
<p class="h-lost-links"><a class="h-btn h-btn-ink" href="{{blogBase}}">Go to {{.SiteName}}</a>{{if .ShowSearch}}<a class="h-btn h-btn-line" href="/search">Search the site</a>{{end}}</p>
</main></div>{{template "end" .}}`

var halcyonTmpl = func() map[string]*template.Template {
	out := map[string]*template.Template{}
	for name, body := range map[string]string{
		"home":     halcyonHomeTmpl,
		"article":  halcyonArticleTmpl,
		"search":   halcyonSearchTmpl,
		"topics":   halcyonTopicsTmpl,
		"notfound": halcyonNotFoundTmpl,
	} {
		t := template.Must(template.New("halcyon-shared").Funcs(halcyonFuncs).Parse(halcyonShared))
		out[name] = template.Must(t.New(name).Parse(body))
	}
	return out
}()

func executeHalcyon(name string, data any) (string, error) {
	var b strings.Builder
	if err := halcyonTmpl[name].ExecuteTemplate(&b, name, data); err != nil {
		return "", fmt.Errorf("halcyon %s: %w", name, err)
	}
	return b.String(), nil
}

// ── Listings: Home, the later pages, a topic ──────────────────────────────────

// HomeInput is everything Home and its later pages are rendered from. The
// Topics, TopicTotal and Desks fields feed Halcyon only; the shared templates
// read the rest.
type HomeInput struct {
	Settings         SiteSettings
	Domain, Version  string
	Articles         []HomeArticle
	Total            int
	Page, TotalPages int
	Topics           []TagInfo // the largest topics, with counts, largest first
	TopicTotal       int       // all posts, the denominator for Ubiquitous
	Desks            []TopicDesk
}

type hListing struct {
	hChrome
	Composition          string // front, river, index
	Lead                 *HomeArticle
	Also                 []HomeArticle
	Desks                []TopicDesk
	River                []HomeArticle
	Months               []hMonth
	Topics               []TagInfo
	Topic                string
	Eyebrow, EyebrowHref string
	Heading              string
	Total                int
	Page, TotalPages     int
}

// RenderHomePage renders Home (or one of its later pages) in the active theme.
func RenderHomePage(in HomeInput) (string, error) {
	if cfg, ok := Halcyon(); ok {
		return renderHalcyonHome(in, cfg)
	}
	return RenderHomeWithSettings(in.Settings, in.Domain, in.Version, in.Articles, in.Total, in.Page, in.TotalPages)
}

// halcyonTopTopics keeps the topics worth showing: not ubiquitous, at most n.
func halcyonTopTopics(topics []TagInfo, total, n int) []TagInfo {
	var out []TagInfo
	for _, t := range topics {
		if Ubiquitous(t.Count, total) || strings.TrimSpace(t.Name) == "" {
			continue
		}
		out = append(out, t)
		if len(out) == n {
			break
		}
	}
	return out
}

func renderHalcyonHome(in HomeInput, cfg theme.HalcyonConfig) (string, error) {
	page, totalPages := max(in.Page, 1), max(in.TotalPages, 1)
	c := halcyonChrome(in.Settings, cfg, "home")
	origin := seo.Origin(in.Domain)
	canonical := blogPageURL(page)
	c.PageTitle = c.SiteName
	if t := strings.TrimSpace(in.Settings.Tagline); t != "" {
		c.PageTitle += " — " + t
	}
	c.Description = firstNonEmptyStr(in.Settings.Description, in.Settings.Tagline, c.SiteName)
	c.Canonical = origin + canonical
	if page > 1 {
		c.PrevURL = blogPageURL(page - 1)
		c.PageTitle = c.SiteName + " — page " + strconv.Itoa(page)
	}
	if page < totalPages {
		c.NextURL = blogPageURL(page + 1)
	}
	c.JSONLD = homeJSONLD(in, origin, canonical)
	c.Scripts = halcyonScripts(cfg.MostRead || cfg.Pinned)

	l := hListing{hChrome: c, Page: page, TotalPages: totalPages, Total: in.Total}
	if cfg.Topics {
		l.Topics = halcyonTopTopics(in.Topics, in.TopicTotal, 8)
	}
	// Page 1 takes the Home composition. A later page is an archive page: the
	// index unless the operator chose "Same as Home", and then the river,
	// because a lead story on page seven would announce an old post as news.
	comp := cfg.Home
	if page > 1 {
		comp = "index"
		if cfg.Archives == "home" {
			comp = "river"
		}
	}
	// A site with nothing published (every new install, on Halcyon) says so,
	// rather than showing a front page or a river of nothing.
	if len(in.Articles) == 0 && comp != "index" {
		comp = "empty"
	}
	l.Composition = comp
	switch comp {
	case "front":
		if len(in.Articles) > 0 {
			lead := in.Articles[0]
			l.Lead = &lead
			l.Also = in.Articles[1:min(4, len(in.Articles))]
			if len(in.Articles) > 4 {
				l.Months = halcyonMonths(in.Articles[4:])
			}
		}
		if cfg.Topics {
			l.Desks = in.Desks
		}
	case "river":
		l.River = in.Articles
	default:
		l.Heading = "Writing"
		l.Eyebrow, l.EyebrowHref = "The archive", "/tags"
		l.Months = halcyonMonths(in.Articles)
	}
	return executeHalcyon("home", l)
}

// homeJSONLD builds Home's structured data from the posts on this page, the
// same graph the shared template emits.
func homeJSONLD(in HomeInput, origin, canonical string) template.HTML {
	posts := make([]seo.HomePost, 0, len(in.Articles))
	for _, a := range in.Articles {
		img := a.Image
		if img != "" && strings.HasPrefix(img, "/") {
			img = origin + img
		}
		posts = append(posts, seo.HomePost{Title: a.Title, Slug: a.Slug, Excerpt: a.Excerpt, Published: a.CreatedAt,
			Author: a.Author, ImageURL: img, AbsoluteURL: origin + "/" + a.Slug})
	}
	searchPath := ""
	if searchEnabled.Load() {
		searchPath = "/search"
	}
	logo := in.Settings.OGImage
	if logo != "" && strings.HasPrefix(logo, "/") {
		logo = origin + logo
	}
	return seo.HomeJSONLD(seo.HomeDoc{Origin: origin, Canonical: canonical, SiteName: in.Settings.Name,
		Description: firstNonEmptyStr(in.Settings.Description, in.Settings.Tagline), LogoURL: logo,
		SearchPath: searchPath, Posts: posts})
}

// TopicInput is everything a topic page is rendered from.
type TopicInput struct {
	Domain, Version string
	Tag             string
	Articles        []HomeArticle
	Total           int
	Topics          []TagInfo // the largest topics, for the topic row
	TopicTotal      int
}

// RenderTopicPage renders one topic's page in the active theme.
func RenderTopicPage(in TopicInput) (string, error) {
	cfg, ok := Halcyon()
	if !ok {
		return RenderTagPage(in.Domain, in.Version, in.Tag, in.Articles, in.Total)
	}
	return renderHalcyonTopic(in, cfg)
}

func renderHalcyonTopic(in TopicInput, cfg theme.HalcyonConfig) (string, error) {
	s := getActiveSettings()
	c := halcyonChrome(s, cfg, "topic")
	c.PageTitle = strings.ReplaceAll(in.Tag, "-", " ") + " — " + c.SiteName
	c.Description = groupThousands(in.Total) + " " + pluralWord(in.Total, "article", "articles") + " filed under “" + in.Tag + "” on " + c.SiteName + "."
	c.Canonical = seo.Origin(in.Domain) + "/tags/" + url.PathEscape(in.Tag)
	if in.Total < thinTagPosts {
		c.Robots = "noindex,follow"
	}
	c.Scripts = halcyonScripts(false)
	l := hListing{hChrome: c, Topic: in.Tag, Total: in.Total, Page: 1, TotalPages: 1,
		Eyebrow: "The archive", EyebrowHref: "/tags", Heading: strings.ReplaceAll(in.Tag, "-", " ")}
	if cfg.Topics {
		l.Topics = halcyonTopTopics(in.Topics, in.TopicTotal, 8)
		// The current topic is always in its own row, marked, even when it is
		// not among the largest.
		found := false
		for _, t := range l.Topics {
			found = found || strings.EqualFold(t.Name, in.Tag)
		}
		if !found && len(l.Topics) > 0 {
			l.Topics = append(l.Topics, TagInfo{Name: in.Tag, Count: in.Total})
		}
	}
	if cfg.Archives == "home" {
		l.Composition = "river"
		l.River = in.Articles
	} else {
		l.Composition = "index"
		l.Months = halcyonMonths(in.Articles)
	}
	return executeHalcyon("home", l)
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

type hTopics struct {
	hChrome
	Tags  []TagInfo
	Total int
}

func renderHalcyonTopics(domain string, tags []TagInfo, total int, cfg theme.HalcyonConfig) (string, error) {
	c := halcyonChrome(getActiveSettings(), cfg, "topics")
	c.PageTitle = "Topics — " + c.SiteName
	c.Description = "Every topic on " + c.SiteName + ", with how many articles each holds."
	c.Canonical = seo.Origin(domain) + "/tags"
	c.Scripts = halcyonScripts(false)
	return executeHalcyon("topics", hTopics{hChrome: c, Tags: tags, Total: total})
}

type hSearch struct {
	hChrome
	Query string
	Hits  []SearchHit
}

func renderHalcyonSearch(query string, hits []SearchHit, cfg theme.HalcyonConfig) (string, error) {
	c := halcyonChrome(getActiveSettings(), cfg, "search")
	c.PageTitle = "Search — " + c.SiteName
	if query != "" {
		c.PageTitle = "Search: " + query + " — " + c.SiteName
	}
	c.Robots = "noindex,follow"
	c.Analytics = false
	c.Scripts = halcyonScripts(false)
	return executeHalcyon("search", hSearch{hChrome: c, Query: query, Hits: hits})
}

func renderHalcyon404(cfg theme.HalcyonConfig) string {
	c := halcyonChrome(getActiveSettings(), cfg, "notfound")
	c.PageTitle = "Not found — " + c.SiteName
	c.Robots = "noindex"
	c.Scripts = halcyonScripts(false)
	out, err := executeHalcyon("notfound", c)
	if err != nil {
		return "Not found"
	}
	return out
}

// ── The article ───────────────────────────────────────────────────────────────

type hArticle struct {
	hChrome
	db.Article
	Body                          template.HTML
	Standfirst                    template.HTML
	Outline                       []OutlineEntry
	Minutes                       int
	Topic                         string
	Author, AuthorBio, AuthorSlug string
	FeatureImage                  string
	IsPage                        bool
	Related                       []RelatedArticle
	CommentsOn, ContactForm       bool
}

// renderHalcyonArticle renders a post or page. data is the shared template's
// model, already resolved (head, author, related), so both themes describe a
// post identically to search engines and differ only in how it reads.
func renderHalcyonArticle(s SiteSettings, data articlePage, cfg theme.HalcyonConfig) (string, error) {
	c := halcyonChrome(s, cfg, "article")
	c.PageTitle = data.TitleTag
	c.Description = data.SEODescription
	c.Canonical = data.Canonical
	var head strings.Builder
	if err := halcyonArticleHead.Execute(&head, data); err != nil {
		return "", err
	}
	c.ArticleHead = template.HTML(head.String()) //nolint:gosec // G203: executed by html/template above
	treated := halcyonTreat(data.Content, cfg.SmallCaps)
	a := hArticle{
		hChrome: c, Article: data.Article, Body: treated.Body, Standfirst: treated.Standfirst,
		Minutes: readMinutes(data.Content), Topic: firstTopic(data.Tags),
		Author: data.Author, AuthorBio: data.AuthorBio, AuthorSlug: data.AuthorSlug,
		FeatureImage: data.FeatureImage, IsPage: data.IsPage, Related: data.Related,
		CommentsOn: data.CommentsEnabled && cfg.Comments, ContactForm: data.ContactForm,
	}
	if cfg.Outline && len(treated.Outline) >= 2 && !data.IsPage {
		a.Outline = treated.Outline
	}
	var scripts strings.Builder
	scripts.WriteString(string(data.VideoFacadeJSLink))
	if a.CommentsOn {
		scripts.WriteString(string(data.CommentsJSLink))
	}
	if data.ContactForm {
		scripts.WriteString(string(data.ContactJSLink))
	}
	scripts.WriteString(string(halcyonScripts(!data.IsPage && (cfg.MostRead || cfg.Pinned))))
	a.Scripts = template.HTML(scripts.String()) //nolint:gosec // G203: script tags built by this package
	return executeHalcyon("article", a)
}

// halcyonArticleHead is the article's head as the shared template writes it:
// Open Graph, Twitter, the BlogPosting and breadcrumb graphs, and the code
// stylesheet when the post has highlighted code.
var halcyonArticleHead = template.Must(template.New("hhead").Funcs(halcyonFuncs).Parse(`<meta property="og:type" content="article">
<meta property="og:title" content="{{.OGTitle}}">
<meta property="og:description" content="{{.OGDescription}}">
<meta property="og:url" content="{{.Canonical}}">
<meta property="og:site_name" content="{{if .SiteName}}{{.SiteName}}{{else}}{{.Domain}}{{end}}">
<meta property="og:locale" content="en">
{{if .OGImage}}<meta property="og:image" content="{{.OGImage}}">{{else if .SiteOGImage}}<meta property="og:image" content="{{.Origin}}{{.SiteOGImage}}">{{end}}
<meta property="article:published_time" content="{{isoDate .CreatedAt}}">
<meta property="article:modified_time" content="{{isoDate .UpdatedAt}}">
{{range .Tags}}<meta property="article:tag" content="{{.}}">{{end}}
<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:title" content="{{.TwitterTitle}}">
<meta name="twitter:description" content="{{.TwitterDescription}}">
{{if .TwitterImageURL}}<meta name="twitter:image" content="{{.TwitterImageURL}}">{{end}}
<script type="application/ld+json">{"@context":"https://schema.org","@type":"BlogPosting","headline":"{{.Title | jsonAttr}}","description":"{{.SEODescription | jsonAttr}}","datePublished":"{{isoDate .CreatedAt}}","dateModified":"{{isoDate .UpdatedAt}}","url":"{{.Canonical}}","mainEntityOfPage":{"@type":"WebPage","@id":"{{.Canonical}}"},"inLanguage":"en",{{if .OGImage}}"image":"{{.OGImage}}",{{end}}"author":{"@type":"Person","name":"{{if .Author}}{{.Author | jsonAttr}}{{else if .SiteName}}{{.SiteName | jsonAttr}}{{else}}{{.Domain | jsonAttr}}{{end}}"},"publisher":{"@type":"Organization","name":"{{if .SiteName}}{{.SiteName | jsonAttr}}{{else}}{{.Domain | jsonAttr}}{{end}}","url":"{{.Origin}}"}}</script>{{.BreadcrumbJSONLD}}
{{.ChromaCSSLink}}`))

// halcyonScripts are the page's deferred scripts after the content: the
// trending widget where most read or pinned is shown, and the search modal
// while search is on. Halcyon's own script is in the head, deferred.
func halcyonScripts(trending bool) template.HTML {
	var b strings.Builder
	if trending {
		b.WriteString(string(TrendingJSLink()))
	}
	if searchEnabled.Load() {
		b.WriteString(string(SearchModalJSLink()))
	}
	return template.HTML(b.String()) //nolint:gosec // G203: script tags built by this package
}

// ── Theme Studio's preview ────────────────────────────────────────────────────

// HalcyonPreview is the real site data Theme Studio previews a page with.
type HalcyonPreview struct {
	Home      HomeInput
	Topic     TopicInput
	Article   *db.Article
	Related   []RelatedArticle
	Overrides ArticleMetaOverrides
	Query     string
	Hits      []SearchHit
	SignIn    string // the sign-in page as cmd builds it
}

// RenderHalcyonPreview renders one page of the site in Halcyon with the
// options being edited (cfg) rather than the saved ones, so the Studio shows
// the real page each setting changes. page is home, topic, article, search or
// signin.
//
// cssHref is the stylesheet being edited, which takes the place of the live
// /theme.css. The reader's saved preferences are not applied (the preview
// shows the site's defaults as they are being set) and the analytics beacon
// is left out, so a preview is never counted as a reader's visit.
func RenderHalcyonPreview(page string, cfg theme.HalcyonConfig, p HalcyonPreview, cssHref string) (string, error) {
	out, err := renderHalcyonPreviewPage(page, cfg, p)
	if err != nil {
		return "", err
	}
	out = strings.Replace(out, `<link rel="stylesheet" href="/theme.css">`,
		`<link id="vayu-theme-css" rel="stylesheet" href="`+template.HTMLEscapeString(cssHref)+`">`, 1)
	out = strings.Replace(out, `<script>`+halcyonPrefsJS+`</script>`, ``, 1)
	out = strings.Replace(out, `class="h"`, `class="h js"`, 1)
	out = strings.Replace(out, `<script defer src="/static/vp-analytics.js"></script>`, ``, 1)
	return out, nil
}

func renderHalcyonPreviewPage(page string, cfg theme.HalcyonConfig, p HalcyonPreview) (string, error) {
	switch page {
	case "topic":
		return renderHalcyonTopic(p.Topic, cfg)
	case "article":
		if p.Article == nil {
			return renderHalcyonHome(p.Home, cfg)
		}
		a := *p.Article
		_, hasContactForm := ParseContactForm(a.Content)
		if hasContactForm {
			a.Content = contactFormRe.ReplaceAllString(a.Content, "")
		}
		a.Content = renderContentHTML(a.Content)
		s := getActiveSettings()
		return renderHalcyonArticle(s, buildArticlePage(s, a, ArticleLayoutDefault, p.Related, p.Overrides, hasContactForm), cfg)
	case "search":
		return renderHalcyonSearch(p.Query, p.Hits, cfg)
	case "signin":
		return halcyonMemberPage(p.SignIn, MemberTask, cfg), nil
	}
	return renderHalcyonHome(p.Home, cfg)
}
