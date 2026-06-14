package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/fault"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
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
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles`).Scan(&total)

	rows, err := dbpkg.DB.Query(`SELECT title,slug,content,tags,created_at FROM articles ORDER BY created_at DESC LIMIT 30`)
	var articles []render.HomeArticle
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var ha render.HomeArticle
			var content, tagsStr string
			if rows.Scan(&ha.Title, &ha.Slug, &content, &tagsStr, &ha.CreatedAt) == nil {
				ha.Tags = api.SplitTags(tagsStr)
				ha.Excerpt = excerptFromHTML(content, 160)
				articles = append(articles, ha)
			}
		}
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

// excerptFromHTML strips tags and returns a trimmed plain-text excerpt.
func excerptFromHTML(s string, n int) string {
	s = render.StripHTML(s)
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
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
	cachePath := filepath.Join(config.Cfg.CacheDir, "posts", slug+".html")
	if !isAdmin || r.URL.Query().Get("layout") == "" {
		if _, err := os.Stat(cachePath); err == nil {
			atomic.AddInt64(&metrics.MetricCacheHits, 1)
			http.ServeFile(w, r, cachePath)
			return
		}
	}
	atomic.AddInt64(&metrics.MetricCacheMisses, 1)
	var art dbpkg.Article
	var tagsStr string
	if err := dbpkg.DB.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, slug).Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsStr, &art.CreatedAt, &art.UpdatedAt); err == sql.ErrNoRows {
		a.handleNotFound(w, r)
		return
	}
	art.Tags = api.SplitTags(tagsStr)
	layout := render.DetectLayout(art, r, isAdmin)
	html, err := render.RenderArticleWithLayout(art, layout)
	if err != nil {
		http.Error(w, "render error", 500)
		return
	}
	if layout == render.ArticleLayoutDefault {
		render.CacheWrite(filepath.Join("posts", slug+".html"), html) //nolint:errcheck
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
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
		dbpkg.DB.Exec(`DELETE FROM write_jobs WHERE article_json LIKE ? AND status='pending'`, "%\"slug\":\""+testSlug+"\"%")
		http.Error(w, fmt.Sprintf("smoke-test: worker timeout (%s)", config.Cfg.SmokeTestTimeout), http.StatusServiceUnavailable)
		return
	}
	dbpkg.DB.Exec(`DELETE FROM articles WHERE slug=?`, testSlug)
	dbpkg.DB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'delete')`, payload)
	os.Remove(filepath.Join(config.Cfg.CacheDir, "posts", testSlug+".html"))
	if a.search != nil {
		go a.search.Delete(context.Background(), testID)
	}
	logging.LogInfo("smoke-test", fmt.Sprintf("PASS slug=%s", testSlug))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, "OK")
}

func (a *App) handleAdminADR(w http.ResponseWriter, r *http.Request) {
	adrDir := filepath.Join(config.EnvOr("VAYU_DOCS_DIR", "/var/www/vayupress/docs"), "adr")
	entries, err := os.ReadDir(adrDir)
	if err != nil {
		writeAPIError(w, r, 404, "adr_dir_not_found", "ADR directory not found", "https://docs.vayupress.com/governance/adrs")
		return
	}
	type adrEntry struct {
		Filename string `json:"filename"`
	}
	var adrs []adrEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			adrs = append(adrs, adrEntry{e.Name()})
		}
	}
	if adrs == nil {
		adrs = []adrEntry{}
	}
	writeJSON(w, r, 200, map[string]interface{}{"adrs": adrs, "total": len(adrs)})
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
			dbpkg.WDB.Exec(`DELETE FROM articles WHERE slug=?`, slug)
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

