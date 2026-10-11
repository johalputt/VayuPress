// SPDX-License-Identifier: Apache-2.0

package seo

import (
	"html/template"
	"regexp"
	"strings"
	"time"
)

// article.go — a post's structured data, one @graph for every theme.
//
// It replaces a BlogPosting written as a JSON string inside each theme's
// template (the fragility jsonld.go names) and says more of what is true of
// the post: who wrote it and where they are, the site it is part of (the
// homepage's #website and #organization, by @id), its topic, its tags, its
// length, and the questions its FAQ section answers. A generative engine
// answering a question quotes a page it can read as a set of facts; a search
// engine links the post to its author and site through the same @ids.

// QA is one question a post's FAQ section answers, as plain text.
type QA struct{ Q, A string }

// ArticleDoc is everything the article graph says. Zero values are omitted.
type ArticleDoc struct {
	Origin    string // "https://example.com", no trailing slash
	Canonical string // the page's absolute URL
	SiteName  string
	Title     string
	// Description is the page's meta description, so the two cannot differ.
	Description         string
	Published, Modified time.Time
	ImageURL            string // absolute
	LogoURL             string // absolute; the publisher's
	Author              string
	AuthorURL           string // absolute; the author's page, when they have one
	Section             string // the post's topic, as the page shows it
	Keywords            []string
	ContentHTML         string
	// IsPage is a standalone page (About, Contact): a WebPage, not a post.
	IsPage bool
}

// faqMax bounds the FAQ graph: a section longer than this is not a FAQ, and
// the graph is read on every crawl.
const faqMax = 30

// faqAnswerMax keeps an answer to what a reader reads in the answer's place,
// not the rest of the post after the last question.
const faqAnswerMax = 2000

var (
	h2Re = regexp.MustCompile(`(?is)<h2\b([^>]*)>(.*?)</h2>`)
	h3Re = regexp.MustCompile(`(?is)<h3\b[^>]*>(.*?)</h3>`)
	// faqTitleRe is a heading that names a FAQ section; faqIDRe the id a
	// writer gives one.
	faqTitleRe = regexp.MustCompile(`(?i)^(frequently asked questions|faqs?|questions and answers|common questions)\b`)
	faqIDRe    = regexp.MustCompile(`(?i)\bid\s*=\s*["']?faqs?["'\s>]`)
)

// ExtractFAQ is the questions and answers of a post's FAQ section: the <h2>
// that names it ("Frequently asked questions", "FAQ", or id="faq") up to the
// next <h2>, each <h3> in it a question and the prose up to the next <h3> its
// answer. Only that section: a question-shaped <h3> elsewhere is a heading of
// the post, and a FAQPage claiming it would describe what the page does not
// show as a FAQ.
func ExtractFAQ(htmlContent string) []QA {
	heads := h2Re.FindAllStringSubmatchIndex(htmlContent, -1)
	for i, h := range heads {
		attrs, title := htmlContent[h[2]:h[3]], PlainText(htmlContent[h[4]:h[5]])
		if !faqTitleRe.MatchString(title) && !faqIDRe.MatchString(attrs+">") {
			continue
		}
		end := len(htmlContent)
		if i+1 < len(heads) {
			end = heads[i+1][0]
		}
		return questions(htmlContent[h[1]:end])
	}
	return nil
}

// questions reads each <h3> of a section as a question and the prose up to
// the next one as its answer.
func questions(section string) []QA {
	qs := h3Re.FindAllStringSubmatchIndex(section, -1)
	var out []QA
	for i, q := range qs {
		end := len(section)
		if i+1 < len(qs) {
			end = qs[i+1][0]
		}
		question := PlainText(section[q[2]:q[3]])
		answer := clip(PlainText(section[q[1]:end]), faqAnswerMax)
		if question == "" || answer == "" {
			continue
		}
		out = append(out, QA{question, answer})
		if len(out) == faqMax {
			break
		}
	}
	return out
}

