// SPDX-License-Identifier: Apache-2.0

// Package markdown is every Markdown renderer VayuPress uses, each configured
// once. goldmark is imported here and nowhere else in the module, so a change
// of goldmark version is a change to this package, and the corpus test beside
// it holds each renderer to the HTML it produced before the change.
//
// None of these output is trusted: every caller sanitises what it renders.
package markdown

import (
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

// Document renders a whole document with GitHub-flavoured Markdown and an id
// on every heading, so a section can be linked: an ADR in the console, a page
// of the docs site, a post imported from another platform.
var Document = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
)

// Mail renders the composer's Markdown. Hard wraps are on because people write
// mail with meaningful line breaks and expect them kept; GFM covers the lists,
// quotes, code fences and strikethrough the toolbar emits.
var Mail = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

// Inline renders inline Markdown (bold, italic, inline code, links,
// strikethrough) inside a block's text, with its line breaks kept.
var Inline = goldmark.New(
	goldmark.WithExtensions(extension.Strikethrough, extension.Linkify),
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

// Block renders the "markdown" block of a post: headings, lists, tables,
// blockquotes, code fences, task lists and footnotes.
var Block = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.Footnote),
)
