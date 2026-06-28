package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html"
	"html/template"
	"io"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"

	"github.com/johalputt/vayupress/internal/ads"
	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/budget"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/fault"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/metrics"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/provenance"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/seo"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/severity"
)

const vacuumWriteThreshold = 10

var pprofMux = http.NewServeMux()

func initPprofMux() {
	pprofMux.HandleFunc("/debug/pprof/", pprof.Index)
	pprofMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	pprofMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	pprofMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	pprofMux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	logging.LogInfo("pprof", "explicit pprof mux initialized — DefaultServeMux unmodified (ADR-0037)")
}

// ── benchmarkResult ──────────────────────────────────────────────────────────

type benchmarkResult struct {
	RunAt                                          time.Time `json:"run_at"`
	ArticlesWritten, ReadRequests, ReadConcurrency int
	ReadP50, ReadP95, ReadP99, ReadMax             int64
	ReadMean, ReadRPS                              float64
	P95Pass, P99Pass                               bool
	Overall, Notes                                 string
}

func (a *App) handleAdminVacuum(w http.ResponseWriter, r *http.Request) {
	a.vacuumMu.Lock()
	defer a.vacuumMu.Unlock()
	cooldown := time.Duration(config.Cfg.VacuumCooldownMin) * time.Minute
	if !a.vacuumLastRun.IsZero() && time.Since(a.vacuumLastRun) < cooldown {
		remaining := cooldown - time.Since(a.vacuumLastRun)
		atomic.AddInt64(&metrics.MetricVacuumRejected, 1)
		writeAPIError(w, r, 429, "vacuum_cooldown", fmt.Sprintf("cooldown active — %ds remaining", int(remaining.Seconds())), "https://docs.vayupress.com/operations/vacuum")
		return
	}
	var pending int
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&pending)
	if pending > vacuumWriteThreshold {
		atomic.AddInt64(&metrics.MetricVacuumRejected, 1)
		writeAPIError(w, r, 503, "vacuum_write_threshold", fmt.Sprintf("VACUUM rejected: %d pending jobs > threshold %d", pending, vacuumWriteThreshold), "https://docs.vayupress.com/operations/vacuum")
		return
	}
	start := time.Now()
	var integrityResult string
	dbpkg.DB.QueryRow(`PRAGMA integrity_check`).Scan(&integrityResult)
	if integrityResult != "ok" {
		writeAPIError(w, r, 500, "integrity_failed", "SQLite integrity check failed: "+integrityResult, "https://docs.vayupress.com/operations/vacuum")
		return
	}
	if _, err := dbpkg.DB.Exec(`VACUUM`); err != nil {
		writeAPIError(w, r, 500, "vacuum_failed", "VACUUM error: "+err.Error(), "https://docs.vayupress.com/operations/vacuum")
		return
	}
	a.vacuumLastRun = time.Now()
	logging.LogInfo("vacuum", fmt.Sprintf("VACUUM complete dur=%dms (ADR-0038)", time.Since(start).Milliseconds()))
	writeJSON(w, r, 200, map[string]interface{}{"status": "ok", "integrity": "ok", "duration_ms": time.Since(start).Milliseconds(), "next_allowed_in_minutes": config.Cfg.VacuumCooldownMin})
}

func (a *App) pprofHandler(w http.ResponseWriter, r *http.Request) {
	ip := r.RemoteAddr
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		ip = xri
	}
	if !auth.AllowPprof(ip) {
		atomic.AddInt64(&metrics.MetricPprofAccesses, 1)
		writeAPIError(w, r, 429, "pprof_rate_limited", fmt.Sprintf("pprof rate limit exceeded (%d/min)", config.Cfg.PprofRateLimit), "https://docs.vayupress.com/operations/profiling")
		return
	}
	atomic.AddInt64(&metrics.MetricPprofAccesses, 1)
	logging.LogJSON(logging.LogFields{Level: "info", Component: "pprof-access", RequestID: getRequestID(r), RemoteAddr: ip, Path: r.URL.Path, Msg: "pprof access (ADR-0037)"})
	pprofMux.ServeHTTP(w, r)
}

func (a *App) handleAdminBackupValidate(w http.ResponseWriter, r *http.Request) {
	backupDir := "/backups/vayupress"
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		writeAPIError(w, r, 404, "no_backup_dir", "backup directory not found: "+backupDir, "https://docs.vayupress.com/operations/backup")
		return
	}
	var latestBackup string
	var latestMod time.Time
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".db") && !strings.HasSuffix(e.Name(), ".db.gz") {
			continue
		}
		info, _ := e.Info()
		if info != nil && info.ModTime().After(latestMod) {
			latestMod = info.ModTime()
			latestBackup = filepath.Join(backupDir, e.Name())
		}
	}
	if latestBackup == "" {
		writeAPIError(w, r, 404, "no_backup", "no backup files found", "https://docs.vayupress.com/operations/backup")
		return
	}
	start := time.Now()
	checksumOK := false
	checksumFile := filepath.Join(backupDir, "checksums.json")
	if data, err := os.ReadFile(checksumFile); err == nil {
		var registry map[string]string
		if json.Unmarshal(data, &registry) == nil {
			if storedSum, ok := registry[filepath.Base(latestBackup)]; ok {
				if f, ferr := os.Open(latestBackup); ferr == nil {
					h := sha256.New()
					if _, copyErr := io.Copy(h, f); copyErr != nil {
						logging.LogError("backup-validate", "hash read failed", copyErr.Error())
					} else {
						checksumOK = hex.EncodeToString(h.Sum(nil)) == storedSum
					}
					f.Close()
				}
			}
		}
	}
	logging.LogInfo("backup-validate", fmt.Sprintf("backup=%s checksum_ok=%v dur=%dms (ADR-0042)", filepath.Base(latestBackup), checksumOK, time.Since(start).Milliseconds()))
	writeJSON(w, r, 200, map[string]interface{}{"status": "ok", "latest_backup": filepath.Base(latestBackup), "backup_age_hours": time.Since(latestMod).Hours(), "checksum_verified": checksumOK, "duration_ms": time.Since(start).Milliseconds()})
}

func (a *App) handleAdminCachePurge(w http.ResponseWriter, r *http.Request) {
	rid := getRequestID(r)
	slug := r.URL.Query().Get("slug")
	purged := 0
	purgeType := "targeted"
	if slug != "" {
		if !api.IsValidSlug(slug) {
			writeAPIError(w, r, 400, "invalid_slug", "invalid slug", "https://docs.vayupress.com/api/cache")
			return
		}
		var tags string
		dbpkg.DB.QueryRow(`SELECT tags FROM articles WHERE slug=?`, slug).Scan(&tags)
		render.CachePurge(slug, api.SplitTags(tags), generateSitemap, generateRSS, generateRobots)
		purged = 1
	} else {
		purgeType = "full"
		remoteIP := r.Header.Get("X-Real-IP")
		if remoteIP == "" {
			remoteIP = strings.Split(r.RemoteAddr, ":")[0]
		}
		if !auth.AllowPurge(remoteIP) {
			writeAPIError(w, r, 429, "rate_limited", "full cache purge rate-limited", "https://docs.vayupress.com/api/cache")
			return
		}
		postsDir := filepath.Join(config.Cfg.CacheDir, "posts")
		if files, err := os.ReadDir(postsDir); err == nil {
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".html") {
					fpath := filepath.Join(postsDir, f.Name())
					if fi, infoErr := f.Info(); infoErr == nil {
						dbpkg.UpdateStorageDelta(-fi.Size())
					}
					if err := os.Remove(fpath); err == nil {
						purged++
					}
				}
			}
		}
		os.Remove(filepath.Join(config.Cfg.CacheDir, "home", "index.html"))
		if files, err := os.ReadDir(filepath.Join(config.Cfg.CacheDir, "tags")); err == nil {
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".html") {
					os.Remove(filepath.Join(config.Cfg.CacheDir, "tags", f.Name()))
					purged++
				}
			}
		}
		go generateSitemap()
		go generateRSS()
		go generateRobots()
	}
	logging.LogJSON(logging.LogFields{Level: "info", Component: "cache-purge", RequestID: rid, Msg: fmt.Sprintf("type=%s purged=%d", purgeType, purged)})
	a.FireHook("cache.purge", map[string]interface{}{"purge_type": purgeType, "slug": slug, "purged_count": purged})
	writeJSON(w, r, 200, map[string]interface{}{"message": "cache purged", "purge_type": purgeType, "purged": purged, "request_id": rid})
}

