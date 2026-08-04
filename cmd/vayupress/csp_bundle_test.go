// SPDX-License-Identifier: Apache-2.0

package main

// csp_bundle_test.go — the pre-flight that tells an operator what a strict CSP
// will refuse to load from a site bundle they just uploaded.
//
// WHY IT EXISTS, from doing the thing it warns about. Cloning an existing site
// onto VayuPress is the obvious first move, and most sites on the web load their
// CSS framework, their JS framework and their fonts from third-party hosts, with
// a small inline <script> configuring them. Every one of those is refused here:
//
//	script-src 'self' 'nonce-<per-request>'; style-src 'self'; font-src 'self'
//
// and a STATIC bundle cannot carry a per-request nonce, so even a first-party
// inline script is blocked. The bundle publishes cleanly, the tool reports
// success, and the page renders as unstyled text with no interactivity — with
// nothing anywhere saying why.
//
// That is the same silent-failure shape this product spent a long day removing
// from its provisioning path: the step reports success, the result is broken,
// and the operator is left to guess.

import (
	"strings"
	"testing"
)

func joinWarn(w []string) string { return strings.Join(w, "\n") }

// A bundle copied from a CDN-based site must produce a warning for EVERY class
// of thing that will be refused — not one, and not a generic "check your CSP".
func TestABundleCopiedFromACDNSiteIsWarnedAboutEveryRefusal(t *testing.T) {
	files := map[string]string{
		"index.html": `<!doctype html><html><head>` +
			`<script src="https://cdn.example.net/tailwind"></script>` +
			`<script>window.conf = {theme:'dark'};</script>` +
			`<link rel="stylesheet" href="https://fonts.example.org/css2?family=Inter" />` +
			`<link rel="icon" href="/favicon.png" />` +
			`</head><body>` +
			`<img src="https://images.example.com/hero.jpg" />` +
			`<a href="https://example.com/docs">Docs</a>` +
			`<script src="assets/app.js"></script>` +
			`</body></html>`,
		"assets/site.css": `@font-face { font-family: X; src: url(https://fonts.example.org/x.woff2); }`,
	}
	w := cspBundleWarnings(files)
	got := joinWarn(w)

	for _, want := range []string{"cdn.example.net", "inline <script>", "fonts.example.org"} {
		if !strings.Contains(got, want) {
			t.Errorf("no warning mentions %q, so that refusal is a surprise at render time:\n%s",
				want, got)
		}
	}

	// The remote FONT in CSS is a separate refusal from the remote stylesheet in
	// HTML, and it is the one operators miss because the CSS file itself loads.
	if !strings.Contains(got, "assets/site.css") {
		t.Errorf("a remote @font-face inside a bundled stylesheet is not flagged, so the CSS "+
			"loads and the typography silently does not:\n%s", got)
	}
}

// FALSE POSITIVES ARE THE FAILURE MODE HERE. A warning list that cries about
// things which genuinely work teaches the operator to skim past it, and then it
// is decoration on the day it is right.
func TestThingsThatGenuinelyWorkAreNotWarnedAbout(t *testing.T) {
	files := map[string]string{
		// img-src admits https:, so remote images really do load. A nav link is
		// not a subresource at all. A same-origin script and stylesheet are the
		// documented correct answer, and must never be flagged.
		"index.html": `<!doctype html><html><head>` +
			`<link rel="stylesheet" href="assets/style.css" />` +
			`<script defer src="assets/app.js"></script>` +
			`<script type="application/ld+json">{"@context":"https://schema.org"}</script>` +
			`</head><body>` +
			`<img src="https://images.example.com/hero.jpg" alt="" />` +
			`<a href="https://github.com/example/repo">Source</a>` +
			`</body></html>`,
		"assets/style.css": `body { background: #04060d; font-family: system-ui; }`,
		"assets/app.js":    `(function(){ 'use strict'; })();`,
	}
	if w := cspBundleWarnings(files); len(w) != 0 {
		t.Fatalf("a correctly self-contained bundle was warned about, which trains the operator "+
			"to ignore the list:\n%s", joinWarn(w))
	}
}

// A JSON-LD block is DATA, not executable code, and is never blocked. Flagging
// it would fire on almost every well-marked-up page — the fastest possible way
// to make this list worthless.
func TestStructuredDataIsNotMistakenForAnInlineScript(t *testing.T) {
	files := map[string]string{
		"index.html": `<html><head><script type="application/ld+json">` +
			`{"@context":"https://schema.org","@type":"Organization","name":"X"}` +
			`</script></head><body></body></html>`,
	}
	if w := cspBundleWarnings(files); len(w) != 0 {
		t.Errorf("structured data was reported as a blocked inline script:\n%s", joinWarn(w))
	}
}

// And the warning has to say what to DO. "Blocked by CSP" is a diagnosis handed
// back as homework; the operator needs the remedy in the same sentence.
func TestEachWarningNamesTheRemedy(t *testing.T) {
	files := map[string]string{
		"index.html": `<html><head><script src="https://cdn.example.net/x.js"></script>` +
			`<script>var a=1;</script></head><body></body></html>`,
	}
	w := cspBundleWarnings(files)
	if len(w) == 0 {
		t.Fatal("nothing was flagged")
	}
	for _, one := range w {
		low := strings.ToLower(one)
		if !strings.Contains(low, "vendor") && !strings.Contains(low, "move the code") &&
			!strings.Contains(low, "embed") {
			t.Errorf("a warning states the problem without the fix, which is a diagnosis handed "+
				"back as homework:\n%s", one)
		}
	}
}
