// Package blockrender converts the Admin v3 block-editor document (a JSON array
// of typed blocks) into sanitised HTML for storage in articles.content.
//
// Security posture (ADR-0068): the block document is operator-authored but may
// embed pasted/interpolated content, so every text field is HTML-escaped at
// emit time and the final HTML is run through a bluemonday UGC policy. The
// renderer never trusts a block's "html" verbatim — there is no raw-HTML block
// that bypasses sanitisation. This makes the stored content safe for the public
// article template, feeds, and search snippets.
package blockrender

import (
	"bytes"
	"encoding/json"
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/diagram"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	goldhtml "github.com/yuin/goldmark/renderer/html"
)

// inlineMD renders inline markdown (bold, italic, inline code, links,
// strikethrough) inside block text. It is GFM-based but HTML-unsafe input is
// escaped by goldmark and the assembled fragment is still run through the
// bluemonday UGC policy below, so this never widens the XSS surface — it only
// upgrades the previously plain-escaped text to safe rich inline HTML.
var inlineMD = goldmark.New(
	goldmark.WithExtensions(extension.Strikethrough, extension.Linkify),
	goldmark.WithRendererOptions(goldhtml.WithHardWraps()),
)

// renderInlineHTML converts s to inline HTML (no enclosing block element). It is
// used for the text of paragraph/heading/quote/callout/list blocks so authors
// can use **bold**, *italic*, `code`, [links](url) and ~~strike~~. The caller
// wraps the result in the appropriate block tag; bluemonday then sanitises the
// whole fragment.
func renderInlineHTML(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := inlineMD.Convert([]byte(s), &buf); err != nil {
		return html.EscapeString(s)
	}
	out := strings.TrimSpace(buf.String())
	// Strip a single enclosing <p>…</p> so the text can be placed inside the
	// caller's own block tag (heading, li, blockquote, …). Multi-paragraph text
	// keeps its inner <p> boundaries, which remain valid after wrapping.
	out = strings.TrimSuffix(strings.TrimPrefix(out, "<p>"), "</p>")
	return out
}

// embedSrcRe is the closed allowlist for a video-facade iframe source: only the
// cookie-free YouTube/Vimeo embed origins, with a constrained id. It is used
// both to validate before emitting the attribute and (re-applied) as the
// bluemonday Matching barrier — a crafted block can never inject another origin.
var embedSrcRe = regexp.MustCompile(
	`^https://(?:www\.youtube-nocookie\.com/embed|player\.vimeo\.com/video)/[A-Za-z0-9_-]{1,64}$`)

// safeEmbedSrc returns s if it is an allowlisted video-embed URL, else "".
func safeEmbedSrc(s string) string {
	if embedSrcRe.MatchString(s) {
		return s
	}
	return ""
}

