// SPDX-License-Identifier: Apache-2.0

package seo

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// node returns the graph's node of the given @type, or nil.
func node(t *testing.T, doc map[string]any, typ string) map[string]any {
	t.Helper()
	for _, n := range doc["@graph"].([]any) {
		if m := n.(map[string]any); m["@type"] == typ {
			return m
		}
	}
	return nil
}

func samplePost() ArticleDoc {
	return ArticleDoc{
		Origin: "https://example.com", Canonical: "https://example.com/hello", SiteName: "Acme Press",
		Title: "Hello", Description: "A greeting.",
		Published: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC), Modified: time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC),
		ImageURL: "https://example.com/share.png", LogoURL: "https://example.com/logo.png",
		Author: "Jane Writer", AuthorURL: "https://example.com/author/jane",
		Section: "site reliability", Keywords: []string{"sre", "on-call"},
		ContentHTML: `<p>One two three.</p><pre><code>not prose</code></pre><svg><text>a label</text></svg><p>Four five.</p>`,
	}
}

// The post names its author and where they are, the site it is part of and
// its publisher by the homepage's @ids, its topic, tags and length.
func TestArticleJSONLDDescribesThePost(t *testing.T) {
	doc := parseLD(t, string(ArticleJSONLD(samplePost())))
	p := node(t, doc, "BlogPosting")
	if p == nil {
		t.Fatalf("no BlogPosting: %v", doc)
	}
	want := map[string]any{
		"@id": "https://example.com/hello#post", "headline": "Hello", "description": "A greeting.",
		"datePublished": "2026-09-01T08:00:00Z", "dateModified": "2026-09-02T08:00:00Z",
		"image": "https://example.com/share.png", "articleSection": "site reliability",
		"keywords": "sre, on-call", "wordCount": float64(5),
	}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("%s = %v, want %v", k, p[k], v)
		}
	}
	if a := p["author"].(map[string]any); a["name"] != "Jane Writer" || a["url"] != "https://example.com/author/jane" {
		t.Errorf("author = %v", a)
	}
	if pub := p["publisher"].(map[string]any); pub["@id"] != "https://example.com/#organization" || pub["name"] != "Acme Press" {
		t.Errorf("publisher = %v", pub)
	}
	if part := p["isPartOf"].(map[string]any); part["@id"] != "https://example.com/#website" {
		t.Errorf("isPartOf = %v", part)
	}
	page := p["mainEntityOfPage"].(map[string]any)
	if page["@id"] != "https://example.com/hello" || page["breadcrumb"].(map[string]any)["@id"] != "https://example.com/hello#breadcrumb" {
		t.Errorf("mainEntityOfPage = %v", page)
	}
	crumbs := node(t, doc, "BreadcrumbList")
	if crumbs == nil || crumbs["@id"] != "https://example.com/hello#breadcrumb" {
		t.Fatalf("breadcrumb = %v", crumbs)
	}
	items := crumbs["itemListElement"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["name"] != "Acme Press" || items[1].(map[string]any)["item"] != "https://example.com/hello" {
		t.Errorf("crumbs = %v", items)
	}
	if node(t, doc, "FAQPage") != nil {
		t.Error("a post with no FAQ section claims one")
	}
}

// A post with no author named is the publisher's.
func TestAnUnsignedPostIsThePublishers(t *testing.T) {
	d := samplePost()
	d.Author, d.AuthorURL = "", ""
	p := node(t, parseLD(t, string(ArticleJSONLD(d))), "BlogPosting")
	if a := p["author"].(map[string]any); a["@id"] != "https://example.com/#organization" || a["name"] != nil {
		t.Errorf("author = %v", a)
	}
}

// A standalone page is a WebPage: no headline, author or word count, as a post
// has.
func TestAPageIsAWebPage(t *testing.T) {
	d := samplePost()
	d.IsPage = true
	doc := parseLD(t, string(ArticleJSONLD(d)))
	if node(t, doc, "BlogPosting") != nil {
		t.Fatal("a page is described as a post")
	}
	p := node(t, doc, "WebPage")
	if p == nil || p["@id"] != "https://example.com/hello" || p["breadcrumb"] == nil {
		t.Fatalf("WebPage = %v", p)
	}
	for _, k := range []string{"headline", "author", "wordCount", "articleSection"} {
		if _, ok := p[k]; ok {
			t.Errorf("a page carries %s", k)
		}
	}
}

