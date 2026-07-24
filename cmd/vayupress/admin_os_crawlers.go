package main

// admin_os_crawlers.go — VayuOS "Search engine & AI crawler access".
//
// A single power switch (Operations → Power & Maintenance) that, when ON, takes
// the PUBLIC site dark to automated indexers — both classic search engines and
// AI crawlers/assistants — with three reinforcing layers so it holds even
// against crawlers that ignore robots.txt:
//
//  1. robots.txt disallows everything (and names the major AI bots explicitly,
//     since several only honour their own token).
//  2. A hard 403 for any request whose User-Agent matches a known search-engine
//     or AI crawler (reusing VayuShield's compiled bot database).
//  3. X-Robots-Tag: noindex, nofollow on every remaining public response, so an
//     unknown crawler that slips past the UA filter still won't index a page.
//
// The VayuOS console, health probes and the well-known / MCP / OAuth operational
// surfaces are never affected (VayuMCP is first-party AI connect, not crawling),
// so the operator and their integrations always keep working. Humans are wholly
// unaffected — none of the three layers changes what a browser renders.

import (
	"context"
	"net/http"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/vayushield/botdb"
)

// crawlerClassifier is the compiled-in, immutable bot database shared with
// VayuShield. It is safe for concurrent reads and cheap to hold at package
// scope, so the crawler-block middleware can classify a User-Agent with no
// per-request allocation.
var crawlerClassifier = botdb.NewStaticDB()

// crawlerBlockedRobotsTxt is the robots.txt served while the crawler block is
// on. The wildcard Disallow stops every compliant crawler; the explicit AI
// blocks are belt-and-suspenders for bots that only honour their own token.
const crawlerBlockedRobotsTxt = `# Crawling disabled by the site owner (VayuOS → Power & Maintenance).
User-agent: *
Disallow: /

User-agent: GPTBot
Disallow: /

User-agent: OAI-SearchBot
Disallow: /

User-agent: ChatGPT-User
Disallow: /

User-agent: ClaudeBot
Disallow: /

User-agent: Claude-User
Disallow: /

User-agent: anthropic-ai
Disallow: /

User-agent: PerplexityBot
Disallow: /

User-agent: Google-Extended
Disallow: /

User-agent: Applebot-Extended
Disallow: /

User-agent: Bytespider
Disallow: /

User-agent: CCBot
Disallow: /

User-agent: Amazonbot
Disallow: /

User-agent: Claude-SearchBot
Disallow: /

User-agent: meta-externalagent
Disallow: /

User-agent: meta-externalfetcher
Disallow: /

User-agent: cohere-ai
Disallow: /
`

// crawlersBlocked reports whether the operator has switched the public site dark
// to search engines and AI crawlers.
func (a *App) crawlersBlocked(ctx context.Context) bool {
	return a.siteSettings != nil && a.siteSettings.Get(ctx, settings.KeyBlockCrawlers) == "on"
}

// operatorToolBots are performance testers and uptime monitors — the operator's
// OWN tooling, not indexers. The crawler "go dark" switch must never 403 them:
// blocking PageSpeed/Lighthouse breaks the operator's performance score and
// blocking uptime monitors makes their dashboards report the live site as down.
// Going dark is about search/AI indexing, not about hiding from monitoring.
var operatorToolBots = map[string]bool{
	"PageSpeed":   true,
	"GTmetrix":    true,
	"UptimeRobot": true,
	"Pingdom":     true,
	"StatusCake":  true,
	"Site24x7":    true,
}

// crawlerUABlocked reports whether ua identifies a search-engine or AI crawler
// that the block should reject. Bad bots (scrapers/scanners) are deliberately
// out of scope here — VayuShield already challenges those; this switch is
// specifically about search engines and AI systems. Operator tooling (PageSpeed,
// uptime monitors) is always exempt (R8) so going dark never breaks the
// operator's own performance scoring or uptime dashboards.
func crawlerUABlocked(ua string) bool {
	sig, ok := crawlerClassifier.MatchUA(ua)
	if !ok {
		return false
	}
	if operatorToolBots[sig.Name] {
		return false
	}
	return sig.Classification == botdb.ClassGoodBot || sig.Classification == botdb.ClassAIAgent
}

// crawlerBlockMiddleware enforces the crawler block for public requests. It is a
// near-free pass-through when the switch is off. Operational surfaces (the whole
// /os console, health probes, /.well-known, /mcp, /oauth) are never touched —
// the same exemption the maintenance gate uses — so first-party integrations and
// the operator are unaffected.
func (a *App) crawlerBlockMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.siteSettings == nil || !a.crawlersBlocked(r.Context()) || maintenancePathExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// Hard block for anything that identifies as a search-engine or AI crawler.
		if crawlerUABlocked(r.UserAgent()) {
			w.Header().Set("X-Robots-Tag", "noindex, nofollow")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("Crawling is disabled by the site owner.\n"))
			return
		}
		// Backstop for crawlers with a generic User-Agent that slipped past the
		// filter: mark every public response noindex so it is never indexed. Humans
		// ignore this header entirely.
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		next.ServeHTTP(w, r)
	})
}

// handleOSPowerCrawlers toggles the search-engine / AI crawler block. Admin-only
// and CSRF-protected (enforced by the route middleware).
func (a *App) handleOSPowerCrawlers(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrator access required", "")
		return
	}
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "settings unavailable", "")
		return
	}
	var body struct {
		Block *bool `json:"block"`
	}
	if err := readCappedJSON(w, r, 4*1024, &body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "invalid request body", "")
		return
	}
	if body.Block == nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "missing 'block' flag", "")
		return
	}
	val := "off"
	if *body.Block {
		val = "on"
	}
	if err := a.siteSettings.SetMany(r.Context(), map[string]string{settings.KeyBlockCrawlers: val}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "write_failed", "could not save the setting", "")
		return
	}
	logging.LogWarn("crawlers", "search-engine/AI crawler block set to "+val+" by admin")
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "blocked": *body.Block})
}