// localMediaRe constrains a self-hosted media URL to the site's own /media path.
// Audio/source elements may only ever point here — never at an external origin —
// keeping playback privacy-preserving (no third-party request on page load).
var localMediaRe = regexp.MustCompile(`^/media/[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// audioPreloadRe is the closed allowlist for the <audio preload> attribute.
var audioPreloadRe = regexp.MustCompile(`^(none|metadata|auto)$`)

// safeMediaURL returns s if it is a local /media URL, else "". It is applied
// before emit and re-applied by the bluemonday Matching barrier below, so a
// crafted block can never point an <audio> element off-site.
func safeMediaURL(s string) string {
	s = strings.TrimSpace(s)
	if localMediaRe.MatchString(s) {
		return s
	}
	return ""
}

// policy sanitises the assembled HTML. UGCPolicy allows a safe subset of tags
// (p, headings, lists, blockquote, pre/code, a, img, em/strong, figure, etc.)
// and strips scripts, event handlers, and javascript: URLs. We additionally
// allow:
//   - class on the structural elements our blocks emit (class can carry no
//     script, so this only widens styling, never the XSS surface);
//   - the validated data-embed-src / data-embed-title on the video-facade div
//     (click-to-load — no iframe is present until the reader acts);
//   - tables (table/thead/tbody/tr/th/td) for the table block;
//   - details/summary for collapsible toggle blocks;
//   - a self-hosted <audio> element whose src is restricted to the local /media
//     path — never an external origin (privacy-first).
var policy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("class").OnElements(
		"div", "span", "pre", "ul", "ol", "li", "figure", "figcaption",
		"table", "thead", "tbody", "tr", "th", "td", "details", "summary", "audio")
	p.AllowAttrs("data-embed-src").Matching(embedSrcRe).OnElements("div")
	p.AllowAttrs("data-embed-title").OnElements("div")
	// Table block.
	p.AllowTables()
	// Collapsible toggle block.
	p.AllowElements("details", "summary")
	p.AllowAttrs("open").OnElements("details")
	// Self-hosted audio block (local /media only — never third-party).
	p.AllowElements("audio")
	p.AllowAttrs("controls").OnElements("audio")
	p.AllowAttrs("preload").Matching(audioPreloadRe).OnElements("audio")
	p.AllowAttrs("src").Matching(localMediaRe).OnElements("audio")
	return p
}()

// Block is a single editor block. Only the fields relevant to a given Type are
// populated. Unknown types are skipped during rendering (forward-compatible).
type Block struct {
	Type  string   `json:"type"`
	Text  string   `json:"text,omitempty"`  // paragraph, heading, quote, code, callout
	Level int      `json:"level,omitempty"` // heading: 2..4
	Items []string `json:"items,omitempty"` // list (bulleted/numbered)
	URL   string   `json:"url,omitempty"`   // image, embed
	Alt   string   `json:"alt,omitempty"`   // image alt text
	Lang  string   `json:"lang,omitempty"`  // code language hint
	Style string   `json:"style,omitempty"` // list: "ordered"|"unordered"; callout: tone
	// embed block fields — resolved server-side at paste time, stored in the block document.
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Provider    string `json:"provider,omitempty"`
	ThumbURL    string `json:"thumbURL,omitempty"` // local /media/... URL
	Kind        string `json:"kind,omitempty"`     // embed: "link" (default) or "video"
	EmbedSrc    string `json:"embedSrc,omitempty"` // video: cookie-free iframe URL (allowlisted)
	// table block — Header is the optional first (heading) row; Rows are the body.
	Header []string   `json:"header,omitempty"`
	Rows   [][]string `json:"rows,omitempty"`
	// tasklist block — Items holds each label; Checked the parallel done-state.
	Checked []bool `json:"checked,omitempty"`
	// toggle block — Summary is the clickable title, Text the body, Open the
	// default expanded state.
	Summary string `json:"summary,omitempty"`
	Open    bool   `json:"open,omitempty"`
}

// Render parses a blocks JSON document and returns sanitised HTML plus a plain-
// text excerpt (first ~200 chars, tags stripped) suitable for search/meta.
// On parse failure it returns empty strings and the error, leaving callers free
// to fall back to legacy Markdown content.
func Render(blocksJSON string) (htmlOut, text string, err error) {
	trimmed := strings.TrimSpace(blocksJSON)
	if trimmed == "" {
		return "", "", nil
	}
	var blocks []Block
	if err := json.Unmarshal([]byte(trimmed), &blocks); err != nil {
		return "", "", err
	}
	// Blocks are sanitised per-fragment so that a diagram block's SVG (validated
	// by the diagram package's own closed allowlist) can survive — the UGC policy
	// would otherwise strip every SVG element. Text blocks still pass through the
	// UGC policy; the result is the concatenation of independently-safe fragments.
	var out strings.Builder
	var plain strings.Builder
	for _, blk := range blocks {
		if blk.Type == "diagram" {
			out.WriteString(renderDiagramBlock(blk, &plain))
			continue
		}
		var frag strings.Builder
		renderBlock(&frag, &plain, blk)
		out.WriteString(policy.Sanitize(frag.String()))
	}
	return out.String(), excerpt(plain.String()), nil
}

// renderDiagramBlock compiles a diagram block's source to a themeable SVG via the
// dependency-free diagram engine. The SVG is already sanitised by that engine's
// allowlist, so it is wrapped in a trusted, constant <figure> and returned
// verbatim. Unsupported/malformed sources degrade to an escaped code block.
func renderDiagramBlock(blk Block, plain *strings.Builder) string {
	src := blk.Text
	svg, err := diagram.Render(src)
	if err != nil {
		var f strings.Builder
		f.WriteString(`<pre class="vp-diagram-fallback"><code>` + html.EscapeString(src) + `</code></pre>`)
		return policy.Sanitize(f.String())
	}
	return `<figure class="vp-diagram-figure">` + svg + `</figure>`
}

func renderBlock(b, plain *strings.Builder, blk Block) {
	switch blk.Type {
	case "paragraph":
		if strings.TrimSpace(blk.Text) == "" {
			return
		}
		b.WriteString("<p>" + renderInlineHTML(blk.Text) + "</p>")
		plain.WriteString(blk.Text + " ")
	case "heading":
		lvl := blk.Level
		if lvl < 2 || lvl > 4 {
			lvl = 2
		}
		tag := "h" + strconv.Itoa(lvl)
		b.WriteString("<" + tag + ">" + renderInlineHTML(blk.Text) + "</" + tag + ">")
		plain.WriteString(blk.Text + " ")
	case "quote":
		b.WriteString("<blockquote><p>" + renderInlineHTML(blk.Text) + "</p></blockquote>")
		plain.WriteString(blk.Text + " ")
	case "code":
		cls := ""
		if blk.Lang != "" {
			// language-<lang>; alnum-restricted to avoid attribute injection.
			cls = ` class="language-` + html.EscapeString(safeLang(blk.Lang)) + `"`
		}
		b.WriteString("<pre><code" + cls + ">" + html.EscapeString(blk.Text) + "</code></pre>")
		plain.WriteString(blk.Text + " ")
	case "list":
		tag := "ul"
		if blk.Style == "ordered" {
			tag = "ol"
		}
		b.WriteString("<" + tag + ">")
		for _, it := range blk.Items {
			b.WriteString("<li>" + renderInlineHTML(it) + "</li>")
			plain.WriteString(it + " ")
		}
		b.WriteString("</" + tag + ">")
	case "image":
		if strings.TrimSpace(blk.URL) == "" {
			return
		}
		b.WriteString(`<figure><img src="` + html.EscapeString(blk.URL) +
			`" alt="` + html.EscapeString(blk.Alt) + `" loading="lazy"></figure>`)
		if blk.Alt != "" {
			plain.WriteString(blk.Alt + " ")
		}
	case "embed":
		if strings.TrimSpace(blk.URL) == "" {
			return
		}
		// Video facade (click-to-load): render a poster + play button, never an
		// iframe. The vetted cookie-free embed URL is carried in data-embed-src so
		// public/video-facade.js can inject a sandboxed iframe only on click; the
		// page CSP narrowly admits the origin only when this attribute is present.
		if blk.Kind == "video" {
			if src := safeEmbedSrc(blk.EmbedSrc); src != "" {
				b.WriteString(`<div class="video-facade" data-embed-src="` + html.EscapeString(src) +
					`" data-embed-title="` + html.EscapeString(blk.Title) + `">`)
				if blk.ThumbURL != "" {
					b.WriteString(`<img class="video-facade__poster" src="` + html.EscapeString(blk.ThumbURL) + `" alt="" loading="lazy">`)
				}
				b.WriteString(`<span class="video-facade__play" aria-hidden="true"></span>`)
				label := blk.Title
				if label == "" {
					label = "Play video"
				}
				b.WriteString(`<a class="video-facade__label" href="` + html.EscapeString(blk.URL) +
					`" rel="noopener noreferrer" target="_blank">` + html.EscapeString(label) + `</a>`)
				b.WriteString(`</div>`)
				if blk.Title != "" {
					plain.WriteString(blk.Title + " ")
				}
				return
			}
			// EmbedSrc failed validation — fall through to a safe link card.
		}
		b.WriteString(`<div class="embed-card">`)
		if blk.ThumbURL != "" {
			b.WriteString(`<a href="` + html.EscapeString(blk.URL) + `" class="embed-card__thumb" rel="noopener noreferrer" target="_blank">`)
			b.WriteString(`<img src="` + html.EscapeString(blk.ThumbURL) + `" alt="" loading="lazy">`)
			b.WriteString(`</a>`)
		}
		b.WriteString(`<div class="embed-card__body">`)
		if blk.Provider != "" {
			b.WriteString(`<span class="embed-card__provider">` + html.EscapeString(blk.Provider) + `</span>`)
		}
		if blk.Title != "" {
			b.WriteString(`<a href="` + html.EscapeString(blk.URL) + `" class="embed-card__title" rel="noopener noreferrer" target="_blank">` + html.EscapeString(blk.Title) + `</a>`)
		}
		if blk.Description != "" {
			b.WriteString(`<p class="embed-card__desc">` + html.EscapeString(blk.Description) + `</p>`)
		}
		b.WriteString(`<span class="embed-card__url">` + html.EscapeString(blk.URL) + `</span>`)
		b.WriteString(`</div></div>`)
		if blk.Title != "" {
			plain.WriteString(blk.Title + " ")
		}
		if blk.Description != "" {
			plain.WriteString(blk.Description + " ")
		}
	case "divider":
		b.WriteString("<hr>")
	case "callout":
		tone := safeLang(blk.Style) // reuse alnum filter for the modifier token
		cls := "callout"
		if tone != "" {
			cls += " callout--" + tone
		}
		b.WriteString(`<div class="` + cls + `"><p>` + renderInlineHTML(blk.Text) + `</p></div>`)
		plain.WriteString(blk.Text + " ")
	case "table":
		// A table renders an optional heading row plus body rows. Cell text goes
		// through the inline-markdown pass (bold/links/code) and the whole table
		// is still bluemonday-sanitised by the caller.
		if len(blk.Header) == 0 && len(blk.Rows) == 0 {
			return
		}
		b.WriteString(`<figure class="vp-table"><table>`)
		if len(blk.Header) > 0 {
			b.WriteString("<thead><tr>")
			for _, h := range blk.Header {
				b.WriteString("<th>" + renderInlineHTML(h) + "</th>")
				plain.WriteString(h + " ")
			}
			b.WriteString("</tr></thead>")
		}
		b.WriteString("<tbody>")
		for _, row := range blk.Rows {
			b.WriteString("<tr>")
			for _, cell := range row {
				b.WriteString("<td>" + renderInlineHTML(cell) + "</td>")
				plain.WriteString(cell + " ")
			}
			b.WriteString("</tr>")
		}
		b.WriteString("</tbody></table></figure>")
	case "toggle":
		// Collapsible <details>; Summary is the always-visible title.
		summary := blk.Summary
		if strings.TrimSpace(summary) == "" {
			summary = "Details"
		}
		openAttr := ""
		if blk.Open {
			openAttr = " open"
		}
		b.WriteString(`<details class="vp-toggle"` + openAttr + `><summary>` + renderInlineHTML(summary) + `</summary>`)
		b.WriteString(`<div class="vp-toggle__body">`)
		if strings.TrimSpace(blk.Text) != "" {
			b.WriteString("<p>" + renderInlineHTML(blk.Text) + "</p>")
		}
		b.WriteString(`</div></details>`)
		plain.WriteString(summary + " " + blk.Text + " ")
	case "tasklist":
		// A checklist. Done items carry a modifier class; we render a static
		// glyph box (no <input>) so the public page stays inert and unsanitised
		// state can never leak through.
		if len(blk.Items) == 0 {
			return
		}
		b.WriteString(`<ul class="vp-tasks">`)
		for i, it := range blk.Items {
			cls := "vp-task"
			if i < len(blk.Checked) && blk.Checked[i] {
				cls += " vp-task--done"
			}
			b.WriteString(`<li class="` + cls + `"><span class="vp-task__box" aria-hidden="true"></span>`)
			b.WriteString(`<span class="vp-task__text">` + renderInlineHTML(it) + `</span></li>`)
			plain.WriteString(it + " ")
		}
		b.WriteString(`</ul>`)
	case "math":
		// Lightweight, dependency-free math: the LaTeX/expression source is
		// escaped and preserved verbatim in a styled element. A theme may later
		// progressively enhance .vp-math (e.g. an optional KaTeX layer) without
		// changing stored content. No external request is made by default.
		if strings.TrimSpace(blk.Text) == "" {
			return
		}
		b.WriteString(`<div class="vp-math">` + html.EscapeString(blk.Text) + `</div>`)
		plain.WriteString(blk.Text + " ")
	case "audio":
		// Self-hosted audio only: the src is restricted to the site's own /media
		// path (double-guarded by safeMediaURL here and the policy Matching rule),
		// so audio never triggers a third-party request.
		src := safeMediaURL(blk.URL)
		if src == "" {
			return
		}
		b.WriteString(`<figure class="vp-audio"><audio controls preload="metadata" src="` + html.EscapeString(src) + `"></audio>`)
		if blk.Alt != "" {
			b.WriteString(`<figcaption>` + renderInlineHTML(blk.Alt) + `</figcaption>`)
		}
		b.WriteString(`</figure>`)
		if blk.Alt != "" {
			plain.WriteString(blk.Alt + " ")
		}
	default:
		// Unknown/forward-compatible block: skip silently.
	}
}

// safeLang keeps only ASCII letters, digits, and hyphen — enough for language
// hints ("go", "js", "c++"→"c") and callout tones ("info", "warn") while
// guaranteeing the value cannot break out of an HTML attribute or class.
func safeLang(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var out strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// excerpt collapses whitespace and truncates to ~200 runes on a word boundary.
func excerpt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 200
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return cut + "…"
}
