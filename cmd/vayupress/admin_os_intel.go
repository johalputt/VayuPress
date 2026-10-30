// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_intel.go — VayuOS intelligence surfaces (ADR-0068, Phase 6):
// a native SEO dashboard and a privacy-preserving analytics page. Both read
// only from the local DB and on-disk cache — no third-party services, matching
// VayuPress's sovereign, zero-telemetry stance.

import (
	"context"
	"fmt"
	"html"
	htmpl "html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/focus"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/span"
	"github.com/johalputt/vayupress/internal/ui"
	"github.com/johalputt/vayupress/internal/vayushield/verifiedbot"
)

// crawlersServed is each recognised search engine and AI system with the page
// requests VayuShield has SERVED it since the last restart, beside SEO's posts
// sentence and Shield's turned-away figures alike. It reads the verified-bot
// tally, counted server-side, so crawlers that never run the analytics beacon
// are here too. Its purpose is assurance: proof the shield lets crawlers
// index the site rather than blocking them.
func (a *App) crawlersServed(title string) htmpl.HTML {
	var stats []verifiedbot.VendorStat
	if a.verifiedBots != nil {
		stats = a.verifiedBots.Stats()
	}
	if len(stats) == 0 {
		return ui.Section(title, "Since the last restart",
			`<p class="settings-row-hint">None has visited yet. Googlebot, Bingbot, GPTBot, ClaudeBot and the others appear here as they crawl.</p>`)
	}
	rows := make([]ui.Row, 0, len(stats))
	for _, s := range stats {
		kind := "Search engine"
		if s.Class == verifiedbot.ClassAIAgent {
			kind = "AI system"
		}
		rows = append(rows, ui.Row{Label: s.Name, Hint: kind,
			Control: `<span class="sa-row__count">` + htmpl.HTML(osGroupInt(int(s.Count))) + ` served</span>`})
	}
	return ui.Section(title, "Since the last restart", ui.Rows(rows...))
}

// seoPostsSentence says the readiness figures as one sentence, naming only the
// gaps there are.
func seoPostsSentence(total, healthy, noTitle, thin int) string {
	if total == 0 {
		return "No posts yet."
	}
	s := strconv.Itoa(healthy) + " of " + countOf(total, "post") + " ready for search"
	var gaps []string
	if noTitle > 0 {
		gaps = append(gaps, strconv.Itoa(noTitle)+" "+ternary(noTitle == 1, "needs", "need")+" a title")
	}
	if thin > 0 {
		gaps = append(gaps, strconv.Itoa(thin)+" "+ternary(thin == 1, "is", "are")+" under 300 words")
	}
	if len(gaps) > 0 {
		s += "; " + strings.Join(gaps, ", ")
	}
	return s + "."
}

// seoState is the page's state beside its title: the worst the checks found.
func seoState(checks []seoCheck) htmpl.HTML {
	var failing, warn int
	for _, c := range checks {
		switch {
		case c.OK:
		case c.Warn:
			warn++
		default:
			failing++
		}
	}
	switch {
	case failing > 0:
		return ui.State("danger", countOf(failing, "check")+" failing")
	case warn > 0:
		return ui.State("warn", countOf(warn, "check")+" to look at")
	}
	return ui.State("ok", "Every check passes")
}

// seoIndexNow is the instant-indexing row: whether a usable key is set, where
// its verification file is, and the one-click connect-and-verify.
func (a *App) seoIndexNow() htmpl.HTML {
	const hint = "Tells Bing, Yandex and the other participating engines the moment a post is published or changed. One click creates the key, hosts its file at the site's root and verifies it."
	key := a.indexNowKey()
	verify := func(label string) htmpl.HTML {
		return `<button type="button" class="btn btn--sm" data-indexnow-test>` + ui.Text(label) + `</button>`
	}
	var state, control htmpl.HTML
	rows := []ui.Row{}
	switch {
	case config.Cfg.OnionMode:
		state = ui.State("neutral", "Off in Tor mode")
	case key == "":
		state, control = ui.State("neutral", "Not connected"), verify("Connect and verify")
	case !validIndexNowKey(key):
		state, control = ui.State("danger", "The key is not valid"), verify("Connect and verify")
		rows = append(rows, ui.Row{Label: "Key", Hint: "8 to 128 characters of a–z, A–Z, 0–9 or hyphen; IndexNow refuses the current one."})
	default:
		state, control = ui.State("ok", "Connected"), verify("Verify again")
		link := "/" + key + ".txt"
		rows = append(rows, ui.Row{Label: "Verification file",
			Control: `<a class="mono" href="` + htmpl.HTML(link) + `" target="_blank" rel="noopener">https://` + ui.Text(config.Cfg.Domain) + htmpl.HTML(link) + `</a>`})
	}
	rows = append([]ui.Row{{Label: "IndexNow", Hint: hint, Control: state + " " + control}}, rows...)
	return ui.Section("Instant indexing", "", ui.Rows(rows...)+`<div class="seo-status mt-3" data-indexnow-result hidden></div>`)
}