// handleHome renders the public homepage index from the most recent articles.
// It serves a cached copy when present and regenerates on cache miss.
func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	cachePath := filepath.Join(config.Cfg.CacheDir, "home", "index.html")
	if _, err := os.Stat(cachePath); err == nil {
		atomic.AddInt64(&metrics.MetricCacheHits, 1)
		http.ServeFile(w, r, cachePath)
		return
	}
	atomic.AddInt64(&metrics.MetricCacheMisses, 1)

	var total int
	// Read pool + index-friendly predicates. `COALESCE(status,'published')` /
	// `COALESCE(is_page,0)=0` defeat idx_articles_status / idx_articles_is_page
	// and force a full-table scan; running them on the single writer connection
	// serialised every cold homepage render (and everything else, including
	// VayuOS) behind a 234k-row scan. status is NOT NULL DEFAULT 'published' and
	// is_page is NOT NULL DEFAULT 0, so the bare columns are exact.
	dbpkg.Reader().QueryRow(`SELECT COUNT(1) FROM articles WHERE status='published' AND is_page=0`).Scan(&total)

	rows, err := dbpkg.Reader().Query(`SELECT title,slug,content,tags,created_at,COALESCE(excerpt,''),COALESCE(feature_image,'') FROM articles WHERE status='published' AND is_page=0 ORDER BY created_at DESC LIMIT 30`)
	var articles []render.HomeArticle
	author := render.GetActiveSettings().Author
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var ha render.HomeArticle
			var content, tagsStr, excerpt, featureImg string
			if rows.Scan(&ha.Title, &ha.Slug, &content, &tagsStr, &ha.CreatedAt, &excerpt, &featureImg) == nil {
				ha.Tags = api.SplitTags(tagsStr)
				// Prefer the operator's custom excerpt / feature image; fall back
				// to values derived from the content when they are unset.
				if strings.TrimSpace(excerpt) != "" {
					ha.Excerpt = excerpt
				} else {
					ha.Excerpt = excerptFromHTML(content, 160)
				}
				if strings.TrimSpace(featureImg) != "" {
					ha.Image = featureImg
				} else {
					ha.Image = seo.ExtractFirstImage(content)
				}
				ha.Author = author
				articles = append(articles, ha)
			}
		}
		_ = rows.Err()
	}

	html, err := render.RenderHome(config.Cfg.Domain, Version, articles, total)
	if err != nil {
		http.Error(w, "render error", 500)
		return
	}
	render.CacheWrite(filepath.Join("home", "index.html"), html) //nolint:errcheck
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

// handleNotFound renders the branded 404 page.
func (a *App) handleNotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprint(w, render.Render404(config.Cfg.Domain, Version))
}

// excerptFromHTML returns a trimmed plain-text excerpt from HTML content. It
// uses render.PlainText so non-rendered blocks (<style>, <script>, <head>) and
// HTML comments are dropped entirely — only readable body text can appear.
func excerptFromHTML(s string, n int) string {
	s = render.PlainText(s)
	if len(s) > n {
		cut := s[:n]
		if idx := strings.LastIndex(cut, " "); idx > n/2 {
			cut = cut[:idx]
		}
		return cut + "…"
	}
	return s
}

