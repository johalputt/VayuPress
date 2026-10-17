// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/url"
	"strings"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// ghostURLTarget answers where a Ghost-shaped address now lives, or "" when it
// is not one. A site moved from Ghost carries every inbound link in Ghost's
// shapes — /my-post/ with its trailing slash, topics at /tag/go/ — and each of
// those 404'd here, while redirect rules match one exact path each and cannot
// cover a catalogue. It runs only on a request that would otherwise 404, so no
// route that answers today changes.
//
// Only GET and HEAD: a 301 turns a POST into a GET, which would silently drop
// a form. No target can leave the site: each is "/" or "/tags/" followed by one
// path-escaped segment of a non-empty first or second segment, so "//host/" and
// "/\host/" match no rule, and a "/" or "\" inside a segment is escaped.
func ghostURLTarget(r *http.Request) string {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return ""
	}
	p := r.URL.Path
	withQuery := func(path string) string {
		if r.URL.RawQuery != "" {
			return path + "?" + r.URL.RawQuery
		}
		return path
	}
	trimmed := strings.TrimRight(p, "/")
	segs := strings.Split(strings.TrimPrefix(trimmed, "/"), "/")

	// /tag/{t} and /tag/{t}/ → /tags/{t}; /tags/{t}/ → /tags/{t}.
	if len(segs) == 2 && segs[1] != "" && (segs[0] == "tag" || (segs[0] == "tags" && trimmed != p)) {
		return withQuery("/tags/" + url.PathEscape(segs[1]))
	}
	// /{slug}/ → /{slug}, for a slug that is a published post or page.
	if trimmed != p && len(segs) == 1 && segs[0] != "" && publishedSlugExists(segs[0]) {
		return withQuery("/" + url.PathEscape(segs[0]))
	}
	return ""
}

// publishedSlugExists reports whether a published post or page has this slug.
func publishedSlugExists(slug string) bool {
	if dbpkg.DB == nil {
		return false
	}
	var one int
	err := dbpkg.Reader().QueryRow(`SELECT 1 FROM articles WHERE slug=? AND status='published' LIMIT 1`, slug).Scan(&one)
	return err == nil
}