// handleOSSEONative renders SEO as an overview: the checks' state beside the
// title, the posts' readiness as a sentence beside the crawlers, then the
// checks, the files crawlers read and instant indexing, each as rows. All of
// it is computed live from the DB and the cache.
func (a *App) handleOSSEONative(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	file := func(name string) (bool, time.Time) {
		fi, err := os.Stat(filepath.Join(config.Cfg.CacheDir, name))
		if err != nil {
			return false, time.Time{}
		}
		return true, fi.ModTime()
	}
	smOK, smAt := file("sitemap.xml")
	feedOK, feedAt := file("feed.xml")
	robotsOK, robotsAt := file("robots.txt")

	var sitemapAge time.Duration
	if smOK {
		sitemapAge = time.Since(smAt)
	}
	robotsBody := ""
	if b, err := os.ReadFile(filepath.Join(config.Cfg.CacheDir, "robots.txt")); err == nil {
		robotsBody = string(b)
	}
	headRobots := ""
	if a.siteSettings != nil {
		headRobots = a.siteSettings.Get(r.Context(), settings.ForPrimary(), settings.KeyHeadRobots)
	}
	checks := evaluateSEOHealth(smOK, sitemapAge, robotsOK, robotsBody, headRobots, config.Cfg.Domain)
	checkRows := make([]ui.Row, 0, len(checks))
	for _, c := range checks {
		state := ui.State("ok", "Passes")
		if !c.OK && c.Warn {
			state = ui.State("warn", "Look at this")
		} else if !c.OK {
			state = ui.State("danger", "Failing")
		}
		checkRows = append(checkRows, ui.Row{Label: c.Label, Hint: c.Detail, Control: state})
	}

	fileRow := func(label, hint string, ok bool, at time.Time) ui.Row {
		if !ok {
			return ui.Row{Label: label, Hint: hint, Control: ui.State("warn", "Not made yet")}
		}
		return ui.Row{Label: label, Hint: hint, Control: ui.State("ok", "Made "+config.FormatSiteStamp(at))}
	}

	// Per-post readiness is counted in the background and cached: on a site of
	// hundreds of thousands of posts the scan would outlast the request, so the
	// page never waits for it.
	stats, ready := seoStatsSnapshot()
	sentence := "Counting posts in the background; reload in a few seconds."
	if ready {
		sentence = seoPostsSentence(stats.total, stats.healthy, stats.noTitle, stats.thin)
	}

	page := ui.Overview(ui.OverviewPage{Title: "SEO", State: seoState(checks),
		Actions: `<button type="button" class="btn btn--primary btn--sm" data-seo-regenerate>Regenerate files</button>`},
		ui.Band{Title: "Posts", Hint: "Ready for search", Sentence: sentence, Aside: a.crawlersServed("Crawlers")},
		ui.Section("Checks", "What a crawler meets", ui.Rows(checkRows...)),
		ui.Section("Files", "What crawlers read first", ui.Rows(
			fileRow("Sitemap", "sitemap.xml, named in robots.txt", smOK, smAt),
			fileRow("RSS feed", "feed.xml", feedOK, feedAt),
			fileRow("robots.txt", "What crawlers may read", robotsOK, robotsAt),
		)+`<div class="seo-status mt-3" data-seo-status hidden></div>`),
		a.seoIndexNow())

	body := string(page) + `
<script nonce="` + nonce + `" src="/os/static/js/admin-os-intel.js?v=` + assetVer("js/admin-os-intel.js") + `"></script>
<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
// Delegated, and looked up per click: Regenerate re-renders this page in place
// (vpRefresh), which replaces the button a direct listener would be bound to.
document.addEventListener('click',function(e){
  var btn=e.target&&e.target.closest?e.target.closest('[data-indexnow-test]'):null;
  var out=document.querySelector('[data-indexnow-result]');
  if(!btn||!out)return;
  btn.disabled=true;out.hidden=false;out.className='seo-status mt-3';out.textContent='Testing IndexNow…';
  fetch('/os/api/seo/indexnow-test',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:'{}'})
   .then(function(r){return r.json();})
   .then(function(j){
     btn.disabled=false;
     var ok=j&&j.ok;
     out.className='seo-status mt-3 '+(ok?'editor-status--ok':'editor-status--warn');
     out.textContent=(ok?'✓ ':'✕ ')+((j&&j.detail)||(j&&j.error&&j.error.message)||'Test failed.');
   })
   .catch(function(){btn.disabled=false;out.className='seo-status mt-3 editor-status--danger';out.textContent='✕ Network error running the test.';});
});
})();
</script>`

	writeOSHTML(w, r, adminOSLayout(nonce, "SEO", "seo", cfg, htmpl.HTML(body)))
}

// seoCheck is one SEO health finding. OK = pass; Warn = advisory; otherwise it
// is a hard problem (red). Detail explains the finding and the fix.
type seoCheck struct {
	Label  string
	OK     bool
	Warn   bool
	Detail string
}

// evaluateSEOHealth runs the actionable SEO checks against the current site
// state. It is a pure function (no I/O) so it is straightforward to unit-test;
// the handler gathers the inputs and renders the results.
func evaluateSEOHealth(sitemapOK bool, sitemapAge time.Duration, robotsOK bool, robotsBody, headRobots, domain string) []seoCheck {
	var out []seoCheck

	out = append(out, seoCheck{
		Label: "Sitemap generated", OK: sitemapOK,
		Detail: ternary(sitemapOK, "sitemap.xml is present and submitted to search engines via robots.txt.",
			"sitemap.xml has not been generated yet — click “Regenerate artefacts”."),
	})
	if sitemapOK {
		stale := sitemapAge > 7*24*time.Hour
		out = append(out, seoCheck{
			Label: "Sitemap fresh", OK: !stale, Warn: stale,
			Detail: ternary(stale, "sitemap.xml is over a week old; regenerate so new posts are discoverable.",
				"sitemap.xml was refreshed within the last week."),
		})
	}

	out = append(out, seoCheck{
		Label: "robots.txt present", OK: robotsOK,
		Detail: ternary(robotsOK, "robots.txt is served and points crawlers at your sitemap.",
			"robots.txt is missing — regenerate artefacts to create it."),
	})

	// A site-wide "Disallow: /" blocks all crawling — a critical, easy-to-miss
	// mistake. Detect it on any user-agent block.
	blocked := false
	for _, line := range strings.Split(robotsBody, "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "disallow: /") {
			blocked = true
			break
		}
	}
	out = append(out, seoCheck{
		Label: "Crawling allowed", OK: !blocked,
		Detail: ternary(blocked, "robots.txt contains “Disallow: /”, which blocks search engines from your whole site.",
			"robots.txt does not block crawling of the site."),
	})

	noindex := strings.Contains(strings.ToLower(headRobots), "noindex")
	out = append(out, seoCheck{
		Label: "Indexing enabled", OK: !noindex,
		Detail: ternary(noindex, "The site-wide robots meta is set to noindex (Theme Studio → Head & SEO), so pages won’t be indexed.",
			"The site-wide robots directive allows indexing."),
	})

	d := strings.TrimSpace(domain)
	badDomain := d == "" || strings.HasPrefix(d, "localhost") || strings.HasPrefix(d, "127.0.0.1")
	out = append(out, seoCheck{
		Label: "Canonical domain set", OK: !badDomain, Warn: badDomain,
		Detail: ternary(badDomain, "The site domain looks unset or local; canonical URLs and share links need a real domain.",
			"Canonical URLs use "+d+"."),
	})

	return out
}

