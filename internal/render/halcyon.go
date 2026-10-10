// SPDX-License-Identifier: Apache-2.0

package render

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/theme"
)

// halcyon.go — Halcyon, the theme that brings its own layout.
//
// Every other theme is CSS over the shared templates in render.go, and they
// are left exactly as they were: the public entry points dispatch here only
// while the active theme's layout is theme.LayoutHalcyon. Halcyon's own
// stylesheet and script are static, content-hashed files; the reader's
// choices (face, size, width, appearance) are applied before first paint by
// a small inline script admitted by its hash, and saved on that device only.

//go:embed halcyon/halcyon.css halcyon/members.css halcyon/halcyon.js
var halcyonFS embed.FS

// halcyonState is the active theme as the renderer needs it. nil means a
// theme on the shared templates.
var halcyonState atomic.Pointer[theme.HalcyonConfig]

// ActivateTheme makes t the theme the public pages render with: its compiled
// stylesheet and, for Halcyon, its layout and options. Every path that applies
// a theme comes through here, so the stylesheet and the templates can never
// belong to different themes.
func ActivateTheme(t theme.Tokens, css string) {
	SetThemeCSS(css)
	if t.Layout == theme.LayoutHalcyon {
		cfg := theme.ResolveHalcyon(t.Options)
		halcyonState.Store(&cfg)
		return
	}
	halcyonState.Store(nil)
}

// Halcyon reports whether Halcyon is the active theme, and its options.
func Halcyon() (theme.HalcyonConfig, bool) {
	if c := halcyonState.Load(); c != nil {
		return *c, true
	}
	return theme.HalcyonConfig{}, false
}

// ── Assets ────────────────────────────────────────────────────────────────────

type halcyonAsset struct {
	body  []byte
	ctype string
	ver   string
}

// halcyonAssets are the theme's stylesheets and script, keyed by the name they
// are served under. Stylesheets go out minified, by the same minifier as the
// rest of the public CSS; the version is the hash of the bytes sent.
var halcyonAssets = func() map[string]halcyonAsset {
	out := map[string]halcyonAsset{}
	for name, src := range map[string]string{
		"halcyon.css":         "halcyon/halcyon.css",
		"halcyon-members.css": "halcyon/members.css",
		"halcyon.js":          "halcyon/halcyon.js",
	} {
		b, err := halcyonFS.ReadFile(src)
		if err != nil {
			panic("render: Halcyon asset missing: " + src)
		}
		ctype := "application/javascript; charset=utf-8"
		if strings.HasSuffix(name, ".css") {
			b, ctype = MinifyCSS(b), "text/css; charset=utf-8"
		}
		if name == "halcyon.css" {
			b = append(b, blockComponentRules(articleCSSMin)...)
		}
		sum := sha256.Sum256(b)
		out[name] = halcyonAsset{body: b, ctype: ctype, ver: hex.EncodeToString(sum[:5])}
	}
	return out
}()

// HalcyonAsset returns one of Halcyon's static files for the server to send.
func HalcyonAsset(name string) (body []byte, contentType string, ok bool) {
	a, ok := halcyonAssets[name]
	return a.body, a.ctype, ok
}

func halcyonCSSHref() string {
	return "/static/css/halcyon.css?v=" + halcyonAssets["halcyon.css"].ver
}

// HalcyonMembersHead returns the head links a member page takes under
// Halcyon: the fonts it sets first, the theme's stylesheet, then the member
// pages' own, and the reader's saved appearance applied before paint.
func HalcyonMembersHead() string {
	return halcyonFontPreload + `<link rel="stylesheet" href="` + halcyonCSSHref() + `">` +
		`<link rel="stylesheet" href="/static/css/halcyon-members.css?v=` + halcyonAssets["halcyon-members.css"].ver + `">` +
		`<script>` + halcyonPrefsJS + `</script>`
}