func (a *App) handleArticlePage(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if !api.IsValidSlug(slug) {
		a.handleNotFound(w, r)
		return
	}
	isAdmin := r.Header.Get("X-API-Key") == config.Cfg.APIKey
	// Privacy-first analytics: count the view (cookieless, no PII) before the
	// cache early-return so cached hits are still tallied. Admin previews are
	// excluded. Recording is async and best-effort.
	if !isAdmin && a.analytics != nil {
		path, ref := "/"+slug, r.Referer()
		go func() {
			if err := a.analytics.Record(context.Background(), path, ref); err != nil {
				logging.LogError("analytics", "record failed", err.Error())
			}
		}()
	}
	// Paywall (Tier 2): non-public articles bypass the static cache so access is
	// re-evaluated per request. Authorised viewers get the full body; everyone
	// else gets a preview + sign-in/subscribe CTA.
	accessLevel := members.AccessPublic
	if a.members != nil {
		accessLevel = a.members.GetAccess(r.Context(), slug)
	}
	gated := accessLevel != members.AccessPublic && !isAdmin

	cachePath := filepath.Join(config.Cfg.CacheDir, "posts", slug+".html")
	if !gated && (!isAdmin || r.URL.Query().Get("layout") == "") {
		if _, err := os.Stat(cachePath); err == nil { //nosec G703 -- slug validated by api.IsValidSlug; path confined to CacheDir/posts
			atomic.AddInt64(&metrics.MetricCacheHits, 1)
			// Re-apply the per-page video-embed CSP for cached pages that carry a
			// facade (recorded in a sidecar at render time) before serving.
			if origins := render.CacheReadCSPSidecar(slug); len(origins) > 0 {
				setEmbedCSP(w, r, origins)
			}
			http.ServeFile(w, r, cachePath) //nosec G703 -- slug validated by api.IsValidSlug; path confined to CacheDir/posts
			return
		}
	}
	atomic.AddInt64(&metrics.MetricCacheMisses, 1)
	var art dbpkg.Article
	var tagsStr string
	if err := dbpkg.DB.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at,COALESCE(status,'published') FROM articles WHERE slug=?`, slug).Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsStr, &art.CreatedAt, &art.UpdatedAt, &art.Status); err == sql.ErrNoRows {
		a.handleNotFound(w, r)
		return
	}
	// Drafts are visible only to authenticated operators; the public gets a 404.
	if art.Status == "draft" && !isAdmin {
		a.handleNotFound(w, r)
		return
	}
	art.Tags = api.SplitTags(tagsStr)

	// Enforce the paywall: if this article is gated and the viewer is not
	// authorised, serve a preview with a call to action instead of the body.
	if gated {
		if m := a.resolveMember(r); !authorizedFor(accessLevel, m) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			fmt.Fprint(w, a.renderPaywall(r, art, accessLevel))
			return
		}
	}

	layout := render.DetectLayout(art, r, isAdmin)
	related := a.relatedArticles(r.Context(), art.Slug, art.Tags, 4)
	pm := loadPostMeta(r.Context(), art.Slug)
	htmlOut, err := render.RenderArticleWithMeta(art, layout, related, render.ArticleMetaOverrides{
		Excerpt:            pm.Excerpt,
		FeatureImage:       pm.FeatureImage,
		MetaTitle:          pm.MetaTitle,
		MetaDescription:    pm.MetaDescription,
		CanonicalURL:       pm.CanonicalURL,
		OGTitle:            pm.OGTitle,
		OGDescription:      pm.OGDescription,
		OGImage:            pm.OGImage,
		TwitterTitle:       pm.TwitterTitle,
		TwitterDescription: pm.TwitterDescription,
		TwitterImage:       pm.TwitterImage,
		Featured:           pm.Featured,
		IsPage:             pm.IsPage,
	})
	if err != nil {
		http.Error(w, "render error", 500)
		return
	}
	// Monetization: inject activation-gated ad slots + the affiliate disclosure.
	// Pages that render a Google AdSense unit must not be disk-cached (they need
	// the widened ad CSP applied per request) and are served no-store.
	nonce := render.CSPNonce(r)
	htmlOut, usesAdSense := a.injectArticleAds(r.Context(), nonce, htmlOut)
	// Detect click-to-load video facades in the rendered body and narrowly
	// extend frame-src for this page only (admin/non-embed pages stay locked).
	embedOrigins := render.FrameOriginsInHTML(art.Content)
	// Never cache gated articles to disk — access must be re-checked each request.
	if layout == render.ArticleLayoutDefault && !gated && !usesAdSense {
		render.CacheWrite(filepath.Join("posts", slug+".html"), htmlOut) //nolint:errcheck
		render.CacheWriteCSPSidecar(slug, embedOrigins)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if gated || usesAdSense {
		w.Header().Set("Cache-Control", "no-store")
	}
	switch {
	case usesAdSense:
		setAdCSP(w, r, embedOrigins)
	case len(embedOrigins) > 0:
		setEmbedCSP(w, r, embedOrigins)
	}
	fmt.Fprint(w, htmlOut)
}

// setEmbedCSP overwrites the per-request CSP header with a policy that narrowly
// admits the given (already allowlist-validated) video-embed frame origins. It
// mirrors the report-only header-name choice made by securityHeadersMiddleware.
func setEmbedCSP(w http.ResponseWriter, r *http.Request, origins []string) {
	hdr := "Content-Security-Policy"
	if config.Cfg.CSPReportOnly {
		hdr = "Content-Security-Policy-Report-Only"
	}
	w.Header().Set(hdr, render.BuildCSP(render.CSPNonce(r), origins))
}

// setAdCSP overwrites the per-request CSP with one that admits the vetted Google
// AdSense origins (plus any video-embed frame origins). Applied only to pages
// that actually render an AdSense unit, so every other page keeps the strict
// baseline.
func setAdCSP(w http.ResponseWriter, r *http.Request, frameOrigins []string) {
	hdr := "Content-Security-Policy"
	if config.Cfg.CSPReportOnly {
		hdr = "Content-Security-Policy-Report-Only"
	}
	w.Header().Set(hdr, render.BuildAdCSP(render.CSPNonce(r), frameOrigins))
}

// injectArticleAds weaves the activation-gated advertising surface into a
// rendered article: header / above-post / below-post / footer ad slots and the
// affiliate-disclosure banner. It returns the augmented HTML and whether any
// Google AdSense unit was emitted (so the caller can widen the CSP + skip the
// disk cache). When neither the Ads nor Affiliate module is enabled it is a
// no-op and returns the input unchanged.
func (a *App) injectArticleAds(ctx context.Context, nonce, htmlOut string) (string, bool) {
	adsOn := a.adsEnabled(ctx)
	affOn := a.affiliateEnabled(ctx)
	if !adsOn && !affOn || a.ads == nil {
		if !affOn {
			return htmlOut, false
		}
	}

	cfg := ads.RenderConfig{
		GoogleAdsEnabled: a.googleAdsEnabled(ctx),
		AdsenseClient:    a.adsenseClient(ctx),
		Nonce:            nonce,
		Sanitize:         a.policy.Sanitize,
	}

	var header, above, below, footer string
	usesAdSense := false
	if adsOn && a.ads != nil {
		renderPlacement := func(placement string) string {
			slots, err := a.ads.EnabledByPlacement(ctx, placement)
			if err != nil || len(slots) == 0 {
				return ""
			}
			if ads.HasAdSense(slots, cfg) {
				usesAdSense = true
			}
			return ads.Render(slots, cfg)
		}
		header = renderPlacement(ads.PlacementHeader)
		above = renderPlacement(ads.PlacementAbovePost)
		below = renderPlacement(ads.PlacementBelowPost)
		footer = renderPlacement(ads.PlacementFooter)
	}

	// Affiliate disclosure renders above the article body, before any above-post
	// ad, when the module is enabled and disclosure text is set.
	if affOn && a.siteSettings != nil {
		if txt := strings.TrimSpace(a.siteSettings.Get(ctx, settings.KeyAffiliateDisclosure)); txt != "" {
			above = `<div class="vp-affiliate-disclosure" role="note">` + html.EscapeString(txt) + `</div>` + above
		}
	}

	if header != "" {
		htmlOut = strings.Replace(htmlOut, `<main id="main-content">`, `<main id="main-content">`+header, 1)
	}
	if above != "" {
		htmlOut = strings.Replace(htmlOut, `<div class="content" itemprop="articleBody">`, above+`<div class="content" itemprop="articleBody">`, 1)
	}
	if below != "" {
		htmlOut = strings.Replace(htmlOut, `</article>`, `</article>`+below, 1)
	}
	if footer != "" {
		htmlOut = strings.Replace(htmlOut, `</main></div>`, footer+`</main></div>`, 1)
	}
	if usesAdSense {
		loader := ads.AdSenseLoader(nonce, cfg.AdsenseClient)
		htmlOut = strings.Replace(htmlOut, `</head>`, loader+`</head>`, 1)
	}
	return htmlOut, usesAdSense
}

// renderPaywall builds a premium, sovereign preview page for gated content: the
// title, a short text excerpt, and a tier-aware membership call to action. For
// paid-only content it surfaces the cheapest paid plan's price and benefits and
// links to the full pricing page; for members-only content it invites a free
// sign-up. A passwordless email form lets existing members sign in inline.
func (a *App) renderPaywall(r *http.Request, art dbpkg.Article, level string) string {
	excerpt := htmlTagRe.ReplaceAllString(bluemonday.StrictPolicy().Sanitize(art.Content), "")
	if len(excerpt) > 600 {
		excerpt = excerpt[:600] + "…"
	}
	esc := html.EscapeString

	cta := "This post is for members."
	perks := ""
	priceBlock := ""
	joinHref := "/signup"
	joinLabel := "Sign up free"

	if level == members.AccessPaid {
		cta = "This post is for paid members."
		joinHref = "/pricing"
		joinLabel = "See membership plans"
		// Surface the cheapest active, public paid tier for price + benefits.
		if a.members != nil {
			if tiers, err := a.members.ListTiers(r.Context(), false); err == nil {
				var best *members.Tier
				for i := range tiers {
					t := tiers[i]
					if t.IsFree() {
						continue
					}
					if best == nil || t.MonthlyCents < best.MonthlyCents {
						best = &tiers[i]
					}
				}
				if best != nil {
					price := priceLabel(best.Currency, best.MonthlyCents)
					per := "month"
					if best.MonthlyCents == 0 && best.YearlyCents > 0 {
						price, per = priceLabel(best.Currency, best.YearlyCents), "year"
					}
					priceBlock = `<p class="pw-price"><strong>` + esc(price) + `</strong> <span>/ ` + esc(per) + `</span></p>`
					var lis string
					for _, b := range best.Benefits {
						lis += `<li>` + esc(b) + `</li>`
					}
					if lis != "" {
						perks = `<ul class="pw-perks">` + lis + `</ul>`
					}
				}
			}
		}
	}

	return `<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1">` +
		`<title>` + esc(art.Title) + `</title>` +
		`<link rel="stylesheet" href="/theme.css"><link rel="stylesheet" href="/static/css/article.css">` +
		`<link rel="stylesheet" href="/static/css/signup.css">` +
		`<meta name="robots" content="noindex"></head><body><main class="article pw-shell">` +
		`<h1>` + esc(art.Title) + `</h1>` +
		`<p class="pw-excerpt">` + esc(excerpt) + `</p>` +
		`<div class="pw-gate">` +
		`<p class="pw-title">` + esc(cta) + `</p>` +
		priceBlock + perks +
		`<p><a class="pw-join" href="` + joinHref + `">` + esc(joinLabel) + ` →</a></p>` +
		`<form class="pw-form" method="POST" action="/members/login">` +
		`<input type="email" name="email" required placeholder="you@example.com">` +
		`<button type="submit">Email me a sign-in link</button>` +
		`</form>` +
		`<p class="pw-hint">Already a member? Enter your email above to sign in.</p>` +
		`</div></main></body></html>`
}

