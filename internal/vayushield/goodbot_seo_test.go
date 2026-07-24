package vayushield

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/vayushield/challenge"
)

// crawlerReq builds a GET carrying a recognised crawler User-Agent.
func crawlerReq(path, ua string) *http.Request {
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("User-Agent", ua)
	return req
}

const (
	googlebotUA  = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
	bingbotUA    = "Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)"
	gptbotUA     = "Mozilla/5.0 (compatible; GPTBot/1.1; +https://openai.com/gptbot)"
	claudebotUA  = "Mozilla/5.0 (compatible; ClaudeBot/1.0; +claudebot@anthropic.com)"
	lighthouseUA = "Mozilla/5.0 (Linux; Android 11) AppleWebKit/537.36 Chrome/130.0.0.0 Mobile Safari/537.36 Chrome-Lighthouse"
)

// TestGoodBotSkipsSurge is the core Phase-1 SEO guarantee: with Sovereign Surge
// forced ON — which serves an unsolvable 503 PoW interstitial to every unproven
// browser (proven by TestSurgeChallengesUnprovenBrowser) — a recognised search /
// AI crawler must instead be served the real page (200), because it took the
// gate-0 SEO fast path BEFORE surge. A non-JS crawler cannot solve the PoW, so a
// 503 here is exactly what de-indexes the site.
func TestGoodBotSkipsSurge(t *testing.T) {
	for _, ua := range []string{googlebotUA, bingbotUA, gptbotUA, claudebotUA, lighthouseUA} {
		m := surgeManager(true)
		rr := httptest.NewRecorder()
		m.Middleware(okHandler()).ServeHTTP(rr, crawlerReq("/post", ua))
		if rr.Code != 200 {
			t.Fatalf("crawler UA %q must be served content under surge, got %d body=%s", ua, rr.Code, rr.Body.String())
		}
	}
}

// TestGoodBotSkipsCriticalSaturationSurge: the zero-config auto-surge path
// (SurgePressureFn, L0 lane critically full) must also never challenge a crawler
// — a legitimate traffic spike must not de-index Googlebot.
func TestGoodBotSkipsCriticalSaturationSurge(t *testing.T) {
	critical := true
	m := New(Config{
		Enabled:         true,
		Signer:          challenge.NewSigner([]byte("s")),
		ClientIP:        func(r *http.Request) string { return "203.0.113.9:1" },
		SurgePressureFn: func() bool { return critical },
	})
	m.ApplySettings(Settings{Enabled: true})
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, crawlerReq("/post", googlebotUA))
	if rr.Code != 200 {
		t.Fatalf("crawler must pass critical-saturation auto-surge, got %d", rr.Code)
	}
}

// TestGoodBotSkipsRateLimitAndJail: a crawler is exempt from the per-IP rate
// limit and the blocklist jail — the exact gates that (a real Googlebot crawls a
// concentrated IP range fast) turned into a 429 + auto-jail that de-indexed the
// site. Proven by exhausting the limiter and jailing the IP, then confirming the
// crawler still gets 200 while a normal browser from that IP is throttled.
func TestGoodBotSkipsRateLimitAndJail(t *testing.T) {
	ip := "203.0.113.42:5555"
	m := New(Config{
		Enabled:  true,
		Signer:   challenge.NewSigner([]byte("s")),
		Now:      time.Now,
		ClientIP: func(r *http.Request) string { return ip },
	})
	m.ApplySettings(Settings{Enabled: true, RateLimit: true, AutoBlock: true, RatePerMinute: 60, Burst: 5})

	// Exhaust the per-IP token bucket and jail the IP outright.
	for i := 0; i < 1000 && m.limiter.Allow("203.0.113.42"); i++ {
	}
	m.blocklist.Block("203.0.113.42", time.Minute)

	// A recognised crawler still gets the real page.
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, crawlerReq("/post", googlebotUA))
	if rr.Code != 200 {
		t.Fatalf("crawler must skip rate-limit + jail and be served content, got %d", rr.Code)
	}

	// Sanity: an ordinary anonymous browser from the same jailed IP is NOT served
	// content (proving the gates really are armed and it's the crawler exemption
	// doing the work, not a disarmed shield).
	rr2 := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr2, getBrowser("/post"))
	if rr2.Code == 200 {
		t.Fatal("a jailed anonymous browser must not be served content — the exemption must be crawler-specific")
	}
}

// TestGoodBotFiresAllowEvent: the fast path preserves the analytics allow-event
// stream so operator dashboards still count recognised-crawler traffic even
// though it now skips the classifier.
func TestGoodBotFiresAllowEvent(t *testing.T) {
	var events int
	m := New(Config{
		Enabled:  true,
		Signer:   challenge.NewSigner([]byte("s")),
		ClientIP: func(r *http.Request) string { return "203.0.113.9:1" },
		OnEvent:  func(a Action, score float64) { events++ },
	})
	m.ApplySettings(Settings{Enabled: true, Surge: true})
	m.Middleware(okHandler()).ServeHTTP(httptest.NewRecorder(), crawlerReq("/post", googlebotUA))
	if events == 0 {
		t.Fatal("a fast-pathed crawler must still emit an allow event for analytics")
	}
}

// TestRobotsAndSitemapAreFeedLike guards the exported shape check the L0
// sovereign lane relies on to never shed robots.txt / sitemap.xml (a 503 on
// robots.txt pauses all crawling).
func TestRobotsAndSitemapAreFeedLike(t *testing.T) {
	for _, p := range []string{"/robots.txt", "/sitemap.xml", "/index.xml", "/feed", "/blog/feed/", "/rss"} {
		if !IsFeedLikePath(p) {
			t.Errorf("%q must be treated as a feed-like (never-shed) path", p)
		}
	}
	for _, p := range []string{"/post", "/about", "/oslo-guide"} {
		if IsFeedLikePath(p) {
			t.Errorf("%q is a content path, must NOT be feed-like", p)
		}
	}
}

// TestBadBotNotFastPathed: the crawler fast path must not accidentally exempt a
// known bad bot — a scraper UA stays on the normal classification path (blocked).
func TestBadBotNotFastPathed(t *testing.T) {
	m := newTestManager(true)
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, crawlerReq("/post", "sqlmap/1.7"))
	if rr.Code == 200 {
		t.Fatal("a bad-bot UA must not ride the good-bot fast path")
	}
}