// halcyonFontPreload names the two Newsreader weights every page sets above
// the fold (400 for reading and the display line, 500 for titles), so the
// text is drawn in its face the first time rather than reflowed into it.
// Italic and 600 load as they are met.
const halcyonFontPreload = `<link rel="preload" href="/static/fonts/newsreader-latin-400-normal.woff2" as="font" type="font/woff2" crossorigin>` +
	`<link rel="preload" href="/static/fonts/newsreader-latin-500-normal.woff2" as="font" type="font/woff2" crossorigin>`

// halcyonPrefsJS applies the reader's own choices before first paint: the
// appearance under the key the shared theme switch already uses (so a reader's
// Light or Dark carries across a theme change), and face, size and width under
// vp-read. A value it does not recognise is ignored, so a hand-edited entry can
// only fail to apply. Without it, or without script, the site's defaults on
// <html> stand.
//
// It also marks <html> with "js", before paint, so the controls that only
// work with script (Aa, the appearance switch, the dock) are laid out from the
// first frame instead of appearing later and moving the page, and are never
// shown to a reader without script.
const halcyonPrefsJS = `(function(){var r=document.documentElement;r.classList.add('js');try{` +
	`var t=localStorage.getItem('vayu-theme');if(t==='light'||t==='dark')r.setAttribute('data-theme',t);` +
	`var p=JSON.parse(localStorage.getItem('vp-read')||'{}');` +
	`if(p.face==='serif'||p.face==='sans')r.setAttribute('data-face',p.face);` +
	`if(p.size>=17&&p.size<=23)r.setAttribute('data-size',String(p.size|0));` +
	`if(p.width==='narrow'||p.width==='wide')r.setAttribute('data-width',p.width);` +
	`}catch(e){}})();`

// HalcyonPrefsCSPHash admits the script above under the strict script-src.
// A hash, not a nonce, because the pages it sits in are cached on disk.
var HalcyonPrefsCSPHash = func() string {
	sum := sha256.Sum256([]byte(halcyonPrefsJS))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}()

// ── Page models ───────────────────────────────────────────────────────────────

// TopicDesk is one numbered topic on the front page: its name, its real count
// and its newest posts.
type TopicDesk struct {
	Name  string
	Count int
	Posts []HomeArticle
}

// hMonth is one month of an index listing.
type hMonth struct {
	Label string
	Rows  []hRow
}

// hRow is one entry of an index listing. The first two entries of a listing
// carry their excerpt and the rest are compact: an intended hierarchy.
type hRow struct {
	HomeArticle
	Day     string
	Topic   string
	WithExc bool
}

// hLink is a navigation or footer link, already checked by safeNavHref.
type hLink struct {
	Label, Href string
	External    bool
}

type hFooterCol struct {
	Title string
	Links []hLink
}

// hChrome is what every Halcyon page shares: the site, its header and footer,
// and the head.
type hChrome struct {
	Cfg            theme.HalcyonConfig
	Kind           string // home, topic, topics, article, search, notfound, author
	SiteName       string
	About          string
	Nav            []hLink
	ShowMembership bool
	ShowSearch     bool
	FooterCols     []hFooterCol
	Social, Legal  []hLink
	Copyright      string

	PageTitle, Description, Canonical, Robots string
	OGType, OGImage                           string
	HeadMeta, JSONLD                          template.HTML
	PrevURL, NextURL                          string
	Version                                   string
	Analytics                                 bool
	ArticleHead                               template.HTML // the article's own meta, script and stylesheet links
	CSSHref                                   string
	PrefsJS                                   template.JS
	FontPreload                               template.HTML
	JSHref                                    string
	Scripts                                   template.HTML
}