// The FAQ is the section a heading names, and only it: a question-shaped
// heading elsewhere in the post is not claimed.
func TestTheFAQIsTheSectionItsHeadingNames(t *testing.T) {
	body := `<h2 id="intro">Intro</h2><h3>Why not before?</h3><p>Not in the FAQ.</p>` +
		`<h2 id="faq">Frequently asked questions</h2>` +
		`<h3>Isn&rsquo;t it slow?</h3><p>No: it is <strong>fast</strong>.</p><p>Very.</p>` +
		`<h3>What about <code>x</code>?</h3><p>It is fine.</p>` +
		`<h3>Empty?</h3>` +
		`<h2>Conclusion</h2><h3>After?</h3><p>Not in the FAQ either.</p>`
	got := ExtractFAQ(body)
	want := []QA{{"Isn’t it slow?", "No: it is fast. Very."}, {"What about x?", "It is fine."}}
	if len(got) != len(want) {
		t.Fatalf("FAQ = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("FAQ[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	d := samplePost()
	d.ContentHTML = body
	faq := node(t, parseLD(t, string(ArticleJSONLD(d))), "FAQPage")
	if faq == nil {
		t.Fatal("no FAQPage for a post with a FAQ section")
	}
	q := faq["mainEntity"].([]any)[0].(map[string]any)
	if q["@type"] != "Question" || q["name"] != "Isn’t it slow?" || q["acceptedAnswer"].(map[string]any)["text"] != "No: it is fast. Very." {
		t.Errorf("first question = %v", q)
	}
}

// Each way a writer names a FAQ section is found: by its heading, and by its
// id when the heading says something else.
func TestAFAQSectionIsFoundByNameOrID(t *testing.T) {
	for _, head := range []string{`<h2>FAQ</h2>`, `<h2>FAQs</h2>`, `<h2>Questions and answers</h2>`, `<h2 id="faq">Reader mail</h2>`, `<h2 id='faqs' class="x">Asked</h2>`} {
		if got := ExtractFAQ(head + `<h3>Q?</h3><p>A.</p>`); len(got) != 1 {
			t.Errorf("%s: FAQ = %q", head, got)
		}
	}
	for _, head := range []string{`<h2>Questions we skipped</h2>`, `<h2>Why a FAQ helps</h2>`, `<h2 id="faq-style">Asked</h2>`, `<h3>FAQ</h3>`} {
		if got := ExtractFAQ(head + `<h3>Q?</h3><p>A.</p>`); got != nil {
			t.Errorf("%s is not a FAQ section: %q", head, got)
		}
	}
}

// Nothing a post's title or answers hold can end the element or break the
// JSON.
func TestArticleJSONLDCannotBreakOutOfTheScriptElement(t *testing.T) {
	d := samplePost()
	d.Title = `Quotes " and \ and </script><script>alert(1)</script>`
	d.ContentHTML = `<h2>FAQ</h2><h3>&lt;/script&gt;?</h3><p>&lt;/script&gt;</p>`
	out := string(ArticleJSONLD(d))
	body := strings.TrimSuffix(strings.TrimPrefix(out, `<script type="application/ld+json">`), `</script>`)
	if strings.Contains(strings.ToLower(body), "</script") {
		t.Fatalf("the payload closes its element: %s", out)
	}
	if p := node(t, parseLD(t, out), "BlogPosting"); p["headline"] != d.Title {
		t.Errorf("headline = %v, want the title kept", p["headline"])
	}
}

func TestArticleJSONLDRefusesToGuess(t *testing.T) {
	for name, mut := range map[string]func(*ArticleDoc){
		"origin": func(d *ArticleDoc) { d.Origin = "" }, "canonical": func(d *ArticleDoc) { d.Canonical = "" }, "title": func(d *ArticleDoc) { d.Title = "" },
	} {
		d := samplePost()
		mut(&d)
		if out := ArticleJSONLD(d); out != "" {
			t.Errorf("no %s, and still emitted %s", name, out)
		}
	}
}

// A description is prose: not a diagram's labels nor code, two paragraphs two
// sentences, cut at a word, marked as cut, and never mid-character.
func TestADescriptionIsTheProseCutAtAWord(t *testing.T) {
	if got := ExtractDescription(`<svg><text>LABEL</text></svg><pre>code()</pre><p>First.</p><p>Second &amp; last.</p>`); got != "First. Second & last." {
		t.Errorf("description = %q", got)
	}
	long := "<p>" + strings.Repeat("word ", 60) + "</p>"
	got := ExtractDescription(long)
	if !strings.HasSuffix(got, "word…") || len(got) > descriptionMax+len("…") {
		t.Errorf("long description = %q (%d bytes)", got, len(got))
	}
	dev := "<p>" + strings.Repeat("हिन्दी", 40) + "</p>"
	if got := ExtractDescription(dev); !utf8.ValidString(got) {
		t.Errorf("a description cut mid-character: %q", got)
	}
}

// A listing names only what it can link to, in order, and its breadcrumb
// runs Home, the trail, then the page.
func TestCollectionJSONLDListsWhatItLinks(t *testing.T) {
	doc := parseLD(t, string(CollectionJSONLD(CollectionDoc{
		Origin: "https://example.com", Canonical: "https://example.com/tags/go", SiteName: "Acme", Name: "go",
		Trail: []Crumb{{"Topics", "https://example.com/tags"}},
		Items: []Crumb{{"One", "https://example.com/one"}, {"No link", ""}, {"", "https://example.com/x"}, {"Two", "https://example.com/two"}},
	})))
	page := node(t, doc, "CollectionPage")
	items := page["mainEntity"].(map[string]any)["itemListElement"].([]any)
	if len(items) != 2 || items[1].(map[string]any)["name"] != "Two" || items[1].(map[string]any)["position"] != float64(2) {
		t.Errorf("items = %v", items)
	}
	trail := node(t, doc, "BreadcrumbList")["itemListElement"].([]any)
	var names []string
	for _, c := range trail {
		names = append(names, c.(map[string]any)["name"].(string))
	}
	if strings.Join(names, " › ") != "Acme › Topics › go" {
		t.Errorf("breadcrumb = %v", names)
	}
}
