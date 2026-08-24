// SPDX-License-Identifier: Apache-2.0

// Package blockrender converts the Admin v3 block-editor document (a JSON array
// of typed blocks) into sanitised HTML for storage in articles.content.
//
// Security posture (ADR-0068): the block document is operator-authored but may
// embed pasted/interpolated content, so every text field is HTML-escaped at
// emit time and the final HTML is run through a bluemonday UGC policy. Even the
// "html" and "markdown" cards are not exempt — their output is passed through
// the same UGC policy as every other block, so they can enrich markup within
// the safe allowlist but can never introduce scripts, event handlers, forms, or
// javascript: URLs. This makes the stored content safe for the public article
// template, feeds, and search snippets.
package blockrender

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"html"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/diagram"
	"github.com/johalputt/vayupress/internal/embeds"
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

// blockMD renders a full Markdown document (the "markdown" card): block-level
// constructs (headings, lists, tables, blockquotes, code fences, task lists)
// plus GFM inline. Its output is still run through the bluemonday UGC policy by
// the caller, so untrusted/raw HTML inside the Markdown cannot widen the XSS
// surface.
var blockMD = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.Footnote),
)

// footnoteIDRe restricts the id/fragment anchors the sanitiser lets through to
// exactly the shapes goldmark's footnote extension emits (fn:… / fnref:…, with
// an optional numeric suffix goldmark adds when the same label repeats). This
// keeps footnote back-links working without opening a general id allow (which
// would invite DOM-clobbering); no author-controlled id can match.
var footnoteIDRe = regexp.MustCompile(`^fn(ref)?:[A-Za-z0-9._-]+$`)

// footnoteHrefRe is the matching in-page href — the same identifier as a
// fragment ("#fn:1" / "#fnref:1"). Only same-document fragments are allowed, so
// this can never become an off-site or javascript: link.
var footnoteHrefRe = regexp.MustCompile(`^#fn(ref)?:[A-Za-z0-9._-]+$`)

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

// embedSrcRe is the closed allowlist for a video-facade iframe source, derived
// from the provider table in internal/embeds rather than restated here. It is
// applied twice — once to validate before the attribute is emitted, and again as
// the bluemonday Matching barrier — so a crafted block document can never inject
// an origin the table does not contain.
//
// It used to be a third hand-written copy of that allowlist. Nothing checked the
// copies against each other, and the way they failed was silent: this file would
// happily emit data-embed-src for an origin internal/render did not recognise,
// the page CSP would never be extended for it, and the reader would click a play
// button whose iframe the page's own policy then refused.
var embedSrcRe = embeds.SrcPattern()

// safeEmbedSrc returns s if it is an allowlisted video-embed URL, else "".
func safeEmbedSrc(s string) string { return embeds.ValidEmbedSrc(s) }

