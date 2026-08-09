// SPDX-License-Identifier: Apache-2.0

package seo

import (
	"encoding/json"
	"html/template"
	"strings"
	"time"
)

// jsonld.go — structured data for the homepage and for article breadcrumbs.
//
// WHY THIS IS BUILT WITH encoding/json AND NOT IN THE TEMPLATE. The article
// page's BlogPosting block is assembled by interpolating fields into a JSON
// string literal inside the Go template. That works only for as long as every
// interpolated value goes through the right escaper, and the failure is silent
// and severe: one unescaped double quote in a post title truncates the JSON, and
// every search engine and language model silently drops the block — the page
// looks fine to a person and is invisible to a machine.
//
// Marshalling a struct cannot produce that. encoding/json also escapes <, > and
// & to <, > and & by default, so a title containing "</script>"
// cannot close the element it is sitting inside.
//
// Everything here is emitted only when it is TRUE. A SearchAction is written
// only when the site actually has search switched on; a blogPost list only
// carries posts that exist. Structured data asserting a capability the site does
// not have is the schema equivalent of a claim with no control behind it, and
// search engines penalise exactly that.

// ldGraph is a schema.org @graph document — several linked entities in one
// block, which is preferred to several sibling <script> tags because the nodes
// can reference each other by @id.
type ldGraph struct {
	Context string `json:"@context"`
	Graph   []any  `json:"@graph"`
}

type ldSearchAction struct {
	Type       string `json:"@type"`
	Target     any    `json:"target"`
	QueryInput string `json:"query-input"`
}

type ldEntryPoint struct {
	Type        string `json:"@type"`
	URLTemplate string `json:"urlTemplate"`
}

// HomeDoc is everything the homepage graph needs. Zero values are safe: an
// empty field is omitted rather than emitted blank.
type HomeDoc struct {
	Origin      string // "https://example.com" — no trailing slash
	Canonical   string // path, e.g. "/" or "/page/2"
	SiteName    string
	Description string
	Language    string
	LogoURL     string // absolute
	// SearchPath is the site's search endpoint ("/search"). Empty when search is
	// off, and then no SearchAction is emitted at all.
	SearchPath string
	Posts      []HomePost
}

// HomePost is one entry in the homepage's blogPost list.
type HomePost struct {
	Title       string
	Slug        string
	Excerpt     string
	Published   time.Time
	Author      string
	ImageURL    string // absolute, optional
	AbsoluteURL string // absolute permalink
}

// HomeJSONLD returns a complete <script type="application/ld+json"> element for
// the homepage, or "" when there is not enough to say anything true.
//
// The graph carries a WebSite (with a SearchAction only when search exists), the
// publishing Organization, and a Blog whose blogPost list is the posts actually
// on the page. Together those are what a search engine uses for a sitelinks
// search box and what a language model reads to learn what the site is and what
// it has recently published.
func HomeJSONLD(d HomeDoc) template.HTML {
	origin := strings.TrimRight(d.Origin, "/")
	if origin == "" {
		return ""
	}
	name := d.SiteName
	if name == "" {
		name = strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	}
	lang := d.Language
	if lang == "" {
		lang = "en"
	}

	siteID := origin + "/#website"
	orgID := origin + "/#organization"

	org := map[string]any{
		"@type": "Organization",
		"@id":   orgID,
		"name":  name,
		"url":   origin + "/",
	}
	if d.LogoURL != "" {
		org["logo"] = map[string]any{"@type": "ImageObject", "url": d.LogoURL}
	}

	site := map[string]any{
		"@type":      "WebSite",
		"@id":        siteID,
		"url":        origin + "/",
		"name":       name,
		"inLanguage": lang,
		"publisher":  map[string]any{"@id": orgID},
	}
	if d.Description != "" {
		site["description"] = d.Description
	}
	// Only claim a search box when one exists. Declaring a SearchAction that
	// 404s is worse than declaring none.
	if d.SearchPath != "" {
		site["potentialAction"] = ldSearchAction{
			Type:       "SearchAction",
			Target:     ldEntryPoint{Type: "EntryPoint", URLTemplate: origin + d.SearchPath + "?q={search_term_string}"},
			QueryInput: "required name=search_term_string",
		}
	}

	graph := []any{site, org}

	if len(d.Posts) > 0 {
		posts := make([]any, 0, len(d.Posts))
		for _, p := range d.Posts {
			if p.AbsoluteURL == "" || p.Title == "" {
				continue
			}
			item := map[string]any{
				"@type":            "BlogPosting",
				"@id":              p.AbsoluteURL + "#post",
				"headline":         p.Title,
				"url":              p.AbsoluteURL,
				"mainEntityOfPage": map[string]any{"@type": "WebPage", "@id": p.AbsoluteURL},
				"inLanguage":       lang,
				"publisher":        map[string]any{"@id": orgID},
			}
			if !p.Published.IsZero() {
				item["datePublished"] = p.Published.UTC().Format(time.RFC3339)
			}
			if p.Excerpt != "" {
				item["description"] = p.Excerpt
			}
			if p.Author != "" {
				item["author"] = map[string]any{"@type": "Person", "name": p.Author}
			}
			if p.ImageURL != "" {
				item["image"] = p.ImageURL
			}
			posts = append(posts, item)
		}
		if len(posts) > 0 {
			blog := map[string]any{
				"@type":      "Blog",
				"@id":        origin + "/#blog",
				"url":        origin + strings.TrimSuffix(d.Canonical, "/"),
				"name":       name,
				"inLanguage": lang,
				"publisher":  map[string]any{"@id": orgID},
				"blogPost":   posts,
			}
			if d.Description != "" {
				blog["description"] = d.Description
			}
			graph = append(graph, blog)
		}
	}

	return marshalLD(ldGraph{Context: "https://schema.org", Graph: graph})
}

// BreadcrumbJSONLD returns a BreadcrumbList for a single article.
//
// It is a separate block rather than part of the article's BlogPosting so that
// the existing template stays untouched — the two are read independently.
func BreadcrumbJSONLD(origin, siteName, title, absoluteURL string) template.HTML {
	origin = strings.TrimRight(origin, "/")
	if origin == "" || absoluteURL == "" || title == "" {
		return ""
	}
	if siteName == "" {
		siteName = "Home"
	}
	crumbs := []any{
		map[string]any{"@type": "ListItem", "position": 1, "name": siteName, "item": origin + "/"},
		map[string]any{"@type": "ListItem", "position": 2, "name": title, "item": absoluteURL},
	}
	return marshalLD(map[string]any{
		"@context":        "https://schema.org",
		"@type":           "BreadcrumbList",
		"itemListElement": crumbs,
	})
}

// marshalLD renders a value as a ready-to-embed ld+json script element.
//
// A marshal error returns "" rather than a partial element: half a JSON-LD block
// is not degraded structured data, it is a parse error on every consumer that
// reads it.
func marshalLD(v any) template.HTML {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	// json.Marshal escapes <, > and & by default, so the payload cannot close
	// the script element it sits in. Asserted by a test, because a future switch
	// to an Encoder with SetEscapeHTML(false) would silently reopen it.
	return template.HTML(`<script type="application/ld+json">` + string(b) + `</script>`)
}
