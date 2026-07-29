// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
)

// canaryRecorder is a minimal http.ResponseWriter that records only the status
// code and discards the body — enough to assert what the shield decided without
// pulling net/http/httptest into the production binary.
type canaryRecorder struct {
	status int
	header http.Header
}

func (c *canaryRecorder) Header() http.Header {
	if c.header == nil {
		c.header = http.Header{}
	}
	return c.header
}

func (c *canaryRecorder) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return len(b), nil
}

func (c *canaryRecorder) WriteHeader(s int) { c.status = s }

// canaryCrawlers are the crawlers the SEO canary asserts are always served
// content. They cover a classic search engine, a second one, an AI crawler, and
// the performance tester whose score the operator watches.
var canaryCrawlers = []struct{ name, ua string }{
	{"Googlebot", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"},
	{"Bingbot", "Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)"},
	{"GPTBot", "Mozilla/5.0 (compatible; GPTBot/1.1; +https://openai.com/gptbot)"},
	{"PageSpeed", "Mozilla/5.0 (Linux; Android 11) AppleWebKit/537.36 Chrome/130 Mobile Safari/537.36 Chrome-Lighthouse"},
}

// canaryReaders are ordinary first-time human visitors — a top-level navigation
// from a mainstream browser, carrying no clearance cookie. They are the other
// half of the promise: a shield that de-indexes nothing is still broken if it
// meets real people with a verification page. Each MUST be served content.
var canaryReaders = []struct{ name, ua string }{
	{"Reader · Chrome (Windows)", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"},
	{"Reader · Safari (iPhone)", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
	{"Reader · Firefox", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:130.0) Gecko/20100101 Firefox/130.0"},
	{"Reader · Brave/Chromium", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"},
}

// canaryProbeResult is one probe's outcome, for the operator-facing report.
type canaryProbeResult struct {
	Name   string
	Group  string // "Readers" | "Crawlers"
	Status int
	OK     bool
	// Why explains a non-200 outcome in the shield's own words — the score, the
	// classification and the top reason the scorer recorded. Without it a red row
	// says only "403", which leaves the operator (and anyone helping them) guessing
	// between a static signature, a learned signature and plain heuristic scoring.
	Why string
	// NotTestable marks a probe whose identity cannot be simulated in-process, so
	// its result is not evidence either way. Crawler probes are in this state once
	// crawler recognition is IP-verified: a synthetic Googlebot from a test address
	// is SUPPOSED to be treated as an impostor, so counting it as a failure would
	// raise a false indexing alarm.
	NotTestable bool
}

// shieldCanaryResult reports how many synthetic probes were served content vs
// challenged/blocked, plus the per-probe detail the admin panel renders.
type shieldCanaryResult struct {
	passed  []string
	failed  []string
	probes  []canaryProbeResult
	readers int // how many reader probes were served content
}

func (r shieldCanaryResult) ok() bool { return len(r.failed) == 0 }

// runShieldCanary drives synthetic verified-crawler requests through the LIVE
// shield middleware and reports which are served real content (200) vs met with
// a challenge/403/429. It is the ground-truth guard for "the shield never
// de-indexes": if a config or code change ever caused Googlebot to be
// challenged, this surfaces it deterministically instead of the operator finding
// out weeks later through lost search rankings.
func (a *App) runShieldCanary() shieldCanaryResult {
	var res shieldCanaryResult
	if a.vayuShield == nil {
		return res
	}
	sentinel := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("content"))
	})
	h := a.vayuShield.Middleware(sentinel)
	// With IP-verified crawler recognition, a crawler's identity cannot be faked
	// from this host, so those probes are informational only.
	crawlersUntestable := a.vayuShield.CrawlerIdentityIsIPVerified()
	probe := func(name, group, ua string, navigation bool) {
		req, err := http.NewRequest(http.MethodGet, "/", nil)
		if err != nil {
			return
		}
		req.Header.Set("User-Agent", ua)
		if navigation {
			// What a real person's page load looks like: a GET asking for HTML.
			req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
			req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		}
		req.RemoteAddr = "203.0.113.1:12345"
		rec := &canaryRecorder{}
		h.ServeHTTP(rec, req)
		ok := rec.status == http.StatusOK
		untestable := group == "Crawlers" && crawlersUntestable && !ok
		why := ""
		if !ok && !untestable {
			// Re-run classification (pure, no side effects) purely to report WHY.
			v := a.vayuShield.Classify(req)
			why = fmt.Sprintf("score %.2f · %s", v.Result.BotScore, v.Result.ClientType)
			if v.Result.BotName != "" {
				why += " · " + v.Result.BotName
			}
			if len(v.Result.Reasons) > 0 {
				why += " · " + v.Result.Reasons[0]
			}
			if v.Result.Authoritative {
				why += " · identified (no benefit of the doubt)"
			}
		}
		res.probes = append(res.probes, canaryProbeResult{
			Name: name, Group: group, Status: rec.status, OK: ok, NotTestable: untestable, Why: why,
		})
		switch {
		case ok:
			res.passed = append(res.passed, name)
			if group == "Readers" {
				res.readers++
			}
		case untestable:
			// Not evidence — a synthetic crawler cannot hold a vendor IP.
		default:
			res.failed = append(res.failed, fmt.Sprintf("%s→%d", name, rec.status))
		}
	}
	for _, c := range canaryReaders {
		probe(c.name, "Readers", c.ua, true)
	}
	for _, c := range canaryCrawlers {
		probe(c.name, "Crawlers", c.ua, false)
	}
	return res
}

// canaryCache memoises the self-test. runShieldCanary drives EIGHT synthetic
// requests through the full middleware (classification, fingerprint hashing, a
// signature-cache lookup each), which is fine on demand but must not be paid on
// every render of the VayuShield page — that is a page an operator reloads
// repeatedly while tuning, and the cost showed up as console lag. The verdict
// only changes when the settings change, so a short TTL is ample; the panel's
// Refresh button calls the fragment endpoint, which forces a fresh run.
var (
	canaryMu      sync.Mutex
	canaryResult  *shieldCanaryResult
	canaryExpires time.Time
)

const canaryTTL = 60 * time.Second

// cachedShieldCanary returns the memoised self-test, running it at most once per
// canaryTTL. Used by the page render; the explicit Refresh path bypasses it.
func (a *App) cachedShieldCanary() shieldCanaryResult {
	canaryMu.Lock()
	if canaryResult != nil && time.Now().Before(canaryExpires) {
		res := *canaryResult
		canaryMu.Unlock()
		return res
	}
	canaryMu.Unlock()

	res := a.runShieldCanary()

	canaryMu.Lock()
	canaryResult = &res
	canaryExpires = time.Now().Add(canaryTTL)
	canaryMu.Unlock()
	return res
}

// invalidateShieldCanary drops the memo so the next read re-tests. Called when
// the operator changes shield settings, since that is exactly when the verdict
// can flip.
func invalidateShieldCanary() {
	canaryMu.Lock()
	canaryResult = nil
	canaryExpires = time.Time{}
	canaryMu.Unlock()
}

// logShieldCanary runs the canary once and logs the outcome at boot.
func (a *App) logShieldCanary() {
	res := a.runShieldCanary()
	if len(res.passed) == 0 && len(res.failed) == 0 {
		return
	}
	if res.ok() {
		logging.LogInfo("vayushield", fmt.Sprintf(
			"SEO canary passed — verified crawlers served content (%v)", res.passed))
		return
	}
	logging.LogError("vayushield",
		"SEO canary FAILED — a verified crawler was not served content and may be de-indexed",
		fmt.Sprintf("challenged/blocked: %v; served: %v", res.failed, res.passed))
}

// crawlerProbesUntestable reports whether the crawler rows in a report are
// informational (identity is IP-verified, so it cannot be simulated here).
func crawlerProbesUntestable(res shieldCanaryResult) bool {
	for _, p := range res.probes {
		if p.Group == "Crawlers" && p.NotTestable {
			return true
		}
	}
	return false
}
