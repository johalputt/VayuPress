// SPDX-License-Identifier: Apache-2.0

// Package markdown is every Markdown renderer VayuPress uses, each configured
// once. goldmark is imported here and nowhere else in the module, so a change
// of goldmark version is a change to this package, and the corpus test beside
// it holds each renderer to the HTML it produced before the change.
//
// None of these output is trusted: every caller sanitises what it renders.
package markdown

import (
	"io"

	"github.com/yuin/goldmark/v2/extension"
	"github.com/yuin/goldmark/v2/parser"
	"github.com/yuin/goldmark/v2/renderer/html"
)

// Renderer is one configured parser and HTML renderer. goldmark v2 keeps the
// two apart and has no Markdown type of its own; Convert is the v1 method of
// that name, which every caller already uses.
type Renderer struct {
	p parser.Parser
	r html.Renderer
}

// Convert renders the Markdown in src to w.
func (m Renderer) Convert(src []byte, w io.Writer) error {
	return m.r.Render(w, src, m.p.Parse(src))
}

// gfm is GitHub-flavoured Markdown's parser and renderer extensions. v2 has
// each renderer hold extension instances of its own (a shared one is
// deprecated), so each call makes new ones.
func gfm() (parser.Option, html.Option) {
	return parser.WithExtensions(extension.NewGFMParser()), html.WithExtensions(extension.NewGFMHTMLRenderer())
}

// Document renders a whole document with GitHub-flavoured Markdown and an id
// on every heading, so a section can be linked: an ADR in the console, a page
// of the docs site, a post imported from another platform.
var Document = func() Renderer {
	po, ro := gfm()
	return Renderer{parser.New(po, parser.WithAutoHeadingID()), html.New(ro)}
}()

// Mail renders the composer's Markdown. Hard wraps are on because people write
// mail with meaningful line breaks and expect them kept; GFM covers the lists,
// quotes, code fences and strikethrough the toolbar emits.
var Mail = func() Renderer {
	po, ro := gfm()
	return Renderer{parser.New(po), html.New(ro, html.WithHardWraps())}
}()

// Inline renders inline Markdown (bold, italic, inline code, links,
// strikethrough) inside a block's text, with its line breaks kept.
var Inline = Renderer{
	parser.New(parser.WithExtensions(extension.NewStrikethroughParser(), extension.NewLinkifyParser())),
	html.New(html.WithHardWraps(), html.WithExtensions(extension.NewStrikethroughHTMLRenderer())),
}

// Block renders the "markdown" block of a post: headings, lists, tables,
// blockquotes, code fences, task lists and footnotes.
var Block = func() Renderer {
	po, ro := gfm()
	return Renderer{
		parser.New(po, parser.WithExtensions(extension.NewFootnoteParser())),
		html.New(ro, html.WithExtensions(extension.NewFootnoteHTMLRenderer())),
	}
}()