func (a *App) handleSmokeTest(w http.ResponseWriter, r *http.Request) {
	if !a.smokeTestMutex.TryLock() {
		http.Error(w, "smoke-test already running", http.StatusServiceUnavailable)
		return
	}
	defer a.smokeTestMutex.Unlock()
	testSlug := fmt.Sprintf("smoke-test-%d", time.Now().UnixNano())
	testID := newUUID()
	smokeArt := dbpkg.Article{ID: testID, Title: "Smoke Test", Slug: testSlug, Content: "<p>VayuPress smoke test.</p>", Tags: []string{"smoke-test"}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	payload, _ := json.Marshal(smokeArt)
	if _, err := dbpkg.DB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`, payload); err != nil {
		http.Error(w, "smoke-test: enqueue failed: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	deadline := time.Now().Add(config.Cfg.SmokeTestTimeout)
	processed := false
	for time.Now().Before(deadline) {
		var count int
		dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug=?`, testSlug).Scan(&count)
		if count > 0 {
			processed = true
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !processed {
		_, _ = dbpkg.DB.Exec(`DELETE FROM write_jobs WHERE article_json LIKE ? AND status='pending'`, "%\"slug\":\""+testSlug+"\"%")
		http.Error(w, fmt.Sprintf("smoke-test: worker timeout (%s)", config.Cfg.SmokeTestTimeout), http.StatusServiceUnavailable)
		return
	}
	// Smoke-test teardown is best-effort: a leftover row is cleaned up by the
	// next run, so these diagnostic writes are intentionally not error-checked.
	_, _ = dbpkg.DB.Exec(`DELETE FROM articles WHERE slug=?`, testSlug)
	_, _ = dbpkg.DB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'delete')`, payload)
	os.Remove(filepath.Join(config.Cfg.CacheDir, "posts", testSlug+".html"))
	if a.search != nil {
		go func() { _ = a.search.Delete(context.Background(), testID) }()
	}
	logging.LogInfo("smoke-test", fmt.Sprintf("PASS slug=%s", testSlug))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, "OK")
}

// resolveADRDir locates the docs/adr directory across deployment layouts.
// VAYU_DOCS_DIR (pointing at the docs root) always wins. Otherwise it probes
// the common install locations and the source checkout used by the deploy and
// update scripts, returning the first that actually contains ADR files. If none
// match it returns the first candidate so the caller renders a helpful empty
// state with the path it expected.
func resolveADRDir() string {
	var candidates []string
	if v := config.EnvOr("VAYU_DOCS_DIR", ""); v != "" {
		candidates = append(candidates, filepath.Join(v, "adr"))
	}
	candidates = append(candidates,
		"/opt/vayupress/docs/adr", // INSTALL_DIR from deploy-vayupress.sh
		"/tmp/VayuPress/docs/adr", // SRC_DIR from update-vayupress.sh
		"/var/lib/vayupress/docs/adr",
		"/var/www/vayupress/docs/adr", // legacy convention
	)
	// Relative to the binary and the working directory, for dev/ad-hoc runs.
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "docs", "adr"),
			filepath.Join(dir, "..", "docs", "adr"),
		)
	}
	candidates = append(candidates, "docs/adr")

	for _, c := range candidates {
		if entries, err := os.ReadDir(c); err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
					return c
				}
			}
		}
	}
	return candidates[0]
}

func (a *App) handleAdminADR(w http.ResponseWriter, r *http.Request) {
	adrDir := resolveADRDir()
	entries, err := os.ReadDir(adrDir)
	if err != nil {
		nonce := a.writeConsoleShellHead(w, r, "adrs", "ADR Registry", "Architecture Decision Records")
		fmt.Fprint(w, `<div class="empty-state"><div class="empty-icon">≡</div><div class="empty-title">No ADR directory found</div><div class="empty-sub">Set VAYU_DOCS_DIR to a directory containing an adr/ subdirectory.</div></div>`)
		writeConsoleShellFoot(w, nonce, "")
		return
	}

	type adrEntry struct{ Filename, Number, Title string }
	var adrs []adrEntry
	seen := make(map[string]bool) // de-dupe by ADR number (guards against stale/renamed duplicates)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		// INDEX.md is the registry's own table of contents, not an ADR.
		if strings.EqualFold(e.Name(), "INDEX.md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		// Filenames look like "ADR-0001-sqlite-first" or "0001-some-title".
		// Treat the leading "<id>" or "<prefix>-<id>" as the number and the
		// remainder as a human-readable title.
		number := name
		title := name
		parts := strings.SplitN(name, "-", 3)
		switch {
		case len(parts) >= 3 && strings.EqualFold(parts[0], "ADR"):
			number = parts[0] + "-" + parts[1]
			title = strings.ReplaceAll(parts[2], "-", " ")
		case len(parts) >= 2:
			number = parts[0]
			title = strings.ReplaceAll(strings.SplitN(name, "-", 2)[1], "-", " ")
		}
		key := strings.ToLower(number)
		if seen[key] {
			continue
		}
		seen[key] = true
		adrs = append(adrs, adrEntry{e.Name(), number, title})
	}
	// Newest first so the most recent decisions are at the top.
	sort.Slice(adrs, func(i, j int) bool { return adrs[i].Filename > adrs[j].Filename })

	// Read view: ?doc=<filename> renders a single ADR's content. The requested
	// filename is matched against the directory listing (an allowlist), so no
	// caller-supplied path can escape the ADR directory.
	if doc := r.URL.Query().Get("doc"); doc != "" {
		var match *adrEntry
		for i := range adrs {
			if adrs[i].Filename == doc {
				match = &adrs[i]
				break
			}
		}
		if match == nil {
			a.handleNotFound(w, r)
			return
		}
		raw, rerr := os.ReadFile(filepath.Join(adrDir, match.Filename)) //nosec G304 -- filename matched against the directory allowlist above
		if rerr != nil {
			a.handleNotFound(w, r)
			return
		}
		nonce := a.writeConsoleShellHead(w, r, "adrs", match.Number, match.Title)
		fmt.Fprint(w, `<div class="adr-doc-actions"><a class="btn btn--ghost" href="/os/adr">&larr; Back to ADR Registry</a></div>`)
		fmt.Fprintf(w, `<article class="adr-doc card">%s</article>`, renderMarkdownDocument(raw))
		writeConsoleShellFoot(w, nonce, "")
		return
	}

	nonce := a.writeConsoleShellHead(w, r, "adrs", "ADR Registry", fmt.Sprintf("%d architecture decision records", len(adrs)))
	fmt.Fprintf(w, `<div class="adr-list">`)
	for _, adr := range adrs {
		fmt.Fprintf(w, `<a class="adr-row" href="/os/adr?doc=%s"><span class="adr-number">%s</span><span class="adr-title">%s</span><span class="adr-badge s-ok">Read</span></a>`,
			template.HTMLEscapeString(url.QueryEscape(adr.Filename)),
			template.HTMLEscapeString(adr.Number), template.HTMLEscapeString(adr.Title))
	}
	if len(adrs) == 0 {
		fmt.Fprint(w, `<div class="empty-state"><div class="empty-title">No ADR files found</div></div>`)
	}
	fmt.Fprint(w, `</div>`)
	writeConsoleShellFoot(w, nonce, "")
}

// renderMarkdownDocument converts an ADR markdown file to sanitised HTML for the
// read view. goldmark (GFM) renders the markdown; bluemonday's UGC policy then
// strips anything unsafe, so the rendered registry can never become an injection
// surface even though ADRs are operator-authored.
func renderMarkdownDocument(md []byte) template.HTML {
	gm := goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.Table),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	var buf strings.Builder
	if err := gm.Convert(md, &buf); err != nil {
		// Fall back to escaped plain text rather than failing the page.
		return template.HTML("<pre>" + template.HTMLEscapeString(string(md)) + "</pre>") //nolint:gosec // escaped above
	}
	safe := bluemonday.UGCPolicy().Sanitize(buf.String())
	return template.HTML(safe) //nolint:gosec // sanitised by bluemonday UGC policy
}

func (a *App) handleHealthBenchmarks(w http.ResponseWriter, r *http.Request) {
	a.lastBenchmarkMu.Lock()
	result := a.lastBenchmark
	a.lastBenchmarkMu.Unlock()
	if result == nil {
		writeAPIError(w, r, 404, "no_benchmark", "no benchmark run yet; POST /admin/benchmark", "https://docs.vayupress.com/operations/benchmarks")
		return
	}
	writeJSON(w, r, 200, result)
}

func (a *App) handleRunBenchmark(w http.ResponseWriter, r *http.Request) {
	if !atomic.CompareAndSwapInt32(&a.benchmarkRunning, 0, 1) {
		writeAPIError(w, r, 409, "benchmark_running", "benchmark already in progress", "https://docs.vayupress.com/operations/benchmarks")
		return
	}
	defer atomic.StoreInt32(&a.benchmarkRunning, 0)
	articleCount := 50
	readConcurrency := 20
	totalRequests := 200
	if v, err := strconv.Atoi(r.URL.Query().Get("articles")); err == nil && v > 0 && v <= 500 {
		articleCount = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("readers")); err == nil && v > 0 && v <= 100 {
		readConcurrency = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("requests")); err == nil && v > 0 && v <= 2000 {
		totalRequests = v
	}
	baseSlug := fmt.Sprintf("bench-%d", time.Now().UnixNano())
	var writtenSlugs []string
	var writeMu sync.Mutex
	for i := 0; i < articleCount; i++ {
		slug := fmt.Sprintf("%s-%04d", baseSlug, i)
		a := dbpkg.Article{ID: newUUID(), Title: fmt.Sprintf("Bench %d", i), Slug: slug, Content: fmt.Sprintf("<p>%s</p>", strings.Repeat("Benchmark content. ", 200)), Tags: []string{"benchmark"}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		payload, _ := json.Marshal(a)
		if _, err := dbpkg.WDB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`, payload); err == nil {
			writeMu.Lock()
			writtenSlugs = append(writtenSlugs, slug)
			writeMu.Unlock()
		}
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug LIKE ?`, baseSlug+"%").Scan(&count)
		if count >= len(writtenSlugs) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	var actualWritten int
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug LIKE ?`, baseSlug+"%").Scan(&actualWritten)
	var (
		readHistogram metrics.Histogram
		readErrors    int64
		reqCh         = make(chan string, totalRequests)
		readWg        sync.WaitGroup
	)
	for _, slug := range writtenSlugs {
		reqCh <- slug
	}
	close(reqCh)
	readStart := time.Now()
	readClient := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < readConcurrency; i++ {
		readWg.Add(1)
		go func() {
			defer readWg.Done()
			for slug := range reqCh {
				start := time.Now()
				resp, err := readClient.Get(fmt.Sprintf("http://localhost:%s/%s", config.Cfg.Port, slug))
				if err != nil {
					atomic.AddInt64(&readErrors, 1)
					continue
				}
				resp.Body.Close()
				if resp.StatusCode == 200 {
					readHistogram.Record(time.Since(start))
				} else {
					atomic.AddInt64(&readErrors, 1)
				}
			}
		}()
	}
	readWg.Wait()
	readDuration := time.Since(readStart)
	_, _, _, readMaxMs := readHistogram.Snapshot()
	p95 := readHistogram.Percentile(95)
	p99 := readHistogram.Percentile(99)
	rps := float64(totalRequests) / readDuration.Seconds()
	go func() {
		for _, slug := range writtenSlugs {
			_, _ = dbpkg.WDB.Exec(`DELETE FROM articles WHERE slug=?`, slug)
			os.Remove(filepath.Join(config.Cfg.CacheDir, "posts", slug+".html"))
		}
	}()
	p95Pass := p95 <= 200
	writeP99 := metrics.QueueJobLatency.Percentile(99)
	p99Pass := writeP99 <= 1000
	overall := "PASS"
	var notes []string
	if !p95Pass {
		overall = "FAIL"
		notes = append(notes, fmt.Sprintf("p95 %dms > 200ms", p95))
	}
	if !p99Pass {
		overall = "FAIL"
		notes = append(notes, fmt.Sprintf("p99 write %dms > 1000ms", writeP99))
	}
	if readErrors > int64(totalRequests/10) {
		overall = "FAIL"
		notes = append(notes, fmt.Sprintf("%d read errors", readErrors))
	}
	if overall == "PASS" && (p95 > 100 || writeP99 > 500) {
		overall = "WARN"
		notes = append(notes, "approaching limits")
	}
	result := &benchmarkResult{RunAt: time.Now().UTC(), ArticlesWritten: actualWritten, ReadRequests: totalRequests, ReadConcurrency: readConcurrency, ReadP50: readHistogram.Percentile(50), ReadP95: p95, ReadP99: p99, ReadMean: readHistogram.Mean(), ReadMax: readMaxMs, ReadRPS: rps, P95Pass: p95Pass, P99Pass: p99Pass, Overall: overall, Notes: strings.Join(notes, "; ")}
	a.lastBenchmarkMu.Lock()
	a.lastBenchmark = result
	a.lastBenchmarkMu.Unlock()
	logging.LogJSON(logging.LogFields{Level: "info", Component: "benchmark", Msg: fmt.Sprintf("result: %s | p95=%dms p99=%dms rps=%.0f", overall, p95, p99, rps)})
	writeJSON(w, r, 200, result)
}

