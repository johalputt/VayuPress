package blockrender

// importer.go — converts legacy article HTML into a block document (ADR-0069
// Stage 1, "Convert to blocks"). The transform is intentionally conservative:
// it maps well-known block-level elements (headings, paragraphs, lists, quotes,
// code, images, dividers) to their editor-block equivalents and extracts plain
// text for inline content. Anything it does not recognise becomes a paragraph
// carrying that subtree's text, so no visible prose is dropped.
//
// Conversion is lossy for rich inline markup (a <strong> inside a paragraph
// becomes plain text), which is why callers gate it behind an explicit operator
// confirmation and never overwrite the rendered article content until the
// operator re-saves from the block editor (ADR-0069 "no silent conversion").

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// ImportHTML parses article HTML and returns the equivalent block document as a
// JSON-marshalable slice. It never returns an error for malformed HTML — the
// x/net/html parser is lenient — but returns at least one (possibly empty)
// paragraph so the editor always has something to hydrate.
func ImportHTML(content string) []Block {
	content = strings.TrimSpace(content)
	if content == "" {
		return []Block{{Type: "paragraph", Text: ""}}
	}

	// Parse as a fragment under <body> so top-level siblings are walked in order.
	root, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return []Block{{Type: "paragraph", Text: content}}
	}

	body := findBody(root)
	if body == nil {
		return []Block{{Type: "paragraph", Text: content}}
	}

	var blocks []Block
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		blocks = append(blocks, blocksFromNode(c)...)
	}

	// Drop empties produced by whitespace-only text nodes.
	cleaned := blocks[:0]
	for _, b := range blocks {
		if isEmptyBlock(b) {
			continue
		}
		cleaned = append(cleaned, b)
	}
	if len(cleaned) == 0 {
		return []Block{{Type: "paragraph", Text: ""}}
	}
	return cleaned
}

func isEmptyBlock(b Block) bool {
	switch b.Type {
	case "divider", "image", "embed":
		return false
	case "list":
		return len(b.Items) == 0
	default:
		return strings.TrimSpace(b.Text) == ""
	}
}

// blocksFromNode maps a single DOM node to zero or more blocks.
func blocksFromNode(n *html.Node) []Block {
	if n.Type == html.TextNode {
		t := strings.TrimSpace(n.Data)
		if t == "" {
			return nil
		}
		return []Block{{Type: "paragraph", Text: t}}
	}
	if n.Type != html.ElementNode {
		return nil
	}

	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		level := int(n.Data[1] - '0') // 'h2' → 2
		if level < 2 {
			level = 2
		}
		if level > 4 {
			level = 4
		}
		return []Block{{Type: "heading", Level: level, Text: nodeText(n)}}

	case atom.P:
		// A paragraph that contains only an image becomes an image block.
		if img := loneImage(n); img != nil {
			return []Block{*img}
		}
		return []Block{{Type: "paragraph", Text: nodeText(n)}}

	case atom.Ul, atom.Ol:
		items := listItems(n)
		style := "unordered"
		if n.DataAtom == atom.Ol {
			style = "ordered"
		}
		return []Block{{Type: "list", Style: style, Items: items}}

	case atom.Blockquote:
		return []Block{{Type: "quote", Text: nodeText(n)}}

	case atom.Pre:
		return []Block{{Type: "code", Text: rawText(n), Lang: codeLang(n)}}

	case atom.Hr:
		return []Block{{Type: "divider"}}

	case atom.Img:
		return []Block{imageBlock(n)}

	case atom.Figure:
		if img := loneImage(n); img != nil {
			return []Block{*img}
		}
		return paragraphsFromChildren(n)

	case atom.Div, atom.Section, atom.Article, atom.Main, atom.Header, atom.Footer:
		// Containers: recurse into children, preserving order.
		var out []Block
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			out = append(out, blocksFromNode(c)...)
		}
		return out

	default:
		// Any other element (span, a, strong at top level, etc.): keep its text.
		t := nodeText(n)
		if t == "" {
			return nil
		}
		return []Block{{Type: "paragraph", Text: t}}
	}
}

func paragraphsFromChildren(n *html.Node) []Block {
	var out []Block
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, blocksFromNode(c)...)
	}
	return out
}

// loneImage returns an image block if n's only meaningful content is a single
// <img> (optionally wrapped in <a> / <figure> with whitespace), else nil.
func loneImage(n *html.Node) *Block {
	var img *html.Node
	var foundText bool
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			switch c.Type {
			case html.TextNode:
				if strings.TrimSpace(c.Data) != "" {
					foundText = true
				}
			case html.ElementNode:
				if c.DataAtom == atom.Img {
					if img != nil {
						foundText = true // more than one image → not lone
					}
					img = c
				} else if c.DataAtom == atom.Figcaption {
					// captions are fine to ignore for the lone-image test
				} else {
					walk(c)
				}
			}
		}
	}
	walk(n)
	if img != nil && !foundText {
		b := imageBlock(img)
		return &b
	}
	return nil
}

func imageBlock(n *html.Node) Block {
	return Block{Type: "image", URL: attr(n, "src"), Alt: attr(n, "alt")}
}

// listItems returns the trimmed text of each direct <li> child.
func listItems(n *html.Node) []string {
	var items []string
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.Li {
			items = append(items, nodeText(c))
		}
	}
	return items
}

// codeLang extracts a language hint from a <code class="language-go"> child.
func codeLang(pre *html.Node) string {
	for c := pre.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.Code {
			cls := attr(c, "class")
			for _, f := range strings.Fields(cls) {
				if strings.HasPrefix(f, "language-") {
					return strings.TrimPrefix(f, "language-")
				}
				if strings.HasPrefix(f, "lang-") {
					return strings.TrimPrefix(f, "lang-")
				}
			}
		}
	}
	return ""
}

// nodeText returns the collapsed plain-text content of a node.
func nodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
			return
		}
		if node.Type == html.ElementNode && node.DataAtom == atom.Br {
			sb.WriteString("\n")
			return
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(collapseSpaces(sb.String()))
}

// rawText returns text content preserving internal newlines (for <pre>).
func rawText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
			return
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Trim(sb.String(), "\n")
}

// collapseSpaces turns runs of whitespace (except newlines) into single spaces.
func collapseSpaces(s string) string {
	var sb strings.Builder
	var lastSpace bool
	for _, r := range s {
		if r == '\n' {
			sb.WriteRune('\n')
			lastSpace = false
			continue
		}
		if r == ' ' || r == '\t' || r == '\r' {
			if !lastSpace {
				sb.WriteRune(' ')
				lastSpace = true
			}
			continue
		}
		sb.WriteRune(r)
		lastSpace = false
	}
	return sb.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func findBody(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == atom.Body {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if b := findBody(c); b != nil {
			return b
		}
	}
	return nil
}
