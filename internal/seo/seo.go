// SPDX-License-Identifier: Apache-2.0

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

// IsOnion reports whether host is a Tor onion address (…​.onion), so callers can
// choose the correct scheme. Empty/whitespace and non-onion hosts return false.
func IsOnion(host string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(host)), ".onion")
}

// Origin returns the absolute-URL origin for a public host and is the single
// source of truth for the scheme: a Tor onion service is reached over plain
// http:// (v3 onions are self-authenticating — no CA TLS, no HTTPS), every other
// host over https://. This lets an onion-only / Tor Space site emit correct
// http://<onion> canonical/OG/sitemap URLs while clearnet hosts stay https://.
// (ADR-0141.)
func Origin(host string) string {
	if IsOnion(host) {
		return "http://" + host
	}
	return "https://" + host
}

// Compute fills ArticleMeta for an article.
func Compute(title, slug, contentHTML string, createdAt, updatedAt time.Time, domain, siteName string) ArticleMeta {
	return ArticleMeta{
		Title:         title,
		Description:   ExtractDescription(contentHTML),
		OGImage:       ExtractFirstImage(contentHTML),
		CanonicalURL:  Origin(domain) + "/articles/" + slug,
		DatePublished: createdAt.UTC().Format(time.RFC3339),
		DateModified:  updatedAt.UTC().Format(time.RFC3339),
		SiteName:      siteName,
	}
}