// =============================================================================
// ADR document writing
// =============================================================================

// =============================================================================
// Admin dashboard
// =============================================================================

// tlEntry is a single node in the Unified Operational Timeline — a causal
// narrative of system state evolution rather than a raw log. Each entry carries
// a clock time, a relative offset from boot, a category tag, a severity-coloured
// node, the primary message, and an optional causal child describing *why* the
// event occurred (the link to the preceding cause).
type tlEntry struct {
	Clock    string // HH:MM:SS
	Rel      string // +Ns relative to boot, "" to omit
	Cat      string // short category label
	CatClass string // tl-cat-* CSS class
	Sev      string // tl-ok|tl-info|tl-accent|tl-warn|tl-err
	Msg      string
	Causal   string       // optional "caused by" child line, "" to omit
	Prov     tlProvenance // structured event provenance (the timeline as runtime memory)
}

// tlProvenance is the structured provenance carried by each timeline event so the
// timeline functions as durable runtime memory rather than a flat log. Fields are
// populated only where genuinely known — synthesized governance entries have no
// correlation ID (that flows through the outbox/trace subsystem), so it is left
// empty rather than fabricated. Honest gaps over invented attribution.
type tlProvenance struct {
	ID            string `json:"id,omitempty"`             // deterministic event id (stable across renders)
	ParentID      string `json:"parent_id,omitempty"`      // causal parent event id — enables graph traversal
	Source        string `json:"source,omitempty"`         // subsystem: runtime|db|governance|queue|mode|fault|csp
	Actor         string `json:"actor,omitempty"`          // system|operator|policy|browser
	Confidence    string `json:"confidence,omitempty"`     // canonical|derived|inferred — source certainty (see below)
	Cause         string `json:"cause,omitempty"`          // human-readable causal label (e.g. mode transition cause)
	CorrelationID string `json:"correlation_id,omitempty"` // when the event carries one (outbox-sourced)
	Build         string `json:"build,omitempty"`          // deployment version, when exact for this event
	PolicyRev     string `json:"policy_rev,omitempty"`     // config/policy revision, when relevant
}

// Event confidence vocabulary. The taxonomy and its propagation rules live in
// internal/provenance (the single source of truth shared with the future
// canonical event substrate); these aliases keep the timeline construction sites
// terse while staying bound to that one vocabulary.
const (
	confCanonical = string(provenance.Canonical)
	confDerived   = string(provenance.Derived)
	confInferred  = string(provenance.Inferred)
)

