// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// renderMailHTML turns the composer's Markdown into the HTML alternative part.
// The body is operator input, and it is about to be signed with this domain's DKIM
// key and sent to third parties — so the output is sanitised even though we
// produced it. A compromised console must not be able to send scripts or tracking
// pixels under a signature that vouches for this domain.

func TestMailHTMLRendersMarkdown(t *testing.T) {
	out := renderMailHTML("# Hi\n\nSome **bold** and *italic* text.\n\n- one\n- two")
	for _, want := range []string{"<h1", "<strong>", "<em>", "<ul>", "<li>"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered HTML is missing %q\n%s", want, out)
		}
	}
	// The toolbar's conventions must all survive the round trip.
	for _, tc := range []struct{ md, want string }{
		{"> quoted", "<blockquote>"},
		{"~~struck~~", "<del>"},
		{"```\ncode\n```", "<pre>"},
		{"1. first\n2. second", "<ol>"},
		{"---", "<hr"},
	} {
		if got := renderMailHTML(tc.md); !strings.Contains(got, tc.want) {
			t.Errorf("%q did not produce %q\n%s", tc.md, tc.want, got)
		}
	}
}

// TestMailHTMLStripsDangerousMarkup is the security property.
func TestMailHTMLStripsDangerousMarkup(t *testing.T) {
	for _, tc := range []struct {
		name, md, banned string
	}{
		{"script tag", "<script>alert(1)</script>", "<script"},
		{"event handler", `<div onclick="steal()">x</div>`, "onclick"},
		{"iframe", `<iframe src="https://evil.example"></iframe>`, "<iframe"},
		{"object", `<object data="x"></object>`, "<object"},
		{"javascript: url", `[click](javascript:alert(1))`, "javascript:"},
		{"form", `<form action="https://evil.example"><input></form>`, "<form"},
		{"style block", `<style>body{display:none}</style>`, "<style>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderMailHTML(tc.md); strings.Contains(strings.ToLower(got), strings.ToLower(tc.banned)) {
				t.Errorf("%q survived sanitising:\n%s", tc.banned, got)
			}
		})
	}
}

// TestMailHTMLSendsNoImages pins the deliberate difference from the policy used to
// DISPLAY received mail. An outbound remote image is indistinguishable from a
// tracking pixel; VayuPress does not send those, and a sanitiser that allowed them
// would make it possible to.
func TestMailHTMLSendsNoImages(t *testing.T) {
	for _, md := range []string{
		`![pixel](https://tracker.example/p.gif)`,
		`<img src="https://tracker.example/p.gif" width="1" height="1">`,
	} {
		if got := renderMailHTML(md); strings.Contains(got, "<img") {
			t.Errorf("an image survived: %q →\n%s", md, got)
		}
	}
}

// TestMailHTMLKeepsSafeLinks — stripping every link would make the HTML part
// useless, so the allowed schemes must still work.
func TestMailHTMLKeepsSafeLinks(t *testing.T) {
	out := renderMailHTML("[site](https://example.com) and [mail](mailto:a@example.com)")
	if !strings.Contains(out, `href="https://example.com"`) {
		t.Errorf("https link was stripped\n%s", out)
	}
	if !strings.Contains(out, "mailto:a@example.com") {
		t.Errorf("mailto link was stripped\n%s", out)
	}
}

// TestMailHTMLIsEmptyForEmptyInput — an empty body must produce no HTML part at
// all, rather than an empty one that makes the message multipart for nothing.
func TestMailHTMLIsEmptyForEmptyInput(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n\t\n"} {
		if got := renderMailHTML(in); got != "" {
			t.Errorf("input %q produced an HTML part: %q", in, got)
		}
	}
}

// TestMailHTMLIsSelfContained checks the part is a whole document a mail client
// will render, and that it carries no external references — no remote stylesheet
// or font to leak a read receipt.
func TestMailHTMLIsSelfContained(t *testing.T) {
	out := renderMailHTML("hello")
	if !strings.HasPrefix(out, "<!DOCTYPE html>") {
		t.Error("the HTML part should be a complete document")
	}
	for _, banned := range []string{"<link", "@import", "url(", "<script"} {
		if strings.Contains(out, banned) {
			t.Errorf("the HTML part must reference nothing external; found %q", banned)
		}
	}
}

// TestRichHTMLIsOptInOnly pins the default. HTML from a young sending domain
// delivers worse than plain text, so this has to be a deliberate choice.
func TestRichHTMLIsOptInOnly(t *testing.T) {
	src := readSourceFile(t, "vayuos_mail.go")
	if !strings.Contains(src, "richHTML := false") {
		t.Error("richHTML must default to false in the send handler")
	}
	if !strings.Contains(src, "data-c-rich>") {
		t.Error("the composer needs the HTML toggle")
	}
	// An unchecked checkbox is the honest default; a `checked` attribute here
	// would opt every operator in silently.
	if strings.Contains(src, `data-c-rich checked`) {
		t.Error("the HTML toggle must ship unchecked")
	}
	// The HTML must be rendered from the body AFTER the signature is appended, or
	// the two parts would carry different messages.
	sigIdx := strings.Index(src, "bodyText = insertSignature(in.Body, sig)")
	renderIdx := strings.Index(src, "htmlBody = renderMailHTML(bodyText)")
	if sigIdx < 0 || renderIdx < 0 {
		t.Fatal("expected both the signature append and the HTML render in the send path")
	}
	if renderIdx < sigIdx {
		t.Error("the HTML part must be rendered after the signature is appended, so both parts match")
	}
}
