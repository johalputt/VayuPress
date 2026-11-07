// SPDX-License-Identifier: Apache-2.0

// Package mdrender writes a post, or a listing of posts, as Markdown for
// agents and tools, from the same model the reader's HTML is rendered from:
// a post written in the editor from its blocks (whose text is already
// inline Markdown), a post written as HTML from that HTML, which is its
// source. It never reads a rendered page: nothing a theme, a script or the
// page around a post adds reaches the Markdown.
package mdrender

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/johalputt/vayupress/internal/blockrender"
)

// Post is what a post's Markdown is made from.
type Post struct {
	Title      string
	URL        string // the post's own address
	Published  time.Time
	Tags       []string
	BlocksJSON string // the editor's blocks, when the post has them
	HTML       string // the post's HTML, when it has no blocks
}

// RenderPost writes p: its title, where it lives, when it was published and
// its topics, then its body.
func RenderPost(p Post) string {
	var b strings.Builder
	b.WriteString("# " + oneLine(p.Title) + "\n\n")
	if p.URL != "" {
		b.WriteString("- URL: " + p.URL + "\n")
	}
	if !p.Published.IsZero() {
		b.WriteString("- Published: " + p.Published.UTC().Format("2006-01-02") + "\n")
	}
	if len(p.Tags) > 0 {
		b.WriteString("- Topics: " + strings.Join(p.Tags, ", ") + "\n")
	}
	b.WriteString("\n")
	body := ""
	if strings.TrimSpace(p.BlocksJSON) != "" {
		if md, err := fromBlocks(p.BlocksJSON); err == nil {
			body = md
		}
	}
	if body == "" {
		body = FromHTML(p.HTML)
	}
	b.WriteString(strings.TrimSpace(body) + "\n")
	return b.String()
}

// Item is one post in a listing.
type Item struct {
	Title, URL, Excerpt string
	Published           time.Time
}

// RenderListing writes a listing: its title, then each post as a heading
// with its link, date and excerpt.
func RenderListing(title string, items []Item) string {
	var b strings.Builder
	b.WriteString("# " + oneLine(title) + "\n")
	for _, it := range items {
		b.WriteString("\n## [" + escapeLinkText(oneLine(it.Title)) + "](" + it.URL + ")\n")
		if !it.Published.IsZero() {
			b.WriteString("\n" + it.Published.UTC().Format("2006-01-02") + "\n")
		}
		if e := oneLine(it.Excerpt); e != "" {
			b.WriteString("\n" + e + "\n")
		}
	}
	return b.String()
}