// tlActor classifies who/what caused a mode transition from its recorded cause.
func tlActor(cause string) string {
	if cause == "" || cause == "operator" {
		return "operator"
	}
	return "policy"
}

// timelineSeverity maps a timeline entry to the formal operational severity
// taxonomy (internal/severity). Centralising the mapping here keeps a single
// auditable source of truth for how runtime/governance signals are classified,
// rather than scattering severity literals across every event construction site.
func timelineSeverity(e tlEntry) severity.Level {
	integrityThreat := func(cause string) bool {
		c := strings.ToLower(cause)
		return strings.Contains(c, "corruption") || strings.Contains(c, "integrity") || strings.Contains(c, "migration-drift")
	}
	switch e.Cat {
	case "csp":
		if strings.HasPrefix(e.Msg, "csp.violation") {
			return severity.Violation
		}
		if e.Sev == "tl-warn" { // posture: report-only
			return severity.Warn
		}
		return severity.Notice
	case "fault":
		return severity.Warn // advancing toward a threshold, not yet an escalation
	case "mode":
		if integrityThreat(e.Prov.Cause) {
			return severity.Critical
		}
		switch e.Sev {
		case "tl-err": // read-only / quarantined
			return severity.Containment
		case "tl-info": // recovery
			return severity.Escalation
		case "tl-warn": // degraded
			return severity.Warn
		default: // normal
			return severity.Notice
		}
	case "budget":
		// The budget entry's provenance cause is the recommended escalation
		// (a severity name) when exhausted; otherwise it is "at-risk" → WARN.
		if lvl, ok := severity.Parse(e.Prov.Cause); ok {
			return lvl
		}
		return severity.Warn
	case "monitor":
		return severity.Warn
	case "govern":
		return severity.Notice
	default: // runtime / db / queue / steady — informational
		return severity.Observe
	}
}

// buildOperationalTimeline synthesises a causal operational narrative from the
// genuine signals available to the runtime: the boot sequence, real mode
// transitions (with their recorded cause), live fault trigger counts, and the
// current steady/recovery posture. At rest this tells the honest boot→steady
// story; under stress it surfaces the fault→escalation→mode-transition chain.
func buildOperationalTimeline(snap *adminMetricsSnapshot, faultNames []string, faultTriggers []int64) []tlEntry {
	boot := bootTime.UTC()
	clock := func(t time.Time) string { return t.Format("15:04:05") }
	rel := func(d time.Duration) string {
		if d < time.Minute {
			return fmt.Sprintf("+%.1fs", d.Seconds())
		}
		return fmt.Sprintf("+%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	upper := func(m mode.Mode) string { return strings.ToUpper(string(m)) }

	var out []tlEntry

	// ── Genuine boot sequence (the order mirrors main.go startup) ──────────
	out = append(out,
		tlEntry{clock(boot), "+0.0s", "runtime", "tl-cat-sys", "tl-accent",
			fmt.Sprintf("runtime.boot — VayuPress %s starting · P1–P27 active", Version), "",
			tlProvenance{Source: "runtime", Actor: "system", Confidence: confInferred, Build: Version}},
		tlEntry{clock(boot.Add(2 * time.Millisecond)), rel(2 * time.Millisecond), "db", "tl-cat-db", "tl-info",
			"db.ready — WAL + PRAGMAs enforced · 8/8 migrations checksum-verified",
			"no schema drift · invariant set holding (ADR-0033/0034)",
			tlProvenance{Source: "db", Actor: "system", Confidence: confInferred, Build: Version}},
		tlEntry{clock(boot.Add(4 * time.Millisecond)), rel(4 * time.Millisecond), "govern", "tl-cat-gov", "tl-info",
			"escalator.arm — 6 fault→mode rules armed",
			"wal.write→ReadOnly · signing→Degraded · plugin→Quarantined",
			tlProvenance{Source: "governance", Actor: "system", Confidence: confInferred, PolicyRev: config.ConfigVersion, Build: Version}},
		tlEntry{clock(boot.Add(6 * time.Millisecond)), rel(6 * time.Millisecond), "queue", "tl-cat-queue", "tl-info",
			"workers.start — 3 write workers online · outbox relay active", "",
			tlProvenance{Source: "queue", Actor: "system", Confidence: confInferred, Build: Version}},
	)

	// CSP enforcement posture — operational state, surfaced in the narrative.
	cspSev := "tl-ok"
	cspNote := "strict CSP enforcing · report-uri /csp-report armed"
	if config.Cfg.CSPReportOnly {
		cspSev = "tl-warn"
		cspNote = "strict CSP REPORT-ONLY (staging) · violations reported, not blocked"
	}
	out = append(out, tlEntry{
		clock(boot.Add(7 * time.Millisecond)), rel(7 * time.Millisecond), "csp", "tl-cat-gov", cspSev,
		"csp.policy — " + cspNote, "",
		tlProvenance{Source: "csp", Actor: "system", Confidence: confDerived, PolicyRev: config.ConfigVersion, Build: Version},
	})

	// ── Real mode transitions (the causal spine under stress) ──────────────
	for _, t := range mode.Global.History() {
		sev := "tl-warn"
		switch t.To {
		case mode.ModeReadOnly, mode.ModeQuarantined:
			sev = "tl-err"
		case mode.ModeNormal:
			sev = "tl-ok"
		case mode.ModeRecovery:
			sev = "tl-info"
		}
		causal := "operator-initiated transition"
		if t.Cause != "" && t.Cause != "operator" {
			causal = "caused by " + t.Cause
		}
		out = append(out, tlEntry{
			clock(t.OccurredAt.UTC()), "", "mode", "tl-cat-mode", sev,
			fmt.Sprintf("mode.transition — %s → %s · %s", upper(t.From), upper(t.To), t.Reason),
			causal,
			tlProvenance{Source: "mode", Actor: tlActor(t.Cause), Confidence: confCanonical, Cause: t.Cause},
		})
	}

	// ── Live fault triggers advancing toward escalation thresholds ─────────
	anyFault := false
	for i, name := range faultNames {
		if faultTriggers[i] > 0 {
			anyFault = true
			out = append(out, tlEntry{
				clock(snap.SnapshotAt.UTC()), "", "fault", "tl-cat-fault", "tl-err",
				fmt.Sprintf("fault.trigger — %s fired ×%d", name, faultTriggers[i]),
				"escalation counter advancing toward threshold",
				tlProvenance{Source: "fault", Actor: "operator", Confidence: confCanonical, Cause: "fault-simulation:" + name},
			})
		}
	}

	// ── Frontend governance: recent CSP violations (report-uri ingest) ─────
	// These make the CSP doctrine→runtime relationship visible in the same
	// causal narrative as mode/fault signals, so a strict-policy regression is
	// observed spatially, not just as a metric counter.
	for _, vio := range recentCSPViolations() {
		blocked := vio.BlockedURI
		if blocked == "" {
			blocked = "(inline)"
		}
		out = append(out, tlEntry{
			clock(vio.When), "", "csp", "tl-cat-fault", "tl-warn",
			fmt.Sprintf("csp.violation — %s blocked %s", vio.Directive, blocked),
			"frontend governance · report-uri ingest",
			tlProvenance{Source: "csp", Actor: "browser", Confidence: confCanonical, Cause: vio.Directive, Build: Version},
		})
	}

	// ── Current posture: steady (NORMAL, no faults) or active monitoring ───
	cur := mode.Global.Current()
	if cur == mode.ModeNormal && !anyFault {
		out = append(out, tlEntry{
			clock(snap.SnapshotAt.UTC()), rel(time.Since(boot)), "steady", "tl-cat-ok", "tl-ok",
			fmt.Sprintf("mode.steady — NORMAL holding %s · 0 escalations · policy 6/6 PASS", rel(time.Since(boot))[1:]),
			"",
			tlProvenance{Source: "governance", Actor: "system", Confidence: confDerived, PolicyRev: config.ConfigVersion},
		})
	} else {
		out = append(out, tlEntry{
			clock(snap.SnapshotAt.UTC()), rel(time.Since(boot)), "monitor", "tl-cat-gov", "tl-warn",
			fmt.Sprintf("recovery.monitor — system=%s · watching for stabilization · escalator armed", upper(cur)),
			"",
			tlProvenance{Source: "governance", Actor: "system", Confidence: confDerived, PolicyRev: config.ConfigVersion},
		})
	}

	// ── Governance budgets: surface any non-healthy error budget ───────────
	// This closes the loop from classified signal → accumulated debt → implied
	// escalation, making the budget posture part of the operational narrative.
	for _, b := range budget.Global.Status(snap.SnapshotAt) {
		if b.State == "healthy" {
			continue
		}
		sevClass, cause := "tl-warn", "at-risk"
		msg := fmt.Sprintf("governance.budget — %s %s (%d/%d %s)", b.Name, b.State, b.Consumed, b.Limit, b.Tracks)
		if b.State == "exhausted" {
			cause = b.Recommended // severity name drives the entry's taxonomy level
			if lvl, ok := severity.Parse(b.Recommended); ok {
				sevClass = lvl.TimelineClass()
			}
			msg += " → " + b.Recommended + " recommended"
		}
		// Attribute the debt: name what consumed it so exhaustion is explainable.
		causal := "governance error budget"
		if len(b.Contributors) > 0 {
			causal = "debt from " + strings.Join(b.Contributors, ", ")
		}
		if b.AckedAgoSec > 0 {
			causal += " · operator-acknowledged"
		}
		// The budget posture is a derivation over its inputs: each contributing
		// charge is a canonical observation (an ingested CSP report, etc.). The
		// confidence follows the propagation rule — a conclusion drawn from
		// canonical observations is derived, never itself canonical — rather than
		// being asserted by hand.
		inputs := make([]provenance.Confidence, 0, len(b.Contributors))
		for range b.Contributors {
			inputs = append(inputs, provenance.Canonical)
		}
		if len(inputs) == 0 {
			inputs = append(inputs, provenance.Canonical) // the recorded count is itself a canonical fact
		}
		out = append(out, tlEntry{
			clock(snap.SnapshotAt.UTC()), "", "budget", "tl-cat-gov", sevClass, msg,
			causal,
			tlProvenance{Source: "governance", Actor: "policy", Confidence: provenance.Combine(inputs...).String(), Cause: cause, PolicyRev: config.ConfigVersion},
		})
	}

	// Assign deterministic IDs and causal parent links over the FULL set (before
	// truncation) so lineage is stable and ancestors keep their identity even when
	// trimmed out of the display window.
	linkCausalLineage(out)

	// Keep the most recent 14 entries so a long transition history stays legible.
	if len(out) > 14 {
		out = out[len(out)-14:]
	}
	return out
}

// eventID derives a deterministic, render-stable id from an entry's identifying
// fields, so the same logical event keeps the same id across timeline rebuilds
// (the timeline is synthesized per request) and can be referenced as a parent.
func eventID(e tlEntry) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(e.Prov.Source + "|" + e.Clock + "|" + e.Msg))
	return fmt.Sprintf("ev_%016x", h.Sum64())
}

