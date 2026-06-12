package main

import (
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
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
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
					io.Copy(h, f)
					f.Close()
					checksumOK = hex.EncodeToString(h.Sum(nil)) == storedSum
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
		if !isValidSlug(slug) {
			writeAPIError(w, r, 400, "invalid_slug", "invalid slug", "https://docs.vayupress.com/api/cache")
			return
		}
		var tags string
		dbpkg.DB.QueryRow(`SELECT tags FROM articles WHERE slug=?`, slug).Scan(&tags)
		render.CachePurge(slug, splitTags(tags), generateSitemap, generateRSS, generateRobots)
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
					if fi, err := os.Stat(fpath); err == nil {
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

func (a *App) handleArticlePage(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if !isValidSlug(slug) {
		http.NotFound(w, r)
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
		http.NotFound(w, r)
		return
	}
	art.Tags = splitTags(tagsStr)
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
	if a.meiliCB != nil {
		go a.meiliDo("DELETE", "/indexes/articles/documents/"+testID, nil)
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

func (a *App) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	snap := a.getAdminSnapshot()
	pluginPanics := atomic.LoadInt64(&metrics.MetricPluginPanics)
	failedClass := "stat-ok"
	if snap.FailedJobs > 0 {
		failedClass = "stat-err"
	}
	storageClass := "stat-ok"
	if snap.StoragePct >= 90 {
		storageClass = "stat-err"
	} else if snap.StoragePct >= 75 {
		storageClass = "stat-warn"
	}
	panicClass := "stat-ok"
	if pluginPanics > 0 {
		panicClass = "stat-warn"
	}
	threshClass := func(ok bool) string {
		if ok {
			return "thresh-ok"
		}
		return "thresh-fail"
	}
	threshLabel := func(ok bool) string {
		if ok {
			return "✓ OK"
		}
		return "✗ FAIL"
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

	maintenanceBanner := ""
	if config.Cfg.MaintenanceMode {
		maintenanceBanner = `<div style="background:var(--warn);color:#000;padding:8px 16px;font-size:12px;font-weight:600;text-align:center">⚠ MAINTENANCE MODE ACTIVE — write queue paused</div>`
	}

	fmt.Fprintf(w, `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>VayuPress Admin — %s</title><meta name="robots" content="noindex, nofollow">
%s%s</head><body>%s
<a href="#main-content" class="skip-link">Skip to main content</a>
<div class="app-shell">
<header class="topbar" role="banner">
  <a href="/admin" class="topbar-brand"><span aria-hidden="true">⚡</span><span>VayuPress</span><span class="topbar-domain">%s</span></a>
  <nav class="topbar-actions">
    <span style="font-size:11px;color:var(--muted);font-family:var(--mono)">⟳ %ds ago</span>
    <button class="kbd-hint" id="shortcut-help-btn" aria-haspopup="dialog">? shortcuts</button>
  </nav>
</header>
<main id="main-content">
<h2 class="section-title">Overview</h2>
<div class="stat-grid">
  <div class="stat-card"><div class="stat-val">%d</div><div class="stat-lbl">Articles</div></div>
  <div class="stat-card"><div class="stat-val">%d</div><div class="stat-lbl">Queue Pending</div><div class="stat-sub">%d completed</div></div>
  <div class="stat-card"><div class="stat-val %s">%d</div><div class="stat-lbl">Queue Failed</div></div>
  <div class="stat-card"><div class="stat-val">%.0fs</div><div class="stat-lbl">Uptime</div></div>
  <div class="stat-card"><div class="stat-val %s">%s</div><div class="stat-lbl">Storage Used</div>
    <div class="storage-bar" role="progressbar" aria-valuenow="%.0f" aria-valuemin="0" aria-valuemax="100"><div class="storage-fill" style="width:%.0f%%"></div></div>
  </div>
  <div class="stat-card"><div class="stat-val %s">%d</div><div class="stat-lbl">Plugin Panics</div><div class="stat-sub">%.1f%% cache hit</div></div>
</div>
<h2 class="section-title">Performance Thresholds</h2>
<div class="thresh-grid">
  <div class="thresh-item"><span class="thresh-name">HTTP p95</span><span><span class="thresh-val">%dms</span> <span class="%s">%s</span> <span class="thresh-name">/ 200ms</span></span></div>
  <div class="thresh-item"><span class="thresh-name">Write p99</span><span><span class="thresh-val">%dms</span> <span class="%s">%s</span> <span class="thresh-name">/ 1000ms</span></span></div>
  <div class="thresh-item"><span class="thresh-name">Render p99</span><span><span class="thresh-val">%dms</span> <span class="%s">%s</span> <span class="thresh-name">/ 500ms</span></span></div>
  <div class="thresh-item"><span class="thresh-name">Cache hit</span><span><span class="thresh-val">%.0f%%</span> <span class="%s">%s</span> <span class="thresh-name">/ 80%%</span></span></div>
</div>
<h2 class="section-title">Quick Actions</h2>
<div id="action-msg" role="status" aria-live="polite" class="action-msg"></div>
<div class="action-row">
  <button class="btn" id="btn-smoke">Smoke test</button>
  <button class="btn" id="btn-purge">Purge cache</button>
  <button class="btn" id="btn-bench">Benchmark</button>
  <a href="/api/v1/stats" class="btn" target="_blank" rel="noopener">Stats JSON</a>
  <a href="/metrics" class="btn" target="_blank" rel="noopener">Metrics</a>
  <a href="/admin/adr" class="btn" target="_blank" rel="noopener">ADRs</a>
</div>
<h2 class="section-title">Recent Articles</h2>
<table class="data-table"><thead><tr><th>Title</th><th>Slug</th><th>Published</th></tr></thead><tbody>`,
		config.Cfg.Domain,
		render.AdminCSSLink(), render.HighContrastCSSLink(),
		template.HTML(maintenanceBanner),
		config.Cfg.Domain, int(time.Since(snap.SnapshotAt).Seconds()),
		snap.TotalArticles, snap.PendingJobs, snap.CompletedJobs,
		failedClass, snap.FailedJobs, snap.UptimeSeconds,
		storageClass, dbpkg.FormatBytes(snap.StorageBytes), snap.StoragePct, snap.StoragePct,
		panicClass, pluginPanics, snap.CacheHitRatio*100,
		snap.HTTPP95, threshClass(httpOK), threshLabel(httpOK),
		snap.WriteP99, threshClass(writeOK), threshLabel(writeOK),
		snap.RenderP99, threshClass(renderOK), threshLabel(renderOK),
		snap.CacheHitRatio*100, threshClass(cacheOK), threshLabel(cacheOK),
	)

	if len(snap.RecentArticles) == 0 {
		fmt.Fprint(w, `<tr><td colspan="3" style="color:var(--muted);text-align:center;padding:2rem">No articles yet.</td></tr>`)
	} else {
		for _, row := range snap.RecentArticles {
			fmt.Fprintf(w, `<tr><td>%s</td><td><a href="/%s" target="_blank">%s</a></td><td><time>%s</time></td></tr>`,
				row.Title, row.Slug, row.Slug, row.CreatedAt.Format("2 Jan 2006"))
		}
	}

	fmt.Fprintf(w, `</tbody></table>
<h2 class="section-title">P8 Health Contracts</h2>
<nav class="links-row">
  <a href="/health/dependencies" target="_blank">Dependencies</a>
  <a href="/health/search" target="_blank">Search</a>
  <a href="/health/queue" target="_blank">Queue</a>
  <a href="/health/workers" target="_blank">Workers</a>
  <a href="/health/storage" target="_blank">Storage</a>
  <a href="/health/migrations" target="_blank">Migrations</a>
  <a href="/admin/backup/validate" target="_blank">Backup Validate</a>
  <a href="/health/benchmarks" target="_blank">Benchmarks</a>
</nav>
<footer class="admin-footer">VayuPress %s &middot; Constitution v6.0 &middot; P1–P14 compliant &middot; Config v%s &middot; Snapshot: %s</footer>
</main></div>
<div class="modal-backdrop" id="shortcut-modal" role="dialog" aria-modal="true" aria-labelledby="modal-title" tabindex="-1">
  <div class="modal">
    <div class="modal-title"><span id="modal-title">Keyboard Shortcuts</span><button class="modal-close" id="modal-close-btn" aria-label="Close">✕</button></div>
    <ul class="shortcut-list">
      <li class="shortcut-item"><span>This help</span><kbd>?</kbd></li>
      <li class="shortcut-item"><span>Smoke test</span><kbd>s</kbd></li>
      <li class="shortcut-item"><span>Benchmark</span><kbd>b</kbd></li>
      <li class="shortcut-item"><span>Reload</span><kbd>r</kbd></li>
      <li class="shortcut-item"><span>Close dialog</span><kbd>Esc</kbd></li>
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
  function showMsg(text,isErr){actionMsg.textContent=text;actionMsg.style.borderColor=isErr?'var(--error)':'var(--success)';actionMsg.classList.add('visible');setTimeout(function(){actionMsg.classList.remove('visible');},5000);}
  function runSmoke(){showMsg('Running smoke test…',false);fetch('/smoke-test').then(function(r){return r.text();}).then(function(t){showMsg('Smoke test: '+t,t.trim()!=='OK');}).catch(function(e){showMsg('Error: '+e,true);});}
  function runPurge(){showMsg('Purging cache…',false);post('/admin/cache-purge').then(function(r){return r.json();}).then(function(d){showMsg('Cache purge: '+(d.message||'done'),false);}).catch(function(e){showMsg('Error: '+e,true);});}
  function runBench(){showMsg('Benchmark started (up to 60s)…',false);post('/admin/benchmark').then(function(r){return r.json();}).then(function(d){showMsg('Benchmark: '+(d.overall||'done')+' · p95='+d.read_p95_ms+'ms',d.overall==='FAIL');}).catch(function(e){showMsg('Error: '+e,true);});}
  document.getElementById('btn-smoke').addEventListener('click',runSmoke);
  document.getElementById('btn-purge').addEventListener('click',runPurge);
  document.getElementById('btn-bench').addEventListener('click',runBench);
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
})();
</script></body></html>`,
		Version, config.ConfigVersion, snap.SnapshotAt.UTC().Format("15:04:05 UTC"),
		nonce,
	)
}