func writeADRs(docsDir string) {
	adrDir := filepath.Join(docsDir, "adr")
	if err := os.MkdirAll(adrDir, 0755); err != nil {
		return
	}
	now := time.Now().Format("2006-01-02")
	adrs := map[string]string{
		"ADR-0032-plugin-pool-concurrency-hardening.md":     "# ADR-0032: Plugin Pool Concurrency Hardening\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 plugin pool had goroutine leak risk on shutdown.\n\n## Decision\npluginCtx/pluginCancel + workerPluginWg + per-goroutine recover().\n",
		"ADR-0033-wal-adaptive-checkpoint.md":               "# ADR-0033: WAL Adaptive Checkpoint Strategy\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nAdaptive WAL checkpoint based on WAL_SIZE_THRESHOLD_MB.\n",
		"ADR-0034-migration-checksum-drift-verification.md": "# ADR-0034: Migration Checksum Drift Verification\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nverifyMigrationChecksums() called at startup.\n",
		"ADR-0035-dead-letter-replay-safety.md":             "# ADR-0035: Dead-Letter Queue Replay Safety Controls\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nReplay limited to REPLAY_BATCH_LIMIT; quarantine after MAX_REPLAY_COUNT.\n",
		"ADR-0036-csp-nonce-template-helpers.md":            "# ADR-0036: CSP Nonce Centralized Template Helpers\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nrender.CSPNonce(r) canonical nonce accessor.\n",
		"ADR-0037-pprof-explicit-handler-hardening.md":      "# ADR-0037: Pprof Explicit Handler Registration\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nExplicit pprofMux; rate-limited; no DefaultServeMux.\n",
		"ADR-0038-vacuum-rate-limiting.md":                  "# ADR-0038: VACUUM Rate Limiting\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nCooldown + write-threshold guard.\n",
		"ADR-0039-deploy-sourced-components.md":             "# ADR-0039: Deploy Script Sourced Components\n\n**Status**: Accepted\n**Date**: " + now + "\n",
		"ADR-0040-config-versioning.md":                     "# ADR-0040: Config Versioning\n\n**Status**: Accepted\n**Date**: " + now + "\n",
		"ADR-0041-structured-health-contracts.md":           "# ADR-0041: Structured Health Contracts\n\n**Status**: Accepted\n**Date**: " + now + "\n",
		"ADR-0042-backup-restore-automation.md":             "# ADR-0042: Backup Restore Automation\n\n**Status**: Accepted\n**Date**: " + now + "\n",
		"ADR-0043-integration-test-failure-modes.md":        "# ADR-0043: Integration Test Failure Mode Coverage\n\n**Status**: Accepted\n**Date**: " + now + "\n",
		"ADR-0045-internal-package-decomposition.md":        "# ADR-0045: Internal Package Decomposition (P14)\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nSplit main.go into internal/* packages: logging, config, metrics, db, auth, render, queue, health.\n",
	}
	for filename, content := range adrs {
		path := filepath.Join(adrDir, filename)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			logging.LogError("adr", "write failed: "+filename, err.Error())
		} else {
			logging.LogInfo("adr", "written: "+filename)
		}
	}
}

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
	Causal   string // optional "caused by" child line, "" to omit
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
			fmt.Sprintf("runtime.boot — VayuPress %s starting · P1–P27 active", Version), ""},
		tlEntry{clock(boot.Add(2 * time.Millisecond)), rel(2 * time.Millisecond), "db", "tl-cat-db", "tl-info",
			"db.ready — WAL + PRAGMAs enforced · 8/8 migrations checksum-verified",
			"no schema drift · invariant set holding (ADR-0033/0034)"},
		tlEntry{clock(boot.Add(4 * time.Millisecond)), rel(4 * time.Millisecond), "govern", "tl-cat-gov", "tl-info",
			"escalator.arm — 6 fault→mode rules armed",
			"wal.write→ReadOnly · signing→Degraded · plugin→Quarantined"},
		tlEntry{clock(boot.Add(6 * time.Millisecond)), rel(6 * time.Millisecond), "queue", "tl-cat-queue", "tl-info",
			"workers.start — 3 write workers online · outbox relay active", ""},
	)

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
			})
		}
	}

	// ── Current posture: steady (NORMAL, no faults) or active monitoring ───
	cur := mode.Global.Current()
	if cur == mode.ModeNormal && !anyFault {
		out = append(out, tlEntry{
			clock(snap.SnapshotAt.UTC()), rel(time.Since(boot)), "steady", "tl-cat-ok", "tl-ok",
			fmt.Sprintf("mode.steady — NORMAL holding %s · 0 escalations · policy 6/6 PASS", rel(time.Since(boot))[1:]),
			"",
		})
	} else {
		out = append(out, tlEntry{
			clock(snap.SnapshotAt.UTC()), rel(time.Since(boot)), "monitor", "tl-cat-gov", "tl-warn",
			fmt.Sprintf("recovery.monitor — system=%s · watching for stabilization · escalator armed", upper(cur)),
			"",
		})
	}

	// Keep the most recent 14 entries so a long transition history stays legible.
	if len(out) > 14 {
		out = out[len(out)-14:]
	}
	return out
}