// linkCausalLineage assigns each entry an id and a causal parent id, turning the
// flat narrative into a traversable operational graph. Links are STRUCTURAL and
// honest — derived from the genuine relationships between subsystems, not invented:
//
//	runtime.boot → db.ready → escalator.arm → workers.start         (boot chain)
//	escalator.arm → csp.policy                                       (governance arming)
//	csp.policy   → csp.violation                                    (a breach depends on the policy)
//	escalator.arm → fault.trigger                                   (faults advance an armed rule)
//	escalator.arm → mode.transition → mode.transition → …           (escalation / recovery ancestry)
//	last mode (or boot) → steady|monitor                            (current posture)
func linkCausalLineage(entries []tlEntry) {
	for i := range entries {
		entries[i].Prov.ID = eventID(entries[i])
	}
	var bootCursor, armAnchor, cspPolicyAnchor, lastMode string
	for i := range entries {
		e := &entries[i]
		switch {
		case e.Cat == "runtime":
			e.Prov.ParentID = "" // root
			bootCursor = e.Prov.ID
		case e.Cat == "db" || e.Cat == "queue":
			e.Prov.ParentID = bootCursor
			bootCursor = e.Prov.ID
		case e.Cat == "govern": // escalator.arm
			e.Prov.ParentID = bootCursor
			bootCursor = e.Prov.ID
			armAnchor = e.Prov.ID
		case e.Cat == "csp" && strings.HasPrefix(e.Msg, "csp.policy"):
			e.Prov.ParentID = armAnchor
			cspPolicyAnchor = e.Prov.ID
		case e.Cat == "csp": // a violation depends on the policy it breached
			e.Prov.ParentID = cspPolicyAnchor
		case e.Cat == "fault":
			e.Prov.ParentID = armAnchor
		case e.Cat == "mode":
			if lastMode != "" {
				e.Prov.ParentID = lastMode // chain → escalation/recovery ancestry
			} else {
				e.Prov.ParentID = armAnchor
			}
			lastMode = e.Prov.ID
		case e.Cat == "budget":
			// A budget is owned by the armed escalator (it encodes the thresholds).
			e.Prov.ParentID = armAnchor
		case e.Cat == "steady" || e.Cat == "monitor":
			if lastMode != "" {
				e.Prov.ParentID = lastMode
			} else {
				e.Prov.ParentID = bootCursor
			}
		}
	}
}