// fromBlocks writes the editor's blocks. Text fields are already inline
// Markdown (what the HTML renderer converts), so they are written as they
// are; only the structure around them is made here.
func fromBlocks(blocksJSON string) (string, error) {
	var blocks []blockrender.Block
	if err := json.Unmarshal([]byte(blocksJSON), &blocks); err != nil {
		return "", err
	}
	var parts []string
	for _, blk := range blocks {
		if s := strings.TrimRight(fromBlock(blk), "\n"); strings.TrimSpace(s) != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

func fromBlock(blk blockrender.Block) string {
	switch blk.Type {
	case "paragraph":
		return blk.Text
	case "heading":
		lvl := blk.Level
		if lvl < 2 || lvl > 4 {
			lvl = 2
		}
		return strings.Repeat("#", lvl) + " " + oneLine(blk.Text)
	case "quote", "callout":
		return quote(blk.Text)
	case "code":
		return fence(blk.Lang, blk.Text)
	case "list":
		var b strings.Builder
		for i, it := range blk.Items {
			if blk.Style == "ordered" {
				b.WriteString(strconv.Itoa(i+1) + ". ")
			} else {
				b.WriteString("- ")
			}
			b.WriteString(oneLine(it) + "\n")
		}
		return b.String()
	case "tasklist":
		var b strings.Builder
		for i, it := range blk.Items {
			box := "[ ]"
			if i < len(blk.Checked) && blk.Checked[i] {
				box = "[x]"
			}
			b.WriteString("- " + box + " " + oneLine(it) + "\n")
		}
		return b.String()
	case "image":
		s := image(blk.Alt, blk.URL)
		if blk.Caption != "" {
			s += "\n\n*" + oneLine(blk.Caption) + "*"
		}
		return s
	case "gallery":
		var out []string
		for _, u := range blk.Images {
			out = append(out, image("", u))
		}
		if blk.Caption != "" {
			out = append(out, "*"+oneLine(blk.Caption)+"*")
		}
		return strings.Join(out, "\n\n")
	case "embed":
		title := blk.Title
		if title == "" {
			title = blk.URL
		}
		s := "[" + escapeLinkText(oneLine(title)) + "](" + blk.URL + ")"
		if blk.Description != "" {
			s += "\n\n" + oneLine(blk.Description)
		}
		return s
	case "divider":
		return "---"
	case "table":
		return table(blk.Header, blk.Rows)
	case "toggle":
		return "**" + oneLine(blk.Summary) + "**\n\n" + blk.Text
	case "markdown":
		return blk.Text
	case "html":
		return FromHTML(blk.Text)
	case "diagram":
		return fence("", blk.Text)
	}
	return ""
}

// FromHTML writes a post's own HTML as Markdown: headings, paragraphs,
// lists, quotes, code, links, images, tables and emphasis. A style, script,
// SVG, form or button carries nothing a reader reads and is left out, as is
// a link or image whose address is not http, https, a fragment or a path on
// the site.
func FromHTML(src string) string {
	if strings.TrimSpace(src) == "" {
		return ""
	}
	nodes, err := html.ParseFragment(strings.NewReader(src), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return ""
	}
	w := &writer{}
	for _, n := range nodes {
		w.block(n)
	}
	return strings.TrimSpace(collapseBlank(w.b.String()))
}

type writer struct {
	b     strings.Builder
	lists []listState
}

type listState struct {
	ordered bool
	n       int
}

// skipped are elements nothing a reader reads comes from.
var skipped = map[atom.Atom]bool{
	atom.Style: true, atom.Script: true, atom.Noscript: true, atom.Svg: true, atom.Template: true,
	atom.Form: true, atom.Button: true, atom.Input: true, atom.Select: true, atom.Textarea: true,
	atom.Iframe: true, atom.Object: true, atom.Head: true, atom.Title: true, atom.Meta: true, atom.Link: true,
}

func (w *writer) para(s string) {
	if s = strings.TrimSpace(s); s != "" {
		w.b.WriteString("\n\n" + s + "\n\n")
	}
}

func (w *writer) block(n *html.Node) {
	if n.Type == html.TextNode {
		w.para(inlineText(n.Data))
		return
	}
	if n.Type != html.ElementNode || skipped[n.DataAtom] {
		return
	}
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		lvl := int(n.Data[1] - '0')
		w.para(strings.Repeat("#", lvl) + " " + oneLine(w.inline(n)))
	case atom.P:
		w.para(w.inline(n))
	case atom.Pre:
		lang := ""
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.DataAtom == atom.Code {
				lang = langOf(c)
			}
		}
		w.para(fence(lang, textOf(n)))
	case atom.Blockquote:
		inner := &writer{}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			inner.block(c)
		}
		w.para(quote(strings.TrimSpace(collapseBlank(inner.b.String()))))
	case atom.Ul, atom.Ol:
		w.lists = append(w.lists, listState{ordered: n.DataAtom == atom.Ol})
		w.b.WriteString("\n\n")
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.DataAtom == atom.Li {
				w.item(c)
			}
		}
		w.lists = w.lists[:len(w.lists)-1]
		w.b.WriteString("\n")
	case atom.Table:
		w.para(w.table(n))
	case atom.Hr:
		w.para("---")
	case atom.Img:
		w.para(imgOf(n))
	case atom.Br:
		w.b.WriteString("\n")
	default:
		// A container (div, section, article, figure, header…): its children,
		// and its own text as a paragraph when it holds only inline content.
		if onlyInline(n) {
			w.para(w.inline(n))
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			w.block(c)
		}
	}
}

func (w *writer) item(li *html.Node) {
	st := &w.lists[len(w.lists)-1]
	st.n++
	marker := "- "
	if st.ordered {
		marker = strconv.Itoa(st.n) + ". "
	}
	indent := strings.Repeat("  ", len(w.lists)-1)
	var text strings.Builder
	var nested []*html.Node
	for c := li.FirstChild; c != nil; c = c.NextSibling {
		if c.DataAtom == atom.Ul || c.DataAtom == atom.Ol {
			nested = append(nested, c)
			continue
		}
		text.WriteString(w.inlineNode(c))
	}
	w.b.WriteString(indent + marker + oneLine(text.String()) + "\n")
	for _, n := range nested {
		w.lists = append(w.lists, listState{ordered: n.DataAtom == atom.Ol})
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.DataAtom == atom.Li {
				w.item(c)
			}
		}
		w.lists = w.lists[:len(w.lists)-1]
	}
}

func (w *writer) table(t *html.Node) string {
	var rows [][]string
	var header []string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.DataAtom == atom.Tr {
				var cells []string
				isHead := false
				for td := c.FirstChild; td != nil; td = td.NextSibling {
					if td.DataAtom == atom.Td || td.DataAtom == atom.Th {
						cells = append(cells, oneLine(w.inline(td)))
						isHead = isHead || td.DataAtom == atom.Th
					}
				}
				if isHead && header == nil && len(rows) == 0 {
					header = cells
				} else {
					rows = append(rows, cells)
				}
				continue
			}
			if !skipped[c.DataAtom] {
				walk(c)
			}
		}
	}
	walk(t)
	return table(header, rows)
}

// inline writes n's children as inline Markdown.
func (w *writer) inline(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(w.inlineNode(c))
	}
	return strings.TrimSpace(tidyInline(b.String()))
}

