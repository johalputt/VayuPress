// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"html"
)

// stillAirOfflineHTML is the page a service worker answers with when there is
// no network: a Still Air notice in the reader's own colour scheme.
//
// It cannot link the stylesheet (there is no network to fetch it from), so it
// carries the few tokens it uses in a <style> of its own. A worker-built
// response has no CSP header, so the <style> is allowed. The values are the
// stylesheet's; TestOfflinePagesUseTheStylesheetsTokens fails if they drift.
func stillAirOfflineHTML(title, heading, detail string) string {
	return `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>` + html.EscapeString(title) + `</title><style>` +
		`:root{color-scheme:light dark;--color-canvas:#0f0f0e;--text-1:#ecebe7;--text-2:#b3b1aa}` +
		`@media (prefers-color-scheme:light){:root{--color-canvas:#f6f5f2;--text-1:#1b1a18;--text-2:#4f4d48}}` +
		`body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--color-canvas);color:var(--text-1);` +
		`font:400 14px/20px Inter,ui-sans-serif,system-ui,sans-serif;-webkit-font-smoothing:antialiased}` +
		`main{max-width:380px;padding:24px;text-align:center}` +
		`h1{font:600 20px/28px Inter,ui-sans-serif,system-ui,sans-serif;letter-spacing:-.01em;margin:0 0 8px}` +
		`p{margin:0;color:var(--text-2)}</style>` +
		`<main><h1>` + html.EscapeString(heading) + `</h1><p>` + html.EscapeString(detail) + `</p></main></html>`
}

// jsString quotes s as a JavaScript string literal, for HTML a worker script
// carries. JSON's string syntax is a subset of JavaScript's.
func jsString(s string) string {
	b, _ := json.Marshal(s) // a string always marshals
	return string(b)
}