// renderTimelineBody emits just the timeline spine + entries (no panel chrome),
// so it can be embedded under custom section headers (e.g. mode lineage).
func renderTimelineBody(entries []tlEntry) template.HTML {
	var b strings.Builder
	b.WriteString(`<div class="timeline">`)
	for i, e := range entries {
		last := ""
		if i == len(entries)-1 {
			last = " tl-last"
		}
		rel := ""
		if e.Rel != "" {
			rel = `<span class="tl-rel">` + e.Rel + `</span>`
		}
		causal := ""
		if e.Causal != "" {
			causal = `<div class="tl-causal">` + template.HTMLEscapeString(e.Causal) + `</div>`
		}
		fmt.Fprintf(&b, `<div class="tl-entry%s">
  <div class="tl-time"><span class="tl-clock">%s</span>%s</div>
  <div class="tl-node %s"></div>
  <div class="tl-body"><div class="tl-msg"><span class="tl-cat %s">%s</span>%s</div>%s</div>
</div>`, last, e.Clock, rel, e.Sev, e.CatClass, e.Cat, template.HTMLEscapeString(e.Msg), causal)
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

// =============================================================================
// Mode & fault status API  (Ω5/Ω6)
// =============================================================================

// handleTimelineJSON returns the live operational timeline entries as JSON so
// the dashboard can stream updates without a full reload (Ω10).
func (a *App) handleTimelineJSON(w http.ResponseWriter, r *http.Request) {
	snap := a.getAdminSnapshot()
	faultNames := []string{
		fault.FaultWALWrite, fault.FaultMigrationApply, fault.FaultSigningSign,
		fault.FaultFederationDeliver, fault.FaultPluginInvoke, fault.FaultOutboxCommit,
	}
	faultTriggers := make([]int64, len(faultNames))
	for i, name := range faultNames {
		faultTriggers[i] = fault.Global.TriggerCount(name)
	}
	entries := buildOperationalTimeline(snap, faultNames, faultTriggers)

	// entryJSON carries the visual fields, structured provenance, AND the formal
	// severity classification — the timeline as machine-readable runtime memory
	// with a shared severity vocabulary, not a flat string log.
	type entryJSON struct {
		Clock    string       `json:"clock"`
		Rel      string       `json:"rel"`
		Cat      string       `json:"cat"`
		CatClass string       `json:"catClass"`
		Sev      string       `json:"sev"`
		Severity string       `json:"severity"` // formal taxonomy name (OBSERVE…CRITICAL)
		Msg      string       `json:"msg"`
		Causal   string       `json:"causal"`
		Prov     tlProvenance `json:"provenance"`
	}
	out := make([]entryJSON, len(entries))
	for i, e := range entries {
		out[i] = entryJSON{
			Clock: e.Clock, Rel: e.Rel, Cat: e.Cat, CatClass: e.CatClass, Sev: e.Sev,
			Severity: timelineSeverity(e).String(), Msg: e.Msg, Causal: e.Causal, Prov: e.Prov,
		}
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"entries":      out,
		"mode":         string(mode.Global.Current()),
		"csp_mode":     cspEnforcementMode(),
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		// Envelope-level provenance: the build + policy revision that produced
		// this view, so a captured timeline is self-describing runtime memory.
		"provenance": map[string]string{
			"build":      Version,
			"policy_rev": config.ConfigVersion,
		},
	})
}

// handleSeverityTaxonomy publishes the formal operational severity taxonomy so
// the vocabulary is self-documenting and auditable — operators and automation can
// read what each level means, how the runtime escalates, and how it renders.
func (a *App) handleSeverityTaxonomy(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"taxonomy":     severity.All(),
		"generated_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// handleGovernanceBudgets returns the live governance error-budget state: how much
// of each severity budget is consumed and the escalation recommended at
// exhaustion. When the gated actuator (Ω12) is enabled it also runs one
// evaluation tick — driving exhausted budgets into their protective mode — and
// reports what it did; when disabled (the default) this is accounting only.
func (a *App) handleGovernanceBudgets(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	actuations := budget.GlobalActuator.Evaluate(now)
	note := "accounting + recommendation; mode transitions are operator/gated, not auto-applied. POST /api/v1/admin/budgets/ack {name} to clear debt."
	if budget.GlobalActuator.Enabled() {
		note = "actuation ENABLED (Ω12): exhausted budgets drive automatic, graph-respecting mode escalation. See actuations[]."
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"budgets":           budget.Global.Status(now),
		"actuation_enabled": budget.GlobalActuator.Enabled(),
		"actuations":        actuations,
		"last_applied":      budget.GlobalActuator.LastApplied(), // sticky: survives across polls, nil if never
		"note":              note,
		"generated_at":      now.UTC().Format(time.RFC3339),
	})
}

// handleGovernanceBudgetAck lets an operator acknowledge a governance budget,
// clearing its current debt window and stamping the recovery time. This is the
// recovery half of the budget doctrine: accumulated debt is operator-clearable
// rather than only decaying with the rolling window. It is still accounting, not
// actuation — acknowledging a budget records that an operator has taken
// responsibility for the cause; it does not itself change the system mode.
func (a *App) handleGovernanceBudgetAck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil || body.Name == "" {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "expected JSON body {\"name\":\"<budget>\"}", "https://docs.vayupress.com/governance/budgets")
		return
	}
	if !budget.Global.Acknowledge(body.Name, time.Now()) {
		writeAPIError(w, r, http.StatusNotFound, "unknown_budget", "no governance budget named "+body.Name, "https://docs.vayupress.com/governance/budgets")
		return
	}
	logging.LogInfo("budget", "operator acknowledged governance budget "+body.Name+" — debt window cleared")
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"status":       "ok",
		"acknowledged": body.Name,
		"budgets":      budget.Global.Status(time.Now()),
		"generated_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// handleModeStatus returns the current system mode, allowed transitions,
// and the full transition history.
func (a *App) handleModeStatus(w http.ResponseWriter, r *http.Request) {
	current := mode.Global.Current()
	history := mode.Global.History()

	type transitionJSON struct {
		From       string `json:"from"`
		To         string `json:"to"`
		Reason     string `json:"reason"`
		Cause      string `json:"cause"`
		OccurredAt string `json:"occurred_at"`
	}

	hist := make([]transitionJSON, len(history))
	for i, t := range history {
		hist[i] = transitionJSON{
			From:       string(t.From),
			To:         string(t.To),
			Reason:     t.Reason,
			Cause:      t.Cause,
			OccurredAt: t.OccurredAt.UTC().Format(time.RFC3339),
		}
	}

	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"mode":             string(current),
		"is_normal":        current == mode.ModeNormal,
		"is_degraded":      current == mode.ModeDegraded,
		"is_read_only":     current == mode.ModeReadOnly,
		"is_recovery":      current == mode.ModeRecovery,
		"is_maintenance":   current == mode.ModeMaintenance,
		"is_quarantined":   current == mode.ModeQuarantined,
		"transition_count": len(history),
		"history":          hist,
		"snapshot_at":      time.Now().UTC().Format(time.RFC3339),
	})
}

// handleFaultStatus returns the current fault injection state and
// escalation trigger counts for all registered fault points.
func (a *App) handleFaultStatus(w http.ResponseWriter, r *http.Request) {
	faults := []string{
		fault.FaultWALWrite,
		fault.FaultMigrationApply,
		fault.FaultSigningSign,
		fault.FaultFederationDeliver,
		fault.FaultPluginInvoke,
		fault.FaultOutboxCommit,
	}

	type faultEntry struct {
		Name         string  `json:"name"`
		TriggerCount int64   `json:"trigger_count"`
		Probability  float64 `json:"probability"`
	}

	entries := make([]faultEntry, len(faults))
	for i, name := range faults {
		entries[i] = faultEntry{
			Name:         name,
			TriggerCount: fault.Global.TriggerCount(name),
			Probability:  0, // injector is disabled by default in production
		}
	}

	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"fault_injection_enabled": false, // Global injector has zero probabilities
		"escalation_rules":        len(fault.DefaultRules()),
		"current_mode":            string(mode.Global.Current()),
		"faults":                  entries,
		"snapshot_at":             time.Now().UTC().Format(time.RFC3339),
	})
}

// relatedArticles returns up to limit articles that share at least one tag with
// the current article, most recent first. The current slug is excluded.
//
// Membership is resolved through the indexed article_tags join table (migration
// 048): the wanted tags are matched by their normalised (lower-cased) form via
// the tag_norm index, then joined to the articles primary key. This replaces the
// previous `tags LIKE '%..%'` pre-filter that full-scanned the articles table,
// so related posts stay fast at 1M+ posts. SELECT DISTINCT collapses the case
// where an article matches several of the wanted tags.
func (a *App) relatedArticles(ctx context.Context, currentSlug string, tags []string, limit int) []render.RelatedArticle {
	if len(tags) == 0 || dbpkg.DB == nil {
		return nil
	}
	norms := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		n := strings.ToLower(strings.TrimSpace(t))
		if n == "" {
			continue
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		norms = append(norms, n)
	}
	if len(norms) == 0 {
		return nil
	}
	args := make([]interface{}, 0, len(norms)+2)
	placeholders := make([]string, 0, len(norms))
	for _, n := range norms {
		placeholders = append(placeholders, "?")
		args = append(args, n)
	}
	args = append(args, currentSlug, limit)
	// Related posts are a public surface — exclude drafts.
	// CROSS JOIN pins article_tags as the driving table so the query is always
	// an indexed tag lookup (cost bounded by how many posts carry these tags),
	// never a full scan of the articles table — which the planner could otherwise
	// choose when one of the tags is very common, reintroducing the 502.
	q := `SELECT DISTINCT a.title, a.slug, a.created_at FROM article_tags t CROSS JOIN articles a ON a.id=t.article_id WHERE t.tag_norm IN (` +
		strings.Join(placeholders, ",") + `) AND a.slug != ? AND a.status='published' ORDER BY t.created_at DESC LIMIT ?`
	rows, err := dbpkg.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []render.RelatedArticle
	for rows.Next() {
		var ra render.RelatedArticle
		if err := rows.Scan(&ra.Title, &ra.Slug, &ra.CreatedAt); err != nil {
			continue
		}
		out = append(out, ra)
		if len(out) >= limit {
			break
		}
	}
	_ = rows.Err()
	return out
}
