package main

import "strings"

import "testing"

func TestRenderMarkdownDocument(t *testing.T) {
	out := string(renderMarkdownDocument([]byte(
		"# Heading\n\nHello **world** and a [link](https://example.com).\n\n" +
			"| A | B |\n|---|---|\n| 1 | 2 |\n",
	)))
	checks := []string{"<h1", "Heading", "<strong>world</strong>", `href="https://example.com"`, "<table>"}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("rendered ADR missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderMarkdownDocumentSanitizesScripts(t *testing.T) {
	out := string(renderMarkdownDocument([]byte(
		"# Safe\n\n<script>alert('xss')</script>\n\n<img src=x onerror=alert(1)>\n",
	)))
	if strings.Contains(out, "<script") || strings.Contains(out, "onerror") {
		t.Errorf("rendered ADR must strip unsafe markup, got:\n%s", out)
	}
}