// localMediaRe constrains a self-hosted media URL to the site's own /media path.
// Audio/source elements may only ever point here — never at an external origin —
// keeping playback privacy-preserving (no third-party request on page load).
var localMediaRe = regexp.MustCompile(`^/media/[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// safeImageURL reports whether an image URL is safe to emit as an <img src>. It
// mirrors the sanitizer's URL-scheme allowlist exactly — a site-relative path
// (our /media uploads and any local path) or an http(s) URL (so external images
// can be hotlinked by direct link, per operator choice). Anything else
// (javascript:, data:, vbscript:, …) is refused at render time, so a dangerous
// or unrenderable URL never produces even a src-stripped <img>.
func safeImageURL(u string) bool {
	u = strings.TrimSpace(u)
	if u == "" {
		return false
	}
	if strings.HasPrefix(u, "/") {
		return true
	}
	lu := strings.ToLower(u)
	return strings.HasPrefix(lu, "https://") || strings.HasPrefix(lu, "http://")
}

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
	// a, p and img are here because this renderer emits classes on all three and
	// they were being stripped from its own output: embed-card__thumb and
	// embed-card__title sit on anchors, embed-card__desc on a paragraph, and
	// video-facade__poster on the image. Without them a link card rendered as
	// loose unstyled text and the facade's poster did not fill its frame — the
	// renderer was quietly deleting half of what it had just written.
	p.AllowAttrs("class").OnElements(
		"div", "span", "pre", "ul", "ol", "li", "figure", "figcaption",
		"table", "thead", "tbody", "tr", "th", "td", "details", "summary", "audio",
		"a", "p", "img")
	p.AllowAttrs("data-embed-src").Matching(embedSrcRe).OnElements("div")
	p.AllowAttrs("data-embed-title").OnElements("div")
	// Footnotes (goldmark extension.Footnote): allow the specific id anchors and
	// same-document fragment hrefs the extension emits so the reference numbers
	// and back-links resolve. The regexes match only fn:/fnref: shapes — never an
	// author-supplied id — so this does not open a general id/anchor surface.
	p.AllowAttrs("id").Matching(footnoteIDRe).OnElements("li", "sup", "div")
	p.AllowAttrs("href").Matching(footnoteHrefRe).OnElements("a")
	p.AllowAttrs("role").Matching(regexp.MustCompile(`^doc-[a-z]+$`)).OnElements("div", "a", "li", "section")
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
	// Images may carry loading=lazy and referrerpolicy=no-referrer. no-referrer
	// keeps a hotlinked external image working past simple referrer-based hotlink
	// protection AND stops leaking the reader's page URL to the third-party host.
	p.AllowAttrs("loading").Matching(regexp.MustCompile(`^(lazy|eager)$`)).OnElements("img")
	p.AllowAttrs("referrerpolicy").Matching(regexp.MustCompile(`^no-referrer$`)).OnElements("img")
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
	// image block extras.
	Caption string `json:"caption,omitempty"` // image / gallery caption
	Width   string `json:"width,omitempty"`   // image layout: "wide" | "full" (else regular)
	// gallery block — Images is an ordered list of local /media (or absolute) URLs.
	Images []string `json:"images,omitempty"`
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
// MarkdownToBlocks converts a full Markdown document into editor blocks by
// rendering it to HTML with the GFM block renderer and importing that. Used by
// the AI "draft from a prompt" feature to turn generated Markdown into real,
// editable blocks. On a render error it falls back to a single markdown block so
// nothing is lost.
func MarkdownToBlocks(md string) []Block {
	var buf bytes.Buffer
	if err := blockMD.Convert([]byte(md), &buf); err != nil {
		return []Block{{Type: "markdown", Text: md}}
	}
	blocks := ImportHTML(buf.String())
	if len(blocks) == 0 {
		return []Block{{Type: "markdown", Text: md}}
	}
	return blocks
}

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
		// A raw SVG pasted into an HTML card is rendered through the SVG-safe path
		// (like the diagram block) — the UGC policy below would otherwise strip
		// every SVG element and leave only run-together text.
		if blk.Type == "html" && LooksLikeSVG(blk.Text) {
			out.WriteString(renderRawSVG(blk.Text))
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
// verbatim. A source that is itself raw SVG markup (pasted from a design tool) is
// sanitised and shown as-is; unsupported/malformed DSL sources degrade to an
// escaped code block.
func renderDiagramBlock(blk Block, plain *strings.Builder) string {
	src := blk.Text
	if LooksLikeSVG(src) {
		return renderRawSVG(src)
	}
	svg, err := diagram.Render(src)
	if err != nil {
		var f strings.Builder
		f.WriteString(`<pre class="vp-diagram-fallback"><code>` + html.EscapeString(src) + `</code></pre>`)
		return policy.Sanitize(f.String())
	}
	return `<figure class="vp-diagram-figure">` + svg + `</figure>`
}

// maxRawSVGBytes bounds a single pasted SVG so a runaway document cannot bloat a
// post or a render pass.
const maxRawSVGBytes = 512 * 1024

var (
	svgOpenRe = regexp.MustCompile(`(?is)^\s*(?:<\?xml[^>]*\?>\s*)?(?:<!doctype[^>]*>\s*)?<svg[\s>]`)
	// CSS url() references, wherever they appear — used by filterSVGCSSURLs to
	// scrub style ATTRIBUTES (a <style> element can no longer exist at all: the
	// parse-based sanitiser drops it wholesale). Local fragment references are
	// kept, because they are how SVG works — fill:url(#gradient) is the normal
	// case and breaking it would make the sanitiser useless rather than safe.
	svgCSSURLRe  = regexp.MustCompile(`(?is)url\(\s*("[^"]*"|'[^']*'|[^)]*)\)`)
	svgJSProtoRe = regexp.MustCompile(`(?is)(javascript|vbscript|data\s*:\s*text/html)\s*:`)
)

// LooksLikeSVG reports whether src is (starts as) a standalone SVG document —
// optionally preceded by an XML declaration or doctype.
func LooksLikeSVG(src string) bool {
	return svgOpenRe.MatchString(src)
}

// SanitizeSVG defensively strips the active-content vectors from a raw SVG.
//
// It is PARSE-BASED by design (audit: the previous implementation stripped
// deny-listed constructs with regular expressions and re-assembled the
// remainder, which two attack classes defeat — an unterminated `<script>`
// that never matches a `</script>` pattern yet still parses in recovery mode,
// and nested/recombined markup such as `<scr<script>ipt>` where removing the
// inner match leaves exactly the outer tag behind). This version walks the
// input with encoding/xml and REBUILDS the document from an allowlist: known
// shape/paint elements, known-safe attributes, href only as a same-document
// #fragment, comments/PIs/DTD dropped, style values filtered for url(). Any
// token error — malformed XML, undefined entities, tag mismatch — rejects the
// whole input. If the strict XML parse succeeds there is no recovery-mode
// ambiguity left for a browser to exploit.
//
// ok=false when the input is too large, fails to parse, or contains no allowed
// <svg> root. Defence in depth: the public CSP already blocks inline script
// execution; this layer additionally stops off-site resource loads (reader
// IP/referrer beacons) even where CSP allows img-src https:.
func SanitizeSVG(src string) (string, bool) {
	src = strings.TrimSpace(src)
	if src == "" || len(src) > maxRawSVGBytes {
		return "", false
	}
	dec := xml.NewDecoder(strings.NewReader(src))
	dec.Strict = true

	var out strings.Builder
	sawSVG := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false // malformed / DTD tricks / tag mismatch — reject all
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			if !svgAllowedElem(name) {
				dec.Skip() // consume the entire subtree, content included
				continue
			}
			if name == "svg" {
				if sawSVG {
					return "", false // nested root: reject
				}
				sawSVG = true
			}
			out.WriteByte('<')
			out.WriteString(name)
			for _, a := range t.Attr {
				if v, keep := sanitizeSVGAttr(a); keep {
					out.WriteByte(' ')
					out.WriteString(attrName(a))
					out.WriteString(`="`)
					out.WriteString(escapeSVGAttrValue(v))
					out.WriteByte('"')
				}
			}
			out.WriteByte('>')
		case xml.EndElement:
			if !svgAllowedElem(t.Name.Local) {
				continue // its start was skipped; nothing to emit
			}
			out.WriteString("</" + t.Name.Local + ">")
		case xml.CharData:
			out.WriteString(escapeSVGText(string(t)))
		default:
			// Comments, directives, processing instructions: never re-emitted.
			// They are parser-state noise at best and splitter attacks at worst.
			continue
		}
	}
	if !sawSVG {
		return "", false
	}
	return out.String(), true
}

