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
	"strconv"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// policy sanitises the assembled HTML. UGCPolicy allows a safe subset of tags
// (p, headings, lists, blockquote, pre/code, a, img, em/strong, etc.) and
// strips scripts, event handlers, and javascript: URLs.
var policy = bluemonday.UGCPolicy()

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
