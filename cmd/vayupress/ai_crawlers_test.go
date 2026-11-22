// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/users"
)

const (
	uaGPTBot    = "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; GPTBot/1.1)"
	uaClaudeBot = "Mozilla/5.0 (compatible; ClaudeBot/1.0; +claudebot@anthropic.com)"
	uaGooglebot = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
	uaBrowser   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Safari/537.36"
)

func aiCrawlerApp(t *testing.T, aiOff bool) *App {
	t.Helper()
	openMigratedDB(t)
	a := &App{siteSettings: settings.New(dbpkg.DB)}
	if aiOff {
		if err := a.siteSettings.SetMany(context.Background(), settings.ForPrimary(), map[string]string{settings.KeyBlockAICrawlers: "on"}); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func crawlThrough(a *App, ua string) *httptest.ResponseRecorder {
	h := a.crawlerBlockMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("page")) }))
	r := httptest.NewRequest(http.MethodGet, "/a-post", nil)
	r.Header.Set("User-Agent", ua)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// With AI crawlers refused, a known AI crawler is answered 403 while a
// search engine and a reader are served the page, and nothing says noindex;
// switched off, the AI crawler is served too.
func TestAICrawlersOnlyAreRefused(t *testing.T) {
	a := aiCrawlerApp(t, true)
	for _, ua := range []string{uaGPTBot, uaClaudeBot} {
		if rec := crawlThrough(a, ua); rec.Code != http.StatusForbidden {
			t.Errorf("an AI crawler (%s) was answered %d with AI crawlers refused", ua, rec.Code)
		}
	}
	for _, ua := range []string{uaGooglebot, uaBrowser} {
		rec := crawlThrough(a, ua)
		if rec.Code != http.StatusOK || rec.Header().Get("X-Robots-Tag") != "" {
			t.Errorf("%s was answered %d, X-Robots-Tag %q, with only AI crawlers refused", ua, rec.Code, rec.Header().Get("X-Robots-Tag"))
		}
	}
	if rec := crawlThrough(aiCrawlerApp(t, false), uaGPTBot); rec.Code != http.StatusOK {
		t.Errorf("an AI crawler was answered %d with AI crawlers allowed", rec.Code)
	}
}

// robots.txt keeps the site's own rules for search engines and names each AI
// crawler after them, disallowed; llms.txt, written for AI, is withheld.
func TestAICrawlersAreToldInRobotsAndDeniedLLMs(t *testing.T) {
	a := aiCrawlerApp(t, true)
	prev := config.Cfg.CacheDir
	config.Cfg.CacheDir = t.TempDir()
	t.Cleanup(func() { config.Cfg.CacheDir = prev })
	own := "User-agent: *\nAllow: /\nSitemap: https://example.test/sitemap.xml\n"
	if err := os.WriteFile(filepath.Join(config.Cfg.CacheDir, "robots.txt"), []byte(own), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.handleRobots(rec, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	body := rec.Body.String()
	if !strings.HasPrefix(body, own) || !strings.Contains(body, "User-agent: GPTBot\nDisallow: /") || !strings.Contains(body, "User-agent: ClaudeBot\nDisallow: /") {
		t.Fatalf("robots.txt with AI crawlers refused is:\n%s", body)
	}
	if strings.Contains(body, "User-agent: *\nDisallow: /") {
		t.Fatal("robots.txt disallows every crawler when only AI crawlers are refused")
	}
	llms := httptest.NewRecorder()
	a.handleLLMsTxt(llms, httptest.NewRequest(http.MethodGet, "/llms.txt", nil))
	if llms.Code != http.StatusNotFound {
		t.Errorf("llms.txt was answered %d with AI crawlers refused", llms.Code)
	}
}

// The Power page's switch for AI crawlers sets that setting alone, and the
// one for every crawler is unchanged by it.
func TestThePowerSwitchSetsAICrawlersAlone(t *testing.T) {
	a := aiCrawlerApp(t, false)
	ctx := context.Background()
	r := httptest.NewRequest(http.MethodPost, "/os/api/power/crawlers", strings.NewReader(`{"ai":true}`))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handleOSPowerCrawlers(rec, withUser(r, &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}))
	if rec.Code != http.StatusOK {
		t.Fatalf("the switch answered %d %s", rec.Code, rec.Body.String())
	}
	if !a.aiCrawlersBlocked(ctx) || a.crawlersBlocked(ctx) {
		t.Fatalf("after the AI switch: AI refused %v, every crawler refused %v", a.aiCrawlersBlocked(ctx), a.crawlersBlocked(ctx))
	}
}