// svgAllowedElem reports whether an element may appear in sanitized output.
// Everything not listed — script, style, foreignObject, image, use, a,
// animation elements — is dropped wholesale, content and all.
func svgAllowedElem(name string) bool {
	switch name {
	case "svg", "g", "defs", "symbol", "use", "title", "desc", "metadata",
		"path", "rect", "circle", "ellipse", "line", "polyline", "polygon",
		"text", "tspan", "textPath",
		"marker", "clipPath", "mask", "pattern",
		"linearGradient", "radialGradient", "stop",
		"filter", "feBlend", "feColorMatrix", "feComponentTransfer",
		"feComposite", "feConvolveMatrix", "feDiffuseLighting",
		"feDisplacementMap", "feDistantLight", "feDropShadow", "feFlood",
		"feFuncA", "feFuncB", "feFuncG", "feFuncR", "feGaussianBlur",
		"feImage", "feMerge", "feMergeNode", "feMorphology", "feOffset",
		"fePointLight", "feSpecularLighting", "feSpotLight", "feTile",
		"feTurbulence":
		return true
	}
	return false
}

// sanitizeSVGAttr filters one attribute. href/xlink:href survive only as
// same-document fragments; event handlers are impossible by construction
// (never on the allowlist) and style values are URL-filtered. Returns
// ("", false) to drop the attribute entirely.
func sanitizeSVGAttr(a xml.Attr) (string, bool) {
	local := strings.ToLower(a.Name.Local)
	isXLink := strings.HasSuffix(strings.ToLower(a.Name.Space), "xlink")
	switch {
	case strings.HasPrefix(local, "on"):
		return "", false // event-handler shape, however spelled
	case local == "href" || (isXLink && local == "href"):
		v := strings.TrimSpace(a.Value)
		if strings.HasPrefix(v, "#") {
			return v, true // gradient/marker refs stay same-document
		}
		return "", false // everything off-site or scheme-bearing goes
	case local == "style":
		return filterSVGCSSURLs(a.Value), true
	case local == "xmlns":
		return "http://www.w3.org/2000/svg", true
	case local == "xlink":
		return "http://www.w3.org/1999/xlink", true
	case local == "src" || local == "srcset" || local == "action" ||
		local == "formaction" || local == "data" || local == "poster":
		return "", false
	}
	if !svgAllowedAttr(local) {
		return "", false
	}
	return a.Value, true
}

