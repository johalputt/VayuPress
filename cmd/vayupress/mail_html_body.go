// SPDX-License-Identifier: Apache-2.0

package main

// mail_html_body.go — deriving the optional HTML alternative from the message the
// operator actually typed.
//
// The composer's textarea stays the single source of truth. Its Markdown is sent
// as the text/plain part exactly as written, and this renders the SAME text into
// the text/html part. Because one is generated from the other they can never
// describe different messages — the failure mode of every WYSIWYG mail composer,
// where the plain-text fallback drifts into "please view this in HTML".
//
// Rendering happens here rather than in internal/vayuos/mail so the mail engine
// stays MIME assembly only, with no Markdown or sanitiser dependency.

import (
	"bytes"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	goldhtml "github.com/yuin/goldmark/renderer/html"
)

// mailMD renders the composer's Markdown. Hard wraps are on because people write
// mail with meaningful line breaks and expect them kept; GFM covers the lists,
// quotes, code fences and strikethrough the toolbar emits.
var mailMD = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(goldhtml.WithHardWraps()),
)

// mailOutboundPolicy sanitises the HTML *we* send. Sanitising our own output
// sounds redundant, but the body is operator input and a compromised console must
// not be able to send tracking pixels, remote images or scripts from this domain
// under its DKIM signature. It is deliberately stricter than the policy used for
// DISPLAYING received mail: no images at all, since an outbound remote image is
// indistinguishable from a tracking pixel, and VayuPress does not send those.
var mailOutboundPolicy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowStandardAttributes()
	p.AllowElements(
		"p", "br", "hr", "div", "span",
		"h1", "h2", "h3", "h4", "h5", "h6",
		"strong", "b", "em", "i", "u", "s", "del", "code", "pre",
		"blockquote", "ul", "ol", "li",
		"table", "thead", "tbody", "tr", "th", "td",
	)
	// Links only, and only to schemes a mail client will honour sensibly.
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireParseableURLs(true)
	p.AddTargetBlankToFullyQualifiedLinks(false)
	return p
}()

// renderMailHTML turns the composer's Markdown into the sanitised HTML alternative
// part, wrapped in a minimal document. It returns "" when the body has no content,
// so an empty message never produces an empty HTML part.
func renderMailHTML(markdown string) string {
	if strings.TrimSpace(markdown) == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := mailMD.Convert([]byte(markdown), &buf); err != nil {
		// A render failure must never lose the message: the caller falls back to
		// sending text/plain alone, which is the honest result.
		return ""
	}
	safe := mailOutboundPolicy.Sanitize(buf.String())
	if strings.TrimSpace(safe) == "" {
		return ""
	}
	// A minimal, self-contained document. Styles are inline on the wrapper because
	// mail clients strip <style> blocks unpredictably, and deliberately modest:
	// heavy styling is what makes mail score as marketing rather than a message.
	return `<!DOCTYPE html><html><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1"></head>` +
		`<body><div style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;` +
		`font-size:15px;line-height:1.6;color:#1a1a1a;max-width:640px">` +
		safe +
		`</div></body></html>`
}
