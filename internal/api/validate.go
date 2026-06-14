// Package api provides the ArticleService and shared validation/response helpers
// used by the cmd/vayupress HTTP handlers (ADR-0047).
package api

import (
	"fmt"
	"regexp"
	"strings"
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,198}[a-z0-9]$|^[a-z0-9]$`)

// ValidateArticleInput returns an error if the supplied article fields fail
// business constraints. Centralises validation that was previously duplicated
// across handleCreateArticle and handleBulkCreateArticles.
func ValidateArticleInput(title, slug, content string, tags []string) error {
	if title == "" || len(title) > 500 {
		return fmt.Errorf("title required (1–500 chars): %w", ErrValidation)
	}
	if !IsValidSlug(slug) {
		return fmt.Errorf("slug must be lowercase alphanumeric with hyphens (1–200 chars): %w", ErrInvalidSlug)
	}
	if content == "" || len(content) > 5_000_000 {
		return fmt.Errorf("content required (1 byte – 5 MB): %w", ErrValidation)
	}
	if len(tags) > 20 {
		return fmt.Errorf("max 20 tags: %w", ErrValidation)
	}
	for _, t := range tags {
		if len(t) > 100 {
			return fmt.Errorf("tag too long: %q: %w", t, ErrValidation)
		}
	}
	return nil
}

// IsValidSlug reports whether s is a well-formed URL slug.
func IsValidSlug(s string) bool { return slugRe.MatchString(s) }

var slugStripRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify derives a URL-safe slug from an arbitrary title: lowercased, with
// runs of non-alphanumeric characters collapsed to single hyphens, trimmed of
// leading/trailing hyphens, and capped at 200 characters. Returns "" if the
// title contains no slug-able characters (caller should treat as invalid).
func Slugify(title string) string {
	s := slugStripRe.ReplaceAllString(strings.ToLower(title), "-")
	s = strings.Trim(s, "-")
	if len(s) > 200 {
		s = strings.Trim(s[:200], "-")
	}
	return s
}

// SplitTags parses a comma-separated tag string into a deduplicated slice.
func SplitTags(s string) []string {
	if s == "" {
		return []string{}
	}
	seen := make(map[string]struct{})
	var out []string
	for _, p := range splitCSV(s) {
		if p != "" {
			if _, dup := seen[p]; !dup {
				seen[p] = struct{}{}
				out = append(out, p)
			}
		}
	}
	if out == nil {
		return []string{}
	}
	return out
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}
