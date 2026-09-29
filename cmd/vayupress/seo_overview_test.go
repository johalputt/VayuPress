// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// seoConfig points the page at a cache of its own and restores what it moved.
func seoConfig(t *testing.T, domain, key string, onion bool) string {
	t.Helper()
	old := config.Cfg
	t.Cleanup(func() { config.Cfg = old })
	config.Cfg.CacheDir, config.Cfg.Domain, config.Cfg.IndexNowKey, config.Cfg.OnionMode = t.TempDir(), domain, key, onion
	return config.Cfg.CacheDir
}

// SEO is an overview: the worst of its checks beside the title, the posts'
// readiness in a sentence beside the crawlers, then checks, files and instant
// indexing as rows. None of it is a disclosure, a badge or a card, and the
// hooks its scripts look up are all on the page.
func TestSEOIsAnOverviewOfRows(t *testing.T) {
	a := membersApp(t)
	seoConfig(t, "example.com", "", false)
	page := getPage(t, a.handleOSSEONative, "/os/seo")
	for _, want := range []string{`data-page-kind="overview"`, `sa-dot--danger" aria-hidden="true"></span>2 checks failing`,
		`class="sa-overview__sentence"`, ">Crawlers</h2>", ">Checks</h2>", ">Files</h2>", ">Instant indexing</h2>",
		"Not made yet", "data-seo-regenerate", "data-seo-status", "data-indexnow-test", "data-indexnow-result"} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	_, own, _ := strings.Cut(page, `data-page-kind="overview"`)
	for _, not := range []string{"mon-acc", "mon-chip", `class="badge`, "stat-card", "style=", "(s)"} {
		if strings.Contains(own, not) {
			t.Errorf("the page carries %q", not)
		}
	}

	dir := seoConfig(t, "example.com", "", false)
	for name, body := range map[string]string{"sitemap.xml": "<urlset/>", "feed.xml": "<rss/>",
		"robots.txt": "User-agent: *\nAllow: /\nSitemap: https://example.com/sitemap.xml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	page = getPage(t, a.handleOSSEONative, "/os/seo")
	if !strings.Contains(page, `sa-dot--ok" aria-hidden="true"></span>Every check passes`) || strings.Contains(page, "Not made yet") {
		t.Error("with every file made and nothing blocked, the page does not say every check passes")
	}
}

// The sentence names only the gaps there are, each in agreement with its count.
func TestSEOPostsSentence(t *testing.T) {
	for _, c := range []struct {
		total, healthy, noTitle, thin int
		want                          string
	}{
		{0, 0, 0, 0, "No posts yet."},
		{4, 4, 0, 0, "4 of 4 posts ready for search."},
		{1, 1, 0, 0, "1 of 1 post ready for search."},
		{5, 4, 1, 0, "4 of 5 posts ready for search; 1 needs a title."},
		{5, 3, 2, 0, "3 of 5 posts ready for search; 2 need a title."},
		{5, 4, 0, 1, "4 of 5 posts ready for search; 1 is under 300 words."},
		{5, 3, 0, 2, "3 of 5 posts ready for search; 2 are under 300 words."},
		{10, 7, 1, 2, "7 of 10 posts ready for search; 1 needs a title, 2 are under 300 words."},
	} {
		if got := seoPostsSentence(c.total, c.healthy, c.noTitle, c.thin); got != c.want {
			t.Errorf("%d/%d/%d/%d: %q, want %q", c.total, c.healthy, c.noTitle, c.thin, got, c.want)
		}
	}
}

// A failing check outranks one to look at, which outranks none; one seed each.
func TestSEOStateIsTheWorstCheck(t *testing.T) {
	pass, warn, fail := seoCheck{OK: true}, seoCheck{Warn: true}, seoCheck{}
	for _, c := range []struct {
		checks []seoCheck
		want   string
	}{
		{[]seoCheck{pass, pass}, `sa-dot--ok" aria-hidden="true"></span>Every check passes`},
		{[]seoCheck{pass, warn}, `sa-dot--warn" aria-hidden="true"></span>1 check to look at`},
		{[]seoCheck{warn, fail}, `sa-dot--danger" aria-hidden="true"></span>1 check failing`},
		{[]seoCheck{fail, fail, warn}, `sa-dot--danger" aria-hidden="true"></span>2 checks failing`},
	} {
		if got := string(seoState(c.checks)); !strings.Contains(got, c.want) {
			t.Errorf("%+v: %s, want %s", c.checks, got, c.want)
		}
	}
}

// Instant indexing says which of its four states it is in, and offers the
// verification only where there is something to verify.
func TestSEOIndexNowSaysItsState(t *testing.T) {
	a := &App{}
	for _, c := range []struct {
		name, key string
		onion     bool
		want      []string
		not       []string
	}{
		{"tor", "abcdefgh12", true, []string{"Off in Tor mode"}, []string{"data-indexnow-test", "Verification file"}},
		{"no key", "", false, []string{"Not connected", ">Connect and verify</button>"}, []string{"Verification file"}},
		{"bad key", "short", false, []string{"The key is not valid", ">Connect and verify</button>"}, []string{"Verification file"}},
		{"connected", "abcdefgh12", false, []string{"Connected", ">Verify again</button>", `href="/abcdefgh12.txt"`, "https://example.com/abcdefgh12.txt"}, []string{"Not connected"}},
	} {
		seoConfig(t, "example.com", c.key, c.onion)
		got := string(a.seoIndexNow())
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: missing %q", c.name, w)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%s: carries %q", c.name, n)
			}
		}
	}
}
