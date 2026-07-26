// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"
	"time"
)

// TestBlogBaseAndPageURL covers the base-path normalisation and the blog page
// URL builder used for canonical + pagination links in both layouts.
func TestBlogBaseAndPageURL(t *testing.T) {
	defer SetBlogBase("/")

	SetBlogBase("")
	if BlogBase() != "/" {
		t.Fatalf("empty base must normalise to /, got %q", BlogBase())
	}

	SetBlogBase("/blog")
	if BlogBase() != "/blog" {
		t.Fatalf("BlogBase = %q, want /blog", BlogBase())
	}
	for n, want := range map[int]string{1: "/blog", 2: "/blog/page/2", 5: "/blog/page/5"} {
		if got := blogPageURL(n); got != want {
			t.Errorf("blogPageURL(%d) = %q, want %q", n, got, want)
		}
	}

	SetBlogBase("/")
	if got := blogPageURL(1); got != "/" {
		t.Errorf("root blogPageURL(1) = %q, want /", got)
	}
	if got := blogPageURL(2); got != "/page/2" {
		t.Errorf("root blogPageURL(2) = %q, want /page/2", got)
	}
}

// TestRenderHomeUsesBlogBase proves the blog index canonical, rel=prev/next and
// pager links are built under /blog when the base is /blog (business_subpath),
// and the nav-brand points at the blog base.
func TestRenderHomeUsesBlogBase(t *testing.T) {
	defer SetBlogBase("/")
	SetBlogBase("/blog")

	arts := []HomeArticle{{Title: "Hello", Slug: "hello", CreatedAt: time.Unix(1700000000, 0)}}
	out, err := RenderHome("example.com", "vtest", arts, 100, 2, 4)
	if err != nil {
		t.Fatalf("RenderHome: %v", err)
	}
	for _, want := range []string{
		`href="https://example.com/blog/page/2"`, // canonical / og:url (page 2)
		`href="/blog/page/3"`,                    // "Older" pager link (page 3)
		`href="/blog" class="vayu-nav-brand"`,    // brand points at the blog base
	} {
		if !strings.Contains(out, want) {
			t.Errorf("home page missing %q", want)
		}
	}
	if strings.Contains(out, `href="/page/`) {
		t.Error("home page still emits root /page/ links under a /blog base")
	}
}