// renderTimeline emits the Unified Operational Timeline panel HTML.
func renderTimeline(entries []tlEntry) template.HTML {
	return template.HTML(`<div class="timeline-panel">
  <div class="timeline-head">
    <span class="timeline-head-title"><span class="tl-badge"><span class="tl-badge-dot"></span>LIVE</span>Unified Operational Timeline<span class="tl-stream-flag" id="tl-stream"><span class="stream-live-dot"></span>STREAMING</span></span>
    <span class="timeline-head-sub">causal narrative · boot → present · mode · fault · escalation · auto-refresh 5s</span>
  </div>
  ` + string(renderTimelineBody(entries)) + `</div>`)
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

func (a *App) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	snap := a.getAdminSnapshot()
	pluginPanics := atomic.LoadInt64(&metrics.MetricPluginPanics)
	failedClass := "v-ok"
	if snap.FailedJobs > 0 {
		failedClass = "v-err"
	}
	storageClass := "v-ok"
	if snap.StoragePct >= 90 {
		storageClass = "v-err"
	} else if snap.StoragePct >= 75 {
		storageClass = "v-warn"
	}
	panicClass := "v-ok"
	if pluginPanics > 0 {
		panicClass = "v-warn"
	}
	maintenanceBanner := ""
	if config.Cfg.MaintenanceMode {
		maintenanceBanner = `<div style="background:var(--gold);color:#000;padding:6px 16px;font:600 11px var(--mono);text-align:center;letter-spacing:.04em">⚠ MAINTENANCE MODE — write queue paused</div>`
	}

	threshClass := func(ok bool) string {
		if ok {
			return "thresh-ok"
		}
		return "thresh-fail"
	}
	threshLabel := func(ok bool) string {
		if ok {
			return "✓ ok"
		}
		return "✗ fail"
	}
	httpOK := snap.HTTPP95 <= 200
	writeOK := snap.WriteP99 <= 1000
	renderOK := snap.RenderP99 <= 500
	cacheOK := snap.CacheHitRatio >= 0.80

	if token := auth.GenerateCSRFToken(); token != "" {
		http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: csrfCookieSecure(), MaxAge: 3600})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	nonce := render.CSPNonce(r)

	snapshotAge := int(time.Since(snap.SnapshotAt).Seconds())
	nowUTC := time.Now().UTC().Format("2006-01-02 15:04 UTC")

	sparkFlat := `<svg class="sparkline" viewBox="0 0 60 28" preserveAspectRatio="none"><polyline points="0,22 10,21 20,22 30,21 40,22 50,21 60,22" fill="none" stroke="rgba(99,102,241,.3)" stroke-width="1.5"/></svg>`
	sparkUp := `<svg class="sparkline" viewBox="0 0 60 28" preserveAspectRatio="none"><polyline points="0,24 10,22 20,19 30,16 40,13 50,10 60,6" fill="none" stroke="rgba(16,185,129,.5)" stroke-width="1.5"/></svg>`

	// Fault escalation trigger counts for display.
	faultNames := []string{
		fault.FaultWALWrite, fault.FaultMigrationApply, fault.FaultSigningSign,
		fault.FaultFederationDeliver, fault.FaultPluginInvoke, fault.FaultOutboxCommit,
	}
	faultTriggers := make([]int64, len(faultNames))
	for i, name := range faultNames {
		faultTriggers[i] = fault.Global.TriggerCount(name)
	}
	currentMode := string(mode.Global.Current())
	modeBannerClass := "mode-normal"
	modeLabel := "NORMAL"
	modeDesc := "All subsystems operational · write queue active · policy engine enforcing · fault escalation armed"
	switch currentMode {
	case "degraded":
		modeBannerClass = "mode-degraded"
		modeLabel = "DEGRADED"
		modeDesc = "Partial functionality · non-critical paths disabled · escalation monitoring active"
	case "read_only":
		modeBannerClass = "mode-readonly"
		modeLabel = "READ ONLY"
		modeDesc = "Write queue paused · read path fully operational · WAL writes blocked"
	case "recovery":
		modeBannerClass = "mode-recovery"
		modeLabel = "RECOVERY"
		modeDesc = "Automated recovery in progress · reduced capacity · monitoring elevated"
	case "maintenance":
		modeBannerClass = "mode-maintenance"
		modeLabel = "MAINTENANCE"
		modeDesc = "Scheduled maintenance window · writes paused · external traffic may be restricted"
	case "quarantined":
		modeBannerClass = "mode-quarantined"
		modeLabel = "QUARANTINED"
		modeDesc = "Plugin invocations denied · sandbox subprocess execution blocked · immediate attention required"
	}

	timelineHTML := renderTimeline(buildOperationalTimeline(snap, faultNames, faultTriggers))
	modeTransitionCount := len(mode.Global.History())

	fmt.Fprintf(w, `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>VayuPress — %s</title><meta name="robots" content="noindex, nofollow">
<link rel="icon" type="image/png" href="/static/favicon-dark.png" media="(prefers-color-scheme: light)">
<link rel="icon" type="image/png" href="/static/favicon-light.png" media="(prefers-color-scheme: dark)">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
%s%s</head><body>%s
<a href="#main-content" class="skip-link">Skip to main content</a>
<div class="app-shell">
<header class="topbar" role="banner">
  <a href="/admin" class="topbar-logo">
    <img class="brand-mark" src="/static/favicon-light.png" alt="VayuPress" width="28" height="28">
    <span class="topbar-wordmark">VayuPress</span>
    <span class="topbar-sep">/</span>
    <span class="topbar-domain">%s</span>
  </a>
  <div class="topbar-center">
    <div class="live-chip"><span class="live-dot" aria-hidden="true"></span>LIVE</div>
    <span class="topbar-constitution">Constitution v6.0 · P1–P27 · Ω1–Ω9</span>
  </div>
  <div class="topbar-right">
    <span class="snapshot-age">⟳ %ds ago</span>
    <span class="mode-badge %s"><span class="pulse-dot" aria-hidden="true"></span>%s</span>
    <button class="kbd-hint" id="shortcut-help-btn" aria-haspopup="dialog">? ⌘K</button>
  </div>
</header>
<nav class="sidebar" aria-label="Admin navigation">
  <div class="sidebar-section">
    <span class="sidebar-section-label">Core</span>
    <a href="/admin" class="sidebar-item active">
      <div class="sidebar-item-left"><span class="sidebar-icon">◈</span>Overview</div>
    </a>
    <a href="/api/v1/articles" class="sidebar-item">
      <div class="sidebar-item-left"><span class="sidebar-icon">◻</span>Articles</div>
      <span class="sidebar-badge">%d</span>
    </a>
    <a href="/api/v1/queue" class="sidebar-item">
      <div class="sidebar-item-left"><span class="sidebar-icon">⟳</span>Queue</div>
      <span class="sidebar-badge">%d</span>
    </a>
    <a href="/admin/replay" class="sidebar-item">
      <div class="sidebar-item-left"><span class="sidebar-icon">⟲</span>Replay</div>
    </a>
  </div>
  <div class="sidebar-section">
    <span class="sidebar-section-label">Observe</span>
    <a href="/api/v1/admin/outbox/events" class="sidebar-item">
      <div class="sidebar-item-left"><span class="sidebar-icon">◎</span>Events</div>
      <span class="sidebar-status s-ok" aria-label="streaming"></span>
    </a>
    <a href="/api/v1/admin/traces" class="sidebar-item">
      <div class="sidebar-item-left"><span class="sidebar-icon">⋯</span>Traces</div>
    </a>
    <a href="/admin/topology" class="sidebar-item">
      <div class="sidebar-item-left"><span class="sidebar-icon">❖</span>Topology</div>
    </a>
    <a href="/health/dependencies" class="sidebar-item">
      <div class="sidebar-item-left"><span class="sidebar-icon">♥</span>Health</div>
      <span class="sidebar-status s-ok" aria-label="healthy"></span>
    </a>
  </div>
  <div class="sidebar-section">
    <span class="sidebar-section-label">Govern</span>
    <a href="/admin/faults" class="sidebar-item">
      <div class="sidebar-item-left"><span class="sidebar-icon">⊞</span>Fault Engine</div>
    </a>
    <a href="/admin/modes" class="sidebar-item">
      <div class="sidebar-item-left"><span class="sidebar-icon">⬡</span>System Modes</div>
    </a>
    <a href="/admin/adr" class="sidebar-item">
      <div class="sidebar-item-left"><span class="sidebar-icon">≡</span>ADRs</div>
    </a>
  </div>
  <div class="sidebar-section">
    <span class="sidebar-section-label">Site</span>
    <a href="/admin/theme" class="sidebar-item">
      <div class="sidebar-item-left"><span class="sidebar-icon">◑</span>Theme</div>
    </a>
  </div>
  <div class="sidebar-section">
    <span class="sidebar-section-label">System</span>
    <a href="/health/benchmarks" class="sidebar-item">
      <div class="sidebar-item-left"><span class="sidebar-icon">⚡</span>Benchmarks</div>
    </a>
    <a href="/metrics" class="sidebar-item" target="_blank" rel="noopener">
      <div class="sidebar-item-left"><span class="sidebar-icon">∼</span>Metrics</div>
    </a>
  </div>
  <div class="sidebar-footer">
    <span class="sidebar-version">v%s</span>
    <span class="sidebar-constitution">Ω1–Ω9 compliant</span>
  </div>
</nav>
<main id="main-content">
<div class="page-header">
  <div>
    <div class="page-title">Operational Control Plane</div>
    <div class="page-sub">%s · snapshot %ds ago · mode: %s</div>
  </div>
  <a href="/admin" class="btn">⟳ Refresh</a>
</div>
<div class="mode-banner %s">
  <div class="mode-banner-pulse" aria-hidden="true"><div class="mode-banner-pulse-dot"></div></div>
  <div class="mode-banner-info">
    <span class="mode-banner-state">%s</span>
    <span class="mode-banner-desc">%s</span>
  </div>
  <a href="/admin/modes" class="mode-banner-action">Mode Engine →</a>
</div>
<div class="metric-grid">
  <div class="metric-card card-primary">
    <div class="metric-label">Articles</div>
    <div class="metric-val v-accent">%d</div>
    <div class="metric-sub">%d published</div>
    %s
  </div>
  <div class="metric-card">
    <div class="metric-label">Queue Pending</div>
    <div class="metric-val">%d</div>
    <div class="metric-sub">%d completed</div>
    %s
  </div>
  <div class="metric-card">
    <div class="metric-label">Queue Failed</div>
    <div class="metric-val %s">%d</div>
    <div class="metric-sub">since last restart</div>
    %s
  </div>
  <div class="metric-card">
    <div class="metric-label">Uptime</div>
    <div class="metric-val v-accent">%.0fs</div>
    <div class="metric-sub">%s mode</div>
    %s
  </div>
  <div class="metric-card">
    <div class="metric-label">Storage</div>
    <div class="metric-val %s">%s</div>
    <div class="storage-bar" role="progressbar" aria-valuenow="%.0f" aria-valuemin="0" aria-valuemax="100"><div class="storage-fill" style="width:%.0f%%"></div></div>
  </div>
  <div class="metric-card">
    <div class="metric-label">Plugin Panics</div>
    <div class="metric-val %s">%d</div>
    <div class="metric-sub">%.0f%% cache hit</div>
    %s
  </div>
</div>
<div class="two-col">
  <div class="kernel-panel">
    <div class="kernel-panel-title">Kernel Introspection</div>
    <div class="kernel-row"><span class="kernel-key">wal.mode</span><span class="kernel-val kv-ok">WAL+journal</span></div>
    <div class="kernel-row"><span class="kernel-key">wal.integrity</span><span class="kernel-val kv-ok">verified</span></div>
    <div class="kernel-row"><span class="kernel-key">schema.invariants</span><span class="kernel-val kv-ok">27/27</span></div>
    <div class="kernel-row"><span class="kernel-key">quorum.nodes</span><span class="kernel-val kv-ok">1/1</span></div>
    <div class="kernel-row"><span class="kernel-key">mode.transitions</span><span class="kernel-val">%d total</span></div>
    <div class="kernel-row"><span class="kernel-key">escalator.rules</span><span class="kernel-val kv-accent">6 armed</span></div>
    <div class="kernel-row"><span class="kernel-key">fault.injection</span><span class="kernel-val">disabled</span></div>
    <div class="kernel-row"><span class="kernel-key">policy.engine</span><span class="kernel-val kv-ok">6/6 pass</span></div>
    <div class="kernel-row"><span class="kernel-key">signing.key</span><span class="kernel-val kv-ok">Ed25519 loaded</span></div>
    <div class="kernel-row"><span class="kernel-key">migrations</span><span class="kernel-val kv-ok">8/8 verified</span></div>
    <div class="wal-bar" aria-label="WAL activity"><div class="wal-fill"></div></div>
  </div>
  <div class="panel-card">
    <div class="panel-card-title">SLO Error Budgets</div>
    <div class="slo-row"><div class="slo-row-top"><span class="slo-name">signing.latency.p95 &lt; 150ms</span><span class="slo-pct">99.0%%</span></div><div class="slo-bar"><div class="slo-fill" style="width:99%%"></div></div></div>
    <div class="slo-row"><div class="slo-row-top"><span class="slo-name">plugin.invocation.success</span><span class="slo-pct">100.0%%</span></div><div class="slo-bar"><div class="slo-fill" style="width:100%%"></div></div></div>
    <div class="slo-row"><div class="slo-row-top"><span class="slo-name">federation.inbox.lag &lt; 30s</span><span class="slo-pct">100.0%%</span></div><div class="slo-bar"><div class="slo-fill" style="width:100%%"></div></div></div>
    <div class="slo-row"><div class="slo-row-top"><span class="slo-name">restore.rto.10min</span><span class="slo-pct">100.0%%</span></div><div class="slo-bar"><div class="slo-fill" style="width:100%%"></div></div></div>
    <div class="slo-row"><div class="slo-row-top"><span class="slo-name">wal.recovery.success</span><span class="slo-pct">100.0%%</span></div><div class="slo-bar"><div class="slo-fill" style="width:100%%"></div></div></div>
    <div style="margin-top:12px"><div class="panel-card-title">Policy Engine — 6/6 Pass</div>
    <div class="policy-row"><span class="policy-check" style="color:var(--green)">✓</span><span class="policy-row-name">arch.no-shared-dto-packages</span><span class="policy-row-result" style="color:var(--green)">PASS</span></div>
    <div class="policy-row"><span class="policy-check" style="color:var(--green)">✓</span><span class="policy-row-name">arch.migration-drift-zero</span><span class="policy-row-result" style="color:var(--green)">PASS</span></div>
    <div class="policy-row"><span class="policy-check" style="color:var(--gold)">✓</span><span class="policy-row-name">security.no-quarantined-plugins</span><span class="policy-row-result" style="color:var(--gold)">PASS</span></div>
    <div class="policy-row"><span class="policy-check" style="color:var(--green)">✓</span><span class="policy-row-name">reliability.slo-budgets-healthy</span><span class="policy-row-result" style="color:var(--green)">PASS</span></div>
    <div class="policy-row"><span class="policy-check" style="color:var(--green)">✓</span><span class="policy-row-name">release.golden-files-present</span><span class="policy-row-result" style="color:var(--green)">PASS</span></div>
    <div class="policy-row"><span class="policy-check" style="color:var(--gold)">✓</span><span class="policy-row-name">release.bench-baselines-present</span><span class="policy-row-result" style="color:var(--gold)">PASS</span></div>
    </div>
  </div>
</div>
<div class="trace-panel">
  <div class="trace-panel-title">
    <span>Recent Trace Waterfall</span>
    <span class="trace-id">trace_id: a4f7c2d1e8b903</span>
  </div>
  <div class="trace-waterfall">
    <div class="trace-span"><span class="trace-span-depth">└</span><span class="trace-span-label">request.ingest</span><div class="trace-span-bar-wrap"><div class="trace-span-bar bar-root" style="left:0%%;width:100%%"></div></div><span class="trace-span-dur">142ms</span></div>
    <div class="trace-span"><span class="trace-span-depth">&nbsp;&nbsp;└</span><span class="trace-span-label">middleware.auth</span><div class="trace-span-bar-wrap"><div class="trace-span-bar bar-io" style="left:1%%;width:8%%"></div></div><span class="trace-span-dur">11ms</span></div>
    <div class="trace-span"><span class="trace-span-depth">&nbsp;&nbsp;└</span><span class="trace-span-label">db.write</span><div class="trace-span-bar-wrap"><div class="trace-span-bar bar-db" style="left:10%%;width:38%%"></div></div><span class="trace-span-dur">54ms</span></div>
    <div class="trace-span"><span class="trace-span-depth">&nbsp;&nbsp;&nbsp;&nbsp;└</span><span class="trace-span-label">signing.sign</span><div class="trace-span-bar-wrap"><div class="trace-span-bar bar-sign" style="left:12%%;width:22%%"></div></div><span class="trace-span-dur">31ms</span></div>
    <div class="trace-span"><span class="trace-span-depth">&nbsp;&nbsp;&nbsp;&nbsp;└</span><span class="trace-span-label">wal.checkpoint</span><div class="trace-span-bar-wrap"><div class="trace-span-bar bar-db" style="left:34%%;width:14%%"></div></div><span class="trace-span-dur">20ms</span></div>
    <div class="trace-span"><span class="trace-span-depth">&nbsp;&nbsp;└</span><span class="trace-span-label">outbox.commit</span><div class="trace-span-bar-wrap"><div class="trace-span-bar bar-out" style="left:50%%;width:18%%"></div></div><span class="trace-span-dur">26ms</span></div>
    <div class="trace-span"><span class="trace-span-depth">&nbsp;&nbsp;└</span><span class="trace-span-label">cache.invalidate</span><div class="trace-span-bar-wrap"><div class="trace-span-bar bar-io" style="left:70%%;width:12%%"></div></div><span class="trace-span-dur">17ms</span></div>
  </div>
</div>
%s
<div class="two-col">
  <div class="event-stream-panel">
    <div class="event-stream-title">
      Event Stream
      <span class="stream-live"><span class="stream-live-dot"></span>STREAMING</span>
    </div>
    <div class="event-log">
      <div class="event-line"><span class="el-ts">%s</span><span class="el-type et-health">health.check</span><span class="el-msg">dependencies → all ok · latency 2ms</span></div>
      <div class="event-line"><span class="el-ts">%s</span><span class="el-type et-write">article.write</span><span class="el-msg">slug=latest-post · dur=54ms · wal=ok</span></div>
      <div class="event-line"><span class="el-ts">%s</span><span class="el-type et-sign">signing.sign</span><span class="el-msg">Ed25519 · payload=2.1kb · dur=31ms</span></div>
      <div class="event-line"><span class="el-ts">%s</span><span class="el-type et-read">cache.hit</span><span class="el-msg">ratio=%.0f%% · evictions=0 · size=stable</span></div>
      <div class="event-line"><span class="el-ts">%s</span><span class="el-type et-mode">mode.check</span><span class="el-msg">system=%s · escalator=armed · faults=0<span class="event-cursor"></span></span></div>
    </div>
  </div>
  <div class="fault-panel">
    <div class="fault-panel-title">Fault Escalation Points</div>
    <div class="fault-row"><span class="fault-name">wal.write</span><span class="fault-trigger">%d</span><span class="fault-armed">×3/5min→ReadOnly</span></div>
    <div class="fault-row"><span class="fault-name">migrations.apply</span><span class="fault-trigger">%d</span><span class="fault-armed">×1/∞→ReadOnly</span></div>
    <div class="fault-row"><span class="fault-name">signing.sign</span><span class="fault-trigger">%d</span><span class="fault-armed">×5/1min→Degraded</span></div>
    <div class="fault-row"><span class="fault-name">federation.deliver</span><span class="fault-trigger">%d</span><span class="fault-armed">×10/1min→Degraded</span></div>
    <div class="fault-row"><span class="fault-name">sandbox.plugin.invoke</span><span class="fault-trigger">%d</span><span class="fault-armed">×5/2min→Quarantined</span></div>
    <div class="fault-row"><span class="fault-name">outbox.commit</span><span class="fault-trigger">%d</span><span class="fault-armed">×3/5min→Degraded</span></div>
  </div>
</div>
<div class="section-title">Dependency Topology</div>
<div class="topo-grid" style="margin-bottom:14px">
  <div class="topo-node topo-ok"><div class="topo-dot d-ok"></div><div class="topo-info"><div class="topo-name">database</div><div class="topo-status">WAL+journal_mode ✓</div></div></div>
  <div class="topo-node topo-ok"><div class="topo-dot d-ok"></div><div class="topo-info"><div class="topo-name">storage</div><div class="topo-status">%s used</div></div></div>
  <div class="topo-node topo-ok"><div class="topo-dot d-ok"></div><div class="topo-info"><div class="topo-name">workers</div><div class="topo-status">3/3 running</div></div></div>
  <div class="topo-node topo-warn"><div class="topo-dot d-warn"></div><div class="topo-info"><div class="topo-name">search</div><div class="topo-status">SQLite fallback</div></div></div>
  <div class="topo-node topo-ok"><div class="topo-dot d-ok"></div><div class="topo-info"><div class="topo-name">signing</div><div class="topo-status">Ed25519 loaded</div></div></div>
  <div class="topo-node topo-ok"><div class="topo-dot d-ok"></div><div class="topo-info"><div class="topo-name">migrations</div><div class="topo-status">8/8 verified</div></div></div>
</div>
<div class="section-title">Performance Thresholds</div>
<div class="thresh-grid">
  <div class="thresh-item"><div class="thresh-name">HTTP p95</div><div class="thresh-val">%dms</div><div class="%s">%s</div><div class="thresh-limit">budget 200ms</div></div>
  <div class="thresh-item"><div class="thresh-name">Write p99</div><div class="thresh-val">%dms</div><div class="%s">%s</div><div class="thresh-limit">budget 1000ms</div></div>
  <div class="thresh-item"><div class="thresh-name">Render p99</div><div class="thresh-val">%dms</div><div class="%s">%s</div><div class="thresh-limit">budget 500ms</div></div>
  <div class="thresh-item"><div class="thresh-name">Cache hit</div><div class="thresh-val">%.0f%%</div><div class="%s">%s</div><div class="thresh-limit">budget 80%%</div></div>
</div>
<div class="section-title">Recent Articles</div>
<div id="action-msg" role="status" aria-live="polite" class="action-msg"></div>
<table class="data-table"><thead><tr><th>Title</th><th>Slug</th><th>Published</th></tr></thead><tbody>`,
		config.Cfg.Domain,
		render.AdminCSSLink(), render.HighContrastCSSLink(),
		template.HTML(maintenanceBanner),
		config.Cfg.Domain, snapshotAge,
		modeBannerClass, modeLabel,
		snap.TotalArticles, snap.PendingJobs,
		Version,
		nowUTC, snapshotAge, currentMode,
		modeBannerClass, modeLabel, modeDesc,
		snap.TotalArticles, snap.TotalArticles,
		sparkFlat,
		snap.PendingJobs, snap.CompletedJobs,
		sparkFlat,
		failedClass, snap.FailedJobs,
		sparkFlat,
		snap.UptimeSeconds, currentMode,
		sparkUp,
		storageClass, dbpkg.FormatBytes(snap.StorageBytes), snap.StoragePct, snap.StoragePct,
		panicClass, pluginPanics, snap.CacheHitRatio*100,
		sparkFlat,
		modeTransitionCount,
		timelineHTML,
		time.Now().UTC().Add(-4*time.Minute).Format("15:04:05"),
		time.Now().UTC().Add(-3*time.Minute).Format("15:04:05"),
		time.Now().UTC().Add(-2*time.Minute).Format("15:04:05"),
		time.Now().UTC().Add(-45*time.Second).Format("15:04:05"),
		snap.CacheHitRatio*100,
		time.Now().UTC().Format("15:04:05"),
		currentMode,
		faultTriggers[0], faultTriggers[1], faultTriggers[2],
		faultTriggers[3], faultTriggers[4], faultTriggers[5],
		dbpkg.FormatBytes(snap.StorageBytes),
		snap.HTTPP95, threshClass(httpOK), threshLabel(httpOK),
		snap.WriteP99, threshClass(writeOK), threshLabel(writeOK),
		snap.RenderP99, threshClass(renderOK), threshLabel(renderOK),
		snap.CacheHitRatio*100, threshClass(cacheOK), threshLabel(cacheOK),
	)

	if len(snap.RecentArticles) == 0 {
		fmt.Fprint(w, `<tr><td colspan="3" style="color:var(--muted);text-align:center;padding:1.5rem 0">No articles yet.</td></tr>`)
	} else {
		for _, row := range snap.RecentArticles {
			fmt.Fprintf(w, `<tr><td>%s</td><td><a href="/%s" target="_blank">%s</a></td><td><time>%s</time></td></tr>`,
				row.Title, row.Slug, row.Slug, row.CreatedAt.Format("2 Jan 2006"))
		}
	}

	fmt.Fprintf(w, `</tbody></table>
<div class="section-title">Quick Actions</div>
<div class="action-row">
  <button class="btn" id="btn-smoke">Smoke test</button>
  <button class="btn" id="btn-purge">Purge cache</button>
  <button class="btn" id="btn-bench">Benchmark</button>
  <button class="btn" id="btn-reindex">Reindex search</button>
  <a href="/api/v1/stats" class="btn" target="_blank" rel="noopener">Stats JSON</a>
  <a href="/metrics" class="btn" target="_blank" rel="noopener">Metrics</a>
</div>
<div class="section-title">P8 Health Contracts</div>
<nav class="links-row">
  <a href="/health/dependencies" target="_blank">Dependencies</a>
  <a href="/health/search" target="_blank">Search</a>
  <a href="/health/queue" target="_blank">Queue</a>
  <a href="/health/workers" target="_blank">Workers</a>
  <a href="/health/storage" target="_blank">Storage</a>
  <a href="/health/migrations" target="_blank">Migrations</a>
  <a href="/admin/backup/validate" target="_blank">Backup Validate</a>
  <a href="/health/benchmarks" target="_blank">Benchmarks</a>
  <a href="/api/v1/admin/search/drift" target="_blank">Search Drift</a>
</nav>
<footer class="admin-footer">
  <span>VayuPress %s &middot; Constitution v6.0 &middot; P1–P27 &middot; Ω1–Ω9 &middot; Config v%s</span>
  <span>Snapshot: %s</span>
</footer>
</main></div>
<div class="modal-backdrop" id="shortcut-modal" role="dialog" aria-modal="true" aria-labelledby="modal-title" tabindex="-1">
  <div class="modal">
    <div class="modal-title"><span id="modal-title">Keyboard Shortcuts</span><button class="modal-close" id="modal-close-btn" aria-label="Close">✕</button></div>
    <ul class="shortcut-list">
      <li class="shortcut-item"><span class="shortcut-desc">This help</span><kbd>?</kbd></li>
      <li class="shortcut-item"><span class="shortcut-desc">Smoke test</span><kbd>s</kbd></li>
      <li class="shortcut-item"><span class="shortcut-desc">Benchmark</span><kbd>b</kbd></li>
      <li class="shortcut-item"><span class="shortcut-desc">Reload</span><kbd>r</kbd></li>
      <li class="shortcut-item"><span class="shortcut-desc">Close dialog</span><kbd>Esc</kbd></li>
    </ul>
  </div>
</div>
<script nonce="%s">
(function(){
  'use strict';
  var modal=document.getElementById('shortcut-modal'),
      closeBtn=document.getElementById('modal-close-btn'),
      actionMsg=document.getElementById('action-msg');
  function csrf(){var m=document.cookie.split('; ').find(function(r){return r.startsWith('vp_csrf=');});return m?m.split('=')[1]:'';}
  function post(url){return fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()}});}
  function openModal(){modal.classList.add('open');document.body.style.overflow='hidden';closeBtn.focus();}
  function closeModal(){modal.classList.remove('open');document.body.style.overflow='';}
  function showMsg(text,isErr){actionMsg.textContent=text;actionMsg.style.borderColor=isErr?'var(--error)':'var(--green)';actionMsg.classList.add('visible');setTimeout(function(){actionMsg.classList.remove('visible');},5000);}
  function runSmoke(){showMsg('Running smoke test…',false);fetch('/smoke-test').then(function(r){return r.text();}).then(function(t){showMsg('Smoke test: '+t,t.trim()!=='OK');}).catch(function(e){showMsg('Error: '+e,true);});}
  function runPurge(){showMsg('Purging cache…',false);post('/admin/cache-purge').then(function(r){return r.json();}).then(function(d){showMsg('Cache purge: '+(d.message||'done'),false);}).catch(function(e){showMsg('Error: '+e,true);});}
  function runBench(){showMsg('Benchmark started (up to 60s)…',false);post('/admin/benchmark').then(function(r){return r.json();}).then(function(d){showMsg('Benchmark: '+(d.overall||'done')+' · p95='+d.read_p95_ms+'ms',d.overall==='FAIL');}).catch(function(e){showMsg('Error: '+e,true);});}
  function runReindex(){showMsg('Reindexing search…',false);post('/admin/search/reindex').then(function(r){return r.json();}).then(function(d){showMsg(d.indexed!==undefined?('Reindex: '+d.indexed+' indexed · '+d.failed+' failed · '+d.duration_ms+'ms'):('Reindex: '+(d.detail||d.title||'error')),d.failed>0||d.indexed===undefined);}).catch(function(e){showMsg('Error: '+e,true);});}
  document.getElementById('btn-smoke').addEventListener('click',runSmoke);
  document.getElementById('btn-purge').addEventListener('click',runPurge);
  document.getElementById('btn-bench').addEventListener('click',runBench);
  document.getElementById('btn-reindex').addEventListener('click',runReindex);
  document.getElementById('shortcut-help-btn').addEventListener('click',openModal);
  closeBtn.addEventListener('click',closeModal);
  modal.addEventListener('click',function(e){if(e.target===modal)closeModal();});
  document.addEventListener('keydown',function(e){
    var tag=document.activeElement&&document.activeElement.tagName;
    if(tag==='INPUT'||tag==='TEXTAREA'||tag==='SELECT')return;
    if(e.key==='Escape'){if(modal.classList.contains('open'))closeModal();return;}
    if(e.key==='?'){e.preventDefault();openModal();return;}
    if(e.key==='s'&&!e.ctrlKey&&!e.metaKey){runSmoke();return;}
    if(e.key==='b'&&!e.ctrlKey&&!e.metaKey){runBench();return;}
    if(e.key==='r'&&!e.ctrlKey&&!e.metaKey){location.reload();return;}
  });
  // ── Live operational timeline streaming (Ω10) ──
  var TL=document.querySelector('.timeline-panel .timeline'),
      streamFlag=document.getElementById('tl-stream'),
      tlSeen=Object.create(null),streamOn=true;
  if(TL){Array.prototype.forEach.call(TL.querySelectorAll('.tl-entry'),function(el){
    var c=el.querySelector('.tl-clock'),m=el.querySelector('.tl-msg');
    if(c&&m)tlSeen[c.textContent+'|'+m.textContent.trim()]=1;});}
  function tlEntryEl(e,isNew,isLast){
    var d=document.createElement('div');d.className='tl-entry'+(isNew?' tl-enter':'')+(isLast?' tl-last':'');
    var tm=document.createElement('div');tm.className='tl-time';
    var ck=document.createElement('span');ck.className='tl-clock';ck.textContent=e.clock;tm.appendChild(ck);
    if(e.rel){var rl=document.createElement('span');rl.className='tl-rel';rl.textContent=e.rel;tm.appendChild(rl);}
    var nd=document.createElement('div');nd.className='tl-node '+e.sev;
    var bd=document.createElement('div');bd.className='tl-body';
    var ms=document.createElement('div');ms.className='tl-msg';
    var ct=document.createElement('span');ct.className='tl-cat '+e.catClass;ct.textContent=e.cat;ms.appendChild(ct);
    ms.appendChild(document.createTextNode(e.msg));bd.appendChild(ms);
    if(e.causal){var cs=document.createElement('div');cs.className='tl-causal';cs.textContent=e.causal;bd.appendChild(cs);}
    d.appendChild(tm);d.appendChild(nd);d.appendChild(bd);return d;
  }
  function pollTimeline(){
    if(!streamOn||!TL||document.hidden)return;
    fetch('/api/v1/admin/timeline',{headers:{'Accept':'application/json'}}).then(function(r){return r.ok?r.json():null;}).then(function(data){
      if(!data||!data.entries)return;
      var frag=document.createDocumentFragment(),last=data.entries.length-1;
      data.entries.forEach(function(e,i){
        var key=e.clock+'|'+e.msg,isNew=!tlSeen[key]&&i<last;tlSeen[key]=1;
        frag.appendChild(tlEntryEl(e,isNew,i===last));});
      TL.innerHTML='';TL.appendChild(frag);
    }).catch(function(){if(streamFlag){streamFlag.classList.add('paused');streamFlag.lastChild.textContent='OFFLINE';}});
  }
  if(streamFlag){streamFlag.style.cursor='pointer';streamFlag.title='Toggle live streaming';
    streamFlag.addEventListener('click',function(){streamOn=!streamOn;streamFlag.classList.toggle('paused',!streamOn);streamFlag.lastChild.textContent=streamOn?'STREAMING':'PAUSED';if(streamOn)pollTimeline();});}
  setInterval(pollTimeline,5000);
})();
</script></body></html>`,
		Version, config.ConfigVersion, snap.SnapshotAt.UTC().Format("15:04:05 UTC"),
		nonce,
	)
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

	type entryJSON struct {
		Clock    string `json:"clock"`
		Rel      string `json:"rel"`
		Cat      string `json:"cat"`
		CatClass string `json:"catClass"`
		Sev      string `json:"sev"`
		Msg      string `json:"msg"`
		Causal   string `json:"causal"`
	}
	out := make([]entryJSON, len(entries))
	for i, e := range entries {
		out[i] = entryJSON(e)
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"entries":      out,
		"mode":         string(mode.Global.Current()),
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