// ArticleJSONLD returns the post's graph as one ld+json element: the post (a
// BlogPosting, or a WebPage for a standalone page), its breadcrumb, and a
// FAQPage when it has a FAQ section. "" when there is not enough to say
// anything true.
func ArticleJSONLD(d ArticleDoc) template.HTML {
	origin := strings.TrimRight(d.Origin, "/")
	if origin == "" || d.Canonical == "" || d.Title == "" {
		return ""
	}
	name := d.SiteName
	if name == "" {
		name = strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	}
	orgID := origin + "/#organization"
	org := map[string]any{"@type": "Organization", "@id": orgID, "name": name, "url": origin + "/"}
	if d.LogoURL != "" {
		org["logo"] = map[string]any{"@type": "ImageObject", "url": d.LogoURL}
	}
	page := map[string]any{
		"url":        d.Canonical,
		"name":       d.Title,
		"inLanguage": "en",
		"isPartOf":   map[string]any{"@id": origin + "/#website"},
	}
	crumbs := map[string]any{"@id": d.Canonical + "#breadcrumb"}
	if d.Description != "" {
		page["description"] = d.Description
	}
	if d.ImageURL != "" {
		page["image"] = d.ImageURL
	}
	if !d.Published.IsZero() {
		page["datePublished"] = d.Published.UTC().Format(time.RFC3339)
	}
	if !d.Modified.IsZero() {
		page["dateModified"] = d.Modified.UTC().Format(time.RFC3339)
	}
	if d.IsPage {
		page["@type"], page["@id"] = "WebPage", d.Canonical
		page["breadcrumb"] = crumbs
		page["publisher"] = org
	} else {
		page["@type"], page["@id"] = "BlogPosting", d.Canonical+"#post"
		page["headline"] = d.Title
		// The breadcrumb is the page's, not the post's (schema.org).
		page["mainEntityOfPage"] = map[string]any{"@type": "WebPage", "@id": d.Canonical, "breadcrumb": crumbs}
		page["publisher"] = org
		// A post with no author named is the site's: the publisher wrote it.
		page["author"] = map[string]any{"@id": orgID}
		if d.Author != "" {
			author := map[string]any{"@type": "Person", "name": d.Author}
			if d.AuthorURL != "" {
				author["url"] = d.AuthorURL
			}
			page["author"] = author
		}
		if d.Section != "" {
			page["articleSection"] = d.Section
		}
		if len(d.Keywords) > 0 {
			page["keywords"] = strings.Join(d.Keywords, ", ")
		}
		if n := len(strings.Fields(PlainText(d.ContentHTML))); n > 0 {
			page["wordCount"] = n
		}
	}
	graph := []any{page, map[string]any{
		"@type": "BreadcrumbList",
		"@id":   d.Canonical + "#breadcrumb",
		"itemListElement": []any{
			map[string]any{"@type": "ListItem", "position": 1, "name": name, "item": origin + "/"},
			map[string]any{"@type": "ListItem", "position": 2, "name": d.Title, "item": d.Canonical},
		},
	}}
	if faq := ExtractFAQ(d.ContentHTML); len(faq) > 0 {
		entities := make([]any, len(faq))
		for i, qa := range faq {
			entities[i] = map[string]any{"@type": "Question", "name": qa.Q,
				"acceptedAnswer": map[string]any{"@type": "Answer", "text": qa.A}}
		}
		graph = append(graph, map[string]any{
			"@type": "FAQPage", "@id": d.Canonical + "#faq", "url": d.Canonical, "mainEntity": entities,
		})
	}
	return marshalLD(ldGraph{Context: "https://schema.org", Graph: graph})
}

// Crumb is a named link: a step of a breadcrumb, or an item of a list.
type Crumb struct{ Name, URL string }

// CollectionDoc is a page that lists: a topic and its posts, or the topics.
type CollectionDoc struct {
	Origin, Canonical, SiteName string
	Name, Description           string
	Trail                       []Crumb // the steps between Home and this page
	Items                       []Crumb // what the page lists, in its order
}

// collectionItemsMax bounds the list: it names what the page shows first,
// not an archive.
const collectionItemsMax = 50

// CollectionJSONLD returns a listing page's graph: a CollectionPage whose main
// entity is the list it shows, and its breadcrumb. A topic page read this way
// is a set of posts on one subject, which is what an engine answering a
// question about that subject looks for. "" without an origin, a canonical or
// a name.
func CollectionJSONLD(d CollectionDoc) template.HTML {
	origin := strings.TrimRight(d.Origin, "/")
	if origin == "" || d.Canonical == "" || d.Name == "" {
		return ""
	}
	site := d.SiteName
	if site == "" {
		site = strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	}
	page := map[string]any{
		"@type": "CollectionPage", "@id": d.Canonical, "url": d.Canonical, "name": d.Name,
		"inLanguage": "en",
		"isPartOf":   map[string]any{"@id": origin + "/#website"},
		"breadcrumb": map[string]any{"@id": d.Canonical + "#breadcrumb"},
	}
	if d.Description != "" {
		page["description"] = d.Description
	}
	var items []any
	for _, it := range d.Items {
		if it.Name == "" || it.URL == "" {
			continue
		}
		items = append(items, map[string]any{"@type": "ListItem", "position": len(items) + 1, "name": it.Name, "url": it.URL})
		if len(items) == collectionItemsMax {
			break
		}
	}
	if len(items) > 0 {
		page["mainEntity"] = map[string]any{"@type": "ItemList", "numberOfItems": len(items), "itemListElement": items}
	}
	trail := []any{map[string]any{"@type": "ListItem", "position": 1, "name": site, "item": origin + "/"}}
	for _, c := range append(d.Trail, Crumb{d.Name, d.Canonical}) {
		trail = append(trail, map[string]any{"@type": "ListItem", "position": len(trail) + 1, "name": c.Name, "item": c.URL})
	}
	return marshalLD(ldGraph{Context: "https://schema.org", Graph: []any{page, map[string]any{
		"@type": "BreadcrumbList", "@id": d.Canonical + "#breadcrumb", "itemListElement": trail,
	}}})
}
