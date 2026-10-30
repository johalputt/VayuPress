// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
)

// A page's report is its readers': its own views, the visits that read it
// and how they arrived, the page read before it and the one after, and the
// way to each neighbour's report; another page's readers are in none of it.
func TestAPagesReportIsItsReaders(t *testing.T) {
	a := focusApp(t)
	page := a.renderPageReport(context.Background(), primaryView, "example.test", 7, "/google-only")
	for _, want := range []string{
		"1 view of /google-only in the last 7 days",
		`data-part="channels"`, `f=visit_channel%3Ais%3Aorganic`,
		`data-part="journey"`, `title="/google-next"`,
		`href="/os/analytics?days=7&amp;page=%2Fgoogle-next&amp;view=page" aria-label="Report on this page"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the report has no %q", want)
		}
	}
	if strings.Contains(page, "bing") {
		t.Error("the report counts a reader who never read the page")
	}
	_, next, _ := strings.Cut(page, ">Went next<")
	if !strings.Contains(next, `title="/google-next"`) {
		t.Error("the page read after it is not under Went next")
	}
	empty := a.renderPageReport(context.Background(), primaryView, "example.test", 7, "/never-read")
	if !strings.Contains(empty, "No one read /never-read in the last 7 days") {
		t.Error("a page no one read does not say so")
	}
}

// A report is asked for by its address, and a page's row leads to it; the
// bar's views lead back to the site, the period and filters kept.
func TestAReportIsReachedFromItsRow(t *testing.T) {
	a := focusApp(t)
	over := a.renderSiteAnalyticsBody(context.Background(), primaryView, "example.test", 7, "overview")
	if !strings.Contains(over, `href="/os/analytics?days=7&amp;page=%2Fgoogle-only&amp;view=page" aria-label="Report on this page"`) {
		t.Error("a top page's row has no way to its report")
	}
	t.Cleanup(func() { forgetCache(t, adminDash) })
	var body string
	for deadline := time.Now().Add(5 * time.Second); ; {
		w := httptest.NewRecorder()
		a.handleOSAnalytics(w, httptest.NewRequest(http.MethodGet, "/os/analytics?view=page&page=/google-only&days=7", nil))
		body = w.Body.String()
		if !strings.Contains(body, "counted in the background") || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, "1 view of /google-only") {
		t.Errorf("the report's address does not open it")
	}
	if !strings.Contains(body, `<a class="tab" href="/os/analytics?days=7&amp;view=audience">Audience</a>`) {
		t.Error("the bar's views from a report carry the page with them")
	}
	if p := pageOf(httptest.NewRequest(http.MethodGet, "/os/analytics?view=page&page=javascript:x", nil)); p != "" {
		t.Errorf("a page that is no path was taken: %q", p)
	}
}

// The link map reads a page's own links, in order, from its content alone,
// each keyed as it is counted, and sets beside each how many followed it.
func TestTheLinkMapIsThePagesLinksInOrder(t *testing.T) {
	body := `<html><body><header><a href="/home">Home</a></header><nav><a href="/x">X</a></nav>
<article><p>Read <a href="/next-post">the next post</a> or <a href="https://github.com/a/b?tab=1">the code</a>,
<a href="#top">top</a>, <a href="mailto:a@b">mail</a>, <a href="/next-post">again</a>.</p></article>
<footer><a href="/about">About</a></footer></body></html>`
	links := linksIn(body, "example.test", "/post")
	if len(links) != 2 || links[0].Key != "/next-post" || links[0].Text != "the next post" || !links[1].Out || links[1].Key != "github.com/a/b" {
		t.Fatalf("links = %+v, want the article's two, the site's then GitHub's", links)
	}
	// With no article, the main region's; with neither, the body's, never
	// its header, navigation or footer.
	if l := linksIn(`<body><nav><a href="/n">N</a></nav><main><a href="/m">M</a></main><aside><a href="/side">S</a></aside></body>`, "example.test", "/p"); len(l) != 1 || l[0].Key != "/m" {
		t.Errorf("links of a page with a main region = %+v, want /m alone", l)
	}
	if l := linksIn(`<body><header><a href="/h">H</a></header><div><a href="/d">D</a></div><footer><a href="/f">F</a></footer></body>`, "example.test", "/p"); len(l) != 1 || l[0].Key != "/d" {
		t.Errorf("links of a bare body = %+v, want /d alone", l)
	}
	m := osLinkMap(links, []analytics.AudienceStat{{Label: "/next-post", Count: 3}}, &analytics.ExploreResult{Rows: []analytics.ExploreRow{{Key: "github.com/a/b", Value: 1}}}, 4)
	for _, want := range []string{`the next post <span class="sa-links__to">/next-post</span></span><span class="vp-bar__val">3<span class="vp-bar__pct">75%</span>`,
		`the code <span class="sa-links__to">Out to github.com/a/b</span></span><span class="vp-bar__val">1<span class="vp-bar__pct">25%</span>`} {
		if !strings.Contains(m, want) {
			t.Errorf("the link map has no %q:\n%s", want, m)
		}
	}
	if strings.Index(m, "the next post") > strings.Index(m, "the code") {
		t.Error("the links are not in the page's order")
	}
}
