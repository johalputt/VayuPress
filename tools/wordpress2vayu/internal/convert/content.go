// Package convert provides helpers for transforming WordPress content.
package convert

import (
	"regexp"
	"strings"
)

var (
	reComment    = regexp.MustCompile(`(?s)<!--.*?-->`)
	reBlankLines = regexp.MustCompile(`(\n\s*){3,}`)
)

// CleanHTML strips HTML comments and collapses 3+ consecutive blank lines to 2.
func CleanHTML(html string) string {
	html = reComment.ReplaceAllString(html, "")
	html = reBlankLines.ReplaceAllString(html, "\n\n")
	return strings.TrimSpace(html)
}
