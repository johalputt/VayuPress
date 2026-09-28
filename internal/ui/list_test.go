// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strings"
	"testing"
)

func TestListSaysItsKindAndPutsTheInspectorBesideTheList(t *testing.T) {
	got := string(List(ListPage{Title: "Posts", Count: "412 · 3 drafts"}, "<table></table>", "<p>one post</p>"))
	for _, want := range []string{`data-page-kind="list"`, `<h1>Posts <span class="sa-list__count">412 · 3 drafts</span></h1>`,
		`<aside class="sa-list__inspector" data-list-inspector aria-label="Details"><p>one post</p></aside>`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Count(got, "<h1") != 1 {
		t.Errorf("a page has exactly one title:\n%s", got)
	}
}

// With nothing to inspect the list takes the whole width, and there is no
// empty panel beside it.
func TestListWithoutAnInspectorHasNoPanel(t *testing.T) {
	got := string(List(ListPage{Title: "Posts"}, "<p>none</p>", ""))
	if strings.Contains(got, "data-list-inspector") || !strings.Contains(got, "sa-list__shell--full") {
		t.Errorf("an empty inspector must leave the list the whole width:\n%s", got)
	}
}

func TestSegmentsMarkTheCurrentViewAndHideNoCount(t *testing.T) {
	got := string(Segments("Show",
		Segment{Label: "All", Href: "/os/posts", Count: 412, On: true},
		Segment{Label: "Drafts", Href: "/os/posts?status=draft", Count: -1},
		Segment{Label: "Waiting", Href: "/os/comments?status=pending", Count: 3, CountID: "cc-pending"}))
	if !strings.Contains(got, `class="seg-btn is-active" aria-current="page" href="/os/posts">All <span class="muted">412</span>`) {
		t.Errorf("the current view must be marked, with its count:\n%s", got)
	}
	if !strings.Contains(got, `href="/os/posts?status=draft">Drafts</a>`) {
		t.Errorf("a negative count must not be shown:\n%s", got)
	}
	if !strings.Contains(got, `Waiting <span class="muted" id="cc-pending">3</span>`) {
		t.Errorf("a named count must carry its id, for the page to update it:\n%s", got)
	}
	if strings.Count(got, "aria-current") != 1 {
		t.Errorf("exactly one view is current:\n%s", got)
	}
}

// A search with an address is a GET form carrying the view it narrows; the
// values a person typed come back escaped.
func TestSearchIsAGetFormKeepingTheView(t *testing.T) {
	got := string(Search(SearchBox{Action: "/os/posts", Name: "q", Value: `"><script>`, Placeholder: "Search posts",
		Keep: [][2]string{{"status", "draft"}}}))
	for _, want := range []string{`<form class="sa-find" method="GET" action="/os/posts" role="search">`,
		`<input type="hidden" name="status" value="draft">`, `type="search"`, `value="&#34;&gt;&lt;script&gt;"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<script>") {
		t.Errorf("a typed value reached the page as markup:\n%s", got)
	}
}

// Without an address it is a field a script filters with, and no form.
func TestSearchWithoutAnAddressIsAHookedField(t *testing.T) {
	got := string(Search(SearchBox{Placeholder: "Filter media", Hook: "data-media-search"}))
	if strings.Contains(got, "<form") || !strings.Contains(got, `type="search"`) || !strings.Contains(got, " data-media-search>") {
		t.Errorf("want a hooked search field outside any form:\n%s", got)
	}
}