// ternary returns a when cond, else b — a tiny readability helper for the checks.
func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// ── SEO stats cache ──────────────────────────────────────────────────────────
//
// Counting "thin"/"missing title" posts requires reading every article body to
// measure its length. On a 234k-post site that is far too slow to run on the
// request path (it would time out behind nginx and return 502). So we compute
// it with a single aggregate query in a background goroutine, cache the result
// with a TTL, and refresh it lazily when the SEO page is viewed and the cache
// is stale. The page itself always renders instantly.

type seoStats struct {
	total, thin, noTitle, healthy int
	computedAt                    time.Time
	ready                         bool
}

var (
	seoStatsMu        sync.Mutex
	seoStatsCache     seoStats
	seoStatsComputing bool
	seoStatsLastTry   time.Time
)

const (
	seoStatsTTL      = 15 * time.Minute // re-use a fresh result this long
	seoStatsRetryGap = 1 * time.Minute  // throttle re-attempts after a miss/failure
)

// seoStatsSnapshot returns the cached tallies and whether a real computation has
// completed. It kicks off a background refresh when the cache is missing/stale
// and one isn't already running. It never blocks on the heavy scan.
func seoStatsSnapshot() (seoStats, bool) {
	seoStatsMu.Lock()
	defer seoStatsMu.Unlock()
	fresh := seoStatsCache.ready && time.Since(seoStatsCache.computedAt) < seoStatsTTL
	if !fresh && !seoStatsComputing && time.Since(seoStatsLastTry) > seoStatsRetryGap {
		seoStatsComputing = true
		seoStatsLastTry = time.Now()
		go computeSEOStats()
	}
	return seoStatsCache, seoStatsCache.ready
}

