package main

import (
	"fmt"
	"net/http"

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

// shieldCanaryResult reports how many synthetic crawler probes were served
// content vs challenged/blocked.
type shieldCanaryResult struct {
	passed []string
	failed []string
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
	for _, c := range canaryCrawlers {
		req, err := http.NewRequest(http.MethodGet, "/", nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", c.ua)
		req.RemoteAddr = "203.0.113.1:12345"
		rec := &canaryRecorder{}
		h.ServeHTTP(rec, req)
		if rec.status == http.StatusOK {
			res.passed = append(res.passed, c.name)
		} else {
			res.failed = append(res.failed, fmt.Sprintf("%s→%d", c.name, rec.status))
		}
	}
	return res
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
