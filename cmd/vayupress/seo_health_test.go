package main

import (
	"testing"
	"time"
)

func findCheck(checks []seoCheck, label string) (seoCheck, bool) {
	for _, c := range checks {
		if c.Label == label {
			return c, true
		}
	}
	return seoCheck{}, false
}

func TestEvaluateSEOHealth(t *testing.T) {
	// Healthy site: fresh sitemap, normal robots, indexable, real domain.
	good := evaluateSEOHealth(true, 1*time.Hour, true, "User-agent: *\nAllow: /\nSitemap: https://x/sitemap.xml", "index, follow", "example.com")
	for _, label := range []string{"Sitemap generated", "robots.txt present", "Crawling allowed", "Indexing enabled", "Canonical domain set"} {
		c, ok := findCheck(good, label)
		if !ok || !c.OK {
			t.Errorf("healthy site: %q should pass, got %+v", label, c)
		}
	}

	// A site-wide Disallow blocks crawling.
	blocked := evaluateSEOHealth(true, time.Hour, true, "User-agent: *\nDisallow: /", "index", "example.com")
	if c, _ := findCheck(blocked, "Crawling allowed"); c.OK {
		t.Error("Disallow: / must fail the crawling check")
	}

	// noindex head directive disables indexing.
	ni := evaluateSEOHealth(true, time.Hour, true, "User-agent: *", "noindex, nofollow", "example.com")
	if c, _ := findCheck(ni, "Indexing enabled"); c.OK {
		t.Error("noindex must fail the indexing check")
	}

	// Stale sitemap warns; missing robots fails; local domain warns.
	bad := evaluateSEOHealth(true, 30*24*time.Hour, false, "", "", "localhost:8080")
	if c, _ := findCheck(bad, "Sitemap fresh"); c.OK || !c.Warn {
		t.Errorf("stale sitemap should warn, got %+v", c)
	}
	if c, _ := findCheck(bad, "robots.txt present"); c.OK {
		t.Error("missing robots should fail")
	}
	if c, _ := findCheck(bad, "Canonical domain set"); c.OK || !c.Warn {
		t.Errorf("local domain should warn, got %+v", c)
	}
}
