// SPDX-License-Identifier: Apache-2.0

package docsite

import (
	"bytes"
	"html/template"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// cardify lays out a home section as the band draws it: its heading and
// opening paragraphs, then each h3 with what follows it, up to the next
// heading, as one card in a grid. A paragraph that holds no letter or digit
// (the old site's emoji over each card) is left out: Still Air draws no
// pictograms. The section's Markdown stays plain; the grid is the design's.
func cardify(body template.HTML) template.HTML {
	nodes, err := html.ParseFragment(strings.NewReader(string(body)), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return body
	}
	var out bytes.Buffer
	var cards []*bytes.Buffer
	closeCards := func() {
		if len(cards) == 0 {
			return
		}
		out.WriteString(`<div class="cards">`)
		for _, c := range cards {
			out.WriteString(`<div class="card">`)
			out.Write(c.Bytes())
			out.WriteString(`</div>`)
		}
		out.WriteString(`</div>`)
		cards = nil
	}
	for _, n := range nodes {
		if n.Type == html.TextNode && strings.TrimSpace(n.Data) == "" {
			continue
		}
		if n.DataAtom == atom.P && !hasWord(n) {
			continue
		}
		switch {
		case n.DataAtom == atom.H3:
			cards = append(cards, &bytes.Buffer{})
		case n.DataAtom == atom.H2 || n.DataAtom == atom.Hr:
			closeCards()
		}
		dst := &out
		if len(cards) > 0 {
			dst = cards[len(cards)-1]
		}
		if err := html.Render(dst, n); err != nil {
			return body
		}
	}
	closeCards()
	return template.HTML(out.String()) //nolint:gosec // re-rendered from the renderer's own sanitised output
}

// hasWord reports whether n's text holds a letter or a digit.
func hasWord(n *html.Node) bool {
	found := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found {
			return
		}
		if n.Type == html.TextNode && strings.IndexFunc(n.Data, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			found = true
			return
		}
		if n.Type == html.ElementNode && (n.DataAtom == atom.Img || n.DataAtom == atom.Code) {
			found = true
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return found
}
