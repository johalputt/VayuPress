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
	"encoding/json"
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

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

// policy sanitises the assembled HTML. UGCPolicy allows a safe subset of tags
// (p, headings, lists, blockquote, pre/code, a, img, em/strong, etc.) and
// strips scripts, event handlers, and javascript: URLs. We additionally allow
// class on div/span and the validated data-embed-src / data-embed-title on the
// video-facade div (click-to-load — no iframe is present until the reader acts).
var policy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("class").OnElements("div", "span")
	p.AllowAttrs("data-embed-src").Matching(embedSrcRe).OnElements("div")
	p.AllowAttrs("data-embed-title").OnElements("div")
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
	var b strings.Builder
	var plain strings.Builder
	for _, blk := range blocks {
		renderBlock(&b, &plain, blk)
	}
	clean := policy.Sanitize(b.String())
	return clean, excerpt(plain.String()), nil
}

func renderBlock(b, plain *strings.Builder, blk Block) {
	switch blk.Type {
	case "paragraph":
		if strings.TrimSpace(blk.Text) == "" {
			return
		}
		b.WriteString("<p>" + html.EscapeString(blk.Text) + "</p>")
		plain.WriteString(blk.Text + " ")
	case "heading":
		lvl := blk.Level
		if lvl < 2 || lvl > 4 {
			lvl = 2
		}
		tag := "h" + strconv.Itoa(lvl)
		b.WriteString("<" + tag + ">" + html.EscapeString(blk.Text) + "</" + tag + ">")
		plain.WriteString(blk.Text + " ")
	case "quote":
		b.WriteString("<blockquote><p>" + html.EscapeString(blk.Text) + "</p></blockquote>")
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
			b.WriteString("<li>" + html.EscapeString(it) + "</li>")
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
		b.WriteString(`<div class="` + cls + `"><p>` + html.EscapeString(blk.Text) + `</p></div>`)
		plain.WriteString(blk.Text + " ")
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
