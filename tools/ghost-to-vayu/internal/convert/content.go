// Package convert prepares Ghost post bodies for VayuPress.
//
// VayuPress stores article content as HTML and sanitizes it with bluemonday
// (UGCPolicy) before rendering it as raw HTML. So the right thing to do is
// pass Ghost's already-clean HTML straight through — images (Unsplash/Pixaway),
// links, headings, and formatting are all preserved, and VayuPress strips
// anything unsafe at render time.
//
// Ghost stores content in three possible columns depending on version:
//   - html      — server-rendered HTML (Ghost 2.x+, almost always populated)
//   - lexical   — JSON editor state (Ghost 5.x), used when html is empty
//   - mobiledoc — JSON editor state (Ghost 1.x–4.x), used as last resort
package convert

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	reHTMLComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	reBlankLines  = regexp.MustCompile(`\n{3,}`)
)

// BestContent returns the richest available HTML body for a Ghost post.
//
// Priority: html > lexical > mobiledoc. If featureImage is set and is not
// already referenced in the body, it is prepended as a leading <figure> so the
// post's hero image survives the migration too.
func BestContent(htmlSrc, mobiledoc, lexical, featureImage string) string {
	var body string
	switch {
	case strings.TrimSpace(htmlSrc) != "":
		body = cleanHTML(htmlSrc)
	case strings.TrimSpace(lexical) != "":
		body = lexicalToHTML(lexical)
	case strings.TrimSpace(mobiledoc) != "":
		body = mobiledocToHTML(mobiledoc)
	}

	featureImage = strings.TrimSpace(featureImage)
	if featureImage != "" && !strings.Contains(body, featureImage) {
		fig := `<figure><img src="` + htmlEscapeAttr(featureImage) + `" alt=""></figure>`
		body = fig + "\n" + body
	}
	return strings.TrimSpace(body)
}

