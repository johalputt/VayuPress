// Package seo provides SEO helpers for VayuPress: description extraction,
// image extraction, canonical URL generation, and structured article metadata.
package seo

import (
	"html"
	"regexp"
	"strings"
	"time"
)

var (
	tagRe    = regexp.MustCompile(`<[^>]*>`)
	imgSrcRe = regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+)["']`)
)

// ExtractDescription returns the first 160 chars of plain text from HTML.
// HTML tags are stripped with a simple regex; HTML entities are unescaped.
func ExtractDescription(htmlContent string) string {
	plain := tagRe.ReplaceAllString(htmlContent, "")
	plain = html.UnescapeString(plain)
	plain = strings.Join(strings.Fields(plain), " ")
	if len(plain) > 160 {
		return plain[:160]
	}
	return plain
}

// ExtractFirstImage returns the src of the first <img> tag in the HTML, or "".
func ExtractFirstImage(htmlContent string) string {
	m := imgSrcRe.FindStringSubmatch(htmlContent)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// ArticleMeta holds computed SEO fields for an article.
type ArticleMeta struct {
	Title         string
	Description   string
	OGImage       string
	CanonicalURL  string
	DatePublished string
	DateModified  string
	SiteName      string
}

// Compute fills ArticleMeta for an article.
func Compute(title, slug, contentHTML string, createdAt, updatedAt time.Time, domain, siteName string) ArticleMeta {
	return ArticleMeta{
		Title:         title,
		Description:   ExtractDescription(contentHTML),
		OGImage:       ExtractFirstImage(contentHTML),
		CanonicalURL:  "https://" + domain + "/articles/" + slug,
		DatePublished: createdAt.UTC().Format(time.RFC3339),
		DateModified:  updatedAt.UTC().Format(time.RFC3339),
		SiteName:      siteName,
	}
}
