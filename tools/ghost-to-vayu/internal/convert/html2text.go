// Package convert turns Ghost HTML / mobiledoc content into clean plain text
// suitable for VayuPress's content field.
package convert

import (
	"encoding/json"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var (
	reMultiSpace  = regexp.MustCompile(`[ \t]+`)
	reMultiBlank  = regexp.MustCompile(`\n{3,}`)
	reHTMLComment = regexp.MustCompile(`<!--.*?-->`)
)

// HTMLToText converts a Ghost HTML string to clean plain text.
// Block-level elements produce newlines; inline elements are collapsed.
func HTMLToText(src string) string {
	src = reHTMLComment.ReplaceAllString(src, "")
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		// fallback: strip tags naively
		return stripTagsNaive(src)
	}
	var sb strings.Builder
	walkNode(doc, &sb)
	out := reMultiBlank.ReplaceAllString(sb.String(), "\n\n")
	return strings.TrimSpace(out)
}

func walkNode(n *html.Node, sb *strings.Builder) {
	if n.Type == html.TextNode {
		t := reMultiSpace.ReplaceAllString(n.Data, " ")
		sb.WriteString(t)
		return
	}
	if n.Type == html.ElementNode {
		tag := strings.ToLower(n.FirstChild.Data) // never called; use n.Data below
		tag = strings.ToLower(n.Data)
		blockBefore := isBlock(tag)
		if blockBefore {
			switch tag {
			case "li":
				sb.WriteString("\n• ")
			case "hr":
				sb.WriteString("\n---\n")
				return
			default:
				sb.WriteString("\n")
			}
		}
		if tag == "img" {
			if alt := attrVal(n, "alt"); alt != "" {
				sb.WriteString("[image: " + alt + "]")
			}
		} else if tag == "a" {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walkNode(c, sb)
			}
			if href := attrVal(n, "href"); href != "" {
				sb.WriteString(" (" + href + ")")
			}
			return
		} else if tag == "br" {
			sb.WriteString("\n")
		} else {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walkNode(c, sb)
			}
		}
		if blockBefore {
			sb.WriteString("\n")
		}
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkNode(c, sb)
	}
}

func isBlock(tag string) bool {
	switch tag {
	case "p", "div", "section", "article", "header", "footer",
		"h1", "h2", "h3", "h4", "h5", "h6",
		"ul", "ol", "li", "blockquote", "pre", "figure", "figcaption",
		"table", "tr", "td", "th", "thead", "tbody", "tfoot":
		return true
	}
	return false
}

func attrVal(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func stripTagsNaive(s string) string {
	re := regexp.MustCompile(`<[^>]+>`)
	return strings.TrimSpace(re.ReplaceAllString(s, " "))
}

// MobiledocToText extracts plain text from Ghost's mobiledoc JSON format.
// Ghost 3.x and earlier use mobiledoc; Ghost 4+ uses HTML rendered server-side.
func MobiledocToText(raw string) string {
	if raw == "" {
		return ""
	}
	var doc struct {
		Sections []json.RawMessage `json:"sections"`
		Cards    []json.RawMessage `json:"cards"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, s := range doc.Sections {
		var section []json.RawMessage
		if err := json.Unmarshal(s, &section); err != nil {
			continue
		}
		if len(section) < 2 {
			continue
		}
		var typeNum float64
		json.Unmarshal(section[0], &typeNum)
		switch int(typeNum) {
		case 1: // markup section — [1, tagName, markers]
			extractMarkupSection(section, &sb)
		case 10: // card section — [10, index, payload]
			// cards are embedded media; skip or note
			sb.WriteString("\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

func extractMarkupSection(section []json.RawMessage, sb *strings.Builder) {
	if len(section) < 3 {
		return
	}
	var markers []json.RawMessage
	json.Unmarshal(section[2], &markers)
	sb.WriteString("\n")
	for _, m := range markers {
		var marker []json.RawMessage
		json.Unmarshal(m, &marker)
		if len(marker) < 4 {
			continue
		}
		var text string
		json.Unmarshal(marker[3], &text)
		sb.WriteString(text)
	}
	sb.WriteString("\n")
}

// BestContent picks the richest available content from a Ghost post.
// Priority: HTML > Lexical (rendered text) > Mobiledoc.
func BestContent(htmlSrc, mobiledoc, lexical string) string {
	if htmlSrc != "" {
		return HTMLToText(htmlSrc)
	}
	if lexical != "" {
		// Lexical is a JSON-based editor format (Ghost 5.x). Extract text blocks.
		t := lexicalToText(lexical)
		if t != "" {
			return t
		}
	}
	if mobiledoc != "" {
		return MobiledocToText(mobiledoc)
	}
	return ""
}

func lexicalToText(raw string) string {
	// Ghost Lexical stores content as nested JSON with paragraph/heading nodes
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
		extractLexicalNode(child, &sb)
	}
	return strings.TrimSpace(sb.String())
}

func extractLexicalNode(raw json.RawMessage, sb *strings.Builder) {
	var node struct {
		Type     string            `json:"type"`
		Text     string            `json:"text"`
		Children []json.RawMessage `json:"children"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		return
	}
	if node.Text != "" {
		sb.WriteString(node.Text)
	}
	for _, c := range node.Children {
		extractLexicalNode(c, sb)
	}
	switch node.Type {
	case "paragraph", "heading", "quote", "listitem":
		sb.WriteString("\n\n")
	}
}
