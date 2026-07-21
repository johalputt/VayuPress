package main

import (
	"strings"
	"testing"
)

// TestCrawlerUABlocked verifies the crawler-block UA filter: known search-engine
// and AI crawlers are rejected, while ordinary browsers and empty/unknown agents
// pass (bad bots are VayuShield's job, not this switch).
func TestCrawlerUABlocked(t *testing.T) {
	blocked := []string{
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
		"Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)",
		"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; GPTBot/1.1)",
		"Mozilla/5.0 (compatible; ClaudeBot/1.0; +claudebot@anthropic.com)",
		"Mozilla/5.0 (compatible; PerplexityBot/1.0)",
		"CCBot/2.0 (https://commoncrawl.org/faq/)",
	}
	for _, ua := range blocked {
		if !crawlerUABlocked(ua) {
			t.Errorf("expected crawler UA to be blocked: %q", ua)
		}
	}
	allowed := []string{
		"", // no UA — not a known crawler
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Safari/537.36",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15",
	}
	for _, ua := range allowed {
		if crawlerUABlocked(ua) {
			t.Errorf("expected UA to pass the crawler filter: %q", ua)
		}
	}
}

// TestCrawlerBlockedRobotsTxt checks the disallow-everything robots.txt served
// while the block is on names the wildcard plus the major AI crawlers.
func TestCrawlerBlockedRobotsTxt(t *testing.T) {
	for _, want := range []string{"User-agent: *", "Disallow: /", "GPTBot", "ClaudeBot", "PerplexityBot", "Google-Extended"} {
		if !strings.Contains(crawlerBlockedRobotsTxt, want) {
			t.Errorf("crawler-blocked robots.txt missing %q", want)
		}
	}
	// A wildcard disallow must be present so compliant crawlers stop everywhere.
	if !strings.Contains(crawlerBlockedRobotsTxt, "User-agent: *\nDisallow: /") {
		t.Error("robots.txt must disallow everything for the wildcard agent")
	}
}
