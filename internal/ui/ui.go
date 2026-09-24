// SPDX-License-Identifier: Apache-2.0

// Package ui holds the console's Still Air primitives: the page, section, row,
// fact, figure, disclosure, table, callout and empty-state patterns every page
// is built from, and the icon set they draw with.
//
// Each primitive takes the text a person reads as a plain string and escapes it
// exactly once, and takes markup it must not touch as HTML. The type is the
// statement of trust: a caller converts to HTML on purpose, where it can be
// seen, so a string escaped by the caller and again here — the "&amp;amp;" the
// console once showed — is a type error to spot rather than a page to notice.
package ui

import (
	"html"
	"html/template"
	"strings"
)

// HTML is markup the caller vouches for. It is html/template's type, so values
// flow into and out of templates without conversion.
type HTML = template.HTML

// Text escapes s for use as HTML.
func Text(s string) HTML { return HTML(html.EscapeString(s)) }

// Join concatenates trusted fragments.
func Join(parts ...HTML) HTML {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(string(p))
	}
	return HTML(b.String())
}