// svgAllowedAttr is the attribute allowlist shared by every element: geometry,
// paint, filter plumbing, typography and accessibility metadata only.
func svgAllowedAttr(local string) bool {
	switch local {
	case "id", "class", "role", "aria-hidden", "aria-label", "aria-labelledby",
		"title", "tabindex", "focusable",
		"x", "y", "x1", "y1", "x2", "y2", "cx", "cy", "r", "rx", "ry",
		"width", "height", "d", "points", "transform", "pathlength",
		"viewbox", "preserveaspectratio",
		"fill", "fill-opacity", "fill-rule", "stroke", "stroke-width",
		"stroke-opacity", "stroke-linecap", "stroke-linejoin",
		"stroke-dasharray", "stroke-dashoffset", "paint-order",
		"opacity", "color", "clip-path", "clip-rule", "mask",
		"filter", "flood-color", "flood-opacity",
		"offset", "stop-color", "stop-opacity",
		"gradientunits", "gradienttransform", "spreadmethod", "fx", "fy", "fr",
		"patternunits", "patterncontentunits", "patterntransform",
		"maskunits", "maskcontentunits", "clippathunits", "primitiveunits",
		"markerwidth", "markerheight", "refx", "refy", "orient",
		"marker-start", "marker-mid", "marker-end",
		"in", "in2", "result", "values", "mode", "operator", "k1", "k2", "k3", "k4",
		"stddeviation", "dx", "dy", "scale", "angle", "radius", "surfacescale",
		"diffuseconstant", "specularconstant", "specularexponent",
		"basefrequency", "numoctaves", "seed", "stitchtiles",
		"xchannelselector", "ychannelselector",
		"tablevalues", "slope", "intercept", "amplitude", "exponent",
		"azimuth", "elevation", "limitingconeangle",
		"pointsatx", "pointsaty", "pointsatz",
		"font-family", "font-size", "font-weight", "font-style",
		"text-anchor", "dominant-baseline", "alignment-baseline", "baseline-shift",
		"letter-spacing", "word-spacing", "text-decoration", "direction",
		"xml:space", "xml:lang", "lang":
		return true
	}
	return false
}

// attrName renders an xml.Attr's full name, preserving the xlink namespace on
// href so downstream processors keep resolving it correctly.
func attrName(a xml.Attr) string {
	if a.Name.Space != "" {
		return a.Name.Space + ":" + a.Name.Local
	}
	return a.Name.Local
}