// computeSEOStats runs the (potentially slow) aggregate scan with a hard timeout
// and caches the result. Runs off the request path.
func computeSEOStats() {
	defer func() {
		seoStatsMu.Lock()
		seoStatsComputing = false
		seoStatsMu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var total, noTitle, thin int
	// Read pool, not the writer connection: this scans content across the whole
	// catalog (which can be hundreds of thousands of posts) and must never block
	// writes/sessions. It is already cached + background-computed + time-limited.
	err := dbpkg.Reader().QueryRowContext(ctx,
		`SELECT COUNT(*),
		        COALESCE(SUM(CASE WHEN TRIM(COALESCE(title,''))='' THEN 1 ELSE 0 END),0),
		        COALESCE(SUM(CASE WHEN TRIM(COALESCE(title,''))<>'' AND LENGTH(COALESCE(content,''))<1500 THEN 1 ELSE 0 END),0)
		 FROM articles`).Scan(&total, &noTitle, &thin)
	if err != nil {
		return // leave previous cache intact; retry is throttled by seoStatsRetryGap
	}
	healthy := total - thin - noTitle
	if healthy < 0 {
		healthy = 0
	}
	seoStatsMu.Lock()
	seoStatsCache = seoStats{total: total, thin: thin, noTitle: noTitle, healthy: healthy, computedAt: time.Now(), ready: true}
	seoStatsMu.Unlock()
}

// platformAnalyticsTitle is the aggregate's name: it is every site's, and says
// so before any figure is read.
const platformAnalyticsTitle = "Every site's analytics"

// handleOSPlatformAnalytics is Platform › Analytics: every site this install
// serves, added up, for an administrator (the "platform" area is admin-only).
// It is the sites' own page, given every site, with what each adds first.
func (a *App) handleOSPlatformAnalytics(w http.ResponseWriter, r *http.Request) {
	a.writeSiteAnalytics(w, r, platformView, "every site", platformAnalyticsTitle, "platform")
}

// analyticsScopeNote says whose traffic these figures are. Every reader here
// is unscoped, so on an install serving several sites each figure adds them
// all up; the note says so and points at where one site's own figures are. On
// a single-site install it says nothing: a caveat that can never apply there
// is one operators learn to skip, the ones that matter included.
func (a *App) analyticsScopeNote(ctx context.Context) (int, ui.HTML) {
	if a.domains == nil {
		return 0, ""
	}
	list, err := a.domains.List(ctx)
	if err != nil || len(list) < 2 {
		return len(list), ""
	}
	return len(list), ui.HTML(`<p class="settings-row-hint">These figures add up all ` + strconv.Itoa(len(list)) +
		` sites this install serves. Each site's own are its page, linked under Each site.</p>`)
}

// analyticsChange says how a figure moved against the window before it, in
// words: a coloured arrow on a filled chip is the badge the page grammar
// replaced, and the words say which way is better on their own. A window
// before with none of the thing counted has nothing to compare with.
func analyticsChange(cur, prev int) string {
	switch {
	case prev == 0:
		return ""
	case cur == prev:
		return "The same as the period before"
	}
	pct := float64(cur-prev) / float64(prev) * 100
	if pct > 0 {
		return fmt.Sprintf("Up %.0f%% on the period before", pct)
	}
	return fmt.Sprintf("Down %.0f%% on the period before", -pct)
}

// analyticsCols sets panels side by side, as many as fit.
func analyticsCols(panels ...string) string {
	return `<div class="sa-cols">` + strings.Join(panels, "") + `</div>`
}

// analyticsPanel is one titled part of a section, without a box: a section
// already frames it, and a box inside it is the nesting the grammar forbids.
func analyticsPanel(title, body string) string {
	return `<div><h3 class="sa-panel__title">` + html.EscapeString(title) + `</h3>` + body + `</div>`
}

// analyticsPeriodOptions defines the selectable reporting windows, in days.
var analyticsPeriodOptions = []struct {
	Days  int
	Label string
}{
	// One day is today so far, from midnight UTC, as every range is a set of
	// UTC days (docs/ANALYTICS.md): "24 hours" said what it was not.
	{1, "Today"}, {7, "7 days"}, {30, "30 days"}, {90, "90 days"},
	{180, "6 months"}, {365, "1 year"}, {730, "2 years"}, {1095, "3 years"},
}

// analyticsLabelForDays returns the human label for a whitelisted window, used
// by the background dashboard warmer (which computes the default window).
func analyticsLabelForDays(days int) string {
	for _, o := range analyticsPeriodOptions {
		if o.Days == days {
			return o.Label
		}
	}
	return strconv.Itoa(days) + " days"
}

// analyticsPeriod resolves the ?days= query param to a whitelisted window,
// returning the day count and its words. Defaults to 30 days; the maximum is
// 3 years (1095 days).
func analyticsPeriod(r *http.Request) (int, string) {
	want, _ := strconv.Atoi(r.URL.Query().Get("days"))
	for _, o := range analyticsPeriodOptions {
		if o.Days == want {
			return o.Days, periodWords(o.Days)
		}
	}
	return 30, periodWords(30)
}

// periodWords is a period as a sentence starts it ("last 30 days"), and
// analyticsPhrase as one goes on to say it ("the last 30 days"). Today is
// said as what it counts. Both are made from the day count alone, so a page
// counted on a request and the same page counted by the warmer say the same.
func periodWords(days int) string {
	if days == 1 {
		return "today, since midnight UTC"
	}
	return "last " + analyticsLabelForDays(days)
}

func analyticsPhrase(days int) string {
	if days == 1 {
		return periodWords(days)
	}
	return "the " + periodWords(days)
}

// analyticsWhen is the period after a count: "in the last 30 days", and
// "today, since midnight UTC", which takes no "in".
func analyticsWhen(days int) string {
	if days == 1 {
		return periodWords(days)
	}
	return "in " + analyticsPhrase(days)
}

// analyticsSpan is the period an Analytics page counts: the last days
// days, or the range of days chosen with ?from=&to= (from and to set, UTC,
// both counted), which the request's context carries to every reader
// (span.With).
type analyticsSpan struct {
	days     int
	from, to time.Time
	// focus is the readers the page is narrowed to (analytics_focus.go),
	// carried with the period because every link that keeps one keeps both.
	focus []analytics.ExploreFilter
}

func (p analyticsSpan) ranged() bool { return !p.from.IsZero() }

// rangeDay is a day as the range is said ("1 Sep 2026").
func rangeDay(t time.Time) string { return t.Format("2 Jan 2006") }

// label is the period as the chooser's button names it.
func (p analyticsSpan) label() string {
	if !p.ranged() {
		if p.days == 1 {
			return "Today"
		}
		return "Last " + analyticsLabelForDays(p.days)
	}
	if p.from.Equal(p.to) {
		return rangeDay(p.from)
	}
	return rangeDay(p.from) + " – " + rangeDay(p.to)
}

// phrase is the period as a sentence goes on to say it ("the last 30
// days", "1 Sep 2026 to 30 Sep 2026"), and when it after a count ("in the
// last 30 days", "from 1 Sep 2026 to 30 Sep 2026").
func (p analyticsSpan) phrase() string {
	if !p.ranged() {
		return analyticsPhrase(p.days)
	}
	if p.from.Equal(p.to) {
		return rangeDay(p.from)
	}
	return rangeDay(p.from) + " to " + rangeDay(p.to)
}

func (p analyticsSpan) when() string {
	if !p.ranged() {
		return analyticsWhen(p.days)
	}
	if p.from.Equal(p.to) {
		return "on " + rangeDay(p.from)
	}
	return "from " + rangeDay(p.from) + " to " + rangeDay(p.to)
}

// set writes the period into q: the range, or the days when they are not
// the default 30.
func (p analyticsSpan) set(q url.Values) {
	for _, f := range p.focus {
		q.Add(focusParam, encodeFocus(f))
	}
	if p.ranged() {
		q.Set("from", p.from.Format("2006-01-02"))
		q.Set("to", p.to.Format("2006-01-02"))
		return
	}
	if p.days != 30 {
		q.Set("days", strconv.Itoa(p.days))
	}
}

// key is the period, and the filters, in a cache key.
func (p analyticsSpan) key() string {
	k := strconv.Itoa(p.days)
	if p.ranged() {
		k = p.from.Format("2006-01-02") + "~" + p.to.Format("2006-01-02")
	}
	for _, f := range p.focus {
		k += "|" + encodeFocus(f)
	}
	return k
}

// analyticsSpanOf is the period r asks for. A range is taken when it is two
// days, the first not after the last, the last not after today, and no
// longer than the longest period offered (three years); anything else is
// the ?days= window, as before.
func analyticsSpanOf(r *http.Request) analyticsSpan {
	days, _ := analyticsPeriod(r)
	q := r.URL.Query()
	fs := focusOf(q)
	from, err1 := time.Parse("2006-01-02", q.Get("from"))
	to, err2 := time.Parse("2006-01-02", q.Get("to"))
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if err1 != nil || err2 != nil || to.Before(from) || to.After(today) || to.Sub(from) >= 1095*24*time.Hour {
		return analyticsSpan{days: days, focus: fs}
	}
	return analyticsSpan{days: int(to.Sub(from).Hours()/24) + 1, from: from, to: to, focus: fs}
}

// spanOfCtx is the period a render counts, ctx's range or days, and the
// readers ctx is narrowed to.
func spanOfCtx(ctx context.Context, days int) analyticsSpan {
	fs := focus.Of(ctx)
	if r, ok := span.Of(ctx); ok {
		return analyticsSpan{days: span.Length(ctx, days), from: r.From, to: r.To, focus: fs}
	}
	return analyticsSpan{days: days, focus: fs}
}

// scoped is ctx carrying p: its range, when it is one, and its readers.
func (a *App) scoped(ctx context.Context, p analyticsSpan) context.Context {
	if p.ranged() {
		ctx = span.With(ctx, span.Range{From: p.from, To: p.to})
	}
	return a.narrowed(ctx, p.focus)
}

// osPeriodSelector is the period chooser: one button naming the period, its
// menu the others, each a link (GET, no script) that keeps the view, and a
// range of the operator's own, chosen in a sheet. Eight buttons in a row,
// each the weight of Explore and Export, were the loudest thing on the page.
func osPeriodSelector(base, view string, p analyticsSpan) (menu, sheet ui.HTML) {
	var items strings.Builder
	for _, o := range analyticsPeriodOptions {
		label := o.Label
		if o.Days > 1 {
			label = "Last " + label
		}
		on := ""
		if !p.ranged() && o.Days == p.days {
			on = ` aria-current="true"`
		}
		items.WriteString(`<a class="sa-menu__item" role="menuitem" href="` + esc(analyticsHref(base, view, analyticsSpan{days: o.Days, focus: p.focus})) + `"` + on + `>` + esc(label) + `</a>`)
	}
	on := ""
	if p.ranged() {
		on = ` aria-current="true"`
	}
	items.WriteString(`<button type="button" class="sa-menu__item" role="menuitem" data-sheet="analytics-range"` + on + `>Choose dates…</button>`)
	// data-period: the analytics script marks the chooser as loading while the
	// chosen range is fetched.
	menu = ui.HTML(`<details class="sa-pop" data-period><summary class="btn btn--sm">` + esc(p.label()) + `</summary>` +
		`<div class="sa-pop__panel sa-menu" role="menu">` + items.String() + `</div></details>`)

	today := time.Now().UTC().Format("2006-01-02")
	from, to := time.Now().UTC().AddDate(0, 0, -29).Format("2006-01-02"), today
	if p.ranged() {
		from, to = p.from.Format("2006-01-02"), p.to.Format("2006-01-02")
	}
	hidden := ""
	if view != "overview" {
		hidden = `<input type="hidden" name="view" value="` + esc(view) + `">`
	}
	for _, f := range p.focus {
		hidden += `<input type="hidden" name="` + focusParam + `" value="` + esc(encodeFocus(f)) + `">`
	}
	sheet = ui.Sheet("analytics-range", "Choose dates", ui.HTML(`<form method="get" action="`+esc(base)+`">`+hidden+
		`<div class="field"><label class="field-label" for="range-from">From</label><input id="range-from" class="input" type="date" name="from" value="`+from+`" max="`+today+`" required></div>`+
		`<div class="field"><label class="field-label" for="range-to">To</label><input id="range-to" class="input" type="date" name="to" value="`+to+`" max="`+today+`" required></div>`+
		`<p class="settings-row-hint">Whole days in UTC, both counted, up to three years.</p>`+
		`<div class="mt-3 sa-list__sheet-actions"><button class="btn btn--primary btn--sm" type="submit">Show these dates</button></div></form>`))
	return menu, sheet
}

// osLiveCompact is Right now on the overview: the count, the readers over
// the last half hour, and the three pages most read now, with the way to the
// Live view and its lists. Filled by admin-os-intel.js from src, as the card.
func osLiveCompact(src, liveHref string) string {
	return `<div class="vm-liveview vm-liveview--compact" data-live data-live-src="` + esc(src) + `">
  <div class="stat-card"><div class="stat-card__value" data-live-count>—</div>
    <div class="stat-card__note"><span data-live-noun>visitors</span> in the last <span data-live-window>5</span> minutes<span data-live-updated></span></div></div>
  <svg class="vm-live-spark" viewBox="0 0 300 40" preserveAspectRatio="none" aria-hidden="true"><polyline data-live-spark points=""></polyline></svg>
  <div class="vp-bars" data-live-pages data-live-max="3"><p class="table-empty">Counting…</p></div>
  <a class="sa-more" href="` + esc(liveHref) + `">Open Live →</a>
</div>`
}

// osLiveCard is who is reading the site now: their count, as a figure, the
// pages they are on, where they came from, the countries (with their flags)
// and cities they are in and what they read on, filled every 10 s from src by
// admin-os-intel.js. A count in ink, not an accent number beside a LIVE pill
// and pulsing rings: it is the same kind of figure as the band's.
func osLiveCard(src, door string) string {
	panel := func(title, attr, dim string) string {
		return `<div><h3 class="sa-panel__title">` + title + `</h3><div class="vp-bars" ` + attr + ` data-live-dim="` + dim + `"><p class="table-empty">Counting…</p></div></div>`
	}
	return `<div class="vm-liveview" data-live data-live-src="` + esc(src) + `" data-live-door="` + esc(door) + `">
  <div class="vm-live-head"><div class="stat-card"><div class="stat-card__label">Reading now</div><div class="stat-card__value" data-live-count>—</div>
    <div class="stat-card__note"><span data-live-noun>visitors</span> in the last <span data-live-window>5</span> minutes<span data-live-updated></span></div></div>
  <svg class="vm-live-spark" viewBox="0 0 300 40" preserveAspectRatio="none" aria-hidden="true"><polyline data-live-spark points=""></polyline></svg></div>
  <div class="sa-cols sa-cols--3">` +
		panel("Pages", "data-live-pages", "path") + panel("How they arrived", "data-live-channels", "visit_channel") + panel("Referrers", "data-live-referrers", "visit_referrer") +
		panel("Countries", "data-live-countries", "country") + panel("Cities", "data-live-cities", "city") + panel("Devices", "data-live-devices", "device") +
		panel("Browsers", "data-live-browsers", "browser") + `</div>
</div>`
}

// osGeoSection is where readers are: countries and the continents they make
// up, which resolve with no proxy, then regions and cities, which need the
// proxy's location headers or the downloaded city table. What is missing is
// said once, and how to get regions and cities is in How this works, not a
// box on the page.
func osGeoSection(countries, regions, cities []analytics.AudienceStat, door func(dim, value string) string) string {
	setup := string(ui.Explain(ui.HTML(`<p>A country comes from the proxy in front of the install when it names one, and otherwise from the table compiled into VayuPress, looked up on this server. A region or a city comes from the proxy's headers: with Cloudflare, turn on Rules › Settings › Add visitor location headers (it adds <code>cf-region</code> and <code>cf-ipcity</code>); CloudFront, Vercel and Fastly headers are read too, and <code>X-Geo-Region</code> and <code>X-Geo-City</code> from any proxy. With no proxy that names cities, an administrator can turn on Settings › Advanced › Visitor cities in the install's console: the install downloads DB-IP's city table and looks each address up on this server.</p>`)))
	if len(countries) == 0 && len(regions) == 0 && len(cities) == 0 {
		return `<p class="table-empty">No location recorded yet.</p>` + setup
	}
	countryBars := make([]osChartBar, 0, len(countries))
	contAgg := map[string]int{}
	for _, c := range countries {
		name := countryName(c.Label)
		if name == "" {
			name = c.Label
		}
		countryBars = append(countryBars, osChartBar{Label: name, Value: c.Count, Flag: c.Label, Href: door("country", c.Label)})
		if cont := continentName(c.Label); cont != "" {
			contAgg[cont] += c.Count
		}
	}
	continentBars := make([]osChartBar, 0, len(contAgg))
	for name, n := range contAgg {
		continentBars = append(continentBars, osChartBar{Label: name, Value: n})
	}
	sort.SliceStable(continentBars, func(i, j int) bool {
		if continentBars[i].Value != continentBars[j].Value {
			return continentBars[i].Value > continentBars[j].Value
		}
		return continentBars[i].Label < continentBars[j].Label
	})
	out := analyticsCols(
		analyticsPanel("Countries", osBarList(countryBars, osShareOfListed(), "No country recorded yet.")),
		analyticsPanel("Continents", osBarList(continentBars, osShareOfListed(), "No continent recorded yet.")))
	if len(regions) == 0 && len(cities) == 0 {
		return out + `<p class="table-empty">No region or city recorded yet.</p>` + setup
	}
	return out + analyticsCols(
		analyticsPanel("Regions", osBarList(doorBars(regions, "region", door), osShareOfListed(), "No region recorded yet.")),
		analyticsPanel("Cities", osBarList(doorBars(cities, "city", door), osShareOfListed(), "No city recorded yet.")))
}

// osGoalsSectionOf is the Goals section for v: its goals counted over its
// visits, with a create form, whose form and Delete buttons write to v's
// goals API (the install's, or a hosted site's).
func (a *App) osGoalsSectionOf(ctx context.Context, v analyticsView, days int) string {
	base := v.goalsAPI()
	results, _ := a.analytics.GoalResultsOver(ctx, v.defs, v.scope, days)
	rows := `<tr><td colspan="6" class="muted">` + noGoalsYet + `</td></tr>`
	if len(results) > 0 {
		rows = ""
		for _, g := range results {
			rows += `<tr><td class="post-row__name">` + html.EscapeString(g.Name) + `</td>` +
				`<td>` + html.EscapeString(goalKindLabel(g.Kind)) + `</td>` +
				`<td class="muted">` + html.EscapeString(g.Target) + `</td>` +
				`<td>` + strconv.Itoa(g.Completions) + ` <span class="muted text-xs">(` + strconv.Itoa(g.UniqueVisitors) + ` visitors)</span></td>` +
				`<td>` + fmt.Sprintf("%.1f%%", g.ConversionRate) + `</td>` +
				`<td><button class="btn btn--ghost btn--sm" data-goal-delete="` + html.EscapeString(g.ID) + `">Delete</button></td></tr>`
		}
	}
	// Adding a goal is a form, so it rises in a sheet.
	sheet := ui.Sheet("goal-new", "New goal", ui.HTML(`<form data-goal-form>
  <div class="field"><label class="field-label" for="goal-name">Name</label>
    <input id="goal-name" class="input" type="text" data-goal-name placeholder="Newsletter signup" required></div>
  <div class="field"><label class="field-label" for="goal-kind">Reached by</label>
    <select id="goal-kind" class="select" data-goal-kind><option value="path">Viewing a page</option><option value="event">A custom event</option></select></div>
  <div class="field"><label class="field-label" for="goal-target">Page or event</label>
    <input id="goal-target" class="input" type="text" data-goal-target placeholder="/thank-you  or  signup" required></div>
  <div class="mt-3 sa-list__sheet-actions"><button class="btn btn--primary btn--sm" type="submit">Add goal</button></div>
</form>`))
	return string(ui.Section("Goals", countOf(len(results), "goal")+", the share of visitors who reach each", ui.HTML(`<div data-goals data-goal-base="`+esc(base)+`">
  <button type="button" class="btn btn--ghost btn--sm" data-sheet="goal-new">`+saIcon("plus")+` New goal</button>
  <div class="table-wrap"><table class="table">
    <thead><tr><th>Goal</th><th>Reached by</th><th>Target</th><th>Completions</th><th>Conversion</th><th></th></tr></thead>
    <tbody>`+rows+`</tbody>
  </table></div></div>`))) + string(sheet)
}

// osSearchesSection is what readers searched scope's site for over the
// window: the most searched queries, and those that found nothing, which is
// where a site is missing what its readers come for.
func (a *App) osSearchesSection(ctx context.Context, scope string, days int, door func(dim, value string) string) string {
	stats, _ := a.analytics.SearchesScoped(ctx, scope, days, 20)
	if len(stats) == 0 {
		return string(ui.Section("Searches", "What readers looked for with the site's search",
			ui.HTML(`<p class="table-empty">No searches yet.</p>`+string(ui.Explain(`<p>Searches made from the site's search page show here; a query that looks like an email address or a long number is never kept.</p>`)))))
	}
	// Bars, as every other top list here: a four-column table did not fit
	// its half of the section on a small laptop and scrolled sideways. The
	// visitor counts are in the API's searches report.
	var most, empty []osChartBar
	for _, st := range stats {
		// A search is an event named by its query: its row is the way into
		// the visits that searched for it.
		most = append(most, osChartBar{Label: st.Query, Value: st.Searches, Href: door("event", st.Query)})
		if st.Empty > 0 {
			empty = append(empty, osChartBar{Label: st.Query, Value: st.Empty, Href: door("event", st.Query)})
		}
	}
	body := analyticsCols(
		analyticsPanel("Most searched", osBarList(most, osShareOfListed(), "")),
		analyticsPanel("Found nothing", osBarList(empty, osShareHidden(), "Every search found something.")))
	if next, err := a.analytics.SearchFollowUpsScoped(ctx, scope, days, 10); err == nil && next.Searches > 0 {
		left := next.Searches - next.Opened - next.Refined
		// Set as counts beside their labels, not a sentence: "1 were
		// followed" is the grammar a count in prose gets wrong.
		lead := `<p class="settings-row-hint">What followed a search in its visit: a page, ` + strconv.Itoa(next.Opened) + ` (` + osShareLabel(next.Opened, next.Searches) +
			`); another search, ` + strconv.Itoa(next.Refined) + ` (` + osShareLabel(next.Refined, next.Searches) + `); nothing, ` + strconv.Itoa(left) + ` (` + osShareLabel(left, next.Searches) +
			`). A page opened next is usually, not always, a result.</p>`
		pages := make([]osChartBar, 0, len(next.Pages))
		for _, p := range next.Pages {
			pages = append(pages, osChartBar{Label: prettyPathText(p.Label), Value: p.Count, Hint: p.Label, Href: door("path", p.Label)})
		}
		again := make([]osChartBar, 0, len(next.Refinements))
		for _, rf := range next.Refinements {
			again = append(again, osChartBar{Label: rf.From + " → " + rf.To, Value: rf.Count})
		}
		body += lead + analyticsCols(
			analyticsPanel("Opened after searching", osBarList(pages, osShareOf(next.Opened), "No search has been followed by a page yet.")),
			analyticsPanel("Searched again as", osBarList(again, osShareOf(next.Refined), "No search has been tried again in other words yet.")))
	}
	return string(ui.Section("Searches", "What readers looked for with the site's search", ui.HTML(body)))
}

// noGoalsYet is the goals table's empty row; the script writes the same words
// when the last goal is deleted.
const noGoalsYet = `No goals yet. Add one with New goal: a page such as /thank-you, or a custom event such as signup.`

// goalKindLabel names how a goal is reached, in words.
func goalKindLabel(kind string) string {
	if kind == "event" {
		return "A custom event"
	}
	return "Viewing a page"
}

// osJourneySection renders the top page-to-page transitions (visitor journey).
func (a *App) osJourneySection(ctx context.Context, scope string, days int) string {
	// Every flow is read, as the visits' entries and exits outnumber the
	// rest and are already the landing and exit pages; ten shown.
	flows, _ := a.analytics.PathFlowsScoped(ctx, scope, days, 1000)
	flows = analytics.BetweenPages(flows)
	bars := make([]osChartBar, 0, 10)
	for i, f := range flows {
		if i == 10 {
			break
		}
		bars = append(bars, osChartBar{Label: prettyPathText(f.From) + " → " + prettyPathText(f.To), Value: f.Count})
	}
	return string(ui.Section("Journeys", countOf(len(flows), "way")+" from one page to another",
		ui.HTML(osBarList(bars, osShareHidden(), "No visit went from one page to another yet."))))
}

// osEachSiteSection is what each site adds to Platform's figures: its page
// views and visitors over the period, its share of the views, and the way to
// its own page. A site removed from the registry keeps its rows until the
// purge, and is named as removed rather than dropped from the sum.
func (a *App) osEachSiteSection(ctx context.Context, days int) string {
	p := spanOfCtx(ctx, days)
	figs, err := a.analytics.BySite(ctx, days)
	if err != nil {
		return string(ui.Section("Each site", "", ui.Callout("danger", ui.Text("Not counted: "+err.Error()))))
	}
	hosts := a.siteHosts(ctx)
	total := 0
	for _, f := range figs {
		total += f.Views
	}
	rows := make([][]ui.HTML, 0, len(figs))
	for _, f := range figs {
		name := ui.Text("A removed site")
		if h, ok := hosts[f.Site]; ok {
			href := analyticsHref("/os/d/"+f.Site+"/analytics", "overview", p)
			if f.Site == "" {
				href = analyticsHref(primaryAnalyticsBase, "overview", p)
			}
			name = ui.HTML(`<a href="` + html.EscapeString(href) + `">` + html.EscapeString(h) + `</a>`)
		}
		rows = append(rows, []ui.HTML{name, ui.Text(strconv.Itoa(f.Visitors)), ui.Text(strconv.Itoa(f.Views)), ui.HTML(osShareLabel(f.Views, total))})
	}
	return string(ui.Section("Each site", "What each adds to these figures; open one for its own",
		ui.Table([]string{"Site", "Visitors", "Page views", "Share of views"}, rows, "No visits recorded yet.")))
}

// osCampaigns is the campaigns a period's tagged links brought: their
// sources as bars, and each source, medium and campaign with its hits.
func osCampaigns(utm []analytics.UTMStat, door func(dim, value string) string) string {
	utmSourceBars := make([]osChartBar, 0, len(utm))
	utmRows := ""
	for _, u := range utm {
		src := u.Source
		if src == "" {
			src = "(direct)"
		}
		utmSourceBars = append(utmSourceBars, osChartBar{Label: src, Value: u.Count, Href: door("utm_source", u.Source)})
		campaign := html.EscapeString(u.Campaign)
		if href := door("utm_campaign", u.Campaign); href != "" {
			campaign = `<a href="` + html.EscapeString(href) + `">` + campaign + `</a>`
		}
		utmRows += `<tr><td class="post-row__name">` + html.EscapeString(src) + `</td><td>` + html.EscapeString(u.Medium) + `</td><td>` + campaign + `</td><td>` + strconv.Itoa(u.Count) + `</td></tr>`
	}
	out := `<p class="table-empty">No campaign traffic yet. Tag the links you share with <code>utm_source</code>, <code>utm_medium</code> and <code>utm_campaign</code> to see which bring visitors.</p>`
	if utmRows != "" {
		out = analyticsCols(
			analyticsPanel("Sources", osBarList(utmSourceBars, osShareHidden(), "")),
			analyticsPanel("Campaigns", `<div class="table-wrap"><table class="table"><thead><tr><th>Source</th><th>Medium</th><th>Campaign</th><th>Hits</th></tr></thead><tbody>`+utmRows+`</tbody></table></div>`))
	}
	return out
}

// osExportSheet is the page's Export button and the sheet it opens: any
// report for the period, as CSV or JSON, from href (the view's own export).
// Fourteen rows of buttons on the page became one choice in a sheet; the
// form is a GET, and the answer downloads (Content-Disposition).
func osExportSheet(href string, p analyticsSpan) (ui.HTML, ui.HTML) {
	labels := map[string]string{
		"overview": "Overview", "pages": "Top pages", "referrers": "Referrers",
		"browsers": "Browsers", "devices": "Devices", "os": "Operating systems",
		"countries": "Countries", "regions": "Regions", "cities": "Cities",
		"utm": "Campaigns", "events": "Custom events", "sessions": "Visits",
		"goals": "Goals", "journey": "Journeys",
	}
	var opts strings.Builder
	for _, rep := range analyticsExportReports {
		opts.WriteString(`<option value="` + rep + `">` + html.EscapeString(labels[rep]) + `</option>`)
	}
	btn := ui.HTML(` <button type="button" class="btn btn--sm" data-sheet="analytics-export">Export</button>`)
	sheet := ui.Sheet("analytics-export", "Export a report", ui.HTML(`<form method="get" action="`+html.EscapeString(href)+`">
  `+exportHidden(p)+`
  <div class="field"><label class="field-label" for="export-report">Report</label>
    <select id="export-report" class="select" name="report">`+opts.String()+`</select></div>
  <div class="field"><label class="field-label" for="export-format">Format</label>
    <select id="export-format" class="select" name="format"><option value="csv">CSV</option><option value="json">JSON</option></select></div>
  <div class="mt-3 sa-list__sheet-actions"><button class="btn btn--primary btn--sm" type="submit">Download</button></div>
</form>`))
	return btn, sheet
}

// exportHidden is the page's period and filters as an export form's hidden
// fields: the download is the figures the page shows.
func exportHidden(p analyticsSpan) string {
	q := url.Values{}
	q.Set("days", strconv.Itoa(p.days))
	p.set(q)
	var b strings.Builder
	for _, k := range []string{"days", "from", "to", focusParam} {
		for _, v := range q[k] {
			b.WriteString(`<input type="hidden" name="` + k + `" value="` + esc(v) + `">`)
		}
	}
	return b.String()
}

// osPrivacyNote renders the trust footer shown at the bottom of the analytics
// page, reassuring operators that nothing leaves their server.