func (w *writer) inlineNode(n *html.Node) string {
	if n.Type == html.TextNode {
		return inlineText(n.Data)
	}
	if n.Type != html.ElementNode || skipped[n.DataAtom] {
		return ""
	}
	inner := w.inline(n)
	switch n.DataAtom {
	case atom.Strong, atom.B:
		if inner == "" {
			return ""
		}
		return "**" + inner + "**"
	case atom.Em, atom.I:
		if inner == "" {
			return ""
		}
		return "*" + inner + "*"
	case atom.Code:
		return "`" + strings.ReplaceAll(textOf(n), "`", "'") + "`"
	case atom.A:
		href := safeURL(attr(n, "href"))
		if href == "" || inner == "" {
			return inner
		}
		return "[" + escapeLinkText(inner) + "](" + href + ")"
	case atom.Img:
		return imgOf(n)
	case atom.Br:
		return "  \n"
	}
	// Any other inline or block element met inline: its text, spaced.
	if isBlockAtom(n.DataAtom) {
		return " " + inner + " "
	}
	return inner
}

var blockAtoms = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.Section: true, atom.Article: true, atom.Header: true, atom.Footer: true,
	atom.Ul: true, atom.Ol: true, atom.Li: true, atom.Table: true, atom.Pre: true, atom.Blockquote: true,
	atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true,
	atom.Figure: true, atom.Hr: true, atom.Main: true, atom.Aside: true, atom.Nav: true, atom.Details: true,
}

func isBlockAtom(a atom.Atom) bool { return blockAtoms[a] }

// onlyInline reports whether n holds no block element at any depth.
func onlyInline(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (isBlockAtom(c.DataAtom) || !onlyInline(c)) {
			return false
		}
	}
	return true
}

func imgOf(n *html.Node) string {
	return image(attr(n, "alt"), safeURL(attr(n, "src")))
}

func image(alt, src string) string {
	if src == "" {
		return ""
	}
	return "![" + escapeLinkText(oneLine(alt)) + "](" + src + ")"
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

// langOf reads a code element's language from its class
// ("language-go", "lang-go").
func langOf(n *html.Node) string {
	for _, c := range strings.Fields(attr(n, "class")) {
		for _, p := range []string{"language-", "lang-"} {
			if l, ok := strings.CutPrefix(c, p); ok {
				return l
			}
		}
	}
	return ""
}

// safeURL keeps an address an agent can follow: http or https, a fragment,
// or a path on the site. Anything else (javascript:, data:) is dropped.
func safeURL(u string) string {
	l := strings.ToLower(u)
	switch {
	case strings.HasPrefix(l, "https://"), strings.HasPrefix(l, "http://"), strings.HasPrefix(l, "/"), strings.HasPrefix(l, "#"):
		return strings.NewReplacer(" ", "%20", ")", "%29", "(", "%28").Replace(u)
	}
	return ""
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// inlineText collapses HTML whitespace as a browser does: a run becomes one
// space, kept at either end, so "<b>a</b> <i>b</i>" stays two words.
func inlineText(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		if s != "" {
			return " "
		}
		return ""
	}
	out := strings.Join(f, " ")
	if strings.TrimLeft(s, " \t\n\r") != s {
		out = " " + out
	}
	if strings.TrimRight(s, " \t\n\r") != s {
		out += " "
	}
	return out
}

// tidyInline collapses the spaces joined inline pieces leave doubled,
// keeping a line break's two trailing spaces.
func tidyInline(s string) string {
	s = strings.ReplaceAll(s, "  \n", "\x00")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.ReplaceAll(s, "\x00", "  \n")
}

func fence(lang, code string) string {
	f := "```"
	for strings.Contains(code, f) {
		f += "`"
	}
	return f + lang + "\n" + strings.TrimRight(code, "\n") + "\n" + f
}

func quote(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight("> "+l, " ")
	}
	return strings.Join(lines, "\n")
}

func table(header []string, rows [][]string) string {
	cols := len(header)
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	if cols == 0 {
		return ""
	}
	if header == nil {
		header = make([]string, cols)
	}
	row := func(cells []string) string {
		out := make([]string, cols)
		for i := range out {
			if i < len(cells) {
				out[i] = strings.ReplaceAll(cells[i], "|", "\\|")
			}
		}
		return "| " + strings.Join(out, " | ") + " |"
	}
	sep := make([]string, cols)
	for i := range sep {
		sep[i] = "---"
	}
	lines := []string{row(header), "| " + strings.Join(sep, " | ") + " |"}
	for _, r := range rows {
		lines = append(lines, row(r))
	}
	return strings.Join(lines, "\n")
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func escapeLinkText(s string) string {
	return strings.NewReplacer("[", "\\[", "]", "\\]").Replace(s)
}

// collapseBlank leaves at most one blank line between blocks.
func collapseBlank(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}