// escapeSVGAttrValue makes a value safe inside a double-quoted attribute.
func escapeSVGAttrValue(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// escapeSVGText makes character data safe as text content.
func escapeSVGText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// filterSVGCSSURLs strips every CSS url() that is not a same-document
// fragment reference from a style value, and neutralises script pseudo-
// schemes while it is there.
func filterSVGCSSURLs(style string) string {
	style = svgCSSURLRe.ReplaceAllStringFunc(style, func(m string) string {
		inner := m[strings.IndexByte(m, '(')+1 : len(m)-1]
		inner = strings.TrimSpace(inner)
		inner = strings.Trim(inner, `"'`)
		if strings.HasPrefix(inner, "#") {
			return m
		}
		return "none"
	})
	return svgJSProtoRe.ReplaceAllString(style, "")
}

// renderRawSVG sanitises a pasted SVG and wraps it in a trusted, constant
// <figure>. Like the diagram engine's output it is returned verbatim (it does not
// pass through the UGC policy, which would strip every SVG element); safety comes
// from SanitizeSVG plus the strict CSP backstop. Invalid/oversized input degrades
// to an escaped code block so nothing renders unsafely.
func renderRawSVG(src string) string {
	svg, ok := SanitizeSVG(src)
	if !ok {
		return `<pre class="vp-diagram-fallback"><code>` + html.EscapeString(strings.TrimSpace(src)) + `</code></pre>`
	}
	return `<figure class="vp-diagram-figure vp-svg-figure">` + svg + `</figure>`
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
		if !safeImageURL(blk.URL) {
			return
		}
		figCls := "vp-figure"
		switch safeLang(blk.Width) {
		case "wide":
			figCls += " vp-figure--wide"
		case "full":
			figCls += " vp-figure--full"
		}
		b.WriteString(`<figure class="` + figCls + `"><img src="` + html.EscapeString(blk.URL) +
			`" alt="` + html.EscapeString(blk.Alt) + `" loading="lazy" referrerpolicy="no-referrer">`)
		if strings.TrimSpace(blk.Caption) != "" {
			b.WriteString(`<figcaption>` + renderInlineHTML(blk.Caption) + `</figcaption>`)
			plain.WriteString(blk.Caption + " ")
		}
		b.WriteString(`</figure>`)
		if blk.Alt != "" {
			plain.WriteString(blk.Alt + " ")
		}
	case "gallery":
		// A responsive image gallery (up to 9 images). Each image is a flex item
		// whose width the public CSS balances per row; an optional shared caption
		// follows. Only well-formed URLs are emitted; empties are skipped.
		imgs := make([]string, 0, len(blk.Images))
		for _, u := range blk.Images {
			if safeImageURL(u) {
				imgs = append(imgs, u)
			}
		}
		if len(imgs) == 0 {
			return
		}
		if len(imgs) > 9 {
			imgs = imgs[:9]
		}
		b.WriteString(`<figure class="vp-gallery"><div class="vp-gallery__grid">`)
		for _, u := range imgs {
			b.WriteString(`<div class="vp-gallery__item"><img src="` + html.EscapeString(u) + `" alt="" loading="lazy" referrerpolicy="no-referrer"></div>`)
		}
		b.WriteString(`</div>`)
		if strings.TrimSpace(blk.Caption) != "" {
			b.WriteString(`<figcaption>` + renderInlineHTML(blk.Caption) + `</figcaption>`)
			plain.WriteString(blk.Caption + " ")
		}
		b.WriteString(`</figure>`)
	case "html":
		// Raw-HTML card: the operator's markup is emitted as-is, then the caller's
		// bluemonday UGC pass sanitises it exactly like every other block — so it
		// can carry rich markup (links, lists, tables, images, formatting) but not
		// scripts, event handlers, forms, or off-site iframes.
		if strings.TrimSpace(blk.Text) == "" {
			return
		}
		b.WriteString(`<div class="vp-html">` + blk.Text + `</div>`)
	case "markdown":
		// Markdown card: a full Markdown sub-document rendered to HTML and then
		// UGC-sanitised by the caller.
		if strings.TrimSpace(blk.Text) == "" {
			return
		}
		var mdBuf bytes.Buffer
		if err := blockMD.Convert([]byte(blk.Text), &mdBuf); err != nil {
			b.WriteString(`<div class="vp-md"><p>` + html.EscapeString(blk.Text) + `</p></div>`)
		} else {
			b.WriteString(`<div class="vp-md">` + mdBuf.String() + `</div>`)
		}
		plain.WriteString(blk.Text + " ")
	case "embed":
		if strings.TrimSpace(blk.URL) == "" {
			return
		}
		// Video facade (click-to-load): render a poster + play button, never an
		// iframe. The vetted cookie-free embed URL is carried in data-embed-src so
		// public/video-facade.js can inject a sandboxed iframe only on click; the
		// page CSP narrowly admits the origin only when this attribute is present.
		//
		// Not in a Tor Space. There, applyOnionCSP strips every external origin
		// from every directive, frame-src included, so the iframe the facade
		// injects on click is refused by the page's own policy. What the reader
		// got was a play button that did nothing at all and said nothing about
		// why — the control was not weakened, it was absent, and the interface
		// went on advertising it. Falling through to the link card is the honest
		// rendering: it names the provider, carries the same poster from local
		// media, and links out, which is a thing the reader's browser can
		// actually do. This is deliberately decided here rather than by dropping
		// the attribute later, so nothing downstream has to infer intent from a
		// facade with no source.
		if blk.Kind == "video" && !config.Cfg.OnionMode {
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
