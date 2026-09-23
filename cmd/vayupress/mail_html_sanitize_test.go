// SPDX-License-Identifier: Apache-2.0

package main

// mail_html_sanitize_test.go — the HTML-mail rendering boundary.
//
// Two properties matter and both are invisible in normal use: a hostile message
// must not be able to run anything in the console, and reading a message must not
// tell the sender that it was read. The second one is the reason images are
// stripped rather than merely blocked by the policy's URL allowlist.

import (
	"strings"
	"testing"
)

const hostileMailHTML = `<div style="position:fixed;top:0" onclick="alert(1)">
<script>steal()</script>
<img src="https://tracker.example/pixel.gif?id=abc" srcset="https://tracker.example/2x.gif 2x" alt="logo" width="20">
<a href="https://example.com/page" style="color:red">a link</a>
<iframe src="https://evil.example/frame"></iframe>
</div>`

// TestHostileMailHTMLCannotRunOrTrack is the core guard.
func TestHostileMailHTMLCannotRunOrTrack(t *testing.T) {
	out := mailHTMLNoImages(hostileMailHTML)
	lower := strings.ToLower(out)
	for _, banned := range []string{"<script", "onclick", "onerror", "<iframe", "javascript:", "position:fixed"} {
		if strings.Contains(lower, banned) {
			t.Errorf("sanitised mail still contains %q — a message must never run code or reposition the console:\n%s", banned, out)
		}
	}
	// The tracking vector: no image source of any kind may survive.
	for _, banned := range []string{"tracker.example", "src=", "srcset="} {
		if strings.Contains(lower, banned) {
			t.Errorf("sanitised mail still contains %q — loading it would tell the sender this mailbox opened the message:\n%s", banned, out)
		}
	}
	// …while the message still reads: alt text and a working link survive.
	if !strings.Contains(out, "logo") {
		t.Errorf("the image's alt text should survive so the layout still reads:\n%s", out)
	}
	if !strings.Contains(out, `href="https://example.com/page"`) {
		t.Errorf("ordinary links must keep working:\n%s", out)
	}
	if !strings.Contains(lower, "noreferrer") {
		t.Errorf("links must not hand the sender a referrer:\n%s", out)
	}
}

// TestLoadingImagesIsExplicitAndStillSanitised — the opt-in relaxes ONE thing.
func TestLoadingImagesIsExplicitAndStillSanitised(t *testing.T) {
	out := mailHTMLPolicyImages.Sanitize(hostileMailHTML)
	if !strings.Contains(out, "tracker.example") {
		t.Error("the explicit image policy must actually allow images, or the toggle does nothing")
	}
	lower := strings.ToLower(out)
	for _, banned := range []string{"<script", "onclick", "<iframe", "javascript:"} {
		if strings.Contains(lower, banned) {
			t.Errorf("asking for images must not also allow %q:\n%s", banned, out)
		}
	}
}

// TestImageStrippingKeepsTheRestOfTheMessageIntact guards against the pass being
// too blunt — a wrapper element or a paragraph must not be lost with the images.
func TestImageStrippingKeepsTheRestOfTheMessageIntact(t *testing.T) {
	in := `<p>Hello <strong>world</strong></p><img src="https://x.example/p.gif" alt="pic"><p>Bye</p>`
	out := mailHTMLNoImages(in)
	for _, want := range []string{"Hello", "<strong>world</strong>", "Bye", "pic"} {
		if !strings.Contains(out, want) {
			t.Errorf("stripping image sources lost %q from the message:\n%s", want, out)
		}
	}
	if strings.Contains(out, "x.example") {
		t.Errorf("the image source survived:\n%s", out)
	}
	if strings.Contains(out, "<html") || strings.Contains(out, "<body") {
		t.Errorf("the fragment must stay a fragment — a full-document parse would break nesting in the reader:\n%s", out)
	}
	// A body with no images is unchanged in substance (not byte-identical: the
	// parse normalises markup, which is expected and harmless).
	plain := mailHTMLNoImages(`<p>Just text</p>`)
	if !strings.Contains(plain, "Just text") {
		t.Errorf("a message without images must still render:\n%s", plain)
	}
}

// TestMailReaderOffersTheViewToggle — the feature is discoverable, and the
// default stays plain text.
func TestMailReaderOffersTheViewToggle(t *testing.T) {
	src := withoutComments(readFileString(t, "vayuos.go"))
	for _, want := range []string{"Render HTML view", "Show plain text", "Load images", "vm-html-toggle"} {
		if !strings.Contains(src, want) {
			t.Errorf("the reader is missing %q", want)
		}
	}
	if !strings.Contains(src, "mailHTMLNoImages(pm.HTML)") {
		t.Error("the default HTML path must be the image-stripped sanitiser")
	}
	if !strings.Contains(src, "mailHTMLPolicyImages.Sanitize") {
		t.Error("the explicit image path must use the image policy")
	}
}

// Every way HTML can make a browser fetch a URL, not just <img src>. The
// console's policy allows https: images, so with images off nothing but the
// sanitiser stands between a message and a tracking request. One vector per
// entry, so a policy change that reopens one names it.
func TestNoTrackingVectorSurvivesWithImagesOff(t *testing.T) {
	for name, v := range map[string]string{
		"img src":          `<img src="https://t.example/a.gif" alt="x">`,
		"img uppercase":    `<IMG SRC="https://t.example/b.gif" alt="x">`,
		"img srcset":       `<img srcset="https://t.example/c.gif 2x" alt="x">`,
		"img lowsrc":       `<img lowsrc="https://t.example/d.gif" alt="x">`,
		"img data-src":     `<img data-src="https://t.example/e.gif" alt="x">`,
		"table background": `<table background="https://t.example/f.gif"><tr><td background="https://t.example/g.gif">x</td></tr></table>`,
		"body background":  `<body background="https://t.example/h.gif">x</body>`,
		"style url":        `<p style="background:url(https://t.example/i.gif)">x</p>`,
		"style element":    `<style>@import url(https://t.example/j.css)</style>`,
		"link stylesheet":  `<link rel="stylesheet" href="https://t.example/k.css">`,
		"picture source":   `<picture><source srcset="https://t.example/l.gif"><img alt="x"></picture>`,
		"video poster":     `<video poster="https://t.example/m.gif" src="https://t.example/n.mp4"></video>`,
		"audio src":        `<audio src="https://t.example/o.mp3"></audio>`,
		"input image":      `<input type="image" src="https://t.example/p.gif">`,
		"svg image":        `<svg><image href="https://t.example/q.gif"/></svg>`,
		"object data":      `<object data="https://t.example/r.swf"></object>`,
		"a ping":           `<a href="https://ok.example" ping="https://t.example/s">x</a>`,
		"base href":        `<base href="https://t.example/"><img src="u.gif" alt="x">`,
		"meta refresh":     `<meta http-equiv="refresh" content="0;url=https://t.example/v">`,
	} {
		if out := mailHTMLNoImages(v); strings.Contains(out, "t.example") {
			t.Errorf("%s reaches the reader with images off: %s", name, out)
		}
	}
}
