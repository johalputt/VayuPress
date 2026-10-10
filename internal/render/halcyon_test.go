// SPDX-License-Identifier: Apache-2.0

package render

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"image"
	pngenc "image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/theme"
)

// withHalcyon makes Halcyon the active theme, with opts over its defaults,
// for the length of a test.
func withHalcyon(t *testing.T, opts map[string]string) {
	t.Helper()
	Init(t.TempDir())
	h := theme.Halcyon()
	for k, v := range opts {
		h.Options[k] = v
	}
	ActivateTheme(h, "")
	t.Cleanup(func() { ActivateTheme(theme.Default(), "") })
}

var fixedTime = time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)

func realPost(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/halcyon/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func halcyonArticle(t *testing.T, content string) string {
	t.Helper()
	art := db.Article{ID: "1", Title: "Technical Debt", Slug: "debt", Content: content,
		Tags: []string{"engineering-culture", "architecture"}, CreatedAt: fixedTime, UpdatedAt: fixedTime}
	out, err := RenderArticleWithMeta(art, ArticleLayoutDefault,
		[]RelatedArticle{{Title: "Code Review", Slug: "review", CreatedAt: fixedTime, Shared: []string{"architecture"}}}, ArticleMetaOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ── The theme is chosen by layout, and only Halcyon's layout picks Halcyon ──

func TestHalcyonRendersOnlyWhileActive(t *testing.T) {
	withHalcyon(t, nil)
	if out := halcyonArticle(t, "<p>x</p>"); !strings.Contains(out, `class="h"`) {
		t.Error("the article did not render in Halcyon while it was the active theme")
	}
	ActivateTheme(theme.Default(), "")
	if out := halcyonArticle(t, "<p>x</p>"); strings.Contains(out, `class="h"`) || !strings.Contains(out, `class="vayu-nav"`) {
		t.Error("the article rendered in Halcyon after another theme was activated")
	}
}

// ── Ads: every anchor the injectors use is on every page that has the slot ──

func TestHalcyonKeepsEveryAdAnchor(t *testing.T) {
	cards := []HomeArticle{{Title: "One", Slug: "one", CreatedAt: fixedTime}, {Title: "Two", Slug: "two", CreatedAt: fixedTime}}
	pages := map[string]func(t *testing.T) string{
		"article": func(t *testing.T) string { return halcyonArticle(t, "<p>Body.</p>") },
	}
	for _, comp := range []string{"front", "river", "index"} {
		comp := comp
		pages["home-"+comp] = func(t *testing.T) string {
			withHalcyon(t, map[string]string{"home": comp})
			out, err := RenderHomePage(HomeInput{Articles: cards, Total: 2, Page: 1, TotalPages: 1})
			if err != nil {
				t.Fatal(err)
			}
			return out
		}
	}
	want := map[string][]string{
		"article":    {`<main id="main-content">`, `<div class="content" itemprop="articleBody">`, `</article>`, `</main></div>`, `<div class="h-rail-ads" data-ads="sidebar"></div>`},
		"home-front": {`<main id="main-content"`, `</main>`, `<div class="h-rail-ads" data-ads="sidebar"></div>`},
		"home-river": {`<main id="main-content"`, `</main>`, `<div class="h-rail-ads" data-ads="sidebar"></div>`},
		"home-index": {`<main id="main-content"`, `</main>`},
	}
	for name, render := range pages {
		withHalcyon(t, nil)
		out := render(t)
		for _, a := range want[name] {
			if !strings.Contains(out, a) {
				t.Errorf("%s: the ad anchor %q is missing, so that placement would never show", name, a)
			}
		}
	}
}

// The home injector puts the footer slot before the shared templates'
// trending section when there is one. Halcyon's most-read list sits in the
// middle of the front page, so it must not carry that anchor, or the footer
// slot would land mid-page.
func TestHalcyonHomeFooterAdStaysAtTheFoot(t *testing.T) {
	withHalcyon(t, nil)
	out, err := RenderHomePage(HomeInput{Articles: []HomeArticle{{Title: "One", Slug: "one", CreatedAt: fixedTime}}, Total: 1, Page: 1, TotalPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `<section class="vayu-trending"`) {
		t.Error("Halcyon's home carries the shared trending anchor; the footer ad would be placed mid-page")
	}
}

// ── The render-time treatments, one seed per rule ───────────────────────────

func TestStandfirstIsOnlyTheOpeningQuotation(t *testing.T) {
	if b := halcyonTreat(`<blockquote><p>Lead.</p></blockquote><p>Body.</p>`, false); b.Standfirst != "Lead." || strings.Contains(string(b.Body), "Lead.") {
		t.Errorf("an opening quotation should become the standfirst: %q / %q", b.Standfirst, b.Body)
	}
	if b := halcyonTreat(`<p>Body.</p><blockquote><p>A quote.</p></blockquote>`, false); b.Standfirst != "" || !strings.Contains(string(b.Body), "A quote.") {
		t.Errorf("a quotation later in the post must stay where it is: %q / %q", b.Standfirst, b.Body)
	}
}

func TestTableCellsAreLabelledByTheirColumn(t *testing.T) {
	b := halcyonTreat(`<table><thead><tr><th>Class</th><th>Example</th></tr></thead><tbody><tr><td>Deliberate</td><td>A flag</td></tr></tbody></table>`, false)
	if !strings.Contains(string(b.Body), `<td data-label="Example">A flag</td>`) {
		t.Errorf("a cell should carry its column's heading: %s", b.Body)
	}
	if !strings.Contains(string(b.Body), `<div class="tbl" data-stack>`) {
		t.Errorf("a table with named columns should be marked to stack on a phone: %s", b.Body)
	}
}

func TestTableWithoutHeadingsDoesNotStack(t *testing.T) {
	b := halcyonTreat(`<table><tr><td>a</td><td>b</td></tr></table>`, false)
	if strings.Contains(string(b.Body), "data-stack") || strings.Contains(string(b.Body), "data-label") {
		t.Errorf("a table with no column names has nothing to label its blocks with: %s", b.Body)
	}
}

func TestSectionHeadingsGetAnchorsAndTheOutline(t *testing.T) {
	b := halcyonTreat(`<h2 id="kept">First</h2><p>x</p><h2>Second part</h2><h2>Second part</h2>`, false)
	for _, want := range []string{`<h2 id="kept">`, `<h2 id="second-part">`, `<h2 id="second-part-2">`} {
		if !strings.Contains(string(b.Body), want) {
			t.Errorf("missing %s in %s", want, b.Body)
		}
	}
	if len(b.Outline) != 3 || b.Outline[0].ID != "kept" || b.Outline[2].ID != "second-part-2" || b.Outline[1].Text != "Second part" {
		t.Errorf("outline = %+v", b.Outline)
	}
}

// A heading made without an anchor must not take one a later heading already
// has, or the outline would link two sections to one place.
func TestMadeAnchorsNeverCollideWithExistingOnes(t *testing.T) {
	b := halcyonTreat(`<h2>Intro</h2><h2 id="intro">Later</h2>`, false)
	if strings.Count(string(b.Body), `id="intro"`) != 1 {
		t.Errorf("two headings share an anchor: %s", b.Body)
	}
}

func TestSmallCapsInRunningText(t *testing.T) {
	if b := halcyonTreat(`<p>Use SQL here.</p>`, true); !strings.Contains(string(b.Body), `<span class="sc">SQL</span>`) {
		t.Errorf("capitals in running text should be set in small caps: %s", b.Body)
	}
	if b := halcyonTreat(`<p>Use SQL here.</p>`, false); strings.Contains(string(b.Body), `class="sc"`) {
		t.Errorf("Running text: As written must leave capitals alone: %s", b.Body)
	}
}

// One seed per element: a code block is both pre and code, so it cannot show
// which of the two rules kept its capitals.
func TestSmallCapsLeaveInlineCodeAlone(t *testing.T) {
	if b := halcyonTreat(`<p>Run <code>SELECT x</code> now.</p>`, true); strings.Contains(string(b.Body), `class="sc"`) {
		t.Errorf("code keeps its capitals: %s", b.Body)
	}
}

func TestSmallCapsLeaveCodeBlocksAlone(t *testing.T) {
	if b := halcyonTreat(`<pre>SELECT x</pre>`, true); strings.Contains(string(b.Body), `class="sc"`) {
		t.Errorf("a code block keeps its capitals: %s", b.Body)
	}
}

func TestSmallCapsLeaveHeadingsAlone(t *testing.T) {
	if b := halcyonTreat(`<h2>Why SQL</h2>`, true); strings.Contains(string(b.Body), `class="sc"`) {
		t.Errorf("headings keep their capitals: %s", b.Body)
	}
}

func TestSmallCapsLeaveLinksAlone(t *testing.T) {
	if b := halcyonTreat(`<p><a href="/x">NASA</a></p>`, true); strings.Contains(string(b.Body), `class="sc"`) {
		t.Errorf("links keep their capitals: %s", b.Body)
	}
}

func TestSmallCapsLeaveTablesAlone(t *testing.T) {
	if b := halcyonTreat(`<table><tr><td>GUI</td></tr></table>`, true); strings.Contains(string(b.Body), `class="sc"`) {
		t.Errorf("tables keep their capitals: %s", b.Body)
	}
}

func TestSmallCapsOnlyWholeWords(t *testing.T) {
	if b := halcyonTreat(`<p>Use SQLite and GraphQL.</p>`, true); strings.Contains(string(b.Body), `class="sc"`) {
		t.Errorf("capitals inside a word are part of its name, not a run to set in small caps: %s", b.Body)
	}
}

// The mojibake in 3au is not a run of capitals and must not be dressed as one.
func TestSmallCapsIgnoreBrokenCharacters(t *testing.T) {
	if b := halcyonTreat(`<p>allocation ÃÃÃÃÃÂ¢ triggers</p>`, true); strings.Contains(string(b.Body), `class="sc"`) {
		t.Errorf("broken characters were set as small caps: %s", b.Body)
	}
}

// ── Nothing a post holds is lost, and the stored post is untouched ──────────

var anyTag = regexp.MustCompile(`<[^>]+>`)

// words is a body's text with its markup removed and its spacing collapsed.
// Tags go without leaving a space, because the treatments add inline markup
// (a span around SQL) that must not read as a new word.
func words(html string) string {
	return strings.Join(strings.Fields(htmlUnescape(anyTag.ReplaceAllString(html, ""))), " ")
}

func htmlUnescape(s string) string {
	r := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&#34;", `"`, "&#39;", "'", "&quot;", `"`)
	return r.Replace(s)
}

func TestTreatmentsLoseNoTextOfARealPost(t *testing.T) {
	Init(t.TempDir())
	for _, name := range []string{"technical-debt-visible-paydown-guide.html", "gitcomet-wide-content.html"} {
		stored := realPost(t, name)
		sum := sha256.Sum256([]byte(stored))
		sanitised := renderContentHTML(stored)
		b := halcyonTreat(sanitised, true)
		if got, want := words(string(b.Standfirst)+" "+string(b.Body)), words(sanitised); got != want {
			t.Errorf("%s: the treated text differs from the sanitised text", name)
		}
		if sha256.Sum256([]byte(stored)) != sum {
			t.Errorf("%s: rendering changed the stored post", name)
		}
	}
}

// ── Speed: the budgets, and the pre-paint script admitted by its hash ───────

func gzipLen(b []byte) int {
	var buf bytes.Buffer
	z, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = z.Write(b)
	_ = z.Close()
	return buf.Len()
}

func TestHalcyonStaysInItsBudgets(t *testing.T) {
	for name, limit := range map[string]int{"halcyon.css": 16 * 1024, "halcyon.js": 6 * 1024} {
		b, _, ok := HalcyonAsset(name)
		if !ok {
			t.Fatalf("%s is not served", name)
		}
		if n := gzipLen(b); n > limit {
			t.Errorf("%s is %d bytes gzipped, over its %d budget", name, n, limit)
		}
	}
}

var inlineScriptRe = regexp.MustCompile(`<script>(.*?)</script>`)

// The inline script a Halcyon page carries is the one the policy admits: a
// changed script with a stale hash would be refused by every browser and the
// reader's choices would never apply.
func TestPrefsScriptIsAdmittedByThePolicy(t *testing.T) {
	withHalcyon(t, nil)
	out := halcyonArticle(t, "<p>x</p>")
	m := inlineScriptRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatal("no inline script in the page")
	}
	sum := sha256.Sum256([]byte(m[1]))
	hash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	if !strings.Contains(BuildCSP("n", nil), hash) || !strings.Contains(BuildAdCSP("n", nil), hash) {
		t.Errorf("the page's inline script %s is not admitted by the page policy", hash)
	}
}

// ── Related reading ───────────────────────────────────────────────────────

func TestRankRelatedIgnoresUbiquitousTopics(t *testing.T) {
	ubiq := func(tag string) bool { return tag == "tutorial" }
	got := RankRelated([]RelatedArticle{{Slug: "only-tutorial", Shared: []string{"tutorial"}}}, ubiq, 3)
	if len(got) != 0 {
		t.Errorf("a post sharing only a ubiquitous topic is not related: %+v", got)
	}
}

func TestRankRelatedOrdersByTopicsShared(t *testing.T) {
	never := func(string) bool { return false }
	got := RankRelated([]RelatedArticle{
		{Slug: "one", CreatedAt: fixedTime.Add(time.Hour), Shared: []string{"a"}},
		{Slug: "two", CreatedAt: fixedTime, Shared: []string{"a", "b"}},
	}, never, 3)
	if len(got) != 2 || got[0].Slug != "two" {
		t.Errorf("the post sharing more topics should lead: %+v", got)
	}
}

func TestRankRelatedBreaksTiesByTheNewer(t *testing.T) {
	never := func(string) bool { return false }
	got := RankRelated([]RelatedArticle{
		{Slug: "older", CreatedAt: fixedTime, Shared: []string{"a"}},
		{Slug: "newer", CreatedAt: fixedTime.Add(time.Hour), Shared: []string{"b"}},
	}, never, 3)
	if got[0].Slug != "newer" {
		t.Errorf("on a tie the newer post should lead: %+v", got)
	}
}

func TestRankRelatedKeepsAtMostN(t *testing.T) {
	never := func(string) bool { return false }
	var c []RelatedArticle
	for i := 0; i < 5; i++ {
		c = append(c, RelatedArticle{Slug: string(rune('a' + i)), Shared: []string{"x"}})
	}
	if got := RankRelated(c, never, 3); len(got) != 3 {
		t.Errorf("kept %d, want 3", len(got))
	}
}

func TestUbiquitousIsMoreThanHalf(t *testing.T) {
	if Ubiquitous(50, 100) || !Ubiquitous(51, 100) || Ubiquitous(1, 0) {
		t.Error("a topic is ubiquitous only when more than half of all posts carry it")
	}
}

func TestSharesReasonReadsAsASentence(t *testing.T) {
	for in, want := range map[string]string{
		"devops":                          "Shares devops",
		"devops,sre":                      "Shares devops and sre",
		"engineering-culture,process,sre": "Shares engineering culture, process and sre",
	} {
		if got := sharesReason(strings.Split(in, ",")); got != want {
			t.Errorf("sharesReason(%s) = %q, want %q", in, got, want)
		}
	}
}

// ── The editor's components, reused from their one source ──────────────────

func TestBlockComponentRulesComeFromTheSharedStylesheet(t *testing.T) {
	got := string(blockComponentRules(articleCSSMin))
	for _, want := range []string{".video-facade{", ".embed-card{", ".vp-figure", "@media"} {
		if !strings.Contains(got, want) {
			t.Errorf("the component rules lack %q", want)
		}
	}
	for _, not := range []string{"body", "html", ".vayu-nav", ".vayu-post-card", ":root", "*"} {
		if regexp.MustCompile(`(^|[}{,])` + regexp.QuoteMeta(not) + `[{,:\s]`).MatchString(got) {
			t.Errorf("a rule of the shared page came with the components: %q", not)
		}
	}
	if css, _, _ := HalcyonAsset("halcyon.css"); !bytes.Contains(css, []byte(".video-facade{")) {
		t.Error("halcyon.css does not carry the editor's component rules")
	}
}

// ── Member pages ──────────────────────────────────────────────────────────

const memberPageFixture = `<!DOCTYPE html><html lang="en"><head><link rel="stylesheet" href="/theme.css">` +
	`<link rel="stylesheet" href="/static/css/signup.css?v=1"></head><body class="su-body"><main><h2>📮 Your VayuMail ID</h2></main></body></html>`

func TestMemberPagesAreUntouchedUnderOtherThemes(t *testing.T) {
	Init(t.TempDir())
	ActivateTheme(theme.Default(), "")
	if got := HalcyonMemberPage(memberPageFixture, MemberTask); got != memberPageFixture {
		t.Errorf("a member page changed under another theme:\n%s", got)
	}
}

func TestMemberPagesTakeHalcyon(t *testing.T) {
	withHalcyon(t, nil)
	got := HalcyonMemberPage(memberPageFixture, MemberTask)
	if !strings.Contains(got, `<html lang="en" class="h"`) {
		t.Error("the member page is not marked for Halcyon")
	}
	if strings.Index(got, "signup.css") > strings.Index(got, "halcyon-members.css") {
		t.Error("Halcyon's member stylesheet must follow signup.css, or signup.css wins")
	}
	if !strings.Contains(got, `<header class="h-focus">`) || !strings.Contains(got, "Back to the site") {
		t.Error("a task page should carry the focused header")
	}
	if strings.Contains(got, "📮") || !strings.Contains(got, "<h2>Your VayuMail ID</h2>") {
		t.Errorf("the heading's pictograph should go and its words stay: %s", got)
	}
}

// A new install starts on Halcyon with nothing published; its Home says so
// rather than showing an empty front page.
func TestAnEmptyHomeSaysSo(t *testing.T) {
	for _, comp := range []string{"front", "river"} {
		withHalcyon(t, map[string]string{"home": comp})
		out, err := RenderHomePage(HomeInput{Page: 1, TotalPages: 1})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "Nothing is published here yet.") {
			t.Errorf("%s: an empty Home does not say so", comp)
		}
	}
}

func TestCachePurgeListingsKeepsEveryPost(t *testing.T) {
	dir := t.TempDir()
	prev := config.Cfg.CacheDir
	config.Cfg.CacheDir = dir
	t.Cleanup(func() { config.Cfg.CacheDir = prev })
	for _, f := range []string{"home/index.html", "home/d_b.example/index.html", "tags/devops.html", "d_b.example/tags/go.html", "posts/debt.html"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	CachePurgeListings()
	for f, want := range map[string]bool{"home/index.html": false, "home/d_b.example/index.html": false, "tags/devops.html": false, "d_b.example/tags/go.html": false, "posts/debt.html": true} {
		if _, err := os.Stat(filepath.Join(dir, f)); (err == nil) != want {
			t.Errorf("%s: present = %v, want %v", f, err == nil, want)
		}
	}
}

// Halcyon's topic page keeps a thin topic out of search as the shared one does
// (tags_thin_test.go), at the same threshold.
func TestAThinHalcyonTopicIsNotIndexed(t *testing.T) {
	withHalcyon(t, nil)
	const noindex = `<meta name="robots" content="noindex,follow">`
	for n, want := range map[int]bool{1: true, thinTagPosts - 1: true, thinTagPosts: false, 40: false} {
		page, err := RenderTopicPage(TopicInput{Domain: "example.com", Version: "1.0.0", Tag: "go", Total: n})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(page, noindex); got != want {
			t.Errorf("%d posts: noindex %v, want %v", n, got, want)
		}
	}
}

// Halcyon's topic page keeps a broad topic out of search as the shared one
// does, however many posts it lists.
func TestABroadHalcyonTopicIsNotIndexed(t *testing.T) {
	withHalcyon(t, nil)
	page, err := RenderTopicPage(TopicInput{Domain: "example.com", Version: "1.0.0", Tag: "tutorial", Total: 40, Broad: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, `<meta name="robots" content="noindex,follow">`) {
		t.Error("a broad Halcyon topic with 40 posts is offered to search")
	}
}

// A Halcyon post links each tag to the tag's one page, its key.
func TestHalcyonLinksATagByItsKey(t *testing.T) {
	withHalcyon(t, nil)
	art := db.Article{ID: "1", Title: "T", Slug: "t", Content: "<p>x</p>", Tags: []string{"Machine Learning"}, CreatedAt: fixedTime, UpdatedAt: fixedTime}
	out, err := RenderArticleWithMeta(art, ArticleLayoutDefault, nil, ArticleMetaOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `href="/tags/machine-learning"`) || strings.Contains(out, `/tags/Machine%20Learning`) {
		t.Error("Halcyon links the tag Machine Learning elsewhere than /tags/machine-learning")
	}
}

// The shared tag page's entry point carries the broad flag into Halcyon too.
func TestABroadTagPageIsNotIndexedInHalcyon(t *testing.T) {
	withHalcyon(t, nil)
	page, err := RenderTagPage("example.com", "1.0.0", "tutorial", nil, 40, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, `<meta name="robots" content="noindex,follow">`) {
		t.Error("a broad tag page in Halcyon with 40 posts is offered to search")
	}
}

// A topic page offers its two orders. Most read says how many of the topic's
// posts were read and over what window, numbers them by rank, and is kept out
// of search engines since its posts are the topic page's own.
func TestATopicPageOffersNewestAndMostRead(t *testing.T) {
	withHalcyon(t, nil)
	arts := []HomeArticle{{Title: "Most", Slug: "most", CreatedAt: fixedTime}, {Title: "Next", Slug: "next", CreatedAt: fixedTime.AddDate(0, -1, 0)}}
	in := TopicInput{Domain: "example.com", Version: "1", Tag: "devops", Articles: arts, Total: 40, ReadDays: 30}
	newest, err := RenderTopicPage(in)
	if err != nil {
		t.Fatal(err)
	}
	in.MostRead = true
	read, err := RenderTopicPage(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, page, want string
		has              bool
	}{
		{"newest names its order", newest, "40 articles, newest first.", true},
		{"newest is current", newest, `href="/tags/devops" aria-current="true">Newest`, true},
		{"newest is indexed", newest, `noindex`, false},
		{"most read says how many and over what", read, "2 of 40 articles read in the last 30 days, most read first.", true},
		{"most read is current", read, `href="/tags/devops?sort=read" aria-current="true">Most read`, true},
		{"most read is not indexed", read, `<meta name="robots" content="noindex,follow">`, true},
		{"most read is ranked, not by month", read, `<h2>Most read</h2><ol><li><a class="h-row" href="/most"><span class="h-d">1</span>`, true},
		{"its search keeps the topic", read, `<input type="hidden" name="topic" value="devops"><input type="hidden" name="sort" value="read">`, true},
	} {
		if strings.Contains(c.page, c.want) != c.has {
			t.Errorf("%s: %q present = %v", c.name, c.want, !c.has)
		}
	}
	in.Articles = nil
	if empty, _ := RenderTopicPage(in); !strings.Contains(empty, "Nothing in this topic was read in the last 30 days.") {
		t.Error("a topic nobody read says so")
	}
}

// Under "Same as Home", a topic page is the river, and still names its topic
// and offers its orders.
func TestATopicInTheRiverKeepsItsHeading(t *testing.T) {
	withHalcyon(t, map[string]string{"archives": "home"})
	out, err := RenderTopicPage(TopicInput{Domain: "example.com", Tag: "devops", Articles: []HomeArticle{{Title: "One", Slug: "one", CreatedAt: fixedTime}}, Total: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<h1>devops</h1>`, `aria-label="Order"`, `class="h-river"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the river topic page lacks %q", want)
		}
	}
}

// A search from a topic page stays in the topic and keeps its order; with
// nothing found it offers every topic. A site-wide search that finds nothing
// offers the largest topics.
func TestASearchCanBeScopedToATopic(t *testing.T) {
	withHalcyon(t, nil)
	scoped, err := RenderSearchPage(SearchInput{Query: "kafka", Topic: "devops", MostRead: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<h1>Search devops</h1>", "Nothing in devops matches “kafka”.", `<input type="hidden" name="topic" value="devops"><input type="hidden" name="sort" value="read">`, `href="/search?q=kafka">Search every topic`} {
		if !strings.Contains(scoped, want) {
			t.Errorf("a topic search lacks %q", want)
		}
	}
	found, _ := RenderSearchPage(SearchInput{Query: "kafka", Topic: "devops", Hits: []SearchHit{{Title: "K", Slug: "k", CreatedAt: fixedTime}}})
	if !strings.Contains(found, "1 result for “kafka”, newest first") {
		t.Error("a topic search's results do not name their order")
	}
	wide, _ := RenderSearchPage(SearchInput{Query: "kafka", Topics: []TagInfo{{Name: "devops", Count: 4}, {Name: "tutorial", Count: 10}}, TopicTotal: 10})
	if !strings.Contains(wide, `<nav class="h-topics" aria-label="Topics"><a href="/tags/devops">devops<span class="h-n">4</span></a></nav>`) {
		t.Error("an empty search does not offer the topics, without the ubiquitous one")
	}
}

// An update that changes Halcyon re-renders a Halcyon site's cached pages,
// and only a Halcyon site's: the fingerprint names Halcyon's templates and
// files while it is active and nothing of them otherwise.
func TestTheCacheFingerprintFollowsHalcyonOnlyWhileItIsActive(t *testing.T) {
	Init(t.TempDir())
	ActivateTheme(theme.Default(), "")
	other := cacheFingerprint()
	withHalcyon(t, nil)
	on := cacheFingerprint()
	if on == other {
		t.Fatal("the fingerprint does not change when Halcyon is active")
	}
	prev := halcyonPages["article"]
	halcyonPages["article"] = prev + " "
	changed := cacheFingerprint()
	halcyonPages["article"] = prev
	if changed == on {
		t.Error("a change to a Halcyon template leaves its cached pages in place")
	}
	ActivateTheme(theme.Default(), "")
	halcyonPages["article"] = prev + " "
	still := cacheFingerprint()
	halcyonPages["article"] = prev
	if still != other {
		t.Error("a change to Halcyon re-renders a site on another theme")
	}
}

// A library image is given its real size so the text below it does not jump
// as it loads; an image from elsewhere, one with a size, and a name reaching
// outside the library are left as written.
func TestALibraryImageCarriesItsSize(t *testing.T) {
	dir := t.TempDir()
	prev := config.Cfg.MediaDir
	config.Cfg.MediaDir = dir
	t.Cleanup(func() { config.Cfg.MediaDir = prev })
	var png bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 640, 360))
	if err := pngenc.Encode(&png, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a1.png"), png.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ in, want string }{
		{`<img src="/media/a1.png" alt="">`, `<img src="/media/a1.png" alt="" width="640" height="360">`},
		{`<img src="/media/a1.png" width="10">`, `<img src="/media/a1.png" width="10">`},
		{`<img src="https://example.com/a1.png">`, `<img src="https://example.com/a1.png">`},
		{`<img src="/media/../media/a1.png">`, `<img src="/media/../media/a1.png">`},
		{`<img src="/media/missing.png">`, `<img src="/media/missing.png">`},
	} {
		if got := string(halcyonTreat(`<p>x</p>`+c.in, false).Body); !strings.Contains(got, c.want) {
			t.Errorf("%s\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

// The member pages' <html> tag is assembled as a string, outside
// html/template; whatever a config value holds stays inside its attribute.
func TestAMemberPageKeepsConfigValuesInTheirAttributes(t *testing.T) {
	cfg := theme.ResolveHalcyon(nil)
	cfg.Face, cfg.Appearance = `serif" onload="x`, `dark" data-x="y`
	out := halcyonMemberPage(`<!DOCTYPE html><html lang="en"><head></head><body></body></html>`, MemberTask, cfg)
	for _, bad := range []string{` onload="x"`, ` data-x="y"`} {
		if strings.Contains(out, bad) {
			t.Errorf("a config value left its attribute: %s", bad)
		}
	}
	if !strings.Contains(out, `data-face="serif&#34; onload=&#34;x"`) {
		t.Error("the face is not written, escaped, into its attribute")
	}
}

// A post's own <h1> becomes a section heading: the page keeps one <h1>, its
// title, and the outline lists the post's.
func TestAPostsOwnH1IsASectionHeading(t *testing.T) {
	b := halcyonTreat(`<h1>Cilium vs Istio</h1><p>x</p><h2>Background</h2>`, false)
	if strings.Contains(string(b.Body), "<h1") {
		t.Errorf("a post's <h1> is still an <h1>: %s", b.Body)
	}
	if !strings.Contains(string(b.Body), `<h2 id="cilium-vs-istio">Cilium vs Istio</h2>`) {
		t.Errorf("a post's <h1> is not a section heading with its anchor: %s", b.Body)
	}
	if len(b.Outline) != 2 || b.Outline[0].Text != "Cilium vs Istio" {
		t.Errorf("the outline does not list the post's own heading: %+v", b.Outline)
	}
}

// A post's cover sits above its text, at the cover's own width, inside the
// figure halcyon.js leaves out when the image does not arrive.
func TestACoverSitsAboveTheText(t *testing.T) {
	withHalcyon(t, nil)
	art := db.Article{ID: "1", Title: "T", Slug: "t", Content: "<p>Body.</p>", CreatedAt: fixedTime, UpdatedAt: fixedTime}
	out, err := RenderArticleWithMeta(art, ArticleLayoutDefault, nil, ArticleMetaOverrides{FeatureImage: "/media/cover.png"})
	if err != nil {
		t.Fatal(err)
	}
	cover := strings.Index(out, `<figure class="h-cover"><img src="https://`)
	body := strings.Index(out, `<div class="content"`)
	if cover < 0 || body < 0 || cover > body {
		t.Errorf("the cover is not a figure above the text (cover at %d, text at %d)", cover, body)
	}
	if without, _ := RenderArticleWithMeta(art, ArticleLayoutDefault, nil, ArticleMetaOverrides{}); strings.Contains(without, "h-cover") {
		t.Error("a post without a cover draws an empty cover")
	}
}
