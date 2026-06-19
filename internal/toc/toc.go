// Package toc extracts a table of contents from article HTML.
// It parses heading elements (h2–h4) from article HTML and produces both a
// structured list and an augmented HTML string with anchor IDs injected.
package toc

import (
	"fmt"
	"regexp"
	"strings"
)

// Entry is a single heading in the table of contents.
type Entry struct {
	Level  int    `json:"level"`   // 2, 3, or 4
	Text   string `json:"text"`    // plain text of the heading
	Anchor string `json:"anchor"`  // URL-safe anchor slug
}

var headingRe = regexp.MustCompile(`(?i)<h([2-4])([^>]*)>(.*?)</h[2-4]>`)
var tagRe     = regexp.MustCompile(`<[^>]+>`)

// Extract returns the table of contents entries from the given HTML.
func Extract(html string) []Entry {
	matches := headingRe.FindAllStringSubmatch(html, -1)
	seen := map[string]int{}
	var entries []Entry
	for _, m := range matches {
		level := int(m[1][0] - '0')
		inner := m[3]
		text := strings.TrimSpace(tagRe.ReplaceAllString(inner, ""))
		if text == "" {
			continue
		}
		anchor := slug(text)
		if n := seen[anchor]; n > 0 {
			anchor = fmt.Sprintf("%s-%d", anchor, n)
		}
		seen[slug(text)]++
		entries = append(entries, Entry{Level: level, Text: text, Anchor: anchor})
	}
	return entries
}

// InjectAnchors returns a copy of html with id= attributes added to each h2–h4
// that does not already have one, so that the TOC links work.
func InjectAnchors(html string) string {
	seen := map[string]int{}
	return headingRe.ReplaceAllStringFunc(html, func(match string) string {
		sub := headingRe.FindStringSubmatch(match)
		if sub == nil {
			return match
		}
		attrs := sub[2]
		inner := sub[3]
		tag := "h" + sub[1]
		// Already has an id — leave it alone.
		if strings.Contains(strings.ToLower(attrs), `id=`) {
			return match
		}
		text := strings.TrimSpace(tagRe.ReplaceAllString(inner, ""))
		anchor := slug(text)
		if n := seen[anchor]; n > 0 {
			anchor = fmt.Sprintf("%s-%d", anchor, n)
		}
		seen[slug(text)]++
		return fmt.Sprintf(`<%s id="%s"%s>%s</%s>`, tag, anchor, attrs, inner, tag)
	})
}

// RenderHTML returns a simple <nav> HTML block for the given entries.
func RenderHTML(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<nav class="toc" aria-label="Table of contents"><ol>`)
	for _, e := range entries {
		indent := strings.Repeat("  ", e.Level-2)
		b.WriteString(fmt.Sprintf(`%s<li class="toc-h%d"><a href="#%s">%s</a></li>`,
			indent, e.Level, e.Anchor, htmlEscape(e.Text)))
	}
	b.WriteString("</ol></nav>")
	return b.String()
}

func slug(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^a-z0-9\s-]`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`[\s-]+`).ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&#34;")
	return s
}