// cleanHTML strips HTML comments and collapses excessive blank lines, leaving
// the markup otherwise intact for VayuPress's sanitizer to handle.
func cleanHTML(s string) string {
	s = reHTMLComment.ReplaceAllString(s, "")
	s = reBlankLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func htmlEscapeAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func htmlEscapeText(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// ── Mobiledoc → HTML (Ghost 1.x–4.x fallback) ───────────────────────────────

// mobiledocToHTML renders the text of a Ghost mobiledoc document as simple HTML.
// Markup sections become their wrapping block tag (<p>, <h2>, …); image cards
// become <img>. This is a best-effort fallback — Ghost almost always also stores
// rendered html, which we prefer.
func mobiledocToHTML(raw string) string {
	var doc struct {
		Sections []json.RawMessage `json:"sections"`
		Cards    []json.RawMessage `json:"cards"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return ""
	}

	// Pre-decode cards: each is [cardName, payload].
	cards := make([]struct {
		Name    string
		Payload map[string]json.RawMessage
	}, len(doc.Cards))
	for i, c := range doc.Cards {
		var tuple []json.RawMessage
		if json.Unmarshal(c, &tuple) == nil && len(tuple) == 2 {
			json.Unmarshal(tuple[0], &cards[i].Name)
			json.Unmarshal(tuple[1], &cards[i].Payload)
		}
	}

	var sb strings.Builder
	for _, s := range doc.Sections {
		var section []json.RawMessage
		if json.Unmarshal(s, &section) != nil || len(section) < 2 {
			continue
		}
		var typeNum float64
		json.Unmarshal(section[0], &typeNum)

		switch int(typeNum) {
		case 1: // markup section: [1, tagName, markers]
			tag := "p"
			json.Unmarshal(section[1], &tag)
			if !allowedBlockTag(tag) {
				tag = "p"
			}
			text := markersText(section)
			if strings.TrimSpace(text) == "" {
				continue
			}
			sb.WriteString("<" + tag + ">" + text + "</" + tag + ">\n")
		case 2, 10: // card section: [10, cardIndex]
			var idx float64
			json.Unmarshal(section[1], &idx)
			if int(idx) >= 0 && int(idx) < len(cards) {
				sb.WriteString(cardHTML(cards[int(idx)].Name, cards[int(idx)].Payload))
			}
		}
	}
	return sb.String()
}

func markersText(section []json.RawMessage) string {
	if len(section) < 3 {
		return ""
	}
	var markers []json.RawMessage
	json.Unmarshal(section[2], &markers)
	var sb strings.Builder
	for _, m := range markers {
		var marker []json.RawMessage
		if json.Unmarshal(m, &marker) != nil || len(marker) < 4 {
			continue
		}
		var text string
		json.Unmarshal(marker[3], &text)
		sb.WriteString(htmlEscapeText(text))
	}
	return sb.String()
}

func cardHTML(name string, payload map[string]json.RawMessage) string {
	switch name {
	case "image":
		var src, caption string
		if raw, ok := payload["src"]; ok {
			json.Unmarshal(raw, &src)
		}
		if raw, ok := payload["caption"]; ok {
			json.Unmarshal(raw, &caption)
		}
		if src == "" {
			return ""
		}
		out := `<figure><img src="` + htmlEscapeAttr(src) + `" alt="">`
		if caption != "" {
			out += `<figcaption>` + htmlEscapeText(caption) + `</figcaption>`
		}
		return out + "</figure>\n"
	case "html", "markdown":
		var html string
		if raw, ok := payload["html"]; ok {
			json.Unmarshal(raw, &html)
		}
		if raw, ok := payload["markdown"]; ok && html == "" {
			json.Unmarshal(raw, &html)
		}
		return cleanHTML(html) + "\n"
	default:
		return ""
	}
}

func allowedBlockTag(tag string) bool {
	switch tag {
	case "p", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre":
		return true
	}
	return false
}

// ── Lexical → HTML (Ghost 5.x fallback) ─────────────────────────────────────

// lexicalToHTML renders a Ghost Lexical document as simple HTML, preserving
// paragraphs, headings, and images. Best-effort fallback for the rare case
// where the html column is empty.
func lexicalToHTML(raw string) string {
	var root struct {
		Root struct {
			Children []json.RawMessage `json:"children"`
		} `json:"root"`
	}
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, child := range root.Root.Children {
		lexicalNodeHTML(child, &sb)
	}
	return sb.String()
}

func lexicalNodeHTML(raw json.RawMessage, sb *strings.Builder) {
	var node struct {
		Type     string            `json:"type"`
		Tag      string            `json:"tag"`
		Text     string            `json:"text"`
		Src      string            `json:"src"`
		Children []json.RawMessage `json:"children"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		return
	}

	switch node.Type {
	case "image":
		if node.Src != "" {
			sb.WriteString(`<figure><img src="` + htmlEscapeAttr(node.Src) + `" alt=""></figure>` + "\n")
		}
		return
	case "heading":
		tag := node.Tag
		if !allowedBlockTag(tag) {
			tag = "h2"
		}
		sb.WriteString("<" + tag + ">")
		lexicalChildrenText(node.Children, sb)
		sb.WriteString("</" + tag + ">\n")
		return
	case "quote":
		sb.WriteString("<blockquote>")
		lexicalChildrenText(node.Children, sb)
		sb.WriteString("</blockquote>\n")
		return
	case "paragraph", "":
		sb.WriteString("<p>")
		lexicalChildrenText(node.Children, sb)
		sb.WriteString("</p>\n")
		return
	default:
		// Lists and other containers: emit their text inside a paragraph so
		// nothing is lost, even if structure is flattened.
		sb.WriteString("<p>")
		lexicalChildrenText(node.Children, sb)
		sb.WriteString("</p>\n")
	}
}

func lexicalChildrenText(children []json.RawMessage, sb *strings.Builder) {
	for _, c := range children {
		var node struct {
			Text     string            `json:"text"`
			Children []json.RawMessage `json:"children"`
		}
		if json.Unmarshal(c, &node) != nil {
			continue
		}
		if node.Text != "" {
			sb.WriteString(htmlEscapeText(node.Text))
		}
		if len(node.Children) > 0 {
			lexicalChildrenText(node.Children, sb)
		}
	}
}
