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
	// unreadRe is markup whose text is not prose a reader reads: a diagram's
	// labels, code, and what is never shown. On johal.in an inline SVG's
	// labels ("STANDING ALLOCATION: 15-20% per team") would otherwise be read
	// as a sentence of the post.
	unreadRe = regexp.MustCompile(`(?is)<(svg|pre|script|style|template|noscript)\b.*?</(?:svg|pre|script|style|template|noscript)>`)
	// blockEndRe ends a block: its text and the next block's are two
	// sentences, not one word ("…end.Next").
	blockEndRe = regexp.MustCompile(`(?i)</(?:p|h[1-6]|li|blockquote|div|td|th|dt|dd|figcaption)>|<br\s*/?>`)
)

// descriptionMax is the length a search result shows of a description before
// it cuts it short itself.
const descriptionMax = 160

// PlainText is the prose a reader reads in a post's HTML, as one line: no
// markup, no code, no diagram labels, entities decoded.
func PlainText(htmlContent string) string {
	plain := unreadRe.ReplaceAllString(htmlContent, " ")
	plain = blockEndRe.ReplaceAllString(plain, " ")
	plain = tagRe.ReplaceAllString(plain, "")
	return oneLine(html.UnescapeString(plain))
}

// ExtractDescription is the opening of a post's prose, cut at a word within
// descriptionMax bytes and marked as cut. It was the first 160 bytes, which
// ended mid-word and could end mid-character: an invalid description is one a
// search engine replaces with text of its own choosing.
func ExtractDescription(htmlContent string) string {
	return clip(PlainText(htmlContent), descriptionMax)
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