// halcyonChrome builds the shared part of every page from the site settings.
func halcyonChrome(s SiteSettings, cfg theme.HalcyonConfig, kind string) hChrome {
	name := strings.TrimSpace(s.Name)
	if name == "" {
		name = config.Cfg.Domain
	}
	if name == "" {
		name = "VayuPress"
	}
	c := hChrome{
		Cfg: cfg, Kind: kind, SiteName: name,
		About:          firstNonEmptyStr(s.Tagline, s.Description),
		Nav:            halcyonNav(s.NavJSON),
		ShowMembership: s.ShowMembership,
		ShowSearch:     searchEnabled.Load(),
		HeadMeta:       headMetaHTML(s),
		Version:        Version,
		Analytics:      kind != "notfound",
		CSSHref:        halcyonCSSHref(),
		PrefsJS:        template.JS(halcyonPrefsJS), //nolint:gosec // G203: a constant, admitted by its hash
		FontPreload:    template.HTML(halcyonFontPreload),
		JSHref:         "/static/js/halcyon.js?v=" + halcyonAssets["halcyon.js"].ver,
	}
	var fc FooterConfig
	if strings.TrimSpace(s.FooterJSON) != "" {
		_ = json.Unmarshal([]byte(s.FooterJSON), &fc)
	}
	if t := strings.TrimSpace(fc.Tagline); t != "" {
		c.About = t
	}
	for _, col := range fc.Columns {
		links := halcyonLinks(col.Links)
		if len(links) == 0 {
			continue
		}
		c.FooterCols = append(c.FooterCols, hFooterCol{Title: strings.TrimSpace(col.Title), Links: links})
	}
	c.Social, c.Legal = halcyonLinks(fc.Social), halcyonLinks(fc.Legal)
	copyLine := strings.TrimSpace(fc.Copyright)
	if copyLine == "" {
		copyLine = "© {year} {site}"
	}
	year := strconv.Itoa(time.Now().UTC().Year())
	c.Copyright = strings.ReplaceAll(strings.ReplaceAll(copyLine, "{year}", year), "{site}", name)
	return c
}

// halcyonNav is the operator's navigation. Without one, Halcyon offers the
// topics and the feed: the shared templates' fallback also links the console,
// which is the operator's door and not a reader's.
func halcyonNav(navJSON string) []hLink {
	var items []NavItem
	if strings.TrimSpace(navJSON) != "" {
		_ = json.Unmarshal([]byte(navJSON), &items)
	}
	if len(items) == 0 {
		return []hLink{{Label: "Topics", Href: "/tags"}, {Label: "Feed", Href: "/feed.xml"}}
	}
	links := make([]FooterLink, len(items))
	for i, it := range items {
		links[i] = FooterLink(it)
	}
	return halcyonLinks(links)
}

func halcyonLinks(in []FooterLink) []hLink {
	var out []hLink
	for _, l := range in {
		label, href := strings.TrimSpace(l.Label), strings.TrimSpace(l.Href)
		if label == "" || href == "" || !safeNavHref(href) {
			continue
		}
		ext := strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://")
		out = append(out, hLink{Label: label, Href: href, External: ext})
	}
	return out
}

// ── Listings ──────────────────────────────────────────────────────────────────

// halcyonMonths groups a listing by month, newest first, as the index shows
// it. The first two entries carry their excerpt.
func halcyonMonths(arts []HomeArticle) []hMonth {
	var out []hMonth
	for i, a := range arts {
		label := config.FormatSite(a.CreatedAt, "January 2006")
		if len(out) == 0 || out[len(out)-1].Label != label {
			out = append(out, hMonth{Label: label})
		}
		m := &out[len(out)-1]
		m.Rows = append(m.Rows, hRow{
			HomeArticle: a,
			Day:         config.FormatSite(a.CreatedAt, "2"),
			Topic:       firstTopic(a.Tags),
			WithExc:     i < 2 && a.Excerpt != "",
		})
	}
	return out
}

// firstTopic is the topic a listing names for a post: its first tag, as
// stored. Tags are shown lower case and never title-cased, because a theme
// cannot know that "sre" is an acronym.
func firstTopic(tags []string) string {
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" {
			return t
		}
	}
	return ""
}

// Ubiquitous reports whether a topic is carried by more than half of all
// posts. Such a topic says nothing about a post (on johal.in every post
// carries "tutorial"), so related reading does not count it as shared and the
// front page does not give it a desk.
func Ubiquitous(count, total int) bool { return total > 0 && count*2 > total }

// RankRelated orders related candidates for Halcyon: by how many topics each
// shares with the post, ignoring ubiquitous ones, the newer first on a tie;
// candidates that share nothing meaningful are dropped, because a related
// post with no reason to give is not related. It keeps at most n.
func RankRelated(cands []RelatedArticle, ubiquitous func(tag string) bool, n int) []RelatedArticle {
	var out []RelatedArticle
	for _, c := range cands {
		var shared []string
		for _, t := range c.Shared {
			if !ubiquitous(t) {
				shared = append(shared, t)
			}
		}
		if len(shared) == 0 {
			continue
		}
		sort.Strings(shared)
		c.Shared = shared
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Shared) != len(out[j].Shared) {
			return len(out[i].Shared) > len(out[j].Shared)
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// sharesReason says why a related post is offered: "Shares devops and sre".
func sharesReason(tags []string) string {
	words := make([]string, len(tags))
	for i, t := range tags {
		words[i] = strings.ReplaceAll(t, "-", " ")
	}
	switch len(words) {
	case 0:
		return ""
	case 1:
		return "Shares " + words[0]
	case 2:
		return "Shares " + words[0] + " and " + words[1]
	}
	return "Shares " + strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// readMinutes is the reading time the shared templates also show: words over
// 200, at least one.
func readMinutes(htmlBody string) int {
	words := len(strings.Fields(htmlTagRe.ReplaceAllString(htmlBody, " ")))
	if words < 200 {
		return 1
	}
	return (words + 199) / 200
}

// blockComponentRules lifts the editor's component rules out of the shared
// public stylesheet: video facades, embed cards, and every vp-* block the
// block renderer emits (figures, galleries, diagrams, tables, tasks, toggles).
// Halcyon does not load that stylesheet, and a post written in the editor
// must look as its author made it; taking the rules from their one source
// means a component restyled there is restyled here too. They read the
// shared variables (--border2, --surface, --radius2, …), which /theme.css sets
// from Halcyon's own tokens, and the --sp* spacing Halcyon defines.
//
// Only a rule whose every selector names a component is taken, alone or
// inside an @media block, so nothing of the shared page (its body, nav, cards)
// can come with it.
func blockComponentRules(css string) []byte {
	isComponent := func(sel string) bool {
		for _, one := range strings.Split(sel, ",") {
			one = strings.TrimSpace(one)
			if !strings.HasPrefix(one, ".video-facade") && !strings.HasPrefix(one, ".embed-card") && !strings.HasPrefix(one, ".vp-") {
				return false
			}
		}
		return true
	}
	var out strings.Builder
	for _, b := range cssBlocks(css) {
		switch {
		case strings.HasPrefix(b.sel, "@media"):
			var inner strings.Builder
			for _, ib := range cssBlocks(b.body) {
				if isComponent(ib.sel) {
					inner.WriteString(ib.sel + "{" + ib.body + "}")
				}
			}
			if inner.Len() > 0 {
				out.WriteString(b.sel + "{" + inner.String() + "}")
			}
		case strings.HasPrefix(b.sel, "@"):
			// Other at-rules (keyframes, font faces) belong to the shared page.
		case isComponent(b.sel):
			out.WriteString(b.sel + "{" + b.body + "}")
		}
	}
	return []byte(out.String())
}

type cssBlock struct{ sel, body string }

// cssBlocks splits minified CSS into its top-level blocks: a prelude and the
// body between its braces. Braces inside a quoted string do not count.
func cssBlocks(css string) []cssBlock {
	var out []cssBlock
	start, depth, open := 0, 0, 0
	var quote byte
	for i := 0; i < len(css); i++ {
		c := css[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '{':
			if depth == 0 {
				open = i
			}
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				out = append(out, cssBlock{sel: strings.TrimSpace(css[start:open]), body: css[open+1 : i]})
				start = i + 1
			}
		}
	}
	return out
}
